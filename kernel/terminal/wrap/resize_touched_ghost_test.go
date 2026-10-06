package wrap

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/vt"
)

// このファイルは「emulator の Touched マークが外部要因 (emu.Resize) で破棄され、
// sweep の帳簿 (lastSent / mainShadow) が grid と乖離したまま固定される」経路の
// 回帰テスト。乖離した行は grid 上に存在しないのに init snapshot と update に
// 流れ続けるため、以下では幽霊行 (ghost) と呼ぶ。
//
// 成立条件:
//  1. 子がカーソルより下の行に内容を書く (TUI の入力ボックス枠線に相当)。
//     100ms tick の sweepLocked がそれを mainShadow / lastSent に写す。
//  2. 子がその行を消し、上部へ再描画する (SIGWINCH reflow / resume 再描画 /
//     compact に相当)。行が消えたことは emulator の Touched マークにしか現れず、
//     次の sweep 待ち。
//  3. sweep が走る前に Touched マークが破棄される。vt の Screen.Resize は
//     寸法が同じでも buf.Touched = nil で全マークを捨てる。
//  4. 以後 sweepLocked (flush.go, main 分岐) は Touched ベースでしか行を見ない
//     ため、消去は永久に観測されない。子のその後の描画は上部にしか触れないので、
//     消えた行のマークは二度と作られない。
//
// 対策は 2 段構え:
//   - 寸法が変わらない resize では emu.Resize を呼ばない (マークを失わない)
//   - 寸法が実際に変わる resize と alt 復帰の直後は全行照合の resweep を 1 回
//     走らせ、Touched に頼らず帳簿を grid へ追従させる
//
// 各テストがどちらを守っているかはコメントに明記する (片方だけを revert した
// ときにどのテストが落ちるべきかの目安)。

// withVirtualRows はテスト中だけ VirtualRows() を上書きする。
func withVirtualRows(t testing.TB, n int) {
	t.Helper()
	orig := virtualRows.Load()
	t.Cleanup(func() { virtualRows.Store(orig) })
	SetVirtualRows(n)
}

// touchedAt は emulator の y 行に Touched マークが立っているかを返す。
func touchedAt(emu *vt.Emulator, y int) bool {
	touched := emu.Touched()
	return y < len(touched) && touched[y] != nil
}

// writeLocked は emulator へ書き込み、readPump と同じくカーソル位置を写す
// (p.mu 保持前提)。
func (p *Process) writeLocked(s string) {
	_, _ = p.emu.Write([]byte(s))
	pos := p.emu.CursorPosition()
	p.cursorX, p.cursorY = pos.X, pos.Y
}

// newGhostSetupProcess は上記 1〜2 まで進めた Process を返す。すなわち
// 「y=300..304 の 90 桁の枠線が帳簿に写っており、grid からは消して y=99 に
// 新しい入力行を描いたが、まだ sweep していない」状態。
func newGhostSetupProcess(t *testing.T) *Process {
	t.Helper()
	withVirtualRows(t, 500)
	p := NewProcess("", "true")
	p.mu.Lock()
	defer p.mu.Unlock()
	p.emu = vt.NewEmulator(90, VirtualRows())
	p.altScreen = false

	// 1. 子が y=299..303 に 90 桁弱の枠線を書く。
	p.writeLocked("\x1b[300;1H")
	for i := 1; i <= 5; i++ {
		p.writeLocked(fmt.Sprintf("OLDBOX-%d-%s\r\n", i, strings.Repeat("─", 80)))
	}
	p.sweepLocked() // flushLoop 1 tick: mainShadow に枠線が写る
	if !strings.Contains(runsText(p.mainShadow[299]), "OLDBOX-1") {
		t.Fatal("前提: sweep 後の mainShadow[299] に枠線が写っていない")
	}

	// 2. 子が y=299 以降を消し、y=99 に新しい入力行を描く (DEC 2026 内)。
	p.writeLocked("\x1b[?2026h\x1b[300;1H\x1b[J\x1b[100;1H\x1b[J❯ LIVE typed\x1b[?2026l")
	if !touchedAt(p.emu, 299) || !touchedAt(p.emu, 99) {
		t.Fatal("前提: 消去 / 再描画直後の行に Touched マークが無い")
	}
	return p
}

// runsText は runs の本文を連結して返す。
func runsText(runs []Run) string {
	var b strings.Builder
	for _, r := range runs {
		b.WriteString(r.T)
	}
	return b.String()
}

