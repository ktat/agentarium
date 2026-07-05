// cmd/agentarium は参照デモアプリ。
// hello + sessions + chat + manifest プラグインと xterm ターミナル、secrets を結線して起動する。
// `agentarium secrets rekey ...` で暗号化値の鍵移行も行う。
package main

import (
	_ "embed"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/ktat/agentarium"
	"github.com/ktat/agentarium/agents/claude"
	"github.com/ktat/agentarium/kernel/pet"
	"github.com/ktat/agentarium/kernel/plugin"
	"github.com/ktat/agentarium/kernel/secrets"
	"github.com/ktat/agentarium/kernel/settings"
	"github.com/ktat/agentarium/kernel/store"
	"github.com/ktat/agentarium/kernel/terminal"
	"github.com/ktat/agentarium/kernel/terminal/wrap"
	"github.com/ktat/agentarium/kernel/terminal/xterm"
	"github.com/ktat/agentarium/plugins/chat"
	"github.com/ktat/agentarium/plugins/hello"
	"github.com/ktat/agentarium/plugins/sessions"
	"github.com/ktat/agentarium/plugins/slack"
)

//go:embed sessions-manifest.json
var sessionsManifestJSON []byte

// secretsPaths は設定データと鍵ファイルのパスを返す（os.UserConfigDir 配下）。
func secretsPaths() (data, key string, err error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", "", err
	}
	base := filepath.Join(dir, "agentarium")
	return filepath.Join(base, "settings.json"), filepath.Join(base, "secret.key"), nil
}

// terminalStorePath は renderer 別のセッション永続化ファイルパスを返す
// （UserConfigDir/agentarium/terminal-<renderer>.json）。
func terminalStorePath(renderer string) (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "agentarium", "terminal-"+renderer+".json"), nil
}

// chatStorePath は chat 履歴の永続化ファイルパスを返す
// （os.UserConfigDir 配下、terminalStorePath と同じ流儀）。
func chatStorePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "agentarium", "chat.json"), nil
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "secrets" {
		if err := runSecrets(os.Args[2:]); err != nil {
			log.Fatalf("secrets: %v", err)
		}
		return
	}
	if err := runServer(); err != nil {
		log.Fatal(err)
	}
}

// runSecrets は `agentarium secrets rekey ...` を処理する。
func runSecrets(args []string) error {
	if len(args) == 0 || args[0] != "rekey" {
		return fmt.Errorf("usage: agentarium secrets rekey [--old-pepper=..] [--new-pepper=..] [--rotate-passphrase]")
	}
	fs := flag.NewFlagSet("secrets rekey", flag.ExitOnError)
	oldPepper := fs.String("old-pepper", "", "現在の暗号値の pepper（既定: 空）")
	newPepper := fs.String("new-pepper", "", "移行後の pepper")
	rotate := fs.Bool("rotate-passphrase", false, "パスフレーズも再生成する")
	dataF := fs.String("data", "", "settings.json パス（既定: UserConfigDir）")
	keyF := fs.String("key", "", "secret.key パス（既定: UserConfigDir）")
	_ = fs.Parse(args[1:])

	data, key := *dataF, *keyF
	if data == "" || key == "" {
		d, k, err := secretsPaths()
		if err != nil {
			return err
		}
		if data == "" {
			data = d
		}
		if key == "" {
			key = k
		}
	}
	n, err := secrets.RekeyFile(data, key, *oldPepper, *newPepper, *rotate)
	if err != nil {
		return err
	}
	fmt.Printf("rekey: %d secret(s) re-encrypted\n", n)
	fmt.Println("注意: 新 pepper でアプリを再ビルドしてください（make build PEPPER=<新>）")
	return nil
}

func runServer() error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}

	manifestPlugin, err := plugin.NewManifestPlugin(sessionsManifestJSON)
	if err != nil {
		return err
	}

	dataPath, keyPath, err := secretsPaths()
	if err != nil {
		return err
	}
	sec, err := secrets.NewStore(dataPath, keyPath)
	if err != nil {
		return err
	}

	chatPath, err := chatStorePath()
	if err != nil {
		return err
	}
	chatStore := store.New[chat.ChatRecord](chatPath)

	agents := terminal.NewAgentRegistry("claude")
	agents.Register(claude.New())
	wrapStorePath, err := terminalStorePath("wrap")
	if err != nil {
		return err
	}
	xtermStorePath, err := terminalStorePath("xterm")
	if err != nil {
		return err
	}
	// xterm / wrap 両 backend をコンパイルに含めて登録し、実行時に active を選ぶ。
	// Store 付きにすることで再起動越えのセッション復元（lazy restore）が有効になる。
	xtermBackend := &xterm.Backend{Registry: xterm.NewRegistryWithStore(wd, agents, terminal.NewStore(xtermStorePath))}
	wrapBackend := &wrap.Backend{Registry: wrap.NewRegistryWithStore(wd, agents, wrap.NewStore(wrapStorePath))}
	// active backend は Settings（kernel.terminal_renderer）→ env → 既定 xterm の順で決定。
	active := settings.TerminalRenderer(sec)
	if active == "" {
		active = terminal.EnvActiveBackend()
	}
	// canResume: 永続レコードの Agent を解決し、その Agent が表明する artifact の存在で
	// resume 可否を判定する（claude なら jsonl 存在チェック）。
	// 永続レコードは常に具体的な Agent 名を持つ前提。agents.Resolve が nil を返す
	// （空名 / 未登録）場合は CanResume が楽観的に true を返す（resolveAgent の Default
	// fallback とは非対称だが、未知 agent は復元側で既に skip 済みのため実害なし）。
	canResume := func(rec terminal.SessionRecord) bool {
		return sessions.CanResume(agents.Resolve(rec.Agent), rec.WorkDir, rec.SessionID)
	}
	svc, err := terminal.NewService(terminal.ServiceConfig{
		Agents:    agents,
		Backends:  []terminal.Backend{xtermBackend, wrapBackend},
		Active:    active,
		CanResume: canResume,
	})
	if err != nil {
		return err
	}

	app := agentarium.New()
	if err := app.Register(
		hello.Plugin{},
		sessions.New(wd),
		// chat に terminal の session_id 逆引きを注入し、/list 取得時にサーバ側で
		// session_id を補完する（ブラウザのポーリング取りこぼし対策）。
		chat.New(chatStore).WithSessionLookup(svc.SessionID),
		manifestPlugin,
		slack.New(sec),
	); err != nil {
		return err
	}
	app.WithSecrets(sec)
	app.WithTerminal(svc)
	app.WithPet(pet.New(sec, svc.EventSubscriberCount))
	log.Printf("agentarium: active terminal renderer = %q", active)

	addr := os.Getenv("AGENTARIUM_ADDR")
	log.Printf("agentarium demo starting (addr=%q, empty=default)", addr)
	return app.Run(addr)
}
