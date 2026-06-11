package xterm

import (
	"embed"
	"errors"
	"io/fs"

	"github.com/ktat/agentarium/kernel/plugin"
	"github.com/ktat/agentarium/kernel/terminal"
)

//go:embed assets/*
var assetsFS embed.FS

// 静的 interface assertion: *Backend は terminal.Backend を満たす。
var _ terminal.Backend = (*Backend)(nil)

// Backend は xterm.Registry を terminal.Backend interface に適合させる adapter。
// 既存 Registry の signature を変更せず、薄いラッパで interface 契約に橋渡しする。
type Backend struct {
	Registry *Registry
	// AssetsFS は /terminal/assets/xterm/ で配信するフロント資産（xterm.min.js 等）。
	// 空 (nil) なら空の fs.FS が返る（P2-3a 範囲ではフロント資産は未同梱。P2-4 で埋まる）。
	AssetsFS fs.FS
}

// Name は backend 識別子。env や API レスポンスで使う。
func (b *Backend) Name() string { return "xterm" }

// Renderer は shell に渡すフロント識別子（fetch する JS 名前空間）。
func (b *Backend) Renderer() string { return "xterm" }

// Start は agent + request で PTY を起動する。Cols/AltRows は xterm では無視する
// （クライアント側 xterm.js が resize で正しい cols を送ってくる）。
func (b *Backend) Start(id, label string, ag terminal.Agent, req terminal.RunRequest) error {
	_, err := b.Registry.Start(id, label, ag, req)
	return err
}

// Stop は id の Process を停止する。
func (b *Backend) Stop(id string) error { return b.Registry.Stop(id) }

// Inject は PTY に raw bytes を流す。enter=true なら末尾に \r を 1 バイト追加。
func (b *Backend) Inject(id, text string, enter bool) error {
	p := b.Registry.Get(id)
	if p == nil {
		return errors.New("terminal not found: " + id)
	}
	payload := []byte(text)
	if enter {
		payload = append(payload, '\r')
	}
	return p.Write(payload)
}

// SetSessionID は Registry の SetSessionID に委譲する。
func (b *Backend) SetSessionID(id, sessionID string) { b.Registry.SetSessionID(id, sessionID) }

// List は Registry の List をそのまま返す。
func (b *Backend) List() []terminal.SessionInfo { return b.Registry.List() }

// Routes は WS handler を返す。Service.MountOn が /terminal 配下に組み込み、
// 最終的に GET /terminal/ws?id=<terminal-id> として公開される。
func (b *Backend) Routes() []plugin.Route {
	return []plugin.Route{
		{Method: "GET", Path: "/ws", Handler: b.HandleWS},
	}
}

// Assets はフロント資産を返す。AssetsFS が指定されていればそれを優先、
// 未指定なら同梱 vendored（xterm.min.js / index.js 等）を返す。
func (b *Backend) Assets() fs.FS {
	if b.AssetsFS != nil {
		return b.AssetsFS
	}
	sub, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		return terminal.EmptyAssets()
	}
	return sub
}
