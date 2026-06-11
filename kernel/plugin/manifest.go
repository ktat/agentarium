package plugin

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed manifest_assets
var manifestAssetsFS embed.FS

// Manifest は宣言的プラグイン（IF B、spec §5）の定義。JSON から 1:1 にマップする。
type Manifest struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	Pane      string          `json:"pane"` // "left" | "right"（省略時 left）
	Order     int             `json:"order"`
	DataURL   string          `json:"dataURL"`
	Render    string          `json:"render"` // "list" のみ
	List      ManifestList    `json:"list"`
	RowAction *ManifestAction `json:"rowAction,omitempty"` // 省略時は読み取り専用一覧
}

// ManifestList は list レンダラの列定義。
type ManifestList struct {
	Columns []ManifestColumn `json:"columns"`
}

// ManifestColumn は 1 列。Value は "{{.uuid}}" 等のテンプレート（レンダラが行で解決）。
type ManifestColumn struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// ManifestAction は行ボタンのアクション。現状 type は "openAgent" のみ。
// テンプレート可フィールドは行オブジェクトでレンダラが解決し openAgentTab に渡す。
type ManifestAction struct {
	Label     string `json:"label"`
	Type      string `json:"type"`
	Agent     string `json:"agent"`
	Model     string `json:"model,omitempty"`
	Resume    string `json:"resume,omitempty"`
	Command   string `json:"command,omitempty"`
	AutoEnter bool   `json:"autoEnter,omitempty"`
	Key       string `json:"key,omitempty"`
	TabLabel  string `json:"tabLabel,omitempty"`
}

// NewManifestPlugin は manifest JSON を検証して plugin.Plugin を返す。
// 返り値は RouteProvider（GET /manifest）と FrontendProvider（共有 list レンダラ）も実装する。
// パース失敗・バリデーション失敗は error（Register 前に弾く）。
func NewManifestPlugin(data []byte) (Plugin, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("manifest: invalid json: %w", err)
	}
	if err := validateManifest(&m); err != nil {
		return nil, err
	}
	raw := append([]byte(nil), data...) // 原文を保持して /manifest でそのまま返す
	return &manifestPlugin{m: m, raw: raw}, nil
}

func validateManifest(m *Manifest) error {
	if !validID.MatchString(m.ID) {
		return fmt.Errorf("manifest: invalid id %q: must match [a-z0-9][a-z0-9_-]*", m.ID)
	}
	if strings.TrimSpace(m.Title) == "" {
		return fmt.Errorf("manifest %q: title is required", m.ID)
	}
	switch m.Pane {
	case "", "left", "right":
	default:
		return fmt.Errorf("manifest %q: invalid pane %q (left|right)", m.ID, m.Pane)
	}
	if m.Render != "list" {
		return fmt.Errorf("manifest %q: unsupported render %q (only \"list\")", m.ID, m.Render)
	}
	if len(m.List.Columns) == 0 {
		return fmt.Errorf("manifest %q: list.columns must have at least one column", m.ID)
	}
	for i, c := range m.List.Columns {
		if strings.TrimSpace(c.Label) == "" {
			return fmt.Errorf("manifest %q: list.columns[%d].label is required", m.ID, i)
		}
	}
	if !strings.HasPrefix(m.DataURL, "/") {
		return fmt.Errorf("manifest %q: dataURL %q must start with '/' (same-origin only)", m.ID, m.DataURL)
	}
	if m.RowAction != nil {
		if m.RowAction.Type != "openAgent" {
			return fmt.Errorf("manifest %q: rowAction.type %q unsupported (only \"openAgent\")", m.ID, m.RowAction.Type)
		}
		if strings.TrimSpace(m.RowAction.Agent) == "" {
			return fmt.Errorf("manifest %q: rowAction.agent is required", m.ID)
		}
	}
	return nil
}

// manifestPlugin は Manifest を plugin IF に適合させる非公開アダプタ。
type manifestPlugin struct {
	m   Manifest
	raw []byte
}

func (p *manifestPlugin) Meta() Meta {
	return Meta{ID: p.m.ID, Title: p.m.Title, Pane: paneFromString(p.m.Pane), Order: p.m.Order}
}

func paneFromString(s string) Pane {
	if s == "right" {
		return PaneRight
	}
	return PaneLeft
}

func (p *manifestPlugin) Routes() []Route {
	return []Route{{Method: "GET", Path: "/manifest", Handler: p.serveManifest}}
}

func (p *manifestPlugin) serveManifest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(p.raw)
}

func (p *manifestPlugin) Assets() fs.FS {
	sub, err := fs.Sub(manifestAssetsFS, "manifest_assets")
	if err != nil {
		panic(err) // embed パス固定なので起こり得ない
	}
	return sub
}
