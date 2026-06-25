package server

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/ktat/agentarium/kernel/plugin"
)

// fakePlugin は Route/Frontend 両対応のテスト用プラグイン。
type fakePlugin struct {
	id   string
	pane plugin.Pane
}

func (f fakePlugin) Meta() plugin.Meta {
	return plugin.Meta{ID: f.id, Title: "T-" + f.id, Pane: f.pane, Order: 0}
}
func (f fakePlugin) Routes() []plugin.Route {
	return []plugin.Route{
		{Method: "GET", Path: "/ping", Handler: func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("pong-" + f.id))
		}},
	}
}
func (f fakePlugin) Assets() fs.FS {
	return fstest.MapFS{"index.js": {Data: []byte("// asset of " + f.id)}}
}

func newTestShellFS() fs.FS {
	return fstest.MapFS{
		"index.html": {Data: []byte("<html><body data-shell=1></body></html>")},
		"app.js":     {Data: []byte("// shell app.js")},
		"app.css":    {Data: []byte("/* shell css */")},
	}
}

func TestAPIPlugins_ReturnsMeta(t *testing.T) {
	reg := plugin.NewRegistry()
	_ = reg.Register(fakePlugin{id: "alpha", pane: plugin.PaneLeft})
	_ = reg.Register(fakePlugin{id: "beta", pane: plugin.PaneRight})
	srv := New(reg, newTestShellFS())

	req := httptest.NewRequest("GET", "/api/plugins", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var got []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 plugins, got %d", len(got))
	}
	if got[0]["id"] != "alpha" || got[0]["pane"] != "left" {
		t.Fatalf("plugin[0] unexpected: %+v", got[0])
	}
	if got[1]["pane"] != "right" {
		t.Fatalf("plugin[1] pane unexpected: %+v", got[1])
	}
}

func TestPluginRoute_Mounted(t *testing.T) {
	reg := plugin.NewRegistry()
	_ = reg.Register(fakePlugin{id: "alpha", pane: plugin.PaneLeft})
	srv := New(reg, newTestShellFS())

	req := httptest.NewRequest("GET", "/plugins/alpha/ping", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if rec.Body.String() != "pong-alpha" {
		t.Fatalf("want pong-alpha, got %q", rec.Body.String())
	}
}

func TestPluginAssets_Served(t *testing.T) {
	reg := plugin.NewRegistry()
	_ = reg.Register(fakePlugin{id: "alpha", pane: plugin.PaneLeft})
	srv := New(reg, newTestShellFS())

	req := httptest.NewRequest("GET", "/plugins/alpha/assets/index.js", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if rec.Body.String() != "// asset of alpha" {
		t.Fatalf("unexpected body: %q", rec.Body.String())
	}
}

func TestIndex_Served(t *testing.T) {
	reg := plugin.NewRegistry()
	srv := New(reg, newTestShellFS())

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct == "" {
		t.Fatal("want Content-Type header on index")
	}
	if body := rec.Body.String(); body == "" || body[0] != '<' {
		t.Fatalf("index body not html: %q", body)
	}
}

func TestIndex_WithTitle_Overrides(t *testing.T) {
	reg := plugin.NewRegistry()
	shellFS := fstest.MapFS{
		"index.html": {Data: []byte(`<html><head><title>Agentarium</title></head><body><span class="title">Agentarium</span></body></html>`)},
	}
	srv := New(reg, shellFS, WithTitle("EDOCODE Board Assistant"))

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "<title>EDOCODE Board Assistant</title>") {
		t.Fatalf("title not overridden: %q", body)
	}
	if !strings.Contains(body, `<span class="title">EDOCODE Board Assistant</span>`) {
		t.Fatalf("header span not overridden: %q", body)
	}
	if strings.Contains(body, "Agentarium") {
		t.Fatalf("old name remains: %q", body)
	}
}

func TestIndex_NoTitle_KeepsDefault(t *testing.T) {
	reg := plugin.NewRegistry()
	shellFS := fstest.MapFS{
		"index.html": {Data: []byte("<html><head><title>Agentarium</title></head></html>")},
	}
	srv := New(reg, shellFS) // WithTitle 未指定

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "<title>Agentarium</title>") {
		t.Fatalf("default title must remain: %q", rec.Body.String())
	}
}

func TestShellAssets_Served(t *testing.T) {
	reg := plugin.NewRegistry()
	srv := New(reg, newTestShellFS())

	req := httptest.NewRequest("GET", "/assets/app.js", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if rec.Body.String() != "// shell app.js" {
		t.Fatalf("unexpected app.js: %q", rec.Body.String())
	}
}

// postPlugin は POST と GET の両方を持つ CSRF テスト用 plugin。
type postPlugin struct{}

func (postPlugin) Meta() plugin.Meta {
	return plugin.Meta{ID: "post", Title: "Post", Pane: plugin.PaneLeft, Order: 0}
}
func (postPlugin) Routes() []plugin.Route {
	return []plugin.Route{
		{Method: "POST", Path: "/echo", Handler: func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
		}},
		{Method: "GET", Path: "/list", Handler: func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
		}},
	}
}

