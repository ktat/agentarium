// Package xterm の WS handler 部分。クライアントは xterm.js が走る前提で、
// PTY の生バイトをそのまま受信し、入力は {type:"input"|"resize"} で送り返す。
package xterm

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"

	"github.com/ktat/agentarium/kernel/server"
	"github.com/ktat/agentarium/kernel/terminal"
)

// xtermUpgrader は gorilla/websocket の Upgrader。CheckOrigin で
// server.IsLocalOriginOrAbsent を呼び CSRF 等価の防御を行う。
var xtermUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return server.IsLocalOriginOrAbsent(r) },
}

type xtermClientMessage struct {
	Type string `json:"type"` // "input" / "resize"
	Data string `json:"data,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
}

type xtermServerMessage struct {
	Type string `json:"type"` // "output"
	Data string `json:"data"`
}

// xtermWSWriter は PTY → WS への書き込みアダプタ。WriteMessage は
// 単一の goroutine で並列呼び出しから保護する必要があるため mu を持つ。
type xtermWSWriter struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (w *xtermWSWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	b, err := json.Marshal(xtermServerMessage{Type: "output", Data: string(p)})
	if err != nil {
		return 0, err
	}
	if err := w.conn.WriteMessage(websocket.TextMessage, b); err != nil {
		return 0, err
	}
	return len(p), nil
}

// HandleWS は GET /ws?id=<terminal-id> に対する WebSocket handler。
// id 未指定 → 400、未知 id → 404、cross-origin Upgrade → 403（upgrader.CheckOrigin）。
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
	conn, err := xtermUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	stop := terminal.ConfigureWSKeepAlive(conn)
	defer stop()

	// 不変条件: ReplayBuffer を先に Write（過去出力を復元）してから AddWriter で
	// live 出力を購読する。Replay と AddWriter の間に到着した出力は最大数 ms の
	// 隙間で取りこぼしうるが実用上許容（移植元と同じ妥協）。
	wr := &xtermWSWriter{conn: conn}
	if replay := p.ReplayBuffer(); len(replay) > 0 {
		if _, err := wr.Write(replay); err != nil {
			return
		}
	}
	p.AddWriter(wr)
	defer p.RemoveWriter(wr)

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var cm xtermClientMessage
		if err := json.Unmarshal(msg, &cm); err != nil {
			continue
		}
		switch cm.Type {
		case "input":
			_ = p.Write([]byte(cm.Data))
		case "resize":
			if cm.Rows > 0 && cm.Cols > 0 {
				_ = p.Resize(cm.Rows, cm.Cols)
			}
		}
	}
}
