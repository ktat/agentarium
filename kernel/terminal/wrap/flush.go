package wrap

import (
	"time"
)

// このファイルは Process の flush パイプライン (100ms tick の差分 sweep と
// broadcast) を実装する。描画ヘルパ (snapshotLine / colorHex 等) は render.go、
// PTY / emulator の lifecycle は process.go を参照。

// idleFlushDecimation: 購読者ゼロの Process が sweep を行う間隔 (tick 数)。
// 100ms tick × 10 = 実効 1Hz。
const idleFlushDecimation = 10

func (p *Process) hasSubscribers() bool {
	p.subMu.Lock()
	defer p.subMu.Unlock()
	return len(p.subs) > 0
}

// flushLoop: 100ms ごとに差分を broadcast。
//
//	alt-screen 中: 全行 sweep (lib Touched が less `>` 等を mark し損ねる回避)
//	main-screen: Touched() + lastSent diff (5000 行毎回 sweep は重い)
//
// 購読者 (Claude タブ / AgentsView の WS) がいない Process は sweep を
// idleFlushDecimation tick に 1 回へ間引く。snapshotLine の全行 sweep が
// CPU の支配項であり、broadcast 先が無い間に毎 tick 回す意味がないため。
// ゼロにせず低頻度で回し続けるのは、buildSnapshot / onAltScreenChange が
// 参照する lastSent / mainShadow の鮮度を最大 ~1s に保ち、再購読時の表示が
// 古くならないようにするため。購読が付けば次 tick からフルレートに戻る。
// 判定は p.mu 外で行うため、判定直後に subscribe された tick だけは取りこぼし
// 最大 1 tick (100ms) 余分に遅れるが、接続時は Snapshot が sweep 済みの最新
// grid を返すため表示の古さにはならず、厳密化 (kick 配線や lock 内判定) の
// 複雑さに見合わないと判断して許容する。
func (p *Process) flushLoop() {
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	// lastCX/lastCY/lastCH: 最後に broadcast したカーソル状態。行変更が無い tick
	// でもカーソルだけ動いたら (シェルでの矢印キー移動や DECTCEM 切替) update を
	// 送るための比較基準。
	lastCX, lastCY, lastCH, idleTick := 0, 0, false, 0
	for range t.C {
		// 間引き判定は p.mu の外 (subMu のみ) で行う。間引かれた tick は
		// closed 判定もスキップするため、購読者ゼロで閉じた Process の
		// goroutine 終了は最大 ~1s 遅れるが、その間 sweep はしないので無害。
		if !p.hasSubscribers() {
			idleTick++
			if idleTick%idleFlushDecimation != 0 {
				continue
			}
		} else {
			idleTick = 0
		}
		p.mu.Lock()
		if p.closed || p.emu == nil {
			p.mu.Unlock()
			return
		}
		// Synchronized Output Mode (DEC 2026) 中は client に途中 frame を送らない。
		// claude TUI は \x1b[?2026h ... \x1b[?2026l を ~33ms 周期で繰り返すため、
		// 単純な `if syncUpdate` だけでは tick 瞬間に偶然 false になった中途 grid を
		// 流してしまう。reset 後 syncUpdateDebounce (~80ms) 連続して false を保てた
		// tick でだけ flush することで、最終 frame のみ送る挙動になる。
		// (本来追っていた session 名残骸は OSC fix で消えた。詳細は Process.syncUpdate
		// のコメント参照。)
		if p.syncUpdate || time.Since(p.syncUpdateLastReset) < syncUpdateDebounce {
			p.mu.Unlock()
			continue
		}
		lines := p.sweepLocked()
		cx, cy, ch := p.cursorX, p.cursorY, p.cursorHidden
		p.mu.Unlock()
		if len(lines) == 0 && cx == lastCX && cy == lastCY && ch == lastCH {
			continue
		}
		lastCX, lastCY, lastCH = cx, cy, ch
		p.broadcast(WSMessage{
			Type:         "update",
			Lines:        lines,
			CursorX:      cx,
			CursorY:      cy,
			CursorHidden: ch,
		})
	}
}

// sweepLocked は flushLoop 1 tick ぶんの差分 sweep を行う (p.mu 保持前提)。
// 変更行の lastSent / mainShadow を更新し、broadcast すべき行を返す。
// flushLoop のほか、Snapshot が接続時に最新状態を映すためにも呼ぶ。
func (p *Process) sweepLocked() []LineUpdate {
	var lines []LineUpdate
	if p.altScreen {
		ar := p.clientAltRows
		if ar <= 0 {
			ar = DefaultAltRows
		}
		if ar > p.emu.Height() {
			ar = p.emu.Height()
		}
		for y := 0; y < ar; y++ {
			runs := p.snapshotLine(y)
			key := runsKey(runs)
			if p.lastSent[y] == key {
				continue
			}
			p.lastSent[y] = key
			lines = append(lines, LineUpdate{Y: y, Runs: runs})
		}
	} else {
		touched := p.emu.Touched()
		for y, ld := range touched {
			if ld == nil || y < 0 || y >= VirtualRows() {
				continue
			}
			// 採用した行の Touched マークをここで消費する。lib はマークを
			// resize 時にしか消さないため、消費しないと「過去に変更された全行」
			// が毎 tick 再 sweep され続け、バッファ満杯後はスクロール 1 回で
			// 全行再マークされる仕様と相まって定常 CPU の支配項になる。
			// Touched() は emulator 内部スライスをそのまま返すので、エントリを
			// nil に戻せば次の変更まで sweep 対象から外れる (次の書き込みで
			// TouchLine が LineData を作り直す)。emu への書き込み (readPump) と
			// 本関数は同じ p.mu 下で動くため、読み取り〜クリア間にマークを
			// 取りこぼすことはない。上の guard で skip した範囲外の行は消費
			// しない (VirtualRows と emulator 行数がズレた場合に「まだ送って
			// いない変更」を落とさないため。残っても skip を通るだけで無害)。
			// NOTE: lib が将来 Touched() でコピーを返すよう変わるとこの消費は
			// 静かに無効化される (壊れないが定常 sweep が全行に戻る)。その退行
			// は TestSweepLocked_consumesTouchedMarks が検知する。
			touched[y] = nil
			if lu, ok := p.syncRowLocked(y, p.snapshotLine(y)); ok {
				lines = append(lines, lu)
			}
		}
	}
	return lines
}

