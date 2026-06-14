package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ktat/agentarium/kernel/plugin"
	"github.com/ktat/agentarium/kernel/store"
	"github.com/ktat/agentarium/kernel/terminal"
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

// routeOf は指定 method/path のハンドラを返す。
func routeOf(t *testing.T, p Plugin, method, path string) http.HandlerFunc {
	t.Helper()
	for _, rt := range p.Routes() {
		if rt.Method == method && rt.Path == path {
			return rt.Handler
		}
	}
	t.Fatalf("route %s %s not found", method, path)
	return nil
}

func TestStartThenList(t *testing.T) {
	p := newTestPlugin(t)

	start := routeOf(t, p, "POST", "/start")
	rec := httptest.NewRecorder()
	start(rec, httptest.NewRequest("POST", "/start", strings.NewReader(`{"summary":"hello world"}`)))
	if rec.Code != 200 {
		t.Fatalf("start status %d body=%s", rec.Code, rec.Body.String())
	}
	var sr struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &sr); err != nil {
		t.Fatalf("decode start resp: %v", err)
	}
	if sr.ID == "" {
		t.Fatal("start should return non-empty id")
	}

	list := routeOf(t, p, "GET", "/list")
	rec = httptest.NewRecorder()
	list(rec, httptest.NewRequest("GET", "/list", nil))
	if rec.Code != 200 {
		t.Fatalf("list status %d", rec.Code)
	}
	var lr struct {
		Items []ChatRecord `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &lr); err != nil {
		t.Fatalf("decode list resp: %v", err)
	}
	if len(lr.Items) != 1 || lr.Items[0].Summary != "hello world" {
		t.Fatalf("want 1 item 'hello world', got %+v", lr.Items)
	}
	if lr.Items[0].ID != sr.ID {
		t.Fatalf("list id %s != start id %s", lr.Items[0].ID, sr.ID)
	}
}

func TestStartRejectsEmptySummary(t *testing.T) {
	p := newTestPlugin(t)
	start := routeOf(t, p, "POST", "/start")
	rec := httptest.NewRecorder()
	start(rec, httptest.NewRequest("POST", "/start", strings.NewReader(`{"summary":"   "}`)))
	if rec.Code != 400 {
		t.Fatalf("want 400 for empty summary, got %d", rec.Code)
	}
}

func TestListEmptyReturnsEmptyArray(t *testing.T) {
	p := newTestPlugin(t)
	rec := httptest.NewRecorder()
	routeOf(t, p, "GET", "/list")(rec, httptest.NewRequest("GET", "/list", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	// items は null ではなく [] であること
	if !strings.Contains(rec.Body.String(), `"items": []`) && !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Fatalf("want items as empty array, got %s", rec.Body.String())
	}
}

// seedOne は 1 件 start して id を返す。
func seedOne(t *testing.T, p Plugin, summary string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	routeOf(t, p, "POST", "/start")(rec, httptest.NewRequest("POST", "/start", strings.NewReader(`{"summary":"`+summary+`"}`)))
	if rec.Code != 200 {
		t.Fatalf("seed start status %d", rec.Code)
	}
	var sr struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &sr)
	return sr.ID
}

func listItems(t *testing.T, p Plugin) []ChatRecord {
	t.Helper()
	rec := httptest.NewRecorder()
	routeOf(t, p, "GET", "/list")(rec, httptest.NewRequest("GET", "/list", nil))
	var lr struct {
		Items []ChatRecord `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &lr); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	return lr.Items
}

func TestUpdateSetsSessionID(t *testing.T) {
	p := newTestPlugin(t)
	id := seedOne(t, p, "hi")

	rec := httptest.NewRecorder()
	routeOf(t, p, "POST", "/update")(rec, httptest.NewRequest("POST", "/update?id="+id+"&session_id=abc-123", nil))
	if rec.Code != 204 {
		t.Fatalf("update status %d body=%s", rec.Code, rec.Body.String())
	}
	items := listItems(t, p)
	if len(items) != 1 || items[0].SessionID != "abc-123" {
		t.Fatalf("want session_id abc-123, got %+v", items)
	}
}

func TestArchiveSetsTimestamp(t *testing.T) {
	p := newTestPlugin(t)
	id := seedOne(t, p, "hi")

	rec := httptest.NewRecorder()
	routeOf(t, p, "POST", "/archive")(rec, httptest.NewRequest("POST", "/archive?id="+id, nil))
	if rec.Code != 204 {
		t.Fatalf("archive status %d", rec.Code)
	}
	items := listItems(t, p)
	if len(items) != 1 || items[0].ArchivedAt == "" {
		t.Fatalf("want archived_at set, got %+v", items)
	}
}

func TestUpdateUnknownIDReturns404(t *testing.T) {
	p := newTestPlugin(t)
	rec := httptest.NewRecorder()
	routeOf(t, p, "POST", "/update")(rec, httptest.NewRequest("POST", "/update?id=nope&session_id=x", nil))
	if rec.Code != 404 {
		t.Fatalf("want 404, got %d", rec.Code)
	}
}

func TestUpdateRequiresID(t *testing.T) {
	p := newTestPlugin(t)
	rec := httptest.NewRecorder()
	routeOf(t, p, "POST", "/update")(rec, httptest.NewRequest("POST", "/update?session_id=x", nil))
	if rec.Code != 400 {
		t.Fatalf("want 400 for missing id, got %d", rec.Code)
	}
}

func TestStartIDIsValidTerminalID(t *testing.T) {
	p := newTestPlugin(t)
	id := seedOne(t, p, "hello")
	if _, err := terminal.NewTerminalID(id); err != nil {
		t.Fatalf("minted chat id %q is not a valid terminal id: %v", id, err)
	}
}
