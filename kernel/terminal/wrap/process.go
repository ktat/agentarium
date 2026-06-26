package wrap

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"sync"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

// Process は 1 つの PTY + vt.Emulator を抱える wrapper terminal セッション。
// 旧 internal/terminal.Process と違い ring buffer ではなく grid を持つので、
// クライアントは init/snapshot で全行を貰い、以降 update で差分のみを受ける。
type Process struct {
	mu      sync.Mutex
	emu     *vt.Emulator
	ptmx    *os.File
	cmd     *exec.Cmd
	workDir string
	command string
	args    []string

	cursorX, cursorY int
	// cursorHidden: DECTCEM (\x1b[?25l/h) のカーソル可視状態。emulator の
	// CursorVisibility callback (readPump の mu 保持中) で更新される。
	cursorHidden  bool
	altScreen     bool
	clientAltRows int
	// syncUpdate: DEC private mode 2026 (Synchronized Output) が enable の間 true。
	// flushLoop はこの間 broadcast を skip し、emulator は grid を atomic に更新する。
	// claude TUI は ~33ms 周期で頻繁に sync update を発行するため、reset 後の中途
	// frame を流さないために syncUpdateDebounce (80ms) も組み合わせる。
	//
	// NOTE: 「session 名が input 行に echo される」現象の根本原因は OSC 0 タイトル中の
	// UTF-8 multi-byte 文字 (例 ✳ = \xe2\x9c\xb3) の 0x9C が ansi parser の C1 ST と
	// 誤認識されて OSC が中途終了し、残り title 文字列が grid に書き込まれていた bug。
	// 修正は go.mod replace 経由の fork (github.com/ktat/x/ansi) で OSC string state の
	// 0x9C terminator entry を外したもの。本機構 (syncUpdate / debounce) は直接の
	// fix ではないが、別の sync update 経由の中途 frame に備えて残置する。不要と
	// 判明したら本フィールド・onEmuMode・flushLoop の skip ロジックを一括削除可能。
	syncUpdate bool
	// syncUpdateLastReset: 直近で syncUpdate=false に遷移した時刻。flushLoop は
	// `syncUpdate==false && time.Since(syncUpdateLastReset) >= syncUpdateDebounce`
	// のときだけ flush する。連続発火 (~33ms 周期) 中は常に skip になる。
	syncUpdateLastReset time.Time
	// initialCols は Start 時の PTY winsize と Emulator width。クライアントが
	// viewport から計算した値を SetInitialSize で渡す前提。default は DefaultCols。
	initialCols int
	// lastSent: broadcast 単位での「最後に送った行 hash」。差分判定に使う。
	lastSent map[int]string
	// mainShadow: main-screen 中の行 runs を保持。alt 突入時に emulator を
	// altRows に resize すると main grid が縮むため、alt 復帰時の client 復元用。
	mainShadow map[int][]Run

	subMu sync.Mutex
	subs  map[int]chan WSMessage
	subID int

	onExit   func()
	onOutput func([]byte) // PTY → 出力 callback (UUID 検知用、broadcast の中で呼ぶ)
	onInput  func([]byte) // client → PTY 入力 callback (sessionoverview の PTY fallback 等で参照)
	closed   bool
}

func NewProcess(workDir, command string, args ...string) *Process {
	return &Process{
		workDir:     workDir,
		command:     command,
		args:        args,
		lastSent:    map[int]string{},
		mainShadow:  map[int][]Run{},
		subs:        map[int]chan WSMessage{},
		initialCols: DefaultCols,
	}
}

// SetInitialSize は Start 前に client viewport 由来の cols/altRows を伝える。
// PTY を最初から正しいサイズで起動して、Claude TUI の cursor 位置と client の
// 表示 cols がズレないようにする。Start 後に呼ぶと反映されない (Resize を使う)。
func (p *Process) SetInitialSize(cols, altRows int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if cols > 0 {
		p.initialCols = cols
	}
	if altRows > 0 {
		p.clientAltRows = altRows
	}
}

