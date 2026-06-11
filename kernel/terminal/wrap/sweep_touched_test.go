package wrap

import (
	"testing"

	"github.com/charmbracelet/x/vt"
)

// newSweepTestProcess は Start (実 PTY) を経由せず main-screen の sweepLocked を
// 検証できる Process を組み立てる。
func newSweepTestProcess() *Process {
	p := NewProcess("", "true")
	emu := vt.NewEmulator(80, 50)
	p.mu.Lock()
	p.emu = emu
	p.altScreen = false
	p.mu.Unlock()
	return p
}

func countTouchedLines(emu *vt.Emulator) int {
	n := 0
	for _, ld := range emu.Touched() {
		if ld != nil {
			n++
		}
	}
	return n
}

func (p *Process) sweepForTest() []LineUpdate {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sweepLocked()
}

// sweepLocked (main-screen) は処理した Touched マークを消費すること。
// 消費しないと「過去に一度でも変更された全行」が毎 tick 再 sweep され続け、
// バッファ満杯後はスクロール 1 回で全行が再マークされる仕様と相まって
// 定常 CPU の支配項になる (perf 計測で確認済み)。
//
// このテストは同時に「Touched() が emulator 内部スライスをそのまま返す」
// という lib 実装への依存を監視する役割も持つ。lib がコピーを返すように
// 変わると消費が静かに無効化される (定常 sweep が全行に戻る) ため、その
// 退行をここで検知する。
func TestSweepLocked_consumesTouchedMarks(t *testing.T) {
	p := newSweepTestProcess()
	_, _ = p.emu.Write([]byte("hello\r\nworld"))
	if countTouchedLines(p.emu) == 0 {
		t.Fatal("precondition: no touched lines after write")
	}
	lines := p.sweepForTest()
	if len(lines) == 0 {
		t.Fatal("sweep returned no lines for touched content")
	}
	if n := countTouchedLines(p.emu); n != 0 {
		t.Errorf("touched marks not consumed after sweep: %d remain", n)
	}
}

// マーク消費後も新しい変更は正しく再マークされ、次の sweep で拾われること。
// 変更が無い間の sweep は走査対象ゼロ (touched なし) かつ送信もゼロであること。
func TestSweepLocked_picksUpNewChangesAfterConsume(t *testing.T) {
	p := newSweepTestProcess()
	_, _ = p.emu.Write([]byte("hello"))
	if lines := p.sweepForTest(); len(lines) == 0 {
		t.Fatal("first sweep returned no lines")
	}

	// 変更なし: 走査対象もゼロ (これが定常 CPU 削減の本体)
	if n := countTouchedLines(p.emu); n != 0 {
		t.Fatalf("touched not empty before idle sweep: %d", n)
	}
	if lines := p.sweepForTest(); len(lines) != 0 {
		t.Errorf("idle sweep returned %d lines, want 0", len(lines))
	}

	// 新しい変更 (row 1) は再マークされ、次の sweep に現れる
	_, _ = p.emu.Write([]byte("\r\nworld"))
	if countTouchedLines(p.emu) == 0 {
		t.Fatal("new write did not re-mark touched lines")
	}
	lines := p.sweepForTest()
	found := false
	for _, ln := range lines {
		if ln.Y == 1 {
			found = true
		}
	}
	if !found {
		t.Errorf("sweep after new write did not contain row 1: %+v", lines)
	}
	if n := countTouchedLines(p.emu); n != 0 {
		t.Errorf("touched marks not consumed after second sweep: %d remain", n)
	}
}

// 定常状態 (変更なし) の sweep コスト。マーク消費が無いと「過去に変更された
// 全行」を毎回 snapshotLine するため、消費の有無で桁が変わる。
func BenchmarkSweepLocked_idleAfterFill(b *testing.B) {
	p := newSweepTestProcess()
	// 全 50 行に内容を書いて全行 touched にしてから 1 回 sweep (消費)
	for i := 0; i < 49; i++ {
		_, _ = p.emu.Write([]byte("0123456789 abcdefghij 0123456789\r\n"))
	}
	_ = p.sweepForTest()
	b.ReportAllocs()
	for b.Loop() {
		_ = p.sweepForTest()
	}
}

// VirtualRows() 範囲外で sweep 対象に採用されなかった行のマークは消費しない
// こと。範囲外マークを消すと、将来 VirtualRows と emulator 行数がズレた場合に
// 「まだ送っていない変更」を取りこぼす。残しても毎 tick の skip 分岐を通る
// だけで無害。
func TestSweepLocked_keepsMarksBeyondVirtualRows(t *testing.T) {
	orig := virtualRows.Load()
	defer virtualRows.Store(orig)
	SetVirtualRows(VirtualRowsMin) // 500

	p := NewProcess("", "true")
	emu := vt.NewEmulator(80, 600) // VirtualRows より大きい emulator
	p.mu.Lock()
	p.emu = emu
	p.altScreen = false
	p.mu.Unlock()

	_, _ = p.emu.Write([]byte("inside"))             // y=0 (範囲内)
	_, _ = p.emu.Write([]byte("\x1b[551;1Houtside")) // y=550 (範囲外)
	if touched := p.emu.Touched(); len(touched) <= 550 || touched[550] == nil {
		t.Fatal("precondition: y=550 not marked touched")
	}

	_ = p.sweepForTest()

	touched := p.emu.Touched()
	if touched[0] != nil {
		t.Errorf("in-range mark (y=0) not consumed")
	}
	if len(touched) <= 550 || touched[550] == nil {
		t.Errorf("out-of-range mark (y=550) was consumed despite being skipped")
	}
}
