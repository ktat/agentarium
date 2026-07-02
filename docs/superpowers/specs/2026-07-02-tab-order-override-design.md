# 消費者による Tab Order 上書き 設計

- 日付: 2026-07-02
- リポジトリ: agentarium（kernel + ファサード）
- ブランチ: `feat/tab-order-override`

## 1. 目的

消費者（`main`）が、プラグインを改変せずにタブの表示順を上書きできるようにする。
特に bundled プラグイン（Chat の `Meta.Order=0` 等）の Order は agentarium 側に固定されており、
消費者から変更できない。これを Register 前後で上書き可能にする。

## 2. 現状

- タブ順は `kernel/plugin/registry.go` の `Plugins()` が `Meta.Order` 昇順（同値は ID 昇順）で sort して決まる。
- `kernel/server` は `reg.Plugins()` の順でルートを mount し、`/api/plugins` DTO も同順・各 `Order: m.Order` を返す。
- シェル（app.js）は `/api/plugins` の配列順にタブを描画（sort はしない）。
- `Meta.Order` はプラグインの `Meta()` に固定されており、消費者が上書きする手段が無い。

## 3. スコープ

### やること
- `Registry` に order override（`map[string]int`）と `SetOrder` / `EffectiveOrder` を追加。
- `Plugins()` の sort を実効 Order（override 優先）に変更。
- `pluginsHandler` の DTO `Order` を実効 Order に変更。
- ファサード `App.SetTabOrder(id, order)` を追加。

### やらないこと（YAGNI）
- Pane / Hidden / Title の上書き。
- 動的（実行時）な並び替え・永続化。
- board-assistant 側で実際に Chat の順を変える wiring（別リポ・別対応）。

## 4. コンポーネント

### 4.1 `kernel/plugin/registry.go`
```go
type Registry struct {
	plugins        []Plugin
	ids            map[string]bool
	orderOverrides map[string]int // id → 上書き Order（消費者が SetOrder で設定）
}

func NewRegistry() *Registry {
	return &Registry{ids: map[string]bool{}, orderOverrides: map[string]int{}}
}

// SetOrder は id のタブ Order を上書きする。Register の前後どちらでも呼べる。
// 未登録 id への設定は保持されるが sort には影響しない（無害）。
func (r *Registry) SetOrder(id string, order int) {
	r.orderOverrides[id] = order
}

// EffectiveOrder は id の実効 Order（override があればそれ、無ければ登録プラグインの Meta.Order）を返す。
// 未登録かつ override 無しは 0。
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
```
`Plugins()` の sort 比較を `Meta.Order` から実効 Order に変更:
```go
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
```
`EffectiveOrder`（公開・id 引数）と `effectiveOrder`（内部・Meta 引数）は役割が違う（前者は
server DTO 用に id から引く、後者は sort 用に Meta を持っている前提）。実装重複を避けるため
`EffectiveOrder` は override を見て、無ければ登録プラグイン検索で Meta.Order を返す形にする。

### 4.2 `kernel/server/server.go`
`pluginsHandler` の DTO 構築を変更:
```go
out = append(out, pluginDTO{
	ID: m.ID, Title: m.Title, Pane: paneString(m.Pane),
	Order: reg.EffectiveOrder(m.ID), Hidden: m.Hidden,
})
```
ルート mount（`for _, p := range reg.Plugins()`）とインターフェース判定は実プラグインのままで不変
（override は Order のみに作用し、mount 対象・ID・Pane・Hidden には影響しない）。

### 4.3 ファサード `agentarium.go`
```go
// SetTabOrder はプラグイン id のタブ表示順を上書きする（bundled プラグイン含む）。
// Register の前後どちらでも呼べる。チェーン可能。
func (a *App) SetTabOrder(id string, order int) *App {
	a.reg.SetOrder(id, order)
	return a
}
```

## 5. データフロー
```
main: app.SetTabOrder("chat", 25)
  → registry.orderOverrides["chat"] = 25
reg.Plugins(): 実効 Order で sort（chat=25 → topics(20) の後ろ）
  → server: mount 順 & /api/plugins DTO の order が一致（chat の order=25）
  → shell: /api/plugins の順でタブ描画（既存のまま）
```

## 6. エラーハンドリング / エッジ
- 未登録 id への `SetTabOrder`: override map に保持されるが sort に出ない。害なし。
- 同一 id への複数回: 最後の値で上書き。
- override 無し: 従来どおり `Meta.Order`。
- `Registry` はスレッドセーフでない前提（起動時に逐次呼ぶ）を踏襲。`SetOrder` も同様。

## 7. テスト（TDD: Go）
- `kernel/plugin/registry_test.go`:
  - `SetOrder` 後に `Plugins()` の並びが override を反映する（例: Order 0 のプラグインを大きい値に上書きすると末尾へ）。
  - `EffectiveOrder`: override 設定時は override 値、未設定の登録プラグインは Meta.Order、未登録は 0。
  - 既存の Order/ID タイブレーク・登録検証テストの非回帰。
- `kernel/server/server_test.go`:
  - `SetOrder`（Registry 経由）後、`/api/plugins` の該当プラグインの `order` と配列順が override を反映する。
  - 既存 `TestAPIPlugins_ReturnsMeta` / `TestAPIPlugins_HiddenFlag` の非回帰。

## 8. ドキュメント追従
- agentarium `README.md`: プラグイン IF / タブの節に `App.SetTabOrder(id, order)`（消費者が bundled 含めタブ順を上書き）を追記。

## 9. 段階的実装の単位（plan で分割）
1. `Registry` に override + `SetOrder` + `EffectiveOrder` + `Plugins()` sort 変更 + テスト。
2. `pluginsHandler` DTO を実効 Order に + server テスト。
3. ファサード `App.SetTabOrder` + テスト（あれば）。
4. README 追従。
