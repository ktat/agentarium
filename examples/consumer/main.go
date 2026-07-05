// examples/consumer は Agentarium を import して自分のアプリを組む消費者の例。
// 実際の消費者リポでは別 go.mod になり、import パスは github.com/<org>/agentarium のまま。
package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/ktat/agentarium"
	"github.com/ktat/agentarium/agents/claude"
	"github.com/ktat/agentarium/kernel/store"          // 同梱プラグイン用の永続ストア
	"github.com/ktat/agentarium/kernel/terminal"       // Agent ターミナルサービス
	"github.com/ktat/agentarium/kernel/terminal/xterm" // xterm backend
	"github.com/ktat/agentarium/plugins/chat"          // 同梱プラグインから opt-in
	"github.com/ktat/agentarium/plugins/hello"         // 同梱プラグインから opt-in
	// 実際にはここで自作の plugins/mybacklog などを import する
)

func main() {
	wd, err := os.Getwd()
	if err != nil {
		log.Fatalf("getwd: %v", err)
	}

	// 同梱プラグインのうち永続化が要るもの（chat）には、消費者 main が保存先パスを渡す。
	// store.New[T](path) は []T を JSON へ atomic 保存する汎用ストア（D7 消費モデル）。
	dir, err := os.UserConfigDir()
	if err != nil {
		log.Fatalf("config dir: %v", err)
	}
	chatStore := store.New[chat.ChatRecord](filepath.Join(dir, "agentarium-consumer", "chat.json"))

	// Chat タブの「送信」で右ペインに Agent ターミナルを起動するには terminal サービスの結線が要る。
	// ここでは xterm backend 1 本の最小構成（active backend は Backends[0]、resume 判定は楽観）。
	agents := terminal.NewAgentRegistry("claude")
	agents.Register(claude.New())
	svc, err := terminal.NewService(terminal.ServiceConfig{
		Agents:   agents,
		Backends: []terminal.Backend{&xterm.Backend{Registry: xterm.NewRegistry(wd, agents)}},
	})
	if err != nil {
		log.Fatalf("terminal service: %v", err)
	}

	app := agentarium.New()
	if err := app.Register(
		hello.Plugin{}, // 同梱（好きなものだけ）
		// 同梱: Chat タブ（自由入力→Agent 起動 + 履歴/再開）。
		// WithSessionLookup で terminal の session_id をサーバ側補完し「再開」を確実に有効化する。
		chat.New(chatStore).WithSessionLookup(svc.SessionID),
		// mybacklog.Plugin{}, // 自作ワークフロー
	); err != nil {
		log.Fatalf("register: %v", err)
	}
	app.WithTerminal(svc)

	log.Fatal(app.Run("127.0.0.1:8780"))
}
