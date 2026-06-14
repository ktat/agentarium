package xterm

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/ktat/agentarium/kernel/terminal"
)

func newWSBackend(t *testing.T) (*Backend, *httptest.Server) {
	t.Helper()
	b := &Backend{Registry: NewRegistry("", nil)}
	mux := http.NewServeMux()
	for _, rt := range b.Routes() {
		mux.HandleFunc(rt.Method+" /terminal"+rt.Path, rt.Handler)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return b, srv
}

func wsURL(httpURL, path, query string) string {
	u := strings.Replace(httpURL, "http://", "ws://", 1)
	if query != "" {
		return u + path + "?" + query
	}
	return u + path
}

func TestXtermWS_EchoViaInput(t *testing.T) {
	b, srv := newWSBackend(t)
	ag := terminal.ConfigAgent{AgentName: "cat", Binary: "cat"}
	if err := b.Start("t1", "L", ag, terminal.RunRequest{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = b.Stop("t1") }()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL(srv.URL, "/terminal/ws", "id=t1"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(map[string]any{"type": "input", "data": "ping\n"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		var msg struct {
			Type string `json:"type"`
			Data string `json:"data"`
		}
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("read: %v", err)
		}
		if msg.Type == "output" && strings.Contains(msg.Data, "ping") {
			return
		}
	}
}

func TestXtermWS_UnknownIDReturns404(t *testing.T) {
	_, srv := newWSBackend(t)
	_, resp, err := websocket.DefaultDialer.Dial(wsURL(srv.URL, "/terminal/ws", "id=missing"), nil)
	if err == nil {
		t.Fatal("expected dial to fail for unknown id")
	}
	if resp == nil || resp.StatusCode != 404 {
		gotStatus := -1
		if resp != nil {
			gotStatus = resp.StatusCode
		}
		t.Fatalf("want 404, got %d", gotStatus)
	}
}

func TestXtermWS_RejectsCrossOrigin(t *testing.T) {
	b, srv := newWSBackend(t)
	ag := terminal.ConfigAgent{AgentName: "cat", Binary: "cat"}
	if err := b.Start("t1", "L", ag, terminal.RunRequest{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = b.Stop("t1") }()

	headers := http.Header{"Origin": []string{"https://evil.example"}}
	_, resp, err := websocket.DefaultDialer.Dial(wsURL(srv.URL, "/terminal/ws", "id=t1"), headers)
	if err == nil {
		t.Fatal("cross-origin dial should fail")
	}
	if resp == nil || resp.StatusCode != 403 {
		gotStatus := -1
		if resp != nil {
			gotStatus = resp.StatusCode
		}
		t.Fatalf("want 403, got %d", gotStatus)
	}
}
