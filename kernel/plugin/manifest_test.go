package plugin

import (
	"io/fs"
	"net/http/httptest"
	"strings"
	"testing"
)

const goodManifest = `{
  "id": "sessions-manifest",
  "title": "Sessions (manifest)",
  "pane": "left",
  "order": 1,
  "dataURL": "/plugins/sessions/list",
  "render": "list",
  "list": { "columns": [
    { "label": "UUID", "value": "{{.uuid}}" },
    { "label": "要約", "value": "{{.summary}}" }
  ]},
  "rowAction": {
    "label": "Resume", "type": "openAgent", "agent": "claude",
    "resume": "{{.uuid}}", "key": "session-{{.uuid}}", "tabLabel": "{{.uuid}}"
  }
}`

func TestNewManifestPlugin_Good(t *testing.T) {
	p, err := NewManifestPlugin([]byte(goodManifest))
	if err != nil {
		t.Fatalf("NewManifestPlugin: %v", err)
	}
	m := p.Meta()
	if m.ID != "sessions-manifest" || m.Title != "Sessions (manifest)" {
		t.Fatalf("meta id/title mismatch: %+v", m)
	}
	if m.Pane != PaneLeft || m.Order != 1 {
		t.Fatalf("meta pane/order mismatch: %+v", m)
	}
	rp, ok := p.(RouteProvider)
	if !ok {
		t.Fatal("manifest plugin should implement RouteProvider")
	}
	routes := rp.Routes()
	if len(routes) != 1 || routes[0].Method != "GET" || routes[0].Path != "/manifest" {
		t.Fatalf("routes mismatch: %+v", routes)
	}
	fp, ok := p.(FrontendProvider)
	if !ok {
		t.Fatal("manifest plugin should implement FrontendProvider")
	}
	if _, err := fs.Stat(fp.Assets(), "index.js"); err != nil {
		t.Fatalf("Assets() must contain index.js: %v", err)
	}
}

func TestManifestPlugin_ServeManifestReturnsRawJSON(t *testing.T) {
	p, err := NewManifestPlugin([]byte(goodManifest))
	if err != nil {
		t.Fatalf("NewManifestPlugin: %v", err)
	}
	h := p.(RouteProvider).Routes()[0].Handler
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("GET", "/manifest", nil))
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
	if !strings.Contains(rec.Body.String(), `"sessions-manifest"`) {
		t.Fatalf("manifest body missing id: %s", rec.Body.String())
	}
}

func TestManifestPlugin_PaneRight(t *testing.T) {
	src := `{"id":"x","title":"X","pane":"right","dataURL":"/d","render":"list",
		"list":{"columns":[{"label":"L","value":"{{.a}}"}]}}`
	p, err := NewManifestPlugin([]byte(src))
	if err != nil {
		t.Fatalf("NewManifestPlugin: %v", err)
	}
	if p.Meta().Pane != PaneRight {
		t.Fatalf("pane = %v, want PaneRight", p.Meta().Pane)
	}
}

func TestNewManifestPlugin_Invalid(t *testing.T) {
	cases := map[string]string{
		"bad json":         `{not json`,
		"invalid id":       `{"id":"Bad ID","title":"T","dataURL":"/d","render":"list","list":{"columns":[{"label":"L","value":"x"}]}}`,
		"empty title":      `{"id":"x","title":"","dataURL":"/d","render":"list","list":{"columns":[{"label":"L","value":"x"}]}}`,
		"bad pane":         `{"id":"x","title":"T","pane":"top","dataURL":"/d","render":"list","list":{"columns":[{"label":"L","value":"x"}]}}`,
		"render not list":  `{"id":"x","title":"T","dataURL":"/d","render":"table","list":{"columns":[{"label":"L","value":"x"}]}}`,
		"no columns":       `{"id":"x","title":"T","dataURL":"/d","render":"list","list":{"columns":[]}}`,
		"empty col label":  `{"id":"x","title":"T","dataURL":"/d","render":"list","list":{"columns":[{"label":"","value":"x"}]}}`,
		"absolute dataURL": `{"id":"x","title":"T","dataURL":"https://evil/x","render":"list","list":{"columns":[{"label":"L","value":"x"}]}}`,
		"bad action type":  `{"id":"x","title":"T","dataURL":"/d","render":"list","list":{"columns":[{"label":"L","value":"x"}]},"rowAction":{"type":"openURL","agent":"claude"}}`,
		"action no agent":  `{"id":"x","title":"T","dataURL":"/d","render":"list","list":{"columns":[{"label":"L","value":"x"}]},"rowAction":{"type":"openAgent","agent":""}}`,
	}
	for name, src := range cases {
		if _, err := NewManifestPlugin([]byte(src)); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

func TestNewManifestPlugin_RowActionOptional(t *testing.T) {
	src := `{"id":"ro","title":"RO","dataURL":"/d","render":"list",
		"list":{"columns":[{"label":"L","value":"{{.a}}"}]}}`
	if _, err := NewManifestPlugin([]byte(src)); err != nil {
		t.Fatalf("rowAction omitted should be valid: %v", err)
	}
}
