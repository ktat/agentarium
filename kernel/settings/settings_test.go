package settings_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"io/fs"

	"github.com/ktat/agentarium/kernel/plugin"
	"github.com/ktat/agentarium/kernel/secrets"
	"github.com/ktat/agentarium/kernel/settings"
)

// --- test helpers ---

type fakeSP struct {
	id     string
	fields []plugin.Field
}

func (f fakeSP) Meta() plugin.Meta {
	return plugin.Meta{ID: f.id, Title: f.id, Pane: plugin.PaneLeft}
}
func (f fakeSP) SettingsSchema() []plugin.Field { return f.fields }

type plainP struct{ id string }

func (p plainP) Meta() plugin.Meta { return plugin.Meta{ID: p.id, Title: p.id} }

func newTestEnv(t *testing.T) (*plugin.Registry, *secrets.Store, plugin.Plugin) {
	t.Helper()
	dir := t.TempDir()
	store, err := secrets.NewStore(filepath.Join(dir, "data.json"), filepath.Join(dir, "key"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	reg := plugin.NewRegistry()
	if err := reg.Register(fakeSP{
		id: "alpha",
		fields: []plugin.Field{
			{Key: "greeting", Label: "Greeting", Secret: false},
			{Key: "token", Label: "Token", Secret: true},
		},
	}); err != nil {
		t.Fatalf("Register alpha: %v", err)
	}
	if err := reg.Register(plainP{id: "beta"}); err != nil {
		t.Fatalf("Register beta: %v", err)
	}
	sp := settings.New(reg, store)
	if err := reg.Register(sp); err != nil {
		t.Fatalf("Register settings: %v", err)
	}
	return reg, store, sp
}

func findRoute(t *testing.T, sp plugin.Plugin, method, path string) http.HandlerFunc {
	t.Helper()
	rp, ok := sp.(plugin.RouteProvider)
	if !ok {
		t.Fatal("settings plugin does not implement RouteProvider")
	}
	for _, rt := range rp.Routes() {
		if rt.Method == method && rt.Path == path {
			return rt.Handler
		}
	}
	t.Fatalf("route %s %s not found", method, path)
	return nil
}

// --- tests ---

func TestSchema_EnumeratesProvidersOnly(t *testing.T) {
	_, store, sp := newTestEnv(t)

	if err := store.Set("alpha.greeting", "hi"); err != nil {
		t.Fatalf("Set greeting: %v", err)
	}
	if err := store.SetSecret("alpha.token", "secret-val"); err != nil {
		t.Fatalf("SetSecret token: %v", err)
	}

	h := findRoute(t, sp, "GET", "/schema")
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest("GET", "/schema", nil))

	res := w.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	bodyStr := string(body)

	// Must not leak secret value
	if strings.Contains(bodyStr, "secret-val") {
		t.Errorf("response leaks secret value: %s", bodyStr)
	}

	var out struct {
		Plugins []struct {
			ID     string `json:"id"`
			Title  string `json:"title"`
			Fields []struct {
				Key    string `json:"key"`
				Label  string `json:"label"`
				Secret bool   `json:"secret"`
				Value  string `json:"value"`
				Set    bool   `json:"set"`
			} `json:"fields"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Exactly 1 plugin (alpha); beta has no SettingsProvider, settings itself is excluded
	if len(out.Plugins) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(out.Plugins))
	}
	pl := out.Plugins[0]
	if pl.ID != "alpha" {
		t.Errorf("expected id=alpha, got %s", pl.ID)
	}

	fields := make(map[string]struct {
		Secret bool
		Value  string
		Set    bool
	})
	for _, f := range pl.Fields {
		fields[f.Key] = struct {
			Secret bool
			Value  string
			Set    bool
		}{f.Secret, f.Value, f.Set}
	}

	if g := fields["greeting"]; g.Value != "hi" {
		t.Errorf("greeting value: want hi, got %q", g.Value)
	}
	tok := fields["token"]
	if !tok.Secret {
		t.Error("token.secret should be true")
	}
	if !tok.Set {
		t.Error("token.set should be true")
	}
	if tok.Value != "" {
		t.Errorf("token.value should be empty, got %q", tok.Value)
	}
}

func TestSave_NamespacesAndEncrypts(t *testing.T) {
	_, store, sp := newTestEnv(t)

	h := findRoute(t, sp, "POST", "/save")
	body := `{"id":"alpha","values":{"greeting":"hello","token":"tok123","unknown":"x"}}`
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest("POST", "/save", strings.NewReader(body)))

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}

	if v, ok := store.Get("alpha.greeting"); !ok || v != "hello" {
		t.Errorf("alpha.greeting: want hello, got %q ok=%v", v, ok)
	}
	if v, ok := store.Get("alpha.token"); !ok || v != "tok123" {
		t.Errorf("alpha.token: want tok123, got %q ok=%v", v, ok)
	}
	if store.Has("alpha.unknown") {
		t.Error("alpha.unknown should not be stored")
	}
}

func TestSave_EmptySecretKeepsExisting(t *testing.T) {
	_, store, sp := newTestEnv(t)

	if err := store.SetSecret("alpha.token", "orig"); err != nil {
		t.Fatalf("SetSecret: %v", err)
	}

	h := findRoute(t, sp, "POST", "/save")
	body := `{"id":"alpha","values":{"token":""}}`
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest("POST", "/save", strings.NewReader(body)))

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
	if v, ok := store.Get("alpha.token"); !ok || v != "orig" {
		t.Errorf("alpha.token: want orig, got %q ok=%v", v, ok)
	}
}

func TestSave_UnknownPlugin400(t *testing.T) {
	_, _, sp := newTestEnv(t)

	h := findRoute(t, sp, "POST", "/save")
	body := `{"id":"ghost","values":{}}`
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest("POST", "/save", strings.NewReader(body)))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestAssets_HasIndexJS(t *testing.T) {
	_, _, sp := newTestEnv(t)

	fp, ok := sp.(plugin.FrontendProvider)
	if !ok {
		t.Fatal("settings plugin does not implement FrontendProvider")
	}
	if _, err := fs.Stat(fp.Assets(), "index.js"); err != nil {
		t.Errorf("index.js not found: %v", err)
	}
}
