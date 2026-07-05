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

	// kernel グループ + alpha の 2 件。beta は SettingsProvider 無し、settings 自身は除外。
	var pl *struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		Fields []struct {
			Key    string `json:"key"`
			Label  string `json:"label"`
			Secret bool   `json:"secret"`
			Value  string `json:"value"`
			Set    bool   `json:"set"`
		} `json:"fields"`
	}
	hasKernel := false
	for i := range out.Plugins {
		switch out.Plugins[i].ID {
		case "alpha":
			pl = &out.Plugins[i]
		case "kernel":
			hasKernel = true
		case "beta", "settings":
			t.Errorf("unexpected group in schema: %s", out.Plugins[i].ID)
		}
	}
	if !hasKernel {
		t.Error("schema should include the kernel group")
	}
	if pl == nil {
		t.Fatal("alpha group not found in schema")
		return // staticcheck(SA5011) は t.Fatal の no-return を認識しないため明示
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

func TestSchema_KernelRendererHasOptions(t *testing.T) {
	_, _, sp := newTestEnv(t)
	h := findRoute(t, sp, "GET", "/schema")
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest("GET", "/schema", nil))
	var out struct {
		Plugins []struct {
			ID     string `json:"id"`
			Fields []struct {
				Key     string   `json:"key"`
				Options []string `json:"options"`
			} `json:"fields"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, g := range out.Plugins {
		if g.ID != "kernel" {
			continue
		}
		for _, f := range g.Fields {
			if f.Key == "terminal_renderer" {
				if len(f.Options) != 2 || f.Options[0] != "xterm" || f.Options[1] != "wrap" {
					t.Fatalf("renderer options = %v, want [xterm wrap]", f.Options)
				}
				return
			}
		}
	}
	t.Fatal("kernel.terminal_renderer field with options not found")
}

func TestSave_KernelRendererValid(t *testing.T) {
	_, store, sp := newTestEnv(t)
	h := findRoute(t, sp, "POST", "/save")
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest("POST", "/save", strings.NewReader(`{"id":"kernel","values":{"terminal_renderer":"wrap"}}`)))
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}
	if v, ok := store.Get(settings.KeyTerminalRenderer); !ok || v != "wrap" {
		t.Errorf("renderer: want wrap, got %q ok=%v", v, ok)
	}
	if got := settings.TerminalRenderer(store); got != "wrap" {
		t.Errorf("TerminalRenderer: want wrap, got %q", got)
	}
}

func TestSave_KernelRendererInvalid(t *testing.T) {
	_, store, sp := newTestEnv(t)
	h := findRoute(t, sp, "POST", "/save")
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest("POST", "/save", strings.NewReader(`{"id":"kernel","values":{"terminal_renderer":"bogus"}}`)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	if got := settings.TerminalRenderer(store); got != "" {
		t.Errorf("invalid renderer must not be stored; TerminalRenderer=%q", got)
	}
}

func TestTerminalRenderer_UnsetIsEmpty(t *testing.T) {
	_, store, _ := newTestEnv(t)
	if got := settings.TerminalRenderer(store); got != "" {
		t.Errorf("unset renderer should be empty, got %q", got)
	}
}

func TestSchema_KernelThemeHasOptions(t *testing.T) {
	_, _, sp := newTestEnv(t)
	h := findRoute(t, sp, "GET", "/schema")
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest("GET", "/schema", nil))

	var out struct {
		Plugins []struct {
			ID     string `json:"id"`
			Fields []struct {
				Key     string   `json:"key"`
				Value   string   `json:"value"`
				Options []string `json:"options"`
			} `json:"fields"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, g := range out.Plugins {
		if g.ID != "kernel" {
			continue
		}
		for _, f := range g.Fields {
			if f.Key == "theme" {
				if len(f.Options) != 3 || f.Options[0] != "system" || f.Options[1] != "light" || f.Options[2] != "dark" {
					t.Fatalf("theme options = %v, want [system light dark]", f.Options)
				}
				if f.Value != "system" {
					t.Fatalf("theme default value = %q, want system", f.Value)
				}
				return
			}
		}
	}
	t.Fatal("kernel.theme field with options not found")
}

func TestSave_KernelThemeValidAndReader(t *testing.T) {
	_, store, sp := newTestEnv(t)
	h := findRoute(t, sp, "POST", "/save")
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest("POST", "/save", strings.NewReader(`{"id":"kernel","values":{"theme":"dark"}}`)))
	if w.Code != http.StatusNoContent {
		t.Fatalf("save theme: want 204, got %d", w.Code)
	}
	if v, ok := store.Get(settings.KeyTheme); !ok || v != "dark" {
		t.Fatalf("stored theme = %q ok=%v, want dark", v, ok)
	}
	if got := settings.Theme(store); got != "dark" {
		t.Fatalf("Theme() = %q, want dark", got)
	}
}

func TestSave_KernelThemeInvalid(t *testing.T) {
	_, _, sp := newTestEnv(t)
	h := findRoute(t, sp, "POST", "/save")
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest("POST", "/save", strings.NewReader(`{"id":"kernel","values":{"theme":"bogus"}}`)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid theme: want 400, got %d", w.Code)
	}
}

func TestTheme_SystemAndUnsetAreEmpty(t *testing.T) {
	_, store, _ := newTestEnv(t)
	if got := settings.Theme(store); got != "" {
		t.Fatalf("unset Theme() = %q, want empty", got)
	}
	_ = store.Set(settings.KeyTheme, "system")
	if got := settings.Theme(store); got != "" {
		t.Fatalf("system Theme() = %q, want empty", got)
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

func postSave(t *testing.T, sp plugin.Plugin, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := findRoute(t, sp, "POST", "/save")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/save", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h(rec, req)
	return rec
}

func TestSave_KernelSecretCRUD(t *testing.T) {
	_, store, sp := newTestEnv(t)
	// 暗号 1 件・平文 1 件を追加
	rec := postSave(t, sp, `{"id":"secret","secrets":[
		{"key":"NOTION_TOKEN","value":"tok","encrypted":true},
		{"key":"REGION","value":"jp","encrypted":false}
	]}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
	if v, ok := store.Get(settings.KernelSecretPrefix + "NOTION_TOKEN"); !ok || v != "tok" {
		t.Fatalf("notion token = %q,%v", v, ok)
	}
	if !store.IsEncrypted(settings.KernelSecretPrefix + "NOTION_TOKEN") {
		t.Fatal("NOTION_TOKEN should be encrypted")
	}
	if store.IsEncrypted(settings.KernelSecretPrefix + "REGION") {
		t.Fatal("REGION should be plaintext")
	}
	// 削除
	rec = postSave(t, sp, `{"id":"secret","secrets":[{"key":"REGION","delete":true}]}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d", rec.Code)
	}
	if store.Has(settings.KernelSecretPrefix + "REGION") {
		t.Fatal("REGION should be deleted")
	}
}

func TestSave_PluginRefAndLiteralExclusive(t *testing.T) {
	_, store, sp := newTestEnv(t)
	_ = store.SetSecret(settings.KernelSecretPrefix+"NOTION_TOKEN", "tok")

	// alpha.token を NOTION_TOKEN 参照に設定
	rec := postSave(t, sp, `{"id":"alpha","refs":{"token":"NOTION_TOKEN"}}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("ref save status = %d", rec.Code)
	}
	if v, ok := store.Get("alpha.token" + settings.RefSuffix); !ok || v != "NOTION_TOKEN" {
		t.Fatalf("ref pointer = %q,%v", v, ok)
	}
	if store.Has("alpha.token") {
		t.Fatal("literal alpha.token should be cleared when ref is set")
	}

	// literal に戻すと __ref が消える
	rec = postSave(t, sp, `{"id":"alpha","values":{"token":"plainsecret"}}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("literal save status = %d", rec.Code)
	}
	if store.Has("alpha.token" + settings.RefSuffix) {
		t.Fatal("__ref should be cleared when literal is set")
	}
	if v, ok := store.Get("alpha.token"); !ok || v != "plainsecret" {
		t.Fatalf("literal token = %q,%v", v, ok)
	}
}

func TestSave_InvalidRefIs400(t *testing.T) {
	_, _, sp := newTestEnv(t)
	rec := postSave(t, sp, `{"id":"alpha","refs":{"token":"NOPE"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid ref status = %d, want 400", rec.Code)
	}
}

func TestSchema_KernelSecretsAndRef(t *testing.T) {
	_, store, sp := newTestEnv(t)
	// 平文・暗号のカーネルシークレットを 1 件ずつ
	_ = store.Set(settings.KernelSecretPrefix+"PLAIN_KEY", "plainval")
	_ = store.SetSecret(settings.KernelSecretPrefix+"SECRET_KEY", "secretval")
	// alpha.token を SECRET_KEY 参照に
	_ = store.Set("alpha.token"+settings.RefSuffix, "SECRET_KEY")

	h := findRoute(t, sp, "GET", "/schema")
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("GET", "/schema", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp struct {
		Plugins []struct {
			ID     string `json:"id"`
			Title  string `json:"title"`
			Fields []struct {
				Key       string `json:"key"`
				Value     string `json:"value"`
				Set       bool   `json:"set"`
				Encrypted bool   `json:"encrypted"`
				Ref       string `json:"ref"`
			} `json:"fields"`
		} `json:"plugins"`
		SecretKeys []string `json:"secretKeys"`
	}
	body, _ := io.ReadAll(rec.Body)
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, body)
	}
	// secretKeys は接頭辞抜きの 2 件
	if len(resp.SecretKeys) != 2 {
		t.Fatalf("secretKeys = %v, want 2", resp.SecretKeys)
	}
	for _, k := range resp.SecretKeys {
		if strings.HasPrefix(k, settings.KernelSecretPrefix) {
			t.Fatalf("secretKeys should be prefix-stripped, got %q", k)
		}
	}
	// Kernel Secrets グループ: 平文は value を返し、暗号は返さない
	var foundSecret bool
	for _, pl := range resp.Plugins {
		if pl.ID == "secret" {
			foundSecret = true
			for _, f := range pl.Fields {
				if f.Key == "PLAIN_KEY" && f.Value != "plainval" {
					t.Fatalf("plain kernel secret value = %q, want plainval", f.Value)
				}
				if f.Key == "SECRET_KEY" && f.Value != "" {
					t.Fatalf("encrypted kernel secret value should be hidden, got %q", f.Value)
				}
				if f.Key == "SECRET_KEY" && !f.Encrypted {
					t.Fatal("SECRET_KEY should be marked encrypted")
				}
			}
		}
		if pl.ID == "alpha" {
			for _, f := range pl.Fields {
				if f.Key == "token" && f.Ref != "SECRET_KEY" {
					t.Fatalf("alpha.token ref = %q, want SECRET_KEY", f.Ref)
				}
			}
		}
	}
	if !foundSecret {
		t.Fatal("Kernel Secrets group (id=secret) missing")
	}
}

func TestReveal_DecryptsKernelSecret(t *testing.T) {
	_, store, sp := newTestEnv(t)
	_ = store.SetSecret(settings.KernelSecretPrefix+"SECRET_KEY", "secretval")

	// POST にすることで CSRF guard の Origin チェック対象になる。
	h := findRoute(t, sp, "POST", "/reveal")

	// {"key":"<KEY>"} でその 1 件の復号値だけを返す
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("POST", "/reveal", strings.NewReader(`{"key":"SECRET_KEY"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("reveal status = %d, want 200", rec.Code)
	}
	var resp struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Value != "secretval" {
		t.Fatalf("reveal value = %q, want secretval", resp.Value)
	}
	// 復号した平文をキャッシュさせない
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}

	// 存在しないキーは 404
	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest("POST", "/reveal", strings.NewReader(`{"key":"NOPE"}`)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("reveal missing key status = %d, want 404", rec.Code)
	}
}
