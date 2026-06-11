package terminal

import (
	"time"

	"github.com/gorilla/websocket"
)

const (
	// wsReadLimit は WS 1 メッセージの最大バイト数（inject の上限と同じ 64 KiB）。
	wsReadLimit = 64 * 1024
	// wsIdleTimeout は pong が来ないまま放置された接続を切るまでの猶予。
	wsIdleTimeout = 90 * time.Second
	// wsPingInterval は keepalive ping の送信間隔。
	wsPingInterval = 30 * time.Second
)

// ConfigureWSKeepAlive は WS 接続に read limit / idle deadline / pong handler を設定し、
// 定期 ping を送る goroutine を起動する。返り値 stop() を defer で呼ぶと ping goroutine を
// 止める。死んだ接続が滞留して fd / メモリリークするのを防ぐ。
//
// gorilla/websocket では WriteControl は他の読み書きと並行して呼んで安全なので、
// ping goroutine は handler 本体の read/write と競合しない。
func ConfigureWSKeepAlive(conn *websocket.Conn) (stop func()) {
	conn.SetReadLimit(wsReadLimit)
	_ = conn.SetReadDeadline(time.Now().Add(wsIdleTimeout))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(wsIdleTimeout))
	})
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(wsPingInterval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)); err != nil {
					return
				}
			}
		}
	}()
	var stopped bool
	return func() {
		if stopped {
			return
		}
		stopped = true
		close(done)
	}
}
