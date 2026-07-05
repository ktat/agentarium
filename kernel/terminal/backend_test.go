package terminal

import (
	"io/fs"
	"testing"

	"github.com/ktat/agentarium/kernel/plugin"
)

// fullBackend は分離後も Backend（= TerminalBackend + TransportBackend）を満たす。
type fullBackend struct{}

func (fullBackend) Name() string                                           { return "x" }
func (fullBackend) Start(id, label string, ag Agent, req RunRequest) error { return nil }
func (fullBackend) Stop(id string) error                                   { return nil }
func (fullBackend) Inject(id, text string, enter bool) error               { return nil }
func (fullBackend) SetSessionID(id, sessionID string)                      {}
func (fullBackend) List() []SessionInfo                                    { return nil }
func (fullBackend) AddStateListener(l StateListener)                       {}
func (fullBackend) AddSessionListener(l SessionListener)                   {}
func (fullBackend) Restore(canResume func(SessionRecord) bool) (int, int)  { return 0, 0 }
func (fullBackend) Renderer() string                                       { return "x" }
func (fullBackend) Routes() []plugin.Route                                 { return nil }
func (fullBackend) Assets() fs.FS                                          { return nil }

func TestBackend_SplitInterfaces(t *testing.T) {
	var b Backend = fullBackend{}
	// domain 面だけを取り出せる
	var d TerminalBackend = b
	if d.Name() != "x" {
		t.Fatal("TerminalBackend.Name")
	}
	// transport 面だけを取り出せる
	var tr TransportBackend = b
	if tr.Renderer() != "x" {
		t.Fatal("TransportBackend.Renderer")
	}
}
