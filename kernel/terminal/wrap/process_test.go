package wrap

import (
	"strings"
	"testing"
	"time"
)

// TestProcess_BashEcho は bash で `echo hello\n` を流したとき、grid に hello が
// 反映されることを確認するスモークテスト。VT emulator が ANSI を喰って
// snapshot が runs として取り出せるところまでを通しで確認する。
func TestProcess_BashEcho(t *testing.T) {
	p := NewProcess("", "bash")
	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop()

	// bash が起動して prompt を出すまで待つ (PS1 描画後でないと書き込み順が乱れる)
	time.Sleep(300 * time.Millisecond)
	if err := p.Write([]byte("echo hello-wrap-test\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// readPump + emu.Write が反映されるまで少し待つ
	time.Sleep(300 * time.Millisecond)

	snap := p.Snapshot()
	if snap.Type != "init" {
		t.Errorf("snapshot type = %q, want init", snap.Type)
	}
	found := false
	for _, ln := range snap.Lines {
		for _, r := range ln.Runs {
			if strings.Contains(r.T, "hello-wrap-test") {
				found = true
			}
		}
	}
	if !found {
		// bash echo の結果と prompt の組み合わせは distro 差があるため
		// debug 用にダンプして失敗させる
		t.Logf("snapshot lines:")
		for _, ln := range snap.Lines {
			var sb strings.Builder
			for _, r := range ln.Runs {
				sb.WriteString(r.T)
			}
			t.Logf("  y=%d %q", ln.Y, sb.String())
		}
		t.Errorf("expected 'hello-wrap-test' in snapshot, not found")
	}
}

// TestProcess_CursorHidden は DECTCEM (\x1b[?25l / \x1b[?25h) で snapshot の
// cursorHidden が切り替わることを確認する。client はこのフラグでブロック
// カーソルの表示/非表示を制御する (Claude TUI はカーソルを隠して独自描画する)。
func TestProcess_CursorHidden(t *testing.T) {
	p := NewProcess("", "bash")
	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop()

	time.Sleep(300 * time.Millisecond)
	if snap := p.Snapshot(); snap.CursorHidden {
		t.Errorf("initial snapshot: cursorHidden = true, want false")
	}

	if err := p.Write([]byte("printf '\\033[?25l'\n")); err != nil {
		t.Fatalf("Write hide: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if snap := p.Snapshot(); !snap.CursorHidden {
		t.Errorf("after DECTCEM reset: cursorHidden = false, want true")
	}

	if err := p.Write([]byte("printf '\\033[?25h'\n")); err != nil {
		t.Fatalf("Write show: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if snap := p.Snapshot(); snap.CursorHidden {
		t.Errorf("after DECTCEM set: cursorHidden = true, want false")
	}
}

func TestProcess_StartStop(t *testing.T) {
	p := NewProcess("", "bash")
	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !p.Running() {
		t.Errorf("expected Running after Start")
	}
	if err := p.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestProcess_BroadcastSurvivesCancelRace(t *testing.T) {
	// subscribe/cancel を高速に繰り返しつつ broadcast を打つ。
	// 旧コード (subMu 解放後の送信) では send-on-closed-channel panic で落ちる。
	p := NewProcess("", "cat")
	if err := p.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = p.Stop() }()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 500; i++ {
			_, cancel := p.Subscribe()
			cancel()
		}
		close(done)
	}()
	// 同時に broadcast を打つ。Stop と race して subscribers の close が起きる。
	for i := 0; i < 500; i++ {
		p.broadcast(WSMessage{Type: "update"})
	}
	<-done
}
