// Package plugin はカーネルがホストするプラグインの契約を定義する。
// 必須は Plugin のみ。能力はオプトインの interface で、server が型アサーションで判定する。
package plugin

import (
	"io/fs"
	"net/http"
)

// Pane はタブを左右どちらのペインに置くか。
type Pane int

const (
	PaneLeft  Pane = iota // 機能タブ（一覧・操作）
	PaneRight             // Agent ターミナル系（S1-P2 以降）
)

// Meta はプラグインの表示メタ情報。ID はルート/アセットの名前空間に使う。
type Meta struct {
	ID     string
	Title  string
	Pane   Pane
	Order  int
	Hidden bool // true: 起動時にタブを出さない。Settings の「開けるタブ」から開く。開いたタブはクローズ可
}

// Plugin は全プラグインが満たす最小契約。
type Plugin interface {
	Meta() Meta
}

// Route は 1 本の HTTP ルート。Path は相対で、server が /plugins/<id> 配下にマウントする。
type Route struct {
	Method  string // "GET" / "POST" など
	Path    string // "/list"（先頭スラッシュ必須）
	Handler http.HandlerFunc
}

// RouteProvider は専用 REST エンドポイントを持つプラグインが実装する。
type RouteProvider interface {
	Routes() []Route
}

// FrontendProvider はフロントアセット（index.js 等）を持つプラグインが実装する。
// 返す fs.FS のルートに index.js を置くこと（server が /plugins/<id>/assets/ に配信）。
type FrontendProvider interface {
	Assets() fs.FS
}

// Field は Settings タブに合流させる設定項目（S2 で本格利用）。
type Field struct {
	Key    string
	Label  string
	Secret bool
}

// SettingsProvider は Settings に設定項目を出すプラグインが実装する。
type SettingsProvider interface {
	SettingsSchema() []Field
}
