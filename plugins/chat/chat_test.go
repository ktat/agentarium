package chat

import (
	"path/filepath"
	"testing"

	"github.com/ktat/agentarium/kernel/plugin"
	"github.com/ktat/agentarium/kernel/store"
)

func newTestPlugin(t *testing.T) Plugin {
	t.Helper()
	st := store.New[ChatRecord](filepath.Join(t.TempDir(), "chat.json"))
	return New(st)
}

func TestPlugin_Meta(t *testing.T) {
	p := newTestPlugin(t)
	var _ plugin.Plugin = p
	if p.Meta().ID != "chat" {
		t.Fatalf("want id chat, got %s", p.Meta().ID)
	}
	if p.Meta().Pane != plugin.PaneLeft {
		t.Fatalf("want pane left, got %v", p.Meta().Pane)
	}
}

func TestPlugin_AssetsHasIndexJS(t *testing.T) {
	p := newTestPlugin(t)
	b, err := p.Assets().Open("index.js")
	if err != nil {
		t.Fatalf("index.js missing: %v", err)
	}
	b.Close()
}
