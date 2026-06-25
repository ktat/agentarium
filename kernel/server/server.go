// Package server はレジストリを走査して HTTP ルートを自動マウントするカーネルサーバ。
package server

import (
	"encoding/json"
	"html"
	"io/fs"
	"net/http"
	"strings"

	"github.com/ktat/agentarium/kernel/plugin"
	"github.com/ktat/agentarium/kernel/viewer"
)

// Mountable は server.WithTerminal が受け取る、Service と同形の interface。
// 直接 terminal.Service に依存すると import 方向が逆になるため interface 抽出。
type Mountable interface {
	MountOn(mux *http.ServeMux)
}

// Option は server.New の挙動を拡張する設定関数。
type Option func(*config)

// config は New 内部の構築状態を持つ。external には公開しない。
type config struct {
	terminal Mountable
	pet      Mountable
	title    string
}

// WithTitle はシェル HTML の <title> を上書きする（消費者アプリ名の表示用）。
// 空のままなら既定（index.html の <title>Agentarium</title>）を使う。
func WithTitle(title string) Option {
	return func(c *config) { c.title = title }
}

// WithTerminal は terminal.Service を server に統合するオプション。
// 渡された Service は MountOn(mux) で /terminal/* を mux に登録する。
func WithTerminal(svc Mountable) Option {
	return func(c *config) { c.terminal = svc }
}

// WithPet は pet supervisor を server に統合する（/pet/* を mux に登録する）。
func WithPet(p Mountable) Option {
	return func(c *config) { c.pet = p }
}

// New はレジストリとシェルアセットから *http.ServeMux を構築する。
// 全 plugin route は csrfGuard でラップし、mutating method (POST/PUT/PATCH/DELETE)
// のときに Origin/Referer を検査する。GET/HEAD は素通り。
// opts で WithTerminal(svc) を渡すと terminal Service を組み込む（opt-in）。
func New(reg *plugin.Registry, shellFS fs.FS, opts ...Option) *http.ServeMux {
	cfg := &config{}
	for _, opt := range opts {
		opt(cfg)
	}
	mux := http.NewServeMux()
	for _, p := range reg.Plugins() {
		id := p.Meta().ID
		if rp, ok := p.(plugin.RouteProvider); ok {
			for _, rt := range rp.Routes() {
				// 例: "GET /plugins/alpha/ping"
				pattern := rt.Method + " /plugins/" + id + rt.Path
				mux.Handle(pattern, csrfGuard(http.HandlerFunc(rt.Handler)))
			}
		}
		if fp, ok := p.(plugin.FrontendProvider); ok {
			prefix := "/plugins/" + id + "/assets/"
			mux.Handle(prefix, noDirListing(http.StripPrefix(prefix, http.FileServer(http.FS(fp.Assets())))))
		}
	}
	mux.HandleFunc("GET /api/plugins", pluginsHandler(reg))
	// ビューア描画ユーティリティ（shell の右ペインビューアが使う）。常時マウント。
	// csrfGuard で cross-origin POST を弾く（render オラクル化防止）。
	mux.Handle("POST /viewer/render", csrfGuard(viewer.Handler()))
	mux.Handle("GET /assets/", noDirListing(http.StripPrefix("/assets/", http.FileServer(http.FS(shellFS)))))
	mux.HandleFunc("GET /{$}", indexHandler(shellFS, cfg.title))
	if cfg.terminal != nil {
		cfg.terminal.MountOn(mux)
	}
	if cfg.pet != nil {
		cfg.pet.MountOn(mux)
	}
	return mux
}

// noDirListing は http.FileServer をラップし、末尾が "/" のリクエスト
// （= ディレクトリ）に 404 を返してファイル一覧の漏洩を防ぐ。個別ファイル取得は通す。
func noDirListing(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func indexHandler(shellFS fs.FS, title string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := fs.ReadFile(shellFS, "index.html")
		if err != nil {
			http.Error(w, "index not found", http.StatusInternalServerError)
			return
		}
		if title != "" {
			b = []byte(strings.Replace(string(b), "<title>Agentarium</title>",
				"<title>"+html.EscapeString(title)+"</title>", 1))
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(b)
	}
}

type pluginDTO struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Pane  string `json:"pane"`
	Order int    `json:"order"`
}

func paneString(p plugin.Pane) string {
	if p == plugin.PaneRight {
		return "right"
	}
	return "left"
}

func pluginsHandler(reg *plugin.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out := []pluginDTO{}
		for _, p := range reg.Plugins() {
			m := p.Meta()
			out = append(out, pluginDTO{ID: m.ID, Title: m.Title, Pane: paneString(m.Pane), Order: m.Order})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}
