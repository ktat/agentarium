// examples/manifest-tab は宣言的 manifest プラグイン（IF B）のサンプル。
// sessions /list を再利用し、Go コードなし（manifest.json だけ）で
// 「セッション一覧 + Resume 行ボタン」タブを追加する例。
package main

import (
	_ "embed"
	"log"
	"os"

	"github.com/ktat/agentarium"
	"github.com/ktat/agentarium/agents/claude"
	"github.com/ktat/agentarium/kernel/plugin"
	"github.com/ktat/agentarium/kernel/terminal"
	"github.com/ktat/agentarium/kernel/terminal/xterm"
	"github.com/ktat/agentarium/plugins/sessions"
)

//go:embed manifest.json
var manifestJSON []byte

func main() {
	wd, err := os.Getwd()
	if err != nil {
		log.Fatalf("getwd: %v", err)
	}

	manifestPlugin, err := plugin.NewManifestPlugin(manifestJSON)
	if err != nil {
		log.Fatalf("manifest plugin: %v", err)
	}

	app := agentarium.New()
	if err := app.Register(
		sessions.New(wd), // /plugins/sessions/list を提供（manifest の dataURL）
		manifestPlugin,   // Go コードなしの宣言的タブ
	); err != nil {
		log.Fatalf("register: %v", err)
	}

	agents := terminal.NewAgentRegistry("claude")
	agents.Register(claude.New())
	xtermBackend := &xterm.Backend{Registry: xterm.NewRegistry(wd, agents)}
	svc, err := terminal.NewService(terminal.ServiceConfig{
		Agents:   agents,
		Backends: []terminal.Backend{xtermBackend},
		Active:   terminal.EnvActiveBackend(),
	})
	if err != nil {
		log.Fatalf("terminal service: %v", err)
	}
	app.WithTerminal(svc)

	addr := os.Getenv("AGENTARIUM_ADDR")
	log.Printf("manifest-tab demo starting (addr=%q, empty=default)", addr)
	log.Fatal(app.Run(addr))
}