// assertNoGhostLocked は init snapshot について「カーソルより下に行が無い」
// 「y=99 の入力行が載っている」「cols を超える幅の行が無い」ことを検証する
// (p.mu 保持前提)。
func assertNoGhostLocked(t *testing.T, p *Process) {
	t.Helper()
	snap := p.buildSnapshot("init")
	cols := p.emu.Width()
	live := false
	for _, lu := range snap.Lines {
		text := runsText(lu.Runs)
		if lu.Y > snap.CursorY {
			t.Errorf("カーソル (y=%d) より下に幽霊行が残った: y=%d %q", snap.CursorY, lu.Y, text)
		}
		if n := len([]rune(text)); n > cols {
			t.Errorf("y=%d の行幅 %d が cols=%d を超えている", lu.Y, n, cols)
		}
		if lu.Y == 99 && strings.Contains(text, "LIVE typed") {
			live = true
		}
	}
	if !live {
		t.Error("init snapshot に y=99 の新しい入力行が載っていない")
	}
	for y := 299; y <= 303; y++ {
		if s := p.lastSent[y]; s != "" {
			t.Errorf("lastSent[%d] に幽霊行が残った: %q", y, s)
		}
	}
}

// 寸法が変わらない resize (WS 接続・リロード・ResizeObserver 由来) が Touched
// マークを破棄しないこと。これが守られている限り、消去は次の sweep が普通に
// 観測するので幽霊行は生まれない。
//
// 【守っている対策】「寸法不変なら emu.Resize を呼ばない」。マークの生存を
// 直接見るので、全行 resweep 側が入っていても症状が隠れない。
func TestClientResize_sameSize_keepsTouchedMarks(t *testing.T) {
	p := newGhostSetupProcess(t)

	// cols は変えない。main 画面では altRows は実効高さに影響しないので、
	// altRows だけ変わっても寸法不変。
	if err := p.Resize(90, 50); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if !touchedAt(p.emu, 299) || !touchedAt(p.emu, 99) {
		t.Error("寸法不変の resize で Touched マークが破棄された")
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.sweepLocked()
	assertNoGhostLocked(t, p)
}

// 寸法不変の resize でも clientAltRows は更新されること (次に alt 画面へ
// 入るときに新しい値を使うため)。
func TestClientResize_sameSize_updatesClientAltRows(t *testing.T) {
	p := newGhostSetupProcess(t)
	_ = p.Resize(90, 33)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.clientAltRows != 33 {
		t.Errorf("clientAltRows = %d, want 33", p.clientAltRows)
	}
}

// vt の Screen.Resize が「寸法が変わらない resize でも」Touched マークを
// 破棄することを直接検証する (寸法不変スキップが必要な理由そのものの監視)。
// lib がこれを改めたら寸法不変スキップは無害な最適化に格下げされる。
func TestEmulatorResize_sameSize_dropsTouchedMarks(t *testing.T) {
	emu := vt.NewEmulator(40, 50)
	_, _ = emu.Write([]byte("hello"))
	if countTouchedLines(emu) == 0 {
		t.Fatal("前提: 書き込み後に Touched マークがない")
	}
	emu.Resize(40, 50) // 同一寸法
	if n := countTouchedLines(emu); n != 0 {
		t.Fatalf("同一寸法の Resize 後も Touched が %d 行残っている (lib の挙動が変わった)", n)
	}
}

// 寸法が実際に変わる resize (90 → 72 桁) では Touched マークを失うことを
// 避けられないので、全行照合の resweep が帳簿を grid へ追従させること。
// 差分は update として購読者にも流れること。
//
// 【守っている対策】「Touched 破棄直後の全行 resweep」。寸法が変わるので
// 「寸法不変なら呼ばない」側は効かず、症状を隠せない。
func TestClientResize_dimensionChange_resweepsGhostAway(t *testing.T) {
	p := newGhostSetupProcess(t)

	sub, cancel := p.Subscribe()
	defer cancel()

	if err := p.Resize(72, 50); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if touchedAt(p.emu, 299) {
		t.Fatal("前提: 寸法が変わる resize でも Touched が残っている (lib の挙動が変わった)")
	}

	// broadcast は Resize の中で (ロック解放後に) 同期で行われるため、
	// この時点で channel に積まれている。
	cleared, live := false, false
drain:
	for {
		select {
		case msg, ok := <-sub:
			if !ok {
				break drain
			}
			for _, lu := range msg.Lines {
				if lu.Y == 299 && len(lu.Runs) == 0 {
					cleared = true
				}
				if lu.Y == 99 && strings.Contains(runsText(lu.Runs), "LIVE typed") {
					live = true
				}
			}
		default:
			break drain
		}
	}
	if !cleared {
		t.Error("resize 後の update に「y=299 が空になった」差分が流れていない")
	}
	if !live {
		t.Error("resize 後の update に y=99 の新しい入力行が流れていない")
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.sweepLocked() // 子はもう何も描かない (Touched 無し)
	assertNoGhostLocked(t, p)
}

// resweep の空白行ショートカット (blankRowLocked) が、背景色付きの空白だけの
// 行を「空」と誤判定しないこと。snapshotLine は bg 付きの空白 run を残すので、
// ショートカットで飛ばすと送るべき行を落とす。
//
// 【守っている対策】resweepAllRowsLocked の空白判定がスタイルを見ること。
func TestResweep_keepsBackgroundOnlyRows(t *testing.T) {
	withVirtualRows(t, 500)
	p := NewProcess("", "true")
	p.mu.Lock()
	defer p.mu.Unlock()
	p.emu = vt.NewEmulator(40, VirtualRows())
	p.altScreen = false
	p.writeLocked("\x1b[10;1H\x1b[44m    \x1b[0m") // y=9 に青背景の空白だけ

	lines := p.resweepAllRowsLocked(false)
	found := false
	for _, lu := range lines {
		if lu.Y == 9 && len(lu.Runs) > 0 {
			found = true
		}
	}
	if !found {
		t.Errorf("背景色付きの空白行 (y=9) が resweep の差分に載っていない: %+v", lines)
	}
}

// alt 画面の出入りも emu.Resize 経由で Touched マークを破棄する。alt 突入前に
// 書かれたが sweep 前だった main 行は、Touched も mainShadow も持たないため、
// 復帰後に帳簿へ回収する経路が全行照合しか無い (回収できないと、grid には
// 実在するのにクライアントが知らない「逆向きの幽霊」になる)。
//
// 【守っている対策】「Touched 破棄直後の全行 resweep」(alt 復帰)。
func TestAltScreenExit_recoversUnsweptGridRows(t *testing.T) {
	withVirtualRows(t, 500)
	p := NewProcess("", "true")
	p.mu.Lock()
	p.emu = vt.NewEmulator(40, VirtualRows())
	p.altScreen = false
	p.mu.Unlock()
	p.emu.SetCallbacks(vt.Callbacks{AltScreen: func(on bool) { p.onAltScreenChange(on) }})

	p.mu.Lock()
	defer p.mu.Unlock()
	// sweep しないまま alt へ入る (mainShadow は空のまま)。
	p.writeLocked("row0\r\nrow1\r\nrow2")
	if len(p.mainShadow) != 0 {
		t.Fatalf("前提: sweep 前なのに mainShadow に %d 行ある", len(p.mainShadow))
	}
	p.writeLocked("\x1b[?1049h")
	p.writeLocked("\x1b[?1049l")

	// 子は復帰後に再描画しない (Touched は無い)。
	p.sweepLocked()
	for y, want := range []string{"row0", "row1", "row2"} {
		if got := runsText(p.mainShadow[y]); !strings.Contains(got, want) {
			t.Errorf("alt 復帰後に main 行 %d が帳簿へ回収されていない: %q", y, got)
		}
	}
}

// alt 復帰時、grid が空の行は mainShadow で補うこと。alt 突入時の emu.Resize
// が main grid を altRows 行に切り詰めるため、grid だけを正とすると復元源の
// mainShadow ごと消してしまう。
//
// 【守っている対策】alt 復帰時 resweep の mainShadow fallback。
func TestAltScreenExit_keepsTruncatedRowsFromShadow(t *testing.T) {
	withVirtualRows(t, 500)
	p := NewProcess("", "true")
	p.mu.Lock()
	p.emu = vt.NewEmulator(40, VirtualRows())
	p.altScreen = false
	p.mu.Unlock()
	p.emu.SetCallbacks(vt.Callbacks{AltScreen: func(on bool) { p.onAltScreenChange(on) }})

	p.mu.Lock()
	defer p.mu.Unlock()
	p.writeLocked("\x1b[200;1Hdeep row")
	p.sweepLocked()
	p.writeLocked("\x1b[?1049h") // DefaultAltRows=40 に切り詰め → y=199 は grid から消える
	p.writeLocked("\x1b[?1049l")
	p.sweepLocked()
	if got := runsText(p.mainShadow[199]); !strings.Contains(got, "deep row") {
		t.Errorf("alt 往復で切り詰められた行が mainShadow から消えた: %q", got)
	}
	if p.lastSent[199] == "" {
		t.Error("alt 復帰後の lastSent[199] が空 (クライアントへの再送と帳簿が不一致)")
	}
}

// 全行照合 1 回のコスト。resize のたびに O(h) × snapshotLine が掛かるため、
// 既定 VirtualRows (5000 行) × 158 桁で測る。
//   - Full:  全行に内容 (最悪ケース)
//   - Typical: 先頭 500 行だけに内容 (残りは空行。実セッションに近い)
func BenchmarkResweepAllRowsLocked(b *testing.B) {
	for _, tc := range []struct {
		name   string
		filled int
	}{
		{"Full5000", defaultVirtualRows},
		{"Filled500of5000", 500},
	} {
		b.Run(tc.name, func(b *testing.B) {
			withVirtualRows(b, defaultVirtualRows)
			p := NewProcess("", "true")
			p.mu.Lock()
			defer p.mu.Unlock()
			p.emu = vt.NewEmulator(158, VirtualRows())
			p.altScreen = false
			for i := 0; i < tc.filled; i++ {
				p.writeLocked(fmt.Sprintf("\x1b[%d;1HL%d 0123456789 abcdefghij ─────────────", i+1, i))
			}
			p.sweepLocked()
			b.ReportAllocs()
			for b.Loop() {
				_ = p.resweepAllRowsLocked(false)
			}
		})
	}
}
