# 消費者による Tab Order 上書き Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 消費者が `app.SetTabOrder(id, order)` で bundled 含む任意プラグインのタブ順を上書きできるようにする。

**Architecture:** `plugin.Registry` に order override マップを持たせ、`Plugins()` の sort と `/api/plugins` DTO が実効 Order（override 優先、無ければ `Meta.Order`）を使う。ファサード `App.SetTabOrder` が registry に委譲。mount・ID・Pane・Hidden には影響しない。

**Tech Stack:** Go 1.26、標準ライブラリのみ。

## Global Constraints

- 言語 Go 1.26 系、標準ライブラリのみ。1 ファイル 700 行以内。TDD（テスト先行）。
- Conventional Commits（日本語・scope 付き）: `feat(plugin):` / `feat(server):` / `feat(agentarium):` / `docs:`。
- `Registry` はスレッドセーフでない前提（起動時に main から逐次呼ぶ）を踏襲。`SetOrder` も同様。
- override は Order のみに作用する。mount 対象・ID・Pane・Hidden・Title には影響させない。
- 既存の並び順仕様: `Plugins()` は Order 昇順、同値は ID 昇順のタイブレーク。
- 既存テスト非回帰: `TestPlugins_SortedByOrderThenID`（registry）, `TestAPIPlugins_ReturnsMeta` / `TestAPIPlugins_HiddenFlag`（server）。

---

### Task 1: Registry に order override を追加

**Files:**
- Modify: `kernel/plugin/registry.go`
- Test: `kernel/plugin/registry_test.go`

**Interfaces:**
- Produces:
  - `func (r *Registry) SetOrder(id string, order int)`
  - `func (r *Registry) EffectiveOrder(id string) int` — override があればそれ、無ければ登録プラグインの `Meta.Order`、未登録かつ override 無しは 0
  - `Plugins()` は実効 Order で sort（override 優先、同値は ID）

- [ ] **Step 1: 失敗するテストを書く**

`kernel/plugin/registry_test.go` に追記（既存 `fakePlugin{id, order}` を流用）:
```go
func TestSetOrder_OverridesSortPosition(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(fakePlugin{id: "chat", order: 0})
	_ = r.Register(fakePlugin{id: "topics", order: 20})
	r.SetOrder("chat", 25) // chat を topics の後ろへ
	got := r.Plugins()
	want := []string{"topics", "chat"}
	for i, p := range got {
		if p.Meta().ID != want[i] {
			t.Fatalf("position %d: want %s, got %s", i, want[i], p.Meta().ID)
		}
	}
}

func TestEffectiveOrder(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(fakePlugin{id: "topics", order: 20})
	r.SetOrder("chat", 25)
	if got := r.EffectiveOrder("chat"); got != 25 { // override（未登録でも保持）
		t.Errorf("chat: want 25, got %d", got)
	}
	if got := r.EffectiveOrder("topics"); got != 20 { // override 無し → Meta.Order
		t.Errorf("topics: want 20, got %d", got)
	}
	if got := r.EffectiveOrder("nope"); got != 0 { // 未登録かつ override 無し
		t.Errorf("nope: want 0, got %d", got)
	}
}
```

- [ ] **Step 2: テストが失敗することを確認**

Run: `cd /home/ktat/git/github/agentarium-wt-order && go test ./kernel/plugin/ -run 'TestSetOrder_OverridesSortPosition|TestEffectiveOrder'`
Expected: FAIL（`SetOrder` / `EffectiveOrder` 未定義）

- [ ] **Step 3: 実装**

`kernel/plugin/registry.go`:
- 構造体とコンストラクタ:
```go
type Registry struct {
	plugins        []Plugin
	ids            map[string]bool
	orderOverrides map[string]int // id → 上書き Order（消費者が SetOrder で設定）
}

func NewRegistry() *Registry {
	return &Registry{ids: map[string]bool{}, orderOverrides: map[string]int{}}
}
```
- override アクセサ:
```go
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
```
- `Plugins()` の sort を実効 Order に変更:
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

- [ ] **Step 4: テストが通ることを確認**

Run: `cd /home/ktat/git/github/agentarium-wt-order && go test ./kernel/plugin/`
Expected: PASS（既存 `TestPlugins_SortedByOrderThenID` 等も緑）

- [ ] **Step 5: コミット**

```bash
git add kernel/plugin/registry.go kernel/plugin/registry_test.go
git commit -m "feat(plugin): Registry に Tab Order 上書き(SetOrder/EffectiveOrder)を追加"
```

---

### Task 2: /api/plugins DTO を実効 Order に

**Files:**
- Modify: `kernel/server/server.go`
- Test: `kernel/server/server_test.go`

**Interfaces:**
- Consumes: `Registry.EffectiveOrder(id)`（Task 1）

- [ ] **Step 1: 失敗するテストを書く**

