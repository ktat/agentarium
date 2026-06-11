package wrap

import (
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
)

// newFlushTestProcess は Start (実 PTY) を経由せずに flushLoop を検証できる
// Process を組み立てる。emu へ直接書き込んだ内容が sweep 対象になる。
func newFlushTestProcess(t *testing.T) *Process {
	t.Helper()
	p := NewProcess("", "true")
	emu := vt.NewEmulator(80, 24)
	p.mu.Lock()
	p.emu = emu
	p.altScreen = true // alt 経路 (全行 sweep) を使う。Claude TUI と同じ
	p.clientAltRows = 24
	p.mu.Unlock()
	t.Cleanup(func() {
		p.mu.Lock()
		p.closed = true
		p.mu.Unlock()
	})
	return p
}

func (p *Process) lastSentLen() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.lastSent)
}

// 購読者ゼロの間は sweep が間引かれる (最初の数 tick では走らない) こと。
func TestFlushLoop_idleSkipsSweepWhileUnsubscribed(t *testing.T) {
	p := newFlushTestProcess(t)
	_, _ = p.emu.Write([]byte("hello"))
	go p.flushLoop()

	// 100ms tick × 3 回ぶん待つ。間引き (idleFlushDecimation=10) 中なので
	// lastSent は未更新のはず。間引きが無いと tick 1 (≈100ms) で更新される。
	time.Sleep(350 * time.Millisecond)
	if n := p.lastSentLen(); n != 0 {
		t.Fatalf("expected no sweep while unsubscribed (within decimation window), lastSent has %d entries", n)
	}
}

// 購読者が付いたら次 tick からフルレート (≤100ms) で update が届くこと。
func TestFlushLoop_resumesFullRateOnSubscribe(t *testing.T) {
	p := newFlushTestProcess(t)
	_, _ = p.emu.Write([]byte("hello"))
	go p.flushLoop()

	time.Sleep(350 * time.Millisecond) // 間引き中 (sweep 未実行) の状態を作る
	ch, cancel := p.Subscribe()
	defer cancel()

	select {
	case msg := <-ch:
		if msg.Type != "update" {
			t.Fatalf("want update, got %q", msg.Type)
		}
		if len(msg.Lines) == 0 {
			t.Fatal("update has no lines")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("no update within 1s after subscribe")
	}
}

// 購読者ゼロでも低頻度 (≈1s 周期) で sweep は走り続け、
// lastSent / mainShadow の鮮度が保たれること。
func TestFlushLoop_idleStillSweepsAtLowRate(t *testing.T) {
	p := newFlushTestProcess(t)
	_, _ = p.emu.Write([]byte("hello"))
	go p.flushLoop()

	// idleFlushDecimation=10 × 100ms = 1s 周期。1.5s 待てば 1 回は走る。
	time.Sleep(1500 * time.Millisecond)
	if n := p.lastSentLen(); n == 0 {
		t.Fatal("expected low-rate sweep to refresh lastSent within ~1.5s while unsubscribed")
	}
}