func (p *Process) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd != nil {
		return nil
	}
	cmd := exec.Command(p.command, p.args...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	if p.workDir != "" {
		cmd.Dir = p.workDir
	}
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("pty start: %w", err)
	}
	p.cmd = cmd
	p.ptmx = ptmx
	cols := p.initialCols
	if cols <= 0 {
		cols = DefaultCols
	}
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: uint16(VirtualRows()), Cols: uint16(cols)})

	p.emu = vt.NewEmulator(cols, VirtualRows())
	p.emu.SetCallbacks(vt.Callbacks{
		AltScreen:        func(on bool) { p.onAltScreenChange(on) },
		CursorVisibility: func(visible bool) { p.cursorHidden = !visible },
		EnableMode:       func(m ansi.Mode) { p.onEmuMode(m, true) },
		DisableMode:      func(m ansi.Mode) { p.onEmuMode(m, false) },
	})
	applyXtermPalette(p.emu)

	go p.readPump()
	go p.responseLoop()
	go p.flushLoop()
	return nil
}

func (p *Process) Running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cmd != nil
}

func (p *Process) SetOnExit(fn func()) {
	p.mu.Lock()
	p.onExit = fn
	p.mu.Unlock()
}

// SetOnOutput は PTY から読み取ったデータごとに呼ばれる callback を登録する。
// nil を渡せば解除。旧 terminal.Process と同じ用途 (sessions.clear_detector が
// PTY 出力を watch して UUID 切替を検知するため)。
func (p *Process) SetOnOutput(fn func([]byte)) {
	p.mu.Lock()
	p.onOutput = fn
	p.mu.Unlock()
}

// SetOnInput は client → PTY に流れた input bytes ごとに呼ばれる callback を登録する。
// sessionoverview の PTYFallback など、observer 経由で input を観測する用途。
// nil を渡せば解除。
func (p *Process) SetOnInput(fn func([]byte)) {
	p.mu.Lock()
	p.onInput = fn
	p.mu.Unlock()
}

// Write は raw bytes を PTY (stdin) へ書き込む。input 系イベントの素直な経路。
func (p *Process) Write(data []byte) error {
	p.mu.Lock()
	ptmx := p.ptmx
	cb := p.onInput
	p.mu.Unlock()
	if ptmx == nil {
		return errors.New("process not running")
	}
	if cb != nil {
		cb(data)
	}
	_, err := ptmx.Write(data)
	return err
}

// Paste は emu.Paste 経由で送る。lib が bracketed paste mode 設定 (?2004h)
// を見て自動で \x1b[200~ / \x1b[201~ で wrap してくれる。
// emu.Paste は internal pipe に書き込むので responseLoop が PTY に流す。
func (p *Process) Paste(text string) {
	p.mu.Lock()
	emu := p.emu
	p.mu.Unlock()
	if emu == nil {
		return
	}
	emu.Paste(text)
}

// Resize は cols と altRows をクライアントから受け取って反映する。
// rows は常に VirtualRows() (main-screen 時) または altRows (alt-screen 時)。
func (p *Process) Resize(cols, altRows int) error {
	if cols <= 0 {
		return nil
	}
	p.mu.Lock()
	if altRows > 0 {
		p.clientAltRows = altRows
	}
	ptyRows := uint16(VirtualRows())
	if p.altScreen {
		ar := p.clientAltRows
		if ar <= 0 {
			ar = DefaultAltRows
		}
		ptyRows = uint16(ar)
	}
	if p.ptmx != nil {
		_ = pty.Setsize(p.ptmx, &pty.Winsize{Rows: ptyRows, Cols: uint16(cols)})
	}
	if p.emu != nil {
		p.emu.Resize(cols, int(ptyRows))
	}
	p.mu.Unlock()
	return nil
}

// Stop は SIGINT で穏便に終了させ、3 秒で応答なければ Kill する。
func (p *Process) Stop() error {
	p.mu.Lock()
	ptmx := p.ptmx
	cmd := p.cmd
	p.mu.Unlock()
	if ptmx != nil {
		_ = ptmx.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Signal(os.Interrupt)
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
		}
	}
	p.cleanup()
	return nil
}

func (p *Process) cleanup() {
	p.mu.Lock()
	p.cmd = nil
	p.ptmx = nil
	p.closed = true
	fn := p.onExit
	p.onExit = nil
	p.mu.Unlock()
	if fn != nil {
		go fn()
	}
	// subscribers にも close を伝えて goroutine を抜けさせる
	p.subMu.Lock()
	for id, ch := range p.subs {
		close(ch)
		delete(p.subs, id)
	}
	p.subMu.Unlock()
}

