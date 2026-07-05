// Package server はレジストリを走査して HTTP ルートを自動マウントするカーネルサーバ。
package server

import (
	"encoding/json"
	"html"
	"io/fs"
	"net/http"
	"strings"

	"github.com/ktat/agentarium/kernel/events"
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
	terminal      Mountable
	pet           Mountable
	title         string
	favicon       string
	themeProvider func() string
}

// WithTitle はシェル HTML の <title> と左上ヘッダ（span.title）を上書きする（消費者アプリ名の表示用）。
// 空のままなら既定（"Agentarium"）を使う。
func WithTitle(title string) Option {
	return func(c *config) { c.title = title }
}

// WithFavicon はシェル HTML の <head> に <link rel="icon" href="..."> を注入する
// （消費者アプリのブラウザタブアイコン用）。href は data URI / プラグイン資産パス
// （例 /plugins/foo/assets/icon.png）/ 絶対URL のいずれでもよい。favicon 実体の
// 配信は消費者責任。空のままなら link を注入しない（既定の無アイコン挙動を維持）。
func WithFavicon(href string) Option {
	return func(c *config) { c.favicon = href }
}

// WithThemeProvider は index.html 描画時に <html> へ data-theme を注入する
// テーマ供給関数を設定する。fn が "light"/"dark" を返すときのみ属性を注入し、
// それ以外（"" = system/未設定）は無置換（@media / :root に委ねる）。
func WithThemeProvider(fn func() string) Option {
	return func(c *config) { c.themeProvider = fn }
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
	// 汎用イベントバス（カーネル pub/sub）。常時マウント。
	hub := events.New()
	mux.Handle("POST /events/publish", csrfGuard(http.HandlerFunc(hub.HandlePublish)))
	mux.HandleFunc("GET /events", hub.HandleSubscribe)
	mux.Handle("GET /assets/", noDirListing(http.StripPrefix("/assets/", http.FileServer(http.FS(shellFS)))))
	mux.HandleFunc("GET /{$}", indexHandler(shellFS, cfg.title, cfg.favicon, cfg.themeProvider))
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

func indexHandler(shellFS fs.FS, title, favicon string, themeProvider func() string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := fs.ReadFile(shellFS, "index.html")
		if err != nil {
			http.Error(w, "index not found", http.StatusInternalServerError)
			return
		}
		s := string(b)
		if favicon != "" {
			link := `<link rel="icon" href="` + html.EscapeString(favicon) + `">`
			s = strings.Replace(s, "<!--favicon-->", link, 1)
		}
		if title != "" {
			esc := html.EscapeString(title)
			s = strings.NewReplacer(
				"<title>Agentarium</title>", "<title>"+esc+"</title>",
				`<span class="title">Agentarium</span>`, `<span class="title">`+esc+"</span>",
			).Replace(s)
		}
		if themeProvider != nil {
			if th := themeProvider(); th == "light" || th == "dark" {
				s = strings.Replace(s, `<html lang="ja">`, `<html lang="ja" data-theme="`+th+`">`, 1)
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(s))
	}
}

type pluginDTO struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Pane   string `json:"pane"`
	Order  int    `json:"order"`
	Hidden bool   `json:"hidden,omitempty"`
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
			out = append(out, pluginDTO{ID: m.ID, Title: m.Title, Pane: paneString(m.Pane), Order: reg.EffectiveOrder(m.ID), Hidden: m.Hidden})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}
