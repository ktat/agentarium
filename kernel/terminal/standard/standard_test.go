package standard_test

import (
	"path/filepath"
	"testing"

	"github.com/ktat/agentarium/kernel/secrets"
	"github.com/ktat/agentarium/kernel/settings"
	"github.com/ktat/agentarium/kernel/terminal"
	"github.com/ktat/agentarium/kernel/terminal/standard"
)

func newSecrets(t *testing.T) *secrets.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := secrets.NewStore(filepath.Join(dir, "data.json"), filepath.Join(dir, "key"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

func newAgents() *terminal.AgentRegistry { return terminal.NewAgentRegistry("claude") }

func TestNewService_ActiveFromSettings(t *testing.T) {
	sec := newSecrets(t)
	if err := sec.Set(settings.KeyTerminalRenderer, "wrap"); err != nil {
		t.Fatal(err)
	}
	svc, err := standard.NewService(standard.Config{
		WorkDir: t.TempDir(), Agents: newAgents(), Secrets: sec, StoreDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if got := svc.Active().Name(); got != "wrap" {
		t.Fatalf("active = %q, want wrap", got)
	}
}

func TestNewService_DefaultsToXterm(t *testing.T) {
	t.Setenv("AGENTARIUM_TERMINAL_RENDERER", "")
	svc, err := standard.NewService(standard.Config{
		WorkDir: t.TempDir(), Agents: newAgents(), Secrets: newSecrets(t), StoreDir: "",
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if got := svc.Active().Name(); got != "xterm" {
		t.Fatalf("active = %q, want xterm", got)
	}
}

func TestNewService_EnvFallback(t *testing.T) {
	t.Setenv("AGENTARIUM_TERMINAL_RENDERER", "wrap")
	svc, err := standard.NewService(standard.Config{
		WorkDir: t.TempDir(), Agents: newAgents(), Secrets: nil, StoreDir: "",
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if got := svc.Active().Name(); got != "wrap" {
		t.Fatalf("active = %q, want wrap (env fallback)", got)
	}
}

func TestNewService_Validation(t *testing.T) {
	if _, err := standard.NewService(standard.Config{WorkDir: "wd", Agents: nil}); err == nil {
		t.Fatal("nil Agents should error")
	}
	if _, err := standard.NewService(standard.Config{WorkDir: "", Agents: newAgents()}); err == nil {
		t.Fatal("empty WorkDir should error")
	}
}