// Subscribe は live update を受ける chan を登録する。cancel で解除。
// 解除後は ch が close される。
func (p *Process) Subscribe() (<-chan WSMessage, func()) {
	ch := make(chan WSMessage, 16)
	p.subMu.Lock()
	id := p.subID
	p.subID++
	p.subs[id] = ch
	p.subMu.Unlock()
	return ch, func() {
		p.subMu.Lock()
		if existing, ok := p.subs[id]; ok && existing == ch {
			delete(p.subs, id)
			close(ch)
		}
		p.subMu.Unlock()
	}
}

func (p *Process) broadcast(msg WSMessage) {
	p.subMu.Lock()
	defer p.subMu.Unlock()
	for _, ch := range p.subs {
		// 詰まったら drop (subscriber が遅いだけで Process 全体を止めない)
		select {
		case ch <- msg:
		default:
		}
	}
}

// Snapshot は現状の grid 全行 + cursor + mode を 1 メッセージにまとめて返す。
// 新規接続時の init や、再 attach のために使う。
func (p *Process) Snapshot() WSMessage {
	p.mu.Lock()
	// 購読者ゼロの間は flushLoop の sweep が間引かれ (idleFlushDecimation)、
	// mainShadow / lastSent が最大 ~1s 古い。接続時 snapshot が常に現在の grid
	// を映すよう、ここで 1 tick ぶんの sweep をしてから組み立てる。進んだ diff
	// は既存購読者へ broadcast する (流さないと lastSent だけが進み、既存購読者
	// がその行の更新を取りこぼす)。syncUpdate 中と reset 後 debounce 窓内は
	// flushLoop と同じく sweep しない (中途 frame 回避)。
	var lines []LineUpdate
	if !p.syncUpdate && time.Since(p.syncUpdateLastReset) >= syncUpdateDebounce && !p.closed && p.emu != nil {
		lines = p.sweepLocked()
	}
	cx, cy, ch := p.cursorX, p.cursorY, p.cursorHidden
	msg := p.buildSnapshot("init")
	p.mu.Unlock()
	if len(lines) > 0 {
		p.broadcast(WSMessage{
			Type:         "update",
			Lines:        lines,
			CursorX:      cx,
			CursorY:      cy,
			CursorHidden: ch,
		})
	}
	return msg
}

// buildSnapshot は mu 保持前提。type を呼び出し側が指定 (init/snapshot)。
func (p *Process) buildSnapshot(typ string) WSMessage {
	mode := "main"
	if p.altScreen {
		mode = "alt"
	}
	all := make([]LineUpdate, 0)
	cols := 0
	if p.emu != nil {
		cols = p.emu.Width()
	}
	if p.altScreen {
		// alt-screen: 行数は emu.Height() (≈ viewport 行数) なのでフルスキャンで問題ない。
		rowsActive := 0
		if p.emu != nil {
			rowsActive = p.emu.Height()
		}
		for y := 0; y < rowsActive; y++ {
			runs := p.snapshotLine(y)
			if len(runs) > 0 {
				all = append(all, LineUpdate{Y: y, Runs: runs})
			}
		}
	} else {
		// main-screen: emulator の VirtualRows() (5000 行) × cols のフルスキャンは
		// 実出力行数と無関係に重く、リロード時の全タブ同時接続で 1 件 100ms 超になる。
		// flushLoop が常に最新化している mainShadow (= lastSent と同内容) から
		// O(実出力行数) で詰める。snapshot は最大 flush tick 1 回分 (100ms) 古く
		// なるが、以降の update diff が lastSent 基準のため client 状態は正しく
		// 追いつく (むしろ snapshot と update の基準が一致する)。
		ys := make([]int, 0, len(p.mainShadow))
		for y := range p.mainShadow {
			ys = append(ys, y)
		}
		sort.Ints(ys)
		for _, y := range ys {
			all = append(all, LineUpdate{Y: y, Runs: p.mainShadow[y]})
		}
	}
	ar := p.clientAltRows
	if ar <= 0 {
		ar = DefaultAltRows
	}
	return WSMessage{
		Type:         typ,
		Lines:        all,
		CursorX:      p.cursorX,
		CursorY:      p.cursorY,
		CursorHidden: p.cursorHidden,
		Cols:         cols,
		Rows:         VirtualRows(),
		Mode:         mode,
		AltRows:      ar,
	}
}

