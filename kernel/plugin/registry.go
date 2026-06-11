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
	plugins []Plugin
	ids     map[string]bool
}

// NewRegistry は空のレジストリを返す。
func NewRegistry() *Registry {
	return &Registry{ids: map[string]bool{}}
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

// Plugins は (Order, ID) 昇順でソートしたコピーを返す。
func (r *Registry) Plugins() []Plugin {
	out := make([]Plugin, len(r.plugins))
	copy(out, r.plugins)
	sort.SliceStable(out, func(i, j int) bool {
		mi, mj := out[i].Meta(), out[j].Meta()
		if mi.Order != mj.Order {
			return mi.Order < mj.Order
		}
		return mi.ID < mj.ID
	})
	return out
}
