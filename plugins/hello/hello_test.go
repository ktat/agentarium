package hello

import (
	"io/fs"
	"net/http/httptest"
	"testing"

	"github.com/ktat/agentarium/kernel/plugin"
)

func TestMeta(t *testing.T) {
	var p plugin.Plugin = Plugin{}
	if p.Meta().ID != "hello" {
		t.Fatalf("want id hello, got %s", p.Meta().ID)
	}
}

func TestPing(t *testing.T) {
	routes := Plugin{}.Routes()
	if len(routes) != 1 {
		t.Fatalf("want 1 route, got %d", len(routes))
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/ping", nil)
	routes[0].Handler(rec, req)
	if rec.Code != 200 {
		t.Fatalf("want 200, got %d", rec.Code)
	}
}

func TestAssetsHaveIndexJS(t *testing.T) {
	b, err := fs.ReadFile(Plugin{}.Assets(), "index.js")
	if err != nil || len(b) == 0 {
		t.Fatalf("index.js missing or empty: %v", err)
	}
}
