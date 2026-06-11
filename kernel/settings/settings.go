// Package settings はカーネル組み込みの Settings タブを提供する。
// registry を列挙して SettingsProvider を持つプラグインの設定フォームを出し、
// 値を kernel/secrets.Store に保存する。App.WithSecrets から登録される。
package settings

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"

	"github.com/ktat/agentarium/kernel/plugin"
	"github.com/ktat/agentarium/kernel/secrets"
)

//go:embed assets
var assetsFS embed.FS

const saveMaxBytes = 64 * 1024

// カーネル自身の設定（プラグインではない）。Settings タブに "Kernel" グループとして出す。
const (
	kernelGroupID = "kernel"
	rendererField = "terminal_renderer"
	// KeyTerminalRenderer は secrets.Store 上のカーネル renderer 設定キー。
	// cmd 側がこのキーを読んで active backend を選ぶ。
	KeyTerminalRenderer = kernelGroupID + "." + rendererField
)

// rendererOptions は terminal_renderer の選択肢（表示順）。UI はこれをラジオで出す。
var rendererOptions = []string{"xterm", "wrap"}

// allowedRenderers は terminal_renderer に許す値（検証用）。
var allowedRenderers = map[string]bool{"xterm": true, "wrap": true}

// TerminalRenderer は store に保存された renderer 設定（"xterm"/"wrap"）を返す。
// 未設定・不正値は "" を返す（呼び出し側が env/既定へフォールバックする想定）。
func TerminalRenderer(store *secrets.Store) string {
	if store == nil {
		return ""
	}
	if v, ok := store.Get(KeyTerminalRenderer); ok && allowedRenderers[v] {
		return v
	}
	return ""
}

type settingsPlugin struct {
	reg   *plugin.Registry
	store *secrets.Store
}

// New は registry と secrets.Store を束ねた組み込み settings プラグインを返す。
func New(reg *plugin.Registry, store *secrets.Store) plugin.Plugin {
	return &settingsPlugin{reg: reg, store: store}
}

func (p *settingsPlugin) Meta() plugin.Meta {
	return plugin.Meta{ID: "settings", Title: "Settings", Pane: plugin.PaneLeft, Order: 1000}
}

func (p *settingsPlugin) Routes() []plugin.Route {
	return []plugin.Route{
		{Method: "GET", Path: "/schema", Handler: p.handleSchema},
		{Method: "POST", Path: "/save", Handler: p.handleSave},
	}
}

func (p *settingsPlugin) Assets() fs.FS {
	sub, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		panic(err) // embed パス固定なので起こり得ない
	}
	return sub
}

type fieldDTO struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Secret  bool     `json:"secret"`
	Value   string   `json:"value,omitempty"`
	Set     bool     `json:"set,omitempty"`
	Options []string `json:"options,omitempty"` // 非空なら UI はラジオで選択させる
}

type pluginDTO struct {
	ID     string     `json:"id"`
	Title  string     `json:"title"`
	Fields []fieldDTO `json:"fields"`
}

// handleSchema は設定を持つプラグイン一覧を表示状態で返す。Secret 値は返さない。
func (p *settingsPlugin) handleSchema(w http.ResponseWriter, r *http.Request) {
	out := make([]pluginDTO, 0)
	// カーネル自身の設定グループを先頭に出す（プラグインではない）。
	rendererDTO := fieldDTO{Key: rendererField, Label: "Terminal renderer（変更は再起動で反映）", Options: rendererOptions}
	if v, ok := p.store.Get(KeyTerminalRenderer); ok {
		rendererDTO.Value = v
	}
	out = append(out, pluginDTO{ID: kernelGroupID, Title: "Kernel", Fields: []fieldDTO{rendererDTO}})
	for _, pl := range p.reg.Plugins() {
		sp, ok := pl.(plugin.SettingsProvider)
		if !ok {
			continue
		}
		m := pl.Meta()
		if m.ID == "settings" {
			continue
		}
		fields := make([]fieldDTO, 0)
		for _, f := range sp.SettingsSchema() {
			d := fieldDTO{Key: f.Key, Label: f.Label, Secret: f.Secret}
			storeKey := m.ID + "." + f.Key
			if f.Secret {
				d.Set = p.store.Has(storeKey)
			} else if v, ok := p.store.Get(storeKey); ok {
				d.Value = v
			}
			fields = append(fields, d)
		}
		out = append(out, pluginDTO{ID: m.ID, Title: m.Title, Fields: fields})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"plugins": out})
}

type saveReq struct {
	ID     string            `json:"id"`
	Values map[string]string `json:"values"`
}

// handleSave は 1 プラグインの設定値を保存する。CSRF は server.New の csrfGuard が
// POST に適用するためここでは検証しない。schema に無い key は無視。
// Secret が空文字なら既存を保持（上書きしない）。
func (p *settingsPlugin) handleSave(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, saveMaxBytes)
	var body saveReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	// カーネル設定グループ（プラグインではない）。renderer のみ受け付ける。
	if body.ID == kernelGroupID {
		if v, ok := body.Values[rendererField]; ok {
			if v != "" && !allowedRenderers[v] {
				http.Error(w, "invalid renderer (xterm|wrap)", http.StatusBadRequest)
				return
			}
			if err := p.store.Set(KeyTerminalRenderer, v); err != nil {
				http.Error(w, "save failed", http.StatusInternalServerError)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	sp := p.findProvider(body.ID)
	if sp == nil {
		http.Error(w, "unknown settings plugin", http.StatusBadRequest)
		return
	}
	schema := make(map[string]plugin.Field)
	for _, f := range sp.SettingsSchema() {
		schema[f.Key] = f
	}
	for k, v := range body.Values {
		f, ok := schema[k]
		if !ok {
			continue // 未知 key 無視
		}
		storeKey := body.ID + "." + k
		var err error
		if f.Secret {
			if v == "" {
				continue // 空 Secret は既存保持
			}
			err = p.store.SetSecret(storeKey, v)
		} else {
			err = p.store.Set(storeKey, v)
		}
		if err != nil {
			http.Error(w, "save failed", http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (p *settingsPlugin) findProvider(id string) plugin.SettingsProvider {
	if id == "settings" {
		return nil
	}
	for _, pl := range p.reg.Plugins() {
		if pl.Meta().ID != id {
			continue
		}
		if sp, ok := pl.(plugin.SettingsProvider); ok {
			return sp
		}
		return nil
	}
	return nil
}