func TestPluginRoute_POSTRejectsCrossOrigin(t *testing.T) {
	reg := plugin.NewRegistry()
	_ = reg.Register(postPlugin{})
	srv := New(reg, newTestShellFS())

	req := httptest.NewRequest("POST", "/plugins/post/echo", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("status want 403 for cross-origin POST, got %d", rec.Code)
	}
}

func TestPluginRoute_POSTAcceptsLocalhost(t *testing.T) {
	reg := plugin.NewRegistry()
	_ = reg.Register(postPlugin{})
	srv := New(reg, newTestShellFS())

	req := httptest.NewRequest("POST", "/plugins/post/echo", nil)
	req.Header.Set("Origin", "http://127.0.0.1:8780")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status want 200 for loopback POST, got %d", rec.Code)
	}
}

func TestPluginRoute_GETUnaffected(t *testing.T) {
	reg := plugin.NewRegistry()
	_ = reg.Register(postPlugin{})
	srv := New(reg, newTestShellFS())
	// GET に Origin がついても素通し（GET は副作用なし）
	req := httptest.NewRequest("GET", "/plugins/post/list", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET should be unaffected by CSRF, got %d", rec.Code)
	}
}

// mountableStub は server.WithTerminal が受け取る最小 interface を満たすテスト用 stub。
// terminal.Service が実 実装。
type mountableStub struct {
	mountCalled bool
}

func (m *mountableStub) MountOn(mux *http.ServeMux) {
	m.mountCalled = true
	mux.HandleFunc("GET /terminal/renderer", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"renderer":"stub"}`))
	})
}

func TestServer_WithTerminalCallsMountOn(t *testing.T) {
	reg := plugin.NewRegistry()
	stub := &mountableStub{}
	srv := New(reg, newTestShellFS(), WithTerminal(stub))
	if !stub.mountCalled {
		t.Fatal("WithTerminal should call MountOn during New")
	}
	req := httptest.NewRequest("GET", "/terminal/renderer", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status want 200, got %d", rec.Code)
	}
}

func TestServer_NoOptionsStillWorks(t *testing.T) {
	// 後方互換: opts なしで New が動く
	reg := plugin.NewRegistry()
	srv := New(reg, newTestShellFS())
	req := httptest.NewRequest("GET", "/api/plugins", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status want 200, got %d", rec.Code)
	}
}

func TestNew_MountsViewerRender(t *testing.T) {
	reg := plugin.NewRegistry()
	srv := New(reg, newTestShellFS())
	ts := httptest.NewServer(srv)
	defer ts.Close()

	// 同一オリジン（Origin/Referer なし）POST → 200 + HTML
	res, err := http.Post(ts.URL+"/viewer/render", "text/markdown", strings.NewReader("# Hi"))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	b, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(b), "<h1") {
		t.Fatalf("no rendered html: %s", b)
	}

	// GET は 405（POST のみ登録）
	gres, err := http.Get(ts.URL + "/viewer/render")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer gres.Body.Close()
	if gres.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", gres.StatusCode)
	}
}

func TestPluginAssets_NoDirectoryListing(t *testing.T) {
	reg := plugin.NewRegistry()
	_ = reg.Register(fakePlugin{id: "alpha", pane: plugin.PaneLeft})
	srv := New(reg, newTestShellFS())

	// ディレクトリそのもの（末尾スラッシュ）を叩くと listing ではなく 404。
	req := httptest.NewRequest("GET", "/plugins/alpha/assets/", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("directory listing should be 404, got %d body=%q", rec.Code, rec.Body.String())
	}
	// 個別ファイルは従来どおり 200。
	req2 := httptest.NewRequest("GET", "/plugins/alpha/assets/index.js", nil)
	rec2 := httptest.NewRecorder()
	srv.ServeHTTP(rec2, req2)
	if rec2.Code != 200 {
		t.Fatalf("file should be 200, got %d", rec2.Code)
	}
}

func TestEventsPublishSubscribe(t *testing.T) {
	reg := plugin.NewRegistry()
	srv := New(reg, newTestShellFS())
	// publish は 204
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"topic":"x","data":{"a":1}}`)
	srv.ServeHTTP(rec, httptest.NewRequest("POST", "/events/publish", body))
	if rec.Code != 204 {
		t.Fatalf("publish status=%d", rec.Code)
	}
	// subscribe エンドポイントが存在し SSE ヘッダを返す（即時 flush 後に context 終了で抜ける）
	req := httptest.NewRequest("GET", "/events?topic=x", nil)
	ctx, cancel := context.WithTimeout(req.Context(), 100*time.Millisecond)
	defer cancel()
	rec2 := httptest.NewRecorder()
	srv.ServeHTTP(rec2, req.WithContext(ctx))
	if ct := rec2.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type=%q", ct)
	}
}
