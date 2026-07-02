package plugin

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// validID は plugin ID の許容文字。先頭は英小文字/数字、以降は英小文字/数字/_/-。
// ルート名前空間 (/plugins/<id>/...) と ServeMux パターンの安全性のため厳格に縛る。
var validID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// reservedIDs はカーネルが内部で使う名前空間（secrets.Store のキー接頭辞
// "secret."、Settings の "kernel." グループ）と衝突するため禁止する plugin ID。
var reservedIDs = map[string]bool{"secret": true, "kernel": true}

// invalidPathChar は Route.Path に含めてはいけない文字を判定する。`{`/`}` は
// net/http の ServeMux でワイルドカード `{name}` と解釈され、空白文字は
// パターン連結時に不正となる（起動時 panic の原因）。R3。
func invalidPathChar(r rune) bool {
	switch r {
	case '{', '}':
		return true
	}
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}

// Registry は登録済みプラグインを保持する。スレッドセーフではない
// （起動時に main から逐次 Register する前提。実行中の動的追加はしない）。
type Registry struct {
	plugins        []Plugin
	ids            map[string]bool
	orderOverrides map[string]int // id → 上書き Order（消費者が SetOrder で設定）
}

// NewRegistry は空のレジストリを返す。
func NewRegistry() *Registry {
	return &Registry{ids: map[string]bool{}, orderOverrides: map[string]int{}}
}

// Register はプラグインを登録する。ID が空・不正・重複ならエラー。
// RouteProvider を実装する場合は各 Route.Path が "/" 始まりであることも検証する。
func (r *Registry) Register(p Plugin) error {
	id := p.Meta().ID
	if id == "" {
		return fmt.Errorf("plugin has empty ID")
	}
	if !validID.MatchString(id) {
		return fmt.Errorf("invalid plugin ID %q: must match [a-z0-9][a-z0-9_-]*", id)
	}
	if reservedIDs[id] {
		return fmt.Errorf("reserved plugin ID: %s", id)
	}
	if r.ids[id] {
		return fmt.Errorf("duplicate plugin ID: %s", id)
	}
	if rp, ok := p.(RouteProvider); ok {
		for _, rt := range rp.Routes() {
			if len(rt.Path) == 0 || rt.Path[0] != '/' {
				return fmt.Errorf("plugin %q route path %q must start with '/'", id, rt.Path)
			}
			if i := strings.IndexFunc(rt.Path, invalidPathChar); i >= 0 {
				return fmt.Errorf("plugin %q route path %q contains an invalid character at %d ('{', '}', whitespace not allowed)", id, rt.Path, i)
			}
		}
	}
	r.ids[id] = true
	r.plugins = append(r.plugins, p)
	return nil
}

// SetOrder は id のタブ Order を上書きする。Register の前後どちらでも呼べる。
// 未登録 id への設定は保持されるが sort には影響しない。
func (r *Registry) SetOrder(id string, order int) {
	r.orderOverrides[id] = order
}

// EffectiveOrder は id の実効 Order を返す。
// override があればそれ、無ければ登録プラグインの Meta.Order、未登録かつ override 無しは 0。
func (r *Registry) EffectiveOrder(id string) int {
	if o, ok := r.orderOverrides[id]; ok {
		return o
	}
	for _, p := range r.plugins {
		if p.Meta().ID == id {
			return p.Meta().Order
		}
	}
	return 0
}

// Plugins は実効 Order（override 優先）で (Order, ID) 昇順でソートしたコピーを返す。
func (r *Registry) Plugins() []Plugin {
	out := make([]Plugin, len(r.plugins))
	copy(out, r.plugins)
	sort.SliceStable(out, func(i, j int) bool {
		mi, mj := out[i].Meta(), out[j].Meta()
		oi, oj := r.effectiveOrder(mi), r.effectiveOrder(mj)
		if oi != oj {
			return oi < oj
		}
		return mi.ID < mj.ID
	})
	return out
}

// effectiveOrder は Meta から実効 Order を返す（override 優先）。
func (r *Registry) effectiveOrder(m Meta) int {
	if o, ok := r.orderOverrides[m.ID]; ok {
		return o
	}
	return m.Order
}
