// Package agentarium は消費者向けのファサード。消費者は自分のリポで main を書き、
// New().Register(...) → Run(addr) でフレームワークを立ち上げる（spec §3.4 / D7）。
// 細かい制御が要る場合は kernel/plugin・kernel/server・kernel/shell を直接使える。
package agentarium

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/ktat/agentarium/kernel/plugin"
	"github.com/ktat/agentarium/kernel/secrets"
	"github.com/ktat/agentarium/kernel/server"
	"github.com/ktat/agentarium/kernel/settings"
	"github.com/ktat/agentarium/kernel/shell"
	"github.com/ktat/agentarium/kernel/terminal"
)

// App は登録済みプラグインを束ねる消費者向けハンドル。
type App struct {
	reg      *plugin.Registry
	terminal *terminal.Service
	secrets  *secrets.Store
	mu       sync.Mutex
	srv      *http.Server
}

// New は空の App を返す。同梱プラグインは登録しない（消費者が opt-in する）。
func New() *App {
	return &App{reg: plugin.NewRegistry()}
}

// Register は 1 つ以上のプラグインを登録する。最初のエラーで中断して返す。
func (a *App) Register(plugins ...plugin.Plugin) error {
	for _, p := range plugins {
		if err := a.reg.Register(p); err != nil {
			return err
		}
	}
	return nil
}

// Registry は生レジストリを返す（脱出口）。
func (a *App) Registry() *plugin.Registry { return a.reg }

// WithTerminal は terminal.Service を App に opt-in 登録する。
// Handler() / Run() で /terminal/* routes が組み込まれる（spec §3.4 D7）。
// nil を渡すと既存の Service が解除される。
func (a *App) WithTerminal(svc *terminal.Service) *App {
	a.terminal = svc
	return a
}

// WithSecrets は secrets.Store を opt-in し、組み込み Settings プラグインを登録する。
// 設定を持つプラグイン（SettingsProvider 実装）が Settings タブに現れる。
// 一度だけ呼ぶこと（重複呼び出しは "settings" ID 衝突で panic）。
func (a *App) WithSecrets(store *secrets.Store) *App {
	a.secrets = store
	if err := a.reg.Register(settings.New(a.reg, store)); err != nil {
		panic("agentarium: WithSecrets: " + err.Error())
	}
	return a
}

// Handler は登録内容から HTTP ハンドラを構築する。
// terminal Service が WithTerminal で渡されていれば /terminal/* も組み込む。
// error は将来の manifest ローダ等の失敗に備えた前方互換のため（現状は常に nil）。
func (a *App) Handler() (http.Handler, error) {
	var opts []server.Option
	if a.terminal != nil {
		opts = append(opts, server.WithTerminal(a.terminal))
	}
	return server.New(a.reg, shell.FS(), opts...), nil
}

// Run は addr で待ち受ける。addr が空なら 127.0.0.1:8780。
// 外部公開（LAN/0.0.0.0/public IP）には AGENTARIUM_ALLOW_PUBLIC=1 が必要。
// Shutdown(ctx) で graceful に停止できる。
func (a *App) Run(addr string) error {
	if addr == "" {
		addr = "127.0.0.1:8780"
	}
	if err := validateAddrLoopback(addr); err != nil {
		return err
	}
	h, err := a.Handler()
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second, // slowloris 緩和
	}
	a.mu.Lock()
	a.srv = srv
	a.mu.Unlock()
	return srv.ListenAndServe()
}

// Shutdown は Run で起動した http.Server を graceful に停止し、続けて
// terminal.Service の background goroutine（wrap backend の warmup / persist 等）を
// Close で停止する（R1）。Run 前に呼ばれても terminal の Close は実行する
// （warmup loop は Run と独立に開始され得るため）。
// http.Server の Shutdown error を優先して返す。
func (a *App) Shutdown(ctx context.Context) error {
	a.mu.Lock()
	srv := a.srv
	term := a.terminal
	a.mu.Unlock()
	var err error
	if srv != nil {
		err = srv.Shutdown(ctx)
	}
	if term != nil {
		if e := term.Close(); e != nil && err == nil {
			err = e
		}
	}
	return err
}

// validateAddrLoopback は addr が loopback (127.0.0.0/8 / ::1 / "localhost") であることを
// 確認する。loopback でなければ AGENTARIUM_ALLOW_PUBLIC=1 でのみ許可する。
// addr のパース失敗もエラー。
func validateAddrLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid addr %q: %w", addr, err)
	}
	publicOK := os.Getenv("AGENTARIUM_ALLOW_PUBLIC") == "1"
	if host == "" || host == "0.0.0.0" || host == "::" {
		if !publicOK {
			return fmt.Errorf("addr %q binds to all interfaces; set AGENTARIUM_ALLOW_PUBLIC=1 to allow", addr)
		}
		return nil
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil {
		if !publicOK {
			return fmt.Errorf("addr host %q is not a loopback; set AGENTARIUM_ALLOW_PUBLIC=1 to allow", host)
		}
		return nil
	}
	if !ip.IsLoopback() {
		if !publicOK {
			return fmt.Errorf("addr host %q is not a loopback IP; set AGENTARIUM_ALLOW_PUBLIC=1 to allow", host)
		}
	}
	return nil
}
