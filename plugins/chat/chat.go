// Package chat は自由入力テキストを既定エージェントの初期入力として起動し、
// chat 起動分の履歴を一覧・再開する同梱プラグイン。
package chat

import (
	"embed"
	"io/fs"
	"sync"

	"github.com/ktat/agentarium/kernel/plugin"
	"github.com/ktat/agentarium/kernel/store"
)

//go:embed assets/*
var assetsFS embed.FS

// ChatRecord は 1 回の chat 起動を表す永続レコード。
type ChatRecord struct {
	ID         string `json:"id"`                    // 採番した chat id（= terminal key）
	Summary    string `json:"summary"`               // ユーザー入力本文（クリーン）
	StartedAt  string `json:"started_at"`            // RFC3339
	SessionID  string `json:"session_id,omitempty"`  // 再開識別子。未取得は空
	ArchivedAt string `json:"archived_at,omitempty"` // archive 時刻。空=非archive
}

// Plugin は chat 履歴ストアを保持する同梱プラグイン。
type Plugin struct {
	store *store.JSONStore[ChatRecord]
	mu    *sync.Mutex
}

// New は chat レコードストアを注入して Plugin を構築する。
// 消費者 main で `chat.New(store.New[chat.ChatRecord](path))` を Register する想定。
func New(st *store.JSONStore[ChatRecord]) Plugin { return Plugin{store: st, mu: &sync.Mutex{}} }

func (p Plugin) Meta() plugin.Meta {
	return plugin.Meta{ID: "chat", Title: "Chat", Pane: plugin.PaneLeft, Order: 0}
}

func (p Plugin) Assets() fs.FS {
	sub, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		panic(err) // embed パス固定なので起こり得ない
	}
	return sub
}
