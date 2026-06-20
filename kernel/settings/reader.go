package settings

import "github.com/ktat/agentarium/kernel/secrets"

const (
	// KernelSecretPrefix はカーネル共有シークレットのストアキー接頭辞。
	KernelSecretPrefix = "secret."
	// RefSuffix はプラグイン設定フィールドの参照ポインタを表すキー接尾辞。
	// 値は参照先カーネルシークレットの KEY（接頭辞抜き）。
	RefSuffix = ".__ref"
)

// Reader は 1 プラグインの設定を読むための、plugin id 束縛のアクセサ。
// ref（カーネルシークレット参照）を透過的に解決する。
type Reader struct {
	store    *secrets.Store
	pluginID string
}

// NewReader は pluginID に束縛した設定リーダーを返す。
func NewReader(store *secrets.Store, pluginID string) *Reader {
	return &Reader{store: store, pluginID: pluginID}
}

// Get は field の設定値を解決して返す。ref があれば参照先カーネルシークレットを、
// なければ literal を返す。未設定・ref 先不在は ("", false)。
// 注意: 値はキャッシュしないこと。ref は実行中に張り替え/削除され得る。
func (r *Reader) Get(field string) (string, bool) {
	if r == nil || r.store == nil {
		return "", false
	}
	base := r.pluginID + "." + field
	if ref, ok := r.store.Get(base + RefSuffix); ok && ref != "" {
		return r.store.Get(KernelSecretPrefix + ref)
	}
	return r.store.Get(base)
}
