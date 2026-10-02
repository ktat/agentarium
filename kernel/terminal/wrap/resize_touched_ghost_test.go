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
