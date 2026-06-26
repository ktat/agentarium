// Package slack は Slack OAuth(v2) でユーザートークンを取得・暗号化保存し、
// そのトークンで Slack API を呼べる同梱プラグイン。
// 消費者 main で `slack.New(secretsStore)` を Register する想定。
package slack

import (
	"embed"
	"io/fs"

	"github.com/ktat/agentarium/kernel/plugin"
	"github.com/ktat/agentarium/kernel/secrets"
	"github.com/ktat/agentarium/kernel/settings"
)

//go:embed assets/*
var assetsFS embed.FS

const (
	pluginID     = "slack"
	keyClientID  = "SLACK_CLIENT_ID"
	keyClientSec = "SLACK_CLIENT_SECRET"
)

// Plugin は Slack OAuth + API クライアントを提供する同梱プラグイン。
//
// states と tokens はポインタで保持する（意図的）。
// Plugin は値渡しされるため、フィールドがポインタでなければ値コピーごとに別インスタンスになる。
// /start が stateStore に CSRF state を書き込んでも /callback の Plugin コピーから見えなくなり、
// CSRF 検証が常に失敗する。値フィールドに変更すると静かにセキュリティが壊れるので変えないこと。
type Plugin struct {
	store  *secrets.Store
	reader *settings.Reader
	tokens *SecretTokenStore
	states *stateStore
}

// New は secrets ストアを注入して Plugin を構築する。
func New(store *secrets.Store) Plugin {
	return Plugin{
		store:  store,
		reader: settings.NewReader(store, pluginID),
		tokens: NewSecretTokenStore(store),
		states: newStateStore(),
	}
}

func (p Plugin) Meta() plugin.Meta {
	return plugin.Meta{ID: pluginID, Title: "Slack", Pane: plugin.PaneLeft, Order: 5}
}

func (p Plugin) Assets() fs.FS {
	sub, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		panic(err) // embed パス固定なので起こり得ない
	}
	return sub
}

func (p Plugin) SettingsSchema() []plugin.Field {
	return []plugin.Field{
		{Key: keyClientID, Label: "Slack Client ID", Secret: true},
		{Key: keyClientSec, Label: "Slack Client Secret", Secret: true},
	}
}

// Client は保存済みトークンから API クライアントを生成して返す。0 件なら ErrNoToken。
func (p Plugin) Client() (*APIClient, error) {
	tok, err := p.tokens.GetAny()
	if err != nil {
		return nil, err
	}
	return NewAPIClient(tok.AccessToken), nil
}

// oauthClient は現在の CLIENT_ID/SECRET から OAuthClient を組み立てる。未設定なら nil。
func (p Plugin) oauthClient() *OAuthClient {
	id, ok1 := p.reader.Get(keyClientID)
	sec, ok2 := p.reader.Get(keyClientSec)
	if !ok1 || !ok2 || id == "" || sec == "" {
		return nil
	}
	return &OAuthClient{ClientID: id, ClientSecret: sec, UserScopes: DefaultUserScopes}
}