// syncRowLocked は main 画面の 1 行ぶんの帳簿 (lastSent / mainShadow) を runs の
// 内容で更新し、broadcast すべきなら LineUpdate と true を返す。最後に送った
// 内容と同じなら何も更新せず false。p.mu 保持前提。
func (p *Process) syncRowLocked(y int, runs []Run) (LineUpdate, bool) {
	key := runsKey(runs)
	if p.lastSent[y] == key {
		return LineUpdate{}, false
	}
	p.lastSent[y] = key
	if len(runs) == 0 {
		delete(p.mainShadow, y)
	} else {
		p.mainShadow[y] = runs
	}
	return LineUpdate{Y: y, Runs: runs}, true
}

// resweepAllRowsLocked は lastSent の帳簿と現在の grid を全行突き合わせ、
// 差分を LineUpdate として返す (p.mu 保持前提、main 画面専用)。
//
// sweepLocked の main 分岐は emulator の Touched マークだけを見るため、マークが
// 外部要因で破棄されると「grid では消えたのに帳簿には残っている行」が二度と
// sweep 対象にならない。行が消えたという事実は Touched にしか現れないので、
// マークを失った時点で差分を知る手段は全行照合しか残らない。vt の Screen.Resize
// は寸法変更のたびに Touched を無条件で全破棄するため、emu.Resize を呼んだ
// 直後 (寸法が実際に変わる Process.Resize と alt 画面からの復帰) に 1 回走らせる。
// コストは O(h × cols) で、既定 5000 行 × 158 桁のとき先頭 500 行だけに内容が
// ある典型ケースで約 7 ms、全行に内容がある最悪ケースで約 28 ms
// (BenchmarkResweepAllRowsLocked)。毎 tick の sweep より桁違いに重いが、
// 寸法が変わる resize はユーザー操作起点で、クライアントも送信を debounce
// しているため頻度は低い。
//
// fallbackToShadow は「grid が空の行を mainShadow で補うか」:
//   - false (resize 直後): grid だけが正。grid が空なら帳簿からも落とす
//   - true (alt からの復帰直後): alt 突入時の emu.Resize が main grid を
//     altRows 行に切り詰めており、それ以降の行は grid から読めない。ここで
//     grid を正とすると復元源の mainShadow ごと消してしまうため、空行に限り
//     mainShadow を残す
func (p *Process) resweepAllRowsLocked(fallbackToShadow bool) []LineUpdate {
	if p.emu == nil || p.altScreen {
		return nil
	}
	h := p.emu.Height()
	if vr := VirtualRows(); h > vr {
		h = vr
	}
	var lines []LineUpdate
	for y := 0; y < h; y++ {
		var runs []Run
		// 送信済みでない (lastSent が空) 空白行は snapshotLine しても空 runs
		// になり照合結果も変わらないので、安価な空白判定だけで済ませる。
		// 既定 5000 行のうち実出力は先頭の一部に限られることが多く、残りの
		// 空行で snapshotLine (色の hex 化・runs 組み立て) を回すのが支配項に
		// なるため。
		if p.lastSent[y] != "" || !p.blankRowLocked(y) {
			runs = p.snapshotLine(y)
		}
		if fallbackToShadow && len(runs) == 0 {
			runs = p.mainShadow[y]
		}
		if lu, ok := p.syncRowLocked(y, runs); ok {
			lines = append(lines, lu)
		}
	}
	return lines
}

// blankRowLocked は y 行が「既定スタイルの空白セルだけ」で構成されるかを返す
// (p.mu 保持前提)。true のとき snapshotLine は必ず空 runs を返す (末尾の
// 空白 run は bg 無しなら trim されるため)。bg や属性付きの空白を含む行は
// false にして snapshotLine に判定を委ねる。
func (p *Process) blankRowLocked(y int) bool {
	w := p.emu.Width()
	for x := 0; x < w; x++ {
		c := p.emu.CellAt(x, y)
		if c == nil {
			continue
		}
		// uv.Style.IsZero (構造体の == 比較) は interface 比較を伴い、5000 行 ×
		// cols の走査では支配項になるため、フィールドを個別に nil / 0 判定する。
		st := &c.Style
		if (c.Content != "" && c.Content != " ") ||
			st.Fg != nil || st.Bg != nil || st.UnderlineColor != nil ||
			st.Underline != 0 || st.Attrs != 0 {
			return false
		}
	}
	return true
}