// readPump: PTY → Emulator。emu.Write が同期で ANSI parse して grid を更新。
func (p *Process) readPump() {
	// p.ptmx は cleanup() が mu 下で nil 化するため、ここでもロック下に捕捉してから使う
	// （フィールドへの read/write 競合回避）。以後はローカルの ptmx を使い続ける。
	p.mu.Lock()
	ptmx := p.ptmx
	p.mu.Unlock()
	if ptmx == nil {
		return
	}
	r := bufio.NewReader(ptmx)
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			p.mu.Lock()
			emu := p.emu
			if emu != nil {
				_, _ = emu.Write(buf[:n])
				pos := emu.CursorPosition()
				p.cursorX, p.cursorY = pos.X, pos.Y
			}
			cb := p.onOutput
			p.mu.Unlock()
			if cb != nil {
				// raw bytes をそのまま渡す (sessions の clear_detector が
				// ANSI sequence や UUID 文字列を pattern match するため)。
				cp := make([]byte, n)
				copy(cp, buf[:n])
				cb(cp)
			}
		}
		if err != nil {
			// io.EOF を含め noisy log を避けるため握りつぶす (Stop 経由で cleanup される)
			p.cleanup()
			return
		}
	}
}

// responseLoop: emu の internal pipe (DA1/DA2/DSR 等の応答) を PTY に戻す。
// これがないと vim 等が DA1 応答待ちで永久 hang。s.mu を握らないこと:
// readPump (mu 保持中) → emu.Write が pipe full でブロック → ここで mu
// を取ろうとするとデッドロック。p.ptmx は cleanup() が mu 下で nil 化するため、ここでロック下に捕捉する。
func (p *Process) responseLoop() {
	p.mu.Lock()
	emu := p.emu
	ptmx := p.ptmx
	p.mu.Unlock()
	if emu == nil {
		return
	}
	buf := make([]byte, 1024)
	for {
		n, err := emu.Read(buf)
		if n > 0 && ptmx != nil {
			_, _ = ptmx.Write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// onEmuMode は emulator の EnableMode / DisableMode callback から呼ばれる
// (readPump の mu 保持中)。Synchronized Output Mode (DEC 2026) のみ拾って
// flushLoop の broadcast を pause/resume する。それ以外の mode は無視。
//
// 残置理由は Process.syncUpdate のコメント参照 (OSC fix が根本対策、本機構は
// 別経路の中途 frame に備えた保険)。
func (p *Process) onEmuMode(mode ansi.Mode, enabled bool) {
	if mode == ansi.ModeSynchronizedOutput {
		p.syncUpdate = enabled
		if !enabled {
			p.syncUpdateLastReset = time.Now()
		}
	}
}

// onAltScreenChange は emulator から呼ばれる (readPump の mu 保持中)。
// mode 切替 + emulator resize + grid 復元 (mainShadow) を atomic に行う。
func (p *Process) onAltScreenChange(on bool) {
	p.altScreen = on
	ar := p.clientAltRows
	if ar <= 0 {
		ar = DefaultAltRows
	}
	w := p.emu.Width()

	var newHeight, newPTYRows uint16
	if on {
		newHeight = uint16(ar)
		newPTYRows = uint16(ar)
	} else {
		newHeight = uint16(VirtualRows())
		newPTYRows = uint16(VirtualRows())
	}
	p.emu.Resize(w, int(newHeight))
	if p.ptmx != nil {
		_ = pty.Setsize(p.ptmx, &pty.Winsize{Rows: newPTYRows, Cols: uint16(w)})
	}

	mode := "main"
	if on {
		mode = "alt"
	}

	// 復帰時は mainShadow から grid を復元 → snapshot lines に詰める
	var snapLines []LineUpdate
	p.lastSent = map[int]string{}
	if on {
		for y := 0; y < int(newHeight); y++ {
			runs := p.snapshotLine(y)
			p.lastSent[y] = runsKey(runs)
			if len(runs) > 0 {
				snapLines = append(snapLines, LineUpdate{Y: y, Runs: runs})
			}
		}
	} else {
		snapLines = make([]LineUpdate, 0, len(p.mainShadow))
		for y, runs := range p.mainShadow {
			if len(runs) == 0 {
				continue
			}
			snapLines = append(snapLines, LineUpdate{Y: y, Runs: runs})
			p.lastSent[y] = runsKey(runs)
		}
	}

	msg := WSMessage{
		Type:         "snapshot",
		Mode:         mode,
		AltRows:      ar,
		Lines:        snapLines,
		CursorX:      p.cursorX,
		CursorY:      p.cursorY,
		CursorHidden: p.cursorHidden,
	}
	go p.broadcast(msg)
}
