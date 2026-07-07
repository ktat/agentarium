// Package standard は agentarium の標準ターミナル配線を再利用ヘルパとして提供する。
// xterm/wrap 両 backend を登録し、Settings(kernel.terminal_renderer) → env → 既定 xterm の
// 順で active を選び、renderer 別 Store でセッション復元を有効化した *terminal.Service を返す。
// active は起動時に確定する（ランタイム切替は非対応。Settings 変更は再起動で反映）。
package standard

import (
	"errors"
	"path/filepath"

	"github.com/ktat/agentarium/kernel/secrets"
	"github.com/ktat/agentarium/kernel/settings"
	"github.com/ktat/agentarium/kernel/terminal"
	"github.com/ktat/agentarium/kernel/terminal/wrap"
	"github.com/ktat/agentarium/kernel/terminal/xterm"
)

// Config は標準ターミナル配線の入力。
type Config struct {
	WorkDir  string                  // 端末 cwd（必須）
	Agents   *terminal.AgentRegistry // 登録済みレジストリ（必須）
	Secrets  *secrets.Store          // active 解決用。nil 可（nil なら env → 既定 xterm）
	StoreDir string                  // renderer 別 Store の置き場。空なら Store 無し（復元なし）
}

// NewService は xterm/wrap 両 backend を登録し、Settings → env → 既定 xterm の順で
// active を決め、CanResume を配線した *terminal.Service を返す。
func NewService(cfg Config) (*terminal.Service, error) {
	if cfg.Agents == nil {
		return nil, errors.New("standard: Agents is required")
	}
	if cfg.WorkDir == "" {
		return nil, errors.New("standard: WorkDir is required")
	}

	var xtermBackend *xterm.Backend
	var wrapBackend *wrap.Backend
	if cfg.StoreDir != "" {
		xtermBackend = &xterm.Backend{Registry: xterm.NewRegistryWithStore(
			cfg.WorkDir, cfg.Agents, terminal.NewStore(filepath.Join(cfg.StoreDir, "terminal-xterm.json")))}
		wrapBackend = &wrap.Backend{Registry: wrap.NewRegistryWithStore(
			cfg.WorkDir, cfg.Agents, wrap.NewStore(filepath.Join(cfg.StoreDir, "terminal-wrap.json")))}
	} else {
		xtermBackend = &xterm.Backend{Registry: xterm.NewRegistry(cfg.WorkDir, cfg.Agents)}
		wrapBackend = &wrap.Backend{Registry: wrap.NewRegistry(cfg.WorkDir, cfg.Agents)}
	}

	// active: Settings(nil セーフ) → env(未設定なら "xterm") の順。常に具体名に解決される。
	active := settings.TerminalRenderer(cfg.Secrets)
	if active == "" {
		active = terminal.EnvActiveBackend()
	}

	canResume := func(rec terminal.SessionRecord) bool {
		return terminal.CanResume(cfg.Agents.Resolve(rec.Agent), rec.WorkDir, rec.SessionID)
	}

	return terminal.NewService(terminal.ServiceConfig{
		Agents:    cfg.Agents,
		Backends:  []terminal.Backend{xtermBackend, wrapBackend},
		Active:    active,
		CanResume: canResume,
	})
}
