package xterm

import (
	"testing"
	"time"

	"github.com/ktat/agentarium/kernel/terminal"
)

// catAgent は cat を起動するテスト用 Agent（stdin 待ちで起動し続ける）。
type catAgent struct{}

func (catAgent) Name() string { return "cat" }
func (catAgent) Invocation(req terminal.RunRequest) (string, []string) {
	return "cat", nil
}

func TestRegistry_StartGetRunning(t *testing.T) {
	r := NewRegistry("")
	p, err := r.Start("t1", "Tab 1", catAgent{}, terminal.RunRequest{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = r.Stop("t1") }()
	if !p.Running() {
		t.Fatal("want running")
	}
	if r.Get("t1") == nil {
		t.Fatal("Get returned nil for started id")
	}
	if r.Get("missing") != nil {
		t.Fatal("Get should be nil for unknown id")
	}
}

func TestRegistry_StartReusesRunning(t *testing.T) {
	r := NewRegistry("")
	p1, _ := r.Start("t1", "L", catAgent{}, terminal.RunRequest{})
	p2, _ := r.Start("t1", "L", catAgent{}, terminal.RunRequest{})
	defer func() { _ = r.Stop("t1") }()
	if p1 != p2 {
		t.Fatal("running process should be reused for same id")
	}
}

func TestRegistry_StartEmptyID(t *testing.T) {
	r := NewRegistry("")
	if _, err := r.Start("", "L", catAgent{}, terminal.RunRequest{}); err == nil {
		t.Fatal("want error for empty id")
	}
}

func TestRegistry_StartNilAgent(t *testing.T) {
	r := NewRegistry("")
	if _, err := r.Start("t1", "L", nil, terminal.RunRequest{}); err == nil {
		t.Fatal("want error for nil agent")
	}
}

func TestRegistry_ListSortedAndRunning(t *testing.T) {
	r := NewRegistry("")
	_, _ = r.Start("b", "B", catAgent{}, terminal.RunRequest{})
	_, _ = r.Start("a", "A", catAgent{}, terminal.RunRequest{})
	defer func() { _ = r.Stop("a"); _ = r.Stop("b") }()
	items := r.List()
	if len(items) != 2 || items[0].ID != "a" || items[1].ID != "b" {
		t.Fatalf("List not sorted by id: %+v", items)
	}
	if !items[0].Running {
		t.Fatal("want running true in SessionInfo")
	}
}

func TestRegistry_SetSessionIDAndIndex(t *testing.T) {
	r := NewRegistry("")
	_, _ = r.Start("t1", "L", catAgent{}, terminal.RunRequest{})
	defer func() { _ = r.Stop("t1") }()
	r.SetSessionID("t1", "sess-xyz")
	if got := r.IDBySessionID("sess-xyz"); got != "t1" {
		t.Fatalf("IDBySessionID want t1, got %q", got)
	}
	if r.IDBySessionID("nope") != "" {
		t.Fatal("unknown session id should map to empty")
	}
}

func TestRegistry_StateTransitionNotifiesListener(t *testing.T) {
	r := NewRegistry("")
	_, _ = r.Start("t1", "L", catAgent{}, terminal.RunRequest{})
	defer func() { _ = r.Stop("t1") }()
	type ev struct {
		id         string
		prev, next terminal.SessionState
	}
	got := make(chan ev, 1)
	r.AddStateListener(func(id string, prev, next terminal.SessionState, source string) {
		got <- ev{id, prev, next}
	})
	r.SetState("t1", terminal.StateRunning, "pty")
	select {
	case e := <-got:
		if e.id != "t1" || e.prev != terminal.StateIdle || e.next != terminal.StateRunning {
			t.Fatalf("unexpected event: %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("state listener not notified")
	}
}

func TestRegistry_StopRemovesEntry(t *testing.T) {
	r := NewRegistry("")
	_, _ = r.Start("t1", "L", catAgent{}, terminal.RunRequest{})
	if err := r.Stop("t1"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if r.Get("t1") != nil {
		t.Fatal("entry should be gone after Stop")
	}
}

// xterm 側 — xterm の Start シグネチャは Start(id, label, ag, req) なので
// cols/altRows なし。catAgent は xterm 側のテストヘルパに合わせる。
// 既存テスト (TestRegistry_StartReusesRunning など) と同じ helper を使う。
func TestRegistry_StopThenStartSameID_OldOnExitDoesNotRemoveNew(t *testing.T) {
	r := NewRegistry("")
	p1, _ := r.Start("t1", "L", catAgent{}, terminal.RunRequest{})
	if err := r.Stop("t1"); err != nil {
		t.Fatalf("stop1: %v", err)
	}
	p2, err := r.Start("t1", "L", catAgent{}, terminal.RunRequest{})
	if err != nil {
		t.Fatalf("start2: %v", err)
	}
	defer func() { _ = r.Stop("t1") }()
	if p1 == p2 {
		t.Fatal("expected new process after Stop+Start")
	}
	if got := r.Get("t1"); got != p2 {
		t.Fatalf("registry lost the new entry; got %v want %v", got, p2)
	}
}
