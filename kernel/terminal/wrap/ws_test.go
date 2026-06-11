package wrap

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
	agents := terminal.NewAgentRegistry("cat")
	agents.Register(terminal.ConfigAgent{AgentName: "cat", Binary: "cat"})
	b := &Backend{Registry: NewRegistry("", agents)}
	mux := http.NewServeMux()
	for _, rt := range b.Routes() {
		mux.HandleFunc(rt.Method+" /terminal"+rt.Path, rt.Handler)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return b, srv
}

func wrapWSURL(httpURL, query string) string {
	return strings.Replace(httpURL, "http://", "ws://", 1) + "/terminal/ws?" + query
}

func TestWrapWS_InitMessageOnConnect(t *testing.T) {
	b, srv := newWSBackend(t)
	ag := terminal.ConfigAgent{AgentName: "cat", Binary: "cat"}
	if err := b.Start("t1", "L", ag, terminal.RunRequest{Cols: 80, AltRows: 30}); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = b.Stop("t1") }()

	conn, _, err := websocket.DefaultDialer.Dial(wrapWSURL(srv.URL, "id=t1"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var msg WSMessage
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read: %v", err)
	}
	if msg.Type != "init" {
		t.Fatalf("want type=init, got %q", msg.Type)
	}
	if msg.Cols != 80 {
		t.Fatalf("want Cols=80, got %d", msg.Cols)
	}
}

func TestWrapWS_UnknownIDReturns404(t *testing.T) {
	_, srv := newWSBackend(t)
	_, resp, err := websocket.DefaultDialer.Dial(wrapWSURL(srv.URL, "id=missing"), nil)
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

func TestWrapWS_RejectsCrossOrigin(t *testing.T) {
	b, srv := newWSBackend(t)
	ag := terminal.ConfigAgent{AgentName: "cat", Binary: "cat"}
	if err := b.Start("t1", "L", ag, terminal.RunRequest{}); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = b.Stop("t1") }()

	headers := http.Header{"Origin": []string{"https://evil.example"}}
	_, resp, err := websocket.DefaultDialer.Dial(wrapWSURL(srv.URL, "id=t1"), headers)
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

func TestClampResizeDim(t *testing.T) {
	cases := map[int]int{
		0:      0,     // 0 はそのまま（backend 既定にフォールバック）
		-1:     0,     // 負値は 0
		80:     80,
		65535:  65535,
		65536:  65535, // uint16 上限でクランプ
		100000: 65535,
	}
	for in, want := range cases {
		if got := clampResizeDim(in); got != want {
			t.Errorf("clampResizeDim(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestWrapWS_InputResize(t *testing.T) {
	b, srv := newWSBackend(t)
	ag := terminal.ConfigAgent{AgentName: "cat", Binary: "cat"}
	if err := b.Start("t1", "L", ag, terminal.RunRequest{Cols: 80, AltRows: 30}); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = b.Stop("t1") }()

	conn, _, err := websocket.DefaultDialer.Dial(wrapWSURL(srv.URL, "id=t1"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	// init を読み捨て
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var init WSMessage
	if err := conn.ReadJSON(&init); err != nil {
		t.Fatalf("read init: %v", err)
	}
	if err := conn.WriteJSON(ClientInput{Type: "input", Data: "x"}); err != nil {
		t.Fatalf("write input: %v", err)
	}
	if err := conn.WriteJSON(ClientInput{Type: "resize", Cols: 120, AltRows: 40}); err != nil {
		t.Fatalf("write resize: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
}
