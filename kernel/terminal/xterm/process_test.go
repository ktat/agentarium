package xterm

import (
	"bytes"
	"testing"
	"time"
)

// waitFor は cond が true になるまで最大 d 待つ（PTY 出力の非同期性に対処）。
func waitFor(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

func TestProcess_StartRunningStop(t *testing.T) {
	p := NewProcess("", "cat") // cat は stdin を待って起動し続ける
	if err := p.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if !p.Running() {
		t.Fatal("want running after start")
	}
	if err := p.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if !waitFor(time.Second, func() bool { return !p.Running() }) {
		t.Fatal("want not running after stop")
	}
}

func TestProcess_WriteEchoesToReplayBuffer(t *testing.T) {
	p := NewProcess("", "cat") // PTY は入力をエコーするので replay に現れる
	if err := p.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()
	if err := p.Write([]byte("hello\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !waitFor(time.Second, func() bool {
		return bytes.Contains(p.ReplayBuffer(), []byte("hello"))
	}) {
		t.Fatalf("replay buffer missing echo: %q", p.ReplayBuffer())
	}
}

func TestProcess_OnExitCalled(t *testing.T) {
	p := NewProcess("", "true") // 即座に終了する
	called := make(chan struct{}, 1)
	p.SetOnExit(func() { called <- struct{}{} })
	if err := p.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("onExit not called within timeout")
	}
}

func TestProcess_WriteNotRunning(t *testing.T) {
	p := NewProcess("", "cat")
	if err := p.Write([]byte("x")); err == nil {
		t.Fatal("want error writing to non-started process")
	}
}
