// Package wrap の WS handler 部分。クライアントは差分描画ブラウザ JS が走る前提で、
// サーバ側で VT エミュレートした grid を WSMessage で配信し、入力は ClientInput で受ける。
package wrap

import (
	"net/http"

	"github.com/gorilla/websocket"

	"github.com/ktat/agentarium/kernel/server"
	"github.com/ktat/agentarium/kernel/terminal"
)

// wrapUpgrader は wrap backend 用の WS Upgrader。
// CheckOrigin で server.IsLocalOriginOrAbsent を呼び CSRF 等価の防御を行う。
var wrapUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return server.IsLocalOriginOrAbsent(r) },
}

// clampResizeDim は WS から来た cols/altRows を PTY winsize の uint16 範囲に収める。
// 負値・0 は 0（backend 既定にフォールバック）、65535 超は 65535 に丸める。
// int のまま Process.Resize → uint16 キャストで wrap-around するのを防ぐ。
func clampResizeDim(v int) int {
	if v < 0 {
		return 0
	}
	if v > 65535 {
		return 65535
	}
	return v
}

// HandleWS は GET /ws?id=<terminal-id> に対する WebSocket handler。
//
// 接続フロー:
//  1. 接続直後に Process.Snapshot() を送る（type=init）
//  2. Process.Subscribe() で update を受信 → conn に転送
//  3. クライアントから ClientInput を受け取り PTY に流す
func (b *Backend) HandleWS(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}
	p := b.Registry.Get(id)
	if p == nil {
		http.Error(w, "process not found", http.StatusNotFound)
		return
	}
	conn, err := wrapUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	stop := terminal.ConfigureWSKeepAlive(conn)
	defer stop()

	// 不変条件（順序を変えるリファクタを禁ずる）:
	//  1) Snapshot() を main goroutine から先に WriteJSON（init を最初に届ける）
	//  2) その後 Subscribe して update を別 goroutine で WriteJSON
	//  3) main goroutine は ReadJSON ループ（client input）
	// WriteJSON は subscribe goroutine と main の 2 系統から呼ばれるが、
	// gorilla は「1 writer / 1 reader の並行」を許すため、書き込みは subscribe
	// goroutine に集約し main は read 専従にしている（init の WriteJSON は
	// subscribe 前なので競合しない）。defer の順序は cancel() → conn.Close() で、
	// cancel が subscribe goroutine を止めてから conn を閉じる。
	// （conn.Close が先に defer 宣言 → cancel が後に defer 宣言 → LIFO で cancel が先に走る）

	// 1) snapshot を init として配信
	if err := conn.WriteJSON(p.Snapshot()); err != nil {
		return
	}

	// 2) subscribe → conn へ転送（別 goroutine）
	ch, cancel := p.Subscribe()
	defer cancel()
	go func() {
		for msg := range ch {
			if err := conn.WriteJSON(msg); err != nil {
				return
			}
		}
	}()

	// 3) クライアント input ループ
	for {
		var ci ClientInput
		if err := conn.ReadJSON(&ci); err != nil {
			return
		}
		switch ci.Type {
		case "input":
			_ = p.Write([]byte(ci.Data))
		case "paste":
			p.Paste(ci.Data)
		case "resize":
			_ = p.Resize(clampResizeDim(ci.Cols), clampResizeDim(ci.AltRows))
		}
	}
}
