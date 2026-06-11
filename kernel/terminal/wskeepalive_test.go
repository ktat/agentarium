package terminal

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestConfigureWSKeepAlive_SetsReadLimit(t *testing.T) {
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		stop := ConfigureWSKeepAlive(conn)
		defer stop()
		// 読み続ける（read limit 超過で error になる）
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	u := strings.Replace(srv.URL, "http://", "ws://", 1)
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// read limit (64KiB) を超えるメッセージを送る → サーバが close する
	big := strings.Repeat("x", 70*1024)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(big)); err != nil {
		t.Fatalf("write: %v", err)
	}
	// サーバが read limit 超過で閉じるので、次の read が error になる
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("expected connection closed after exceeding read limit")
	}
}

func TestConfigureWSKeepAlive_StopIsIdempotentSafe(t *testing.T) {
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		stop := ConfigureWSKeepAlive(conn)
		stop() // 即停止しても panic / leak しない
		stop() // 2 回目の stop も冪等（done の二重 close で panic しない）
		_ = conn.Close()
	}))
	defer srv.Close()
	u := strings.Replace(srv.URL, "http://", "ws://", 1)
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = conn.Close()
}
