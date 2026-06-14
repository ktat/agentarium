// cmd/agentarium は参照デモアプリ。
// hello + sessions + manifest プラグインと xterm ターミナル、secrets を結線して起動する。
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
	"github.com/ktat/agentarium/kernel/pet"
	"github.com/ktat/agentarium/kernel/plugin"
	"github.com/ktat/agentarium/kernel/secrets"
	"github.com/ktat/agentarium/kernel/settings"
	"github.com/ktat/agentarium/kernel/terminal"
	"github.com/ktat/agentarium/kernel/terminal/wrap"
	"github.com/ktat/agentarium/kernel/terminal/xterm"
	"github.com/ktat/agentarium/plugins/hello"
	"github.com/ktat/agentarium/plugins/sessions"
)

//go:embed sessions-manifest.json
var sessionsManifestJSON []byte

// claudeAgent は claude バイナリ用の Agent。RunRequest を claude 固有引数に変換する。
type claudeAgent struct{}

func (claudeAgent) Name() string { return "claude" }
func (claudeAgent) Invocation(req terminal.RunRequest) (string, []string) {
	var args []string
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	if req.Resume != "" {
		args = append(args, "--resume", req.Resume)
	}
	if req.SessionName != "" {
		args = append(args, "-n", req.SessionName)
	}
	return "claude", args
}

// ResumeArtifact は claude セッション履歴 jsonl のパスを返す（terminal.ResumableAgent）。
// 存在すれば --resume 可能。sessions プラグインの projects dir 規約を再利用する。
func (claudeAgent) ResumeArtifact(workDir, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	dir, err := sessions.SessionsDirFor(workDir)
	if err != nil {
		return ""
	}
	return filepath.Join(dir, sessionID+".jsonl")
}

// secretsPaths は設定データと鍵ファイルのパスを返す（os.UserConfigDir 配下）。
func secretsPaths() (data, key string, err error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", "", err
	}
	base := filepath.Join(dir, "agentarium")
	return filepath.Join(base, "settings.json"), filepath.Join(base, "secret.key"), nil
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

	app := agentarium.New()
	if err := app.Register(
		hello.Plugin{},
		sessions.New(wd),
		manifestPlugin,
	); err != nil {
		return err
	}
	app.WithSecrets(sec)

	agents := terminal.NewAgentRegistry("claude")
	agents.Register(claudeAgent{})
	// xterm / wrap 両 backend をコンパイルに含めて登録し、実行時に active を選ぶ。
	xtermBackend := &xterm.Backend{Registry: xterm.NewRegistry(wd, agents)}
	wrapBackend := &wrap.Backend{Registry: wrap.NewRegistry(wd, agents)}
	// active backend は Settings（kernel.terminal_renderer）→ env → 既定 xterm の順で決定。
	active := settings.TerminalRenderer(sec)
	if active == "" {
		active = terminal.EnvActiveBackend()
	}
	svc, err := terminal.NewService(terminal.ServiceConfig{
		Agents:   agents,
		Backends: []terminal.Backend{xtermBackend, wrapBackend},
		Active:   active,
	})
	if err != nil {
		return err
	}
	app.WithTerminal(svc)
	app.WithPet(pet.New(sec, svc.EventSubscriberCount))
	log.Printf("agentarium: active terminal renderer = %q", active)

	addr := os.Getenv("AGENTARIUM_ADDR")
	log.Printf("agentarium demo starting (addr=%q, empty=default)", addr)
	return app.Run(addr)
}
