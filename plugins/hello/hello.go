// Package hello は IF を end-to-end で検証する最小バンドルプラグイン。
package hello

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"

	"github.com/ktat/agentarium/kernel/plugin"
)

//go:embed assets/*
var assetsFS embed.FS

// Plugin は hello タブ。Route + Frontend を提供する。
type Plugin struct{}

func (Plugin) Meta() plugin.Meta {
	return plugin.Meta{ID: "hello", Title: "Hello", Pane: plugin.PaneLeft, Order: 0}
}

func (Plugin) Routes() []plugin.Route {
	return []plugin.Route{
		{Method: "GET", Path: "/ping", Handler: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "pong"})
		}},
	}
}

func (Plugin) Assets() fs.FS {
	sub, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		panic(err)
	}
	return sub
}

// SettingsSchema は hello の設定項目（dogfood: 非 Secret と Secret を 1 つずつ）。
func (Plugin) SettingsSchema() []plugin.Field {
	return []plugin.Field{
		{Key: "greeting", Label: "Greeting", Secret: false},
		{Key: "token", Label: "API Token", Secret: true},
	}
}
