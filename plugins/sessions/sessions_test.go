package sessions

import (
	"encoding/json"
	"io/fs"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ktat/agentarium/kernel/plugin"
)

func TestPlugin_Meta(t *testing.T) {
	p := New("/some/dir")
	var _ plugin.Plugin = p
	if p.Meta().ID != "sessions" {
		t.Fatalf("want id sessions, got %s", p.Meta().ID)
	}
	if p.Meta().Pane != plugin.PaneLeft {
		t.Fatalf("want pane left, got %v", p.Meta().Pane)
	}
}

func TestPlugin_ListHandlerReturnsJSON(t *testing.T) {
	tmp := t.TempDir()
	dir, _ := SessionsDirFor(tmp)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Skipf("mkdir %s failed (HOME 設定無し?): %v", dir, err)
	}
	defer os.Remove(filepath.Join(dir, "x.jsonl"))
	if err := os.WriteFile(filepath.Join(dir, "x.jsonl"), []byte{}, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	p := New(tmp)
	var listH func(rec *httptest.ResponseRecorder)
	for _, rt := range p.Routes() {
		if rt.Method == "GET" && rt.Path == "/list" {
			rt := rt
			listH = func(rec *httptest.ResponseRecorder) {
				req := httptest.NewRequest("GET", "/list", nil)
				rt.Handler(rec, req)
			}
		}
	}
	if listH == nil {
		t.Fatal("GET /list route not found")
	}
	rec := httptest.NewRecorder()
	listH(rec)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var got []Session
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("json: %v body=%s", err, rec.Body.String())
	}
	if len(got) != 1 || got[0].UUID != "x" {
		t.Fatalf("want 1 session uuid=x, got %+v", got)
	}
}

// TestPlugin_ListHandlerDoesNotLeakPathOnError は走査失敗時の HTTP 応答に
// 絶対パス（~/.claude/projects/<encoded> 等）が漏れないことを検証する（R4）。
func TestPlugin_ListHandlerDoesNotLeakPathOnError(t *testing.T) {
	tmp := t.TempDir()
	dir, err := SessionsDirFor(tmp)
	if err != nil {
		t.Skipf("SessionsDirFor failed (HOME 設定無し?): %v", err)
	}
	// sessions dir をディレクトリではなくファイルとして作る → ReadDir が
	// ENOTDIR エラー（path を含む）を返し、エラー分岐に入る。
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Skipf("mkdir parent failed: %v", err)
	}
	if err := os.WriteFile(dir, []byte("x"), 0o644); err != nil {
		t.Skipf("write file failed: %v", err)
	}
	defer os.Remove(dir)

	p := New(tmp)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/list", nil)
	p.handleList(rec, req)

	if rec.Code != 500 {
		t.Fatalf("want 500, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, dir) || strings.Contains(body, ".claude") || strings.Contains(body, tmp) {
		t.Fatalf("error response leaks filesystem path: %q", body)
	}
}

func TestPlugin_AssetsHasIndexJS(t *testing.T) {
	p := New("/x")
	b, err := fs.ReadFile(p.Assets(), "index.js")
	if err != nil || len(b) == 0 {
		t.Fatalf("index.js missing: %v", err)
	}
}
