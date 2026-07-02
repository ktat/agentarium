package agentarium

import (
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ktat/agentarium/kernel/pet"
	"github.com/ktat/agentarium/kernel/plugin"
	"github.com/ktat/agentarium/kernel/secrets"
	"github.com/ktat/agentarium/kernel/terminal"
)

func TestWithPet_MountsRoutes(t *testing.T) {
	dir := t.TempDir()
	store, err := secrets.NewStore(filepath.Join(dir, "d.json"), filepath.Join(dir, "k.key"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	app := New().WithSecrets(store).WithPet(pet.New(store, func() int { return 0 }))
	h, err := app.Handler()
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	res, err := http.Get(srv.URL + "/pet/status")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("pet/status status %d", res.StatusCode)
	}
}

type fakePlugin struct{}

func (fakePlugin) Meta() plugin.Meta {
	return plugin.Meta{ID: "fake", Title: "Fake", Pane: plugin.PaneLeft}
}

func TestApp_RegisterAndHandler(t *testing.T) {
	app := New()
	if err := app.Register(fakePlugin{}); err != nil {
		t.Fatalf("register: %v", err)
	}
	h, err := app.Handler()
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	req := httptest.NewRequest("GET", "/api/plugins", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("want 200, got %d", rec.Code)
	}
}

func TestApp_RegistryEscapeHatch(t *testing.T) {
	if New().Registry() == nil {
		t.Fatal("Registry() returned nil")
	}
}

func TestApp_RegisterDuplicateReturnsError(t *testing.T) {
	app := New()
	_ = app.Register(fakePlugin{})
	if err := app.Register(fakePlugin{}); err == nil {
		t.Fatal("want duplicate error, got nil")
	}
}

func TestValidateAddrLoopback_LoopbackAllowed(t *testing.T) {
	t.Setenv("AGENTARIUM_ALLOW_PUBLIC", "")
	cases := []string{
		"127.0.0.1:8780",
		"127.0.0.5:8780",
		"[::1]:8780",
		"localhost:8780",
	}
	for _, addr := range cases {
		if err := validateAddrLoopback(addr); err != nil {
			t.Errorf("loopback %q should be allowed, got %v", addr, err)
		}
	}
}

func TestValidateAddrLoopback_PublicRejectedWithoutOptIn(t *testing.T) {
	t.Setenv("AGENTARIUM_ALLOW_PUBLIC", "")
	cases := []string{
		":8780",
		"0.0.0.0:8780",
		"[::]:8780",
		"192.168.1.5:8780",
		"8.8.8.8:8780",
		"example.com:8780",
	}
	for _, addr := range cases {
		if err := validateAddrLoopback(addr); err == nil {
			t.Errorf("non-loopback %q should require opt-in, got nil", addr)
		}
	}
}

func TestValidateAddrLoopback_PublicAllowedWithOptIn(t *testing.T) {
	t.Setenv("AGENTARIUM_ALLOW_PUBLIC", "1")
	cases := []string{":8780", "0.0.0.0:8780", "192.168.1.5:8780"}
	for _, addr := range cases {
		if err := validateAddrLoopback(addr); err != nil {
			t.Errorf("with opt-in %q should be allowed, got %v", addr, err)
		}
	}
}

func TestValidateAddrLoopback_InvalidAddr(t *testing.T) {
	t.Setenv("AGENTARIUM_ALLOW_PUBLIC", "")
	if err := validateAddrLoopback("not-an-addr"); err == nil {
		t.Error("malformed addr should error")
	}
}

// fakeBackendForApp は terminal.Backend interface を満たすテスト用の最小実装。
// terminal package 内の fakeBackend は private なので、この test では自前定義する。
type fakeBackendForApp struct{}

func (fakeBackendForApp) Name() string     { return "xterm" }
func (fakeBackendForApp) Renderer() string { return "xterm" }
func (fakeBackendForApp) Start(id, label string, ag terminal.Agent, req terminal.RunRequest) error {
	return nil
}
func (fakeBackendForApp) Stop(id string) error                                           { return nil }
func (fakeBackendForApp) Inject(id, text string, enter bool) error                       { return nil }
func (fakeBackendForApp) SetSessionID(id, sessionID string)                              {}
func (fakeBackendForApp) List() []terminal.SessionInfo                                   { return nil }
func (fakeBackendForApp) Routes() []plugin.Route                                         { return nil }
func (fakeBackendForApp) Assets() fs.FS                                                  { return nil }
func (fakeBackendForApp) AddStateListener(l terminal.StateListener)                      {}
func (fakeBackendForApp) Restore(canResume func(terminal.SessionRecord) bool) (int, int) { return 0, 0 }

func TestApp_WithTerminalRegistersRoutes(t *testing.T) {
	app := New()
	agents := terminal.NewAgentRegistry("claude")
	agents.Register(terminal.ConfigAgent{AgentName: "claude", Binary: "claude"})
	svc, err := terminal.NewService(terminal.ServiceConfig{
		Agents:   agents,
		Backends: []terminal.Backend{fakeBackendForApp{}},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	app.WithTerminal(svc)
	h, err := app.Handler()
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	req := httptest.NewRequest("GET", "/terminal/renderer", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status want 200, got %d", rec.Code)
	}
}

func TestApp_WithoutTerminalNoTerminalRoutes(t *testing.T) {
	app := New()
	h, _ := app.Handler()
	req := httptest.NewRequest("GET", "/terminal/renderer", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("status want 404 (terminal not mounted), got %d", rec.Code)
	}
}

func TestApp_WithTerminalReturnsSelfForChaining(t *testing.T) {
	app := New()
	agents := terminal.NewAgentRegistry("claude")
	agents.Register(terminal.ConfigAgent{AgentName: "claude", Binary: "claude"})
	svc, _ := terminal.NewService(terminal.ServiceConfig{
		Agents:   agents,
		Backends: []terminal.Backend{fakeBackendForApp{}},
	})
	if app.WithTerminal(svc) != app {
		t.Fatal("WithTerminal should return the same *App for chaining")
	}
}

func TestApp_RunAndShutdown(t *testing.T) {
	app := New()
	errCh := make(chan error, 1)
	go func() { errCh <- app.Run("127.0.0.1:0") }()
	// Run は ListenAndServe を呼ぶので listener が立つまで少し待つ。
	// addr=:0 だと実 port が分からないので、ここでは Shutdown が
	// http.ErrServerClosed を返して綺麗に止まることだけ検証する。
	time.Sleep(100 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := app.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			t.Fatalf("Run returned unexpected error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after Shutdown")
	}
}

func TestApp_ShutdownBeforeRunIsNoop(t *testing.T) {
	app := New()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := app.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown before run should be nil, got %v", err)
	}
}

func TestWithSecrets_RegistersSettingsPlugin(t *testing.T) {
	dir := t.TempDir()
	store, err := secrets.NewStore(filepath.Join(dir, "d.json"), filepath.Join(dir, "k.key"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	app := New().WithSecrets(store)
	found := false
	for _, p := range app.Registry().Plugins() {
		if p.Meta().ID == "settings" {
			found = true
		}
	}
	if !found {
		t.Fatal("WithSecrets should register the settings plugin")
	}
}

func TestApp_PluginSettings(t *testing.T) {
	app := New()
	// WithSecrets 前は nil
	if app.PluginSettings("eval") != nil {
		t.Fatal("PluginSettings should be nil before WithSecrets")
	}
	dir := t.TempDir()
	store, err := secrets.NewStore(filepath.Join(dir, "data.json"), filepath.Join(dir, "key"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	app.WithSecrets(store)
	_ = store.Set("eval.NOTION_APP_TOKEN", "v")
	r := app.PluginSettings("eval")
	if r == nil {
		t.Fatal("PluginSettings should be non-nil after WithSecrets")
	}
	if v, ok := r.Get("NOTION_APP_TOKEN"); !ok || v != "v" {
		t.Fatalf("reader get = %q,%v", v, ok)
	}
}

func TestSetTabOrder_DelegatesToRegistry(t *testing.T) {
	app := New().SetTabOrder("chat", 25)
	if got := app.Registry().EffectiveOrder("chat"); got != 25 {
		t.Errorf("EffectiveOrder(chat) = %d, want 25", got)
	}
}
