package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ktat/agentarium/kernel/terminal"
)

// 任意 IF の実装をコンパイル時に保証する。
var (
	_ terminal.Agent           = Agent{}
	_ terminal.ResumableAgent  = Agent{}
	_ terminal.SessionDetector = Agent{}
	_ terminal.StateAware      = Agent{}
)

func TestName(t *testing.T) {
	if got := New().Name(); got != "claude" {
		t.Fatalf("Name() = %q, want %q", got, "claude")
	}
}

func TestInvocation(t *testing.T) {
	bin, args := New().Invocation(terminal.RunRequest{
		Model:       "opus",
		Resume:      "sess-1",
		SessionName: "demo",
	})
	if bin != "claude" {
		t.Fatalf("binary = %q, want %q", bin, "claude")
	}
	want := []string{"--model", "opus", "--resume", "sess-1", "-n", "demo"}
	if strings.Join(args, " ") != strings.Join(want, " ") {
		t.Fatalf("args = %v, want %v", args, want)
	}
}

func TestInvocationEmpty(t *testing.T) {
	bin, args := New().Invocation(terminal.RunRequest{})
	if bin != "claude" {
		t.Fatalf("binary = %q, want %q", bin, "claude")
	}
	if len(args) != 0 {
		t.Fatalf("args = %v, want empty", args)
	}
}

func TestResumeArtifact(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("UserHomeDir unavailable")
	}
	got := New().ResumeArtifact("/tmp/work", "sess-1")
	if !strings.HasSuffix(got, "sess-1.jsonl") {
		t.Fatalf("ResumeArtifact should end with <sessionID>.jsonl: %s", got)
	}
	if !strings.HasPrefix(got, filepath.Join(home, ".claude", "projects")) {
		t.Fatalf("ResumeArtifact should be under ~/.claude/projects: %s", got)
	}
	if New().ResumeArtifact("/tmp/work", "") != "" {
		t.Fatal("empty sessionID should return empty path")
	}
}

func TestStatePatterns(t *testing.T) {
	p := New().StatePatterns()
	if p.Permission == nil || !p.Permission.MatchString("Do you want to proceed?") {
		t.Fatal("Permission should match claude 許可プロンプト")
	}
	if p.SustainedRunning != 2*time.Second {
		t.Fatalf("SustainedRunning = %v, want 2s", p.SustainedRunning)
	}
	if p.IdleTimeout != 1500*time.Millisecond {
		t.Fatalf("IdleTimeout = %v, want 1500ms", p.IdleTimeout)
	}
	if p.BurstGap != time.Second {
		t.Fatalf("BurstGap = %v, want 1s", p.BurstGap)
	}
}
