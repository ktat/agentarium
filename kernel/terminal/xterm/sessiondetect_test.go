package xterm

import (
	"sync"
	"testing"
	"time"

	"github.com/ktat/agentarium/kernel/terminal"
)

// detectCatAgent は cat を起動しつつ SessionDetector も満たすテスト用 Agent。
// 1 回目の ListSessionIDs（=ウォッチャの起動前スナップショット）は preexisting のみ、
// 2 回目以降に new-uuid が出現する、という「起動後の新規セッション」を決定的に模す。
// 全メソッドをポインタレシーバにして mutex のコピー（copylocks/race）を避ける。
type detectCatAgent struct {
	mu    sync.Mutex
	calls int
}

func (a *detectCatAgent) Name() string                                      { return "cat" }
func (a *detectCatAgent) Invocation(terminal.RunRequest) (string, []string) { return "cat", nil }
func (a *detectCatAgent) ListSessionIDs(string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	if a.calls <= 1 {
		return []string{"preexisting"}
	}
	return []string{"preexisting", "new-uuid"}
}

func TestRegistry_FreshStartDetectsAndSetsSessionID(t *testing.T) {
	oi, ot := terminal.SessionWatchInterval, terminal.SessionWatchTimeout
	terminal.SessionWatchInterval = 5 * time.Millisecond
	terminal.SessionWatchTimeout = 2 * time.Second
	defer func() { terminal.SessionWatchInterval, terminal.SessionWatchTimeout = oi, ot }()

	r := NewRegistry("", nil)
	if _, err := r.Start("chat-1", "L", &detectCatAgent{}, terminal.RunRequest{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = r.Stop("chat-1") }()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if r.IDBySessionID("new-uuid") == "chat-1" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := r.IDBySessionID("new-uuid"); got != "chat-1" {
		t.Fatalf("want chat-1 bound to new-uuid, got %q", got)
	}
	var found bool
	for _, it := range r.List() {
		if it.ID == "chat-1" && it.SessionID == "new-uuid" {
			found = true
		}
	}
	if !found {
		t.Fatalf("List should expose SessionID new-uuid for chat-1: %+v", r.List())
	}
}

func TestRegistry_FreshStartFiresSessionListener(t *testing.T) {
	oi, ot := terminal.SessionWatchInterval, terminal.SessionWatchTimeout
	terminal.SessionWatchInterval = 5 * time.Millisecond
	terminal.SessionWatchTimeout = 2 * time.Second
	defer func() { terminal.SessionWatchInterval, terminal.SessionWatchTimeout = oi, ot }()

	r := NewRegistry("", nil)
	var mu sync.Mutex
	var fired []string
	r.AddSessionListener(func(id, sid string) {
		mu.Lock()
		defer mu.Unlock()
		fired = append(fired, id+"="+sid)
	})
	if _, err := r.Start("chat-3", "L", &detectCatAgent{}, terminal.RunRequest{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = r.Stop("chat-3") }()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(fired)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(fired) != 1 || fired[0] != "chat-3=new-uuid" {
		t.Fatalf("session listener should fire once for detected session; got %v", fired)
	}
}

func TestRegistry_ResumeStartSetsSessionIDImmediately(t *testing.T) {
	r := NewRegistry("", nil)
	if _, err := r.Start("chat-2", "L", &detectCatAgent{}, terminal.RunRequest{Resume: "resume-uuid"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = r.Stop("chat-2") }()
	if got := r.IDBySessionID("resume-uuid"); got != "chat-2" {
		t.Fatalf("resume should set SessionID immediately; IDBySessionID=%q", got)
	}
}
