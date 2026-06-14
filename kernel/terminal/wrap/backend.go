package wrap

import (
	"embed"
	"errors"
	"io/fs"
	"time"

	"github.com/ktat/agentarium/kernel/plugin"
	"github.com/ktat/agentarium/kernel/terminal"
)

//go:embed assets/*
var assetsFS embed.FS

// 静的 interface assertion: *Backend は terminal.Backend を満たす。
var _ terminal.Backend = (*Backend)(nil)

// Backend は wrap.Registry を terminal.Backend interface に適合させる adapter。
// req.Cols / req.AltRows を Registry.Start の cols/altRows 引数へ橋渡しする
// （xterm と違い wrap は仮想 5000 行グリッドの初期 cols/alt-rows を起動時に必要とする）。
type Backend struct {
	Registry *Registry
	// AssetsFS は /terminal/assets/wrap/ で配信するフロント資産（wrap renderer JS）。
	// 空 (nil) なら空の fs.FS が返る（P2-4 で埋まる）。
	AssetsFS fs.FS
}

// Name は backend 識別子。
func (b *Backend) Name() string { return "wrap" }

// Renderer は shell に渡すフロント識別子。
func (b *Backend) Renderer() string { return "wrap" }

// Start は req.Cols/req.AltRows を Registry.Start に渡して PTY+VT エミュレータを起動する。
// Cols=0/AltRows=0 のときは wrap.DefaultCols / wrap.DefaultAltRows が後段で使われる。
func (b *Backend) Start(id, label string, ag terminal.Agent, req terminal.RunRequest) error {
	_, err := b.Registry.Start(id, label, ag, req, req.Cols, req.AltRows)
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

// AddStateListener は Registry の AddStateListener に委譲する。
func (b *Backend) AddStateListener(l terminal.StateListener) { b.Registry.AddStateListener(l) }

// Close は Registry の background goroutine（warmup / persist loop）を停止する。
// terminal.Service.Close から呼ばれ、library 消費者の graceful shutdown で
// goroutine leak を防ぐ（R1）。error は将来の拡張用に返すが現状は常に nil。
func (b *Backend) Close() error {
	b.Registry.Close()
	return nil
}

// warmupInterval は Restore 後の lazy warmup が pending entry を 1 件ずつ起動する間隔。
// 復元コストを時間軸に分散する（registry_lazy.go の StartLazyWarmupLoop 参照）。
// 現状は固定値。消費者が調整したくなったら ServiceConfig 等に逃がす（YAGNI のため今は定数）。
const warmupInterval = 2 * time.Second

// Restore は store の永続レコードを lazy 復元（pending 登録）し、warmup loop を起動する。
// pending entry は WS attach（EnsureStarted）でも起動するため、誰も開かない entry を
// warmup が時間差で起動する。canResume=false のレコードは skip（resume 不能セッション回避）。
func (b *Backend) Restore(canResume func(terminal.SessionRecord) bool) (int, int) {
	pending, total := b.Registry.RestoreFromStoreLazy(canResume)
	if pending > 0 {
		b.Registry.StartLazyWarmupLoop(warmupInterval)
	}
	return pending, total
}

// Routes は WS handler を返す。Service.MountOn が /terminal 配下に組み込み、
// 最終的に GET /terminal/ws?id=<terminal-id> として公開される。
func (b *Backend) Routes() []plugin.Route {
	return []plugin.Route{
		{Method: "GET", Path: "/ws", Handler: b.HandleWS},
	}
}

// Assets はフロント資産を返す。AssetsFS が指定されていればそれを優先、
// 未指定なら同梱（index.js 最小 renderer）を返す。
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
