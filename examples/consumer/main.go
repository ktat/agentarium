// examples/consumer は Agentarium を import して自分のアプリを組む消費者の例。
// 実際の消費者リポでは別 go.mod になり、import パスは github.com/<org>/agentarium のまま。
package main

import (
	"log"

	"github.com/ktat/agentarium"
	"github.com/ktat/agentarium/plugins/hello" // 同梱プラグインから opt-in
	// 実際にはここで自作の plugins/mybacklog などを import する
)

func main() {
	app := agentarium.New()
	if err := app.Register(
		hello.Plugin{}, // 同梱（好きなものだけ）
		// mybacklog.Plugin{}, // 自作ワークフロー
	); err != nil {
		log.Fatalf("register: %v", err)
	}
	log.Fatal(app.Run("127.0.0.1:8780"))
}