`kernel/server/server_test.go` に追記（既存 `fakePlugin{id, pane, hidden}` と `New(reg, newTestShellFS())` を流用）:
```go
func TestAPIPlugins_OrderOverride(t *testing.T) {
	reg := plugin.NewRegistry()
	_ = reg.Register(fakePlugin{id: "chat", pane: plugin.PaneLeft})   // Meta.Order=0
	_ = reg.Register(fakePlugin{id: "topics", pane: plugin.PaneLeft}) // Meta.Order=0
	reg.SetOrder("chat", 25) // chat を後ろへ
	srv := New(reg, newTestShellFS())

	req := httptest.NewRequest("GET", "/api/plugins", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var got []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	// 配列順は topics(0) → chat(25)。chat の order フィールドも 25。
	if got[0]["id"] != "topics" || got[1]["id"] != "chat" {
		t.Fatalf("order wrong: got %v, %v", got[0]["id"], got[1]["id"])
	}
	if got[1]["order"].(float64) != 25 {
		t.Errorf("chat order: want 25, got %v", got[1]["order"])
	}
}
```
（注: `fakePlugin` の `Meta().Order` は 0 固定。両者 0 のとき従来は ID 昇順で chat→topics だが、override で chat=25 → topics(0) が先、chat(25) が後になることを検証する。）

- [ ] **Step 2: テストが失敗することを確認**

Run: `cd /home/ktat/git/github/agentarium-wt-order && go test ./kernel/server/ -run TestAPIPlugins_OrderOverride`
Expected: FAIL（配列順は Task 1 の sort で既に topics→chat になるが、DTO の `order` フィールドが `m.Order`(=0) のままで、`got[1]["order"]==25` の assert が 0 で落ちる）

- [ ] **Step 3: 実装**

`kernel/server/server.go` の `pluginsHandler` の DTO 構築を変更:
```go
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
```

- [ ] **Step 4: テストが通ることを確認**

Run: `cd /home/ktat/git/github/agentarium-wt-order && go test ./kernel/server/`
Expected: PASS（既存 `TestAPIPlugins_ReturnsMeta` / `TestAPIPlugins_HiddenFlag` 含め緑）

- [ ] **Step 5: コミット**

```bash
git add kernel/server/server.go kernel/server/server_test.go
git commit -m "feat(server): /api/plugins DTO の order を実効 Order(override 反映)に"
```

---

### Task 3: ファサード App.SetTabOrder

**Files:**
- Modify: `agentarium.go`
- Test: `agentarium_test.go`（存在すれば追記、無ければ新規 `package agentarium`）

**Interfaces:**
- Consumes: `Registry.SetOrder` / `EffectiveOrder`（Task 1）
- Produces: `func (a *App) SetTabOrder(id string, order int) *App`

- [ ] **Step 1: 失敗するテストを書く**

`agentarium_test.go`（無ければ新規、`package agentarium`）に追記:
```go
func TestSetTabOrder_DelegatesToRegistry(t *testing.T) {
	app := New().SetTabOrder("chat", 25)
	if got := app.Registry().EffectiveOrder("chat"); got != 25 {
		t.Errorf("EffectiveOrder(chat) = %d, want 25", got)
	}
}
```
（`New()` は `*App` を返し、`Registry()` は生 `*plugin.Registry` を返す既存 API。`SetTabOrder` は chainable。）

- [ ] **Step 2: テストが失敗することを確認**

Run: `cd /home/ktat/git/github/agentarium-wt-order && go test . -run TestSetTabOrder_DelegatesToRegistry`
Expected: FAIL（`SetTabOrder` 未定義）

- [ ] **Step 3: 実装**

`agentarium.go` に追記（`Registry()` 等の近くに）:
```go
// SetTabOrder はプラグイン id のタブ表示順を上書きする（bundled プラグイン含む）。
// Register の前後どちらでも呼べる。チェーン可能。
func (a *App) SetTabOrder(id string, order int) *App {
	a.reg.SetOrder(id, order)
	return a
}
```

- [ ] **Step 4: テストが通ることを確認**

Run: `cd /home/ktat/git/github/agentarium-wt-order && go test .`
Expected: PASS

- [ ] **Step 5: 全体テスト + vet**

Run: `cd /home/ktat/git/github/agentarium-wt-order && go vet ./... && go test ./...`
Expected: PASS

- [ ] **Step 6: コミット**

```bash
git add agentarium.go agentarium_test.go
git commit -m "feat(agentarium): App.SetTabOrder でタブ順を上書きする API を追加"
```

---

### Task 4: README 追従

**Files:**
- Modify: `README.md`

**Interfaces:** なし（ドキュメント）

- [ ] **Step 1: README を更新**

`README.md` の該当箇所（プラグイン IF / タブ / 公開 API の節）に追記:
- `App.SetTabOrder(id string, order int) *App`: 消費者が bundled 含む任意プラグインのタブ表示順（`Meta.Order` の実効値）を上書きできる。Register の前後どちらでも呼べる。Order 昇順・同値は ID 昇順でタブが並ぶ、という既存仕様を明記。

- [ ] **Step 2: コミット**

```bash
git add README.md
git commit -m "docs: App.SetTabOrder（タブ順上書き）を README に追記"
```

---

## 完了後

- `go test -race ./...` と `go vet ./...` がグリーンであることを確認。
- PR 作成（CodeRabbit レビュー → 解消 → main マージ）。
- 別途 **board-assistant リポ**で `app.SetTabOrder("chat", N)` を追加し Chat の位置を調整（別リポ・別対応。位置は利用者と相談）。
