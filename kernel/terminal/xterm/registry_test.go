package xterm

import (
	"path/filepath"
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
	r := NewRegistry("", nil)
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
	r := NewRegistry("", nil)
	p1, _ := r.Start("t1", "L", catAgent{}, terminal.RunRequest{})
	p2, _ := r.Start("t1", "L", catAgent{}, terminal.RunRequest{})
	defer func() { _ = r.Stop("t1") }()
	if p1 != p2 {
		t.Fatal("running process should be reused for same id")
	}
}

func TestRegistry_StartEmptyID(t *testing.T) {
	r := NewRegistry("", nil)
	if _, err := r.Start("", "L", catAgent{}, terminal.RunRequest{}); err == nil {
		t.Fatal("want error for empty id")
	}
}

func TestRegistry_StartNilAgent(t *testing.T) {
	r := NewRegistry("", nil)
	if _, err := r.Start("t1", "L", nil, terminal.RunRequest{}); err == nil {
		t.Fatal("want error for nil agent")
	}
}

func TestRegistry_ListSortedAndRunning(t *testing.T) {
	r := NewRegistry("", nil)
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
	r := NewRegistry("", nil)
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
	r := NewRegistry("", nil)
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
	r := NewRegistry("", nil)
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
	r := NewRegistry("", nil)
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

func TestPersist_WritesSessionRecord(t *testing.T) {
	dir := t.TempDir()
	store := terminal.NewStore(filepath.Join(dir, "x.json"))
	r := NewRegistryWithStore(dir, nil, store)
	ag := terminal.ConfigAgent{AgentName: "claude", Binary: "cat", ModelFlag: "--model"}
	if _, err := r.Start("t1", "L1", ag, terminal.RunRequest{Model: "opus"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	r.SetSessionID("t1", "s1")

	recs, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("want 1 record, got %d", len(recs))
	}
	got := recs[0]
	if got.Agent != "claude" || got.Model != "opus" || got.SessionID != "s1" || got.Label != "L1" {
		t.Fatalf("record missing fields: %+v", got)
	}
	if got.WorkDir != dir {
		t.Fatalf("WorkDir not wired: want %q, got %q", dir, got.WorkDir)
	}
}

func TestRestoreFromStoreLazy_RegistersPendingXterm(t *testing.T) {
	dir := t.TempDir()
	store := terminal.NewStore(filepath.Join(dir, "x.json"))
	if err := store.Save([]terminal.SessionRecord{
		{ID: "t1", Label: "L1", Agent: "cat", WorkDir: dir},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	agents := terminal.NewAgentRegistry("cat")
	agents.Register(terminal.ConfigAgent{AgentName: "cat", Binary: "cat"})
	r := NewRegistryWithStore(dir, agents, store)
	t.Cleanup(r.Close)

	pending, total := r.RestoreFromStoreLazy(nil)
	if pending != 1 || total != 1 {
		t.Fatalf("want (1,1), got (%d,%d)", pending, total)
	}
	items := r.List()
	if len(items) != 1 || items[0].Running {
		t.Fatalf("want 1 pending (Running=false), got %+v", items)
	}
	// EnsureStarted で pending が起動する。
	p, ok := r.EnsureStarted("t1")
	if !ok || p == nil || !p.Running() {
		t.Fatalf("EnsureStarted did not start pending: ok=%v", ok)
	}
	t.Cleanup(func() { _ = p.Stop() })
}

func TestRestoreFromStoreLazy_SkipsWhenCannotResumeXterm(t *testing.T) {
	dir := t.TempDir()
	store := terminal.NewStore(filepath.Join(dir, "x.json"))
	if err := store.Save([]terminal.SessionRecord{
		{ID: "t1", Label: "L1", Agent: "cat", SessionID: "s1", WorkDir: dir},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	agents := terminal.NewAgentRegistry("cat")
	agents.Register(terminal.ConfigAgent{AgentName: "cat", Binary: "cat"})
	r := NewRegistryWithStore(dir, agents, store)
	t.Cleanup(r.Close)

	pending, total := r.RestoreFromStoreLazy(func(terminal.SessionRecord) bool { return false })
	if pending != 0 || total != 1 {
		t.Fatalf("want (0,1) when cannot resume, got (%d,%d)", pending, total)
	}
}
