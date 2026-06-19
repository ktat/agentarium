package xterm

import (
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/ktat/agentarium/kernel/terminal"
)

func newBackend() *Backend {
	return &Backend{Registry: NewRegistry("", nil)}
}

func TestBackend_NameAndRenderer(t *testing.T) {
	b := newBackend()
	if b.Name() != "xterm" {
		t.Fatalf("Name want xterm, got %q", b.Name())
	}
	if b.Renderer() != "xterm" {
		t.Fatalf("Renderer want xterm, got %q", b.Renderer())
	}
}

func TestBackend_StartStopList(t *testing.T) {
	b := newBackend()
	ag := terminal.ConfigAgent{AgentName: "cat", Binary: "cat"}
	if err := b.Start("t1", "L", ag, terminal.RunRequest{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = b.Stop("t1") }()
	items := b.List()
	if len(items) != 1 || items[0].ID != "t1" || !items[0].Running {
		t.Fatalf("List unexpected: %+v", items)
	}
}

func TestBackend_StartNilAgent(t *testing.T) {
	b := newBackend()
	if err := b.Start("t1", "L", nil, terminal.RunRequest{}); err == nil {
		t.Fatal("want error for nil agent")
	}
}

func TestBackend_Inject(t *testing.T) {
	b := newBackend()
	ag := terminal.ConfigAgent{AgentName: "cat", Binary: "cat"}
	if err := b.Start("t1", "L", ag, terminal.RunRequest{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = b.Stop("t1") }()
	if err := b.Inject("t1", "hello", false); err != nil {
		t.Fatalf("inject: %v", err)
	}
}

func TestBackend_InjectUnknownID(t *testing.T) {
	b := newBackend()
	if err := b.Inject("missing", "x", false); err == nil {
		t.Fatal("want error for unknown id")
	}
}

func TestBackend_SetSessionID(t *testing.T) {
	b := newBackend()
	ag := terminal.ConfigAgent{AgentName: "cat", Binary: "cat"}
	_ = b.Start("t1", "L", ag, terminal.RunRequest{})
	defer func() { _ = b.Stop("t1") }()
	b.SetSessionID("t1", "sess-x")
	items := b.List()
	if len(items) != 1 || items[0].SessionID != "sess-x" {
		t.Fatalf("SessionID not propagated: %+v", items)
	}
}

func TestBackend_RoutesIncludesWS(t *testing.T) {
	b := newBackend()
	routes := b.Routes()
	if len(routes) != 1 || routes[0].Path != "/ws" || routes[0].Method != "GET" {
		t.Fatalf("Routes unexpected: %+v", routes)
	}
	var _ = b.Assets()
	if _, err := fs.ReadFile(b.Assets(), "index.js"); err != nil {
		t.Fatalf("index.js should be embedded: %v", err)
	}
}

func TestBackend_SatisfiesObserverAndStateSetter(t *testing.T) {
	var _ terminal.ObserverBackend = (*Backend)(nil)
	var _ terminal.StateSetter = (*Backend)(nil)
}

// TestBackend_Restore_RegistersPendingXterm は Restore が store の永続レコードを
// pending 復元すること（List に Running=false で載ること）を検証する。
func TestBackend_Restore_RegistersPendingXterm(t *testing.T) {
	dir := t.TempDir()
	store := terminal.NewStore(filepath.Join(dir, "x.json"))
	if err := store.Save([]terminal.SessionRecord{
		{ID: "t1", Label: "L1", Agent: "cat", WorkDir: dir},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	agents := terminal.NewAgentRegistry("cat")
	agents.Register(terminal.ConfigAgent{AgentName: "cat", Binary: "cat"})
	b := &Backend{Registry: NewRegistryWithStore(dir, agents, store)}
	t.Cleanup(func() { _ = b.Close() })

	restored, total := b.Restore(nil)
	if restored != 1 || total != 1 {
		t.Fatalf("want (1,1), got (%d,%d)", restored, total)
	}
	items := b.List()
	if len(items) != 1 || items[0].ID != "t1" || items[0].Running {
		t.Fatalf("want 1 pending item, got %+v", items)
	}
}
