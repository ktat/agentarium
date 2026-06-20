package wrap

import (
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/ktat/agentarium/kernel/terminal"
)

func newBackend() *Backend {
	agents := terminal.NewAgentRegistry("cat")
	agents.Register(terminal.ConfigAgent{AgentName: "cat", Binary: "cat"})
	return &Backend{Registry: NewRegistry("", agents)}
}

func TestBackend_NameAndRenderer(t *testing.T) {
	b := newBackend()
	if b.Name() != "wrap" {
		t.Fatalf("Name want wrap, got %q", b.Name())
	}
	if b.Renderer() != "wrap" {
		t.Fatalf("Renderer want wrap, got %q", b.Renderer())
	}
}

func TestBackend_StartUsesColsAltRowsFromRequest(t *testing.T) {
	b := newBackend()
	ag := terminal.ConfigAgent{AgentName: "cat", Binary: "cat"}
	req := terminal.RunRequest{Cols: 100, AltRows: 30}
	if err := b.Start("t1", "L", ag, req); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = b.Stop("t1") }()
	items := b.List()
	if len(items) != 1 || items[0].ID != "t1" {
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
	if err := b.Inject("t1", "hello", true); err != nil {
		t.Fatalf("inject: %v", err)
	}
}

func TestBackend_InjectUnknownID(t *testing.T) {
	b := newBackend()
	if err := b.Inject("missing", "x", false); err == nil {
		t.Fatal("want error for unknown id")
	}
}

func TestBackend_SetSessionIDAndList(t *testing.T) {
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

func TestBackendAssets_HasRendererFiles(t *testing.T) {
	assets := newBackend().Assets()
	// 同梱フォント (fonts/*.woff2) も embed されること。列モデルの整列に必須で、
	// 欠けると wrap.css の @font-face が 404 になり全角/罫線がずれる。
	for _, name := range []string{"index.js", "wrap.css", "fonts/AgentariumTerminalJP-subset.woff2", "fonts/LICENSE"} {
		if _, err := fs.Stat(assets, name); err != nil {
			t.Errorf("assets missing %s: %v", name, err)
		}
	}
}

func TestBackend_SatisfiesObserverAndStateSetter(t *testing.T) {
	var _ terminal.ObserverBackend = (*Backend)(nil)
	var _ terminal.StateSetter = (*Backend)(nil)
}

func TestBackend_Restore_RegistersPending(t *testing.T) {
	dir := t.TempDir()
	store := terminal.NewStore(filepath.Join(dir, "w.json"))
	if err := store.Save([]terminal.SessionRecord{
		{ID: "t1", Label: "L1", Agent: "cat", WorkDir: dir},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	agents := terminal.NewAgentRegistry("cat")
	agents.Register(terminal.ConfigAgent{AgentName: "cat", Binary: "cat"})
	reg := NewRegistryWithStore(dir, agents, store)
	t.Cleanup(reg.Close)
	b := &Backend{Registry: reg}

	restored, total := b.Restore(nil)
	if total != 1 || restored != 1 {
		t.Fatalf("want (1,1), got (%d,%d)", restored, total)
	}
	items := b.List()
	if len(items) != 1 || items[0].ID != "t1" || items[0].Running {
		t.Fatalf("want 1 pending (Running=false) item, got %+v", items)
	}
	// b.Restore は warmup loop を起動する。warmupInterval(2s) を跨ぐと cat が
	// spawn されうるため、検証後すぐ Close で loop を確定停止する（Close は
	// 冪等＆wg.Wait で in-flight 起動を待つので、cleanup の Close は no-op）。
	reg.Close()
}
