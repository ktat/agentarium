# 既定非表示・オンデマンド/クローズ可能タブ + Settings 起動 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** プラグインタブを `Meta.Hidden` で既定非表示にでき、Settings の「開けるタブ」から開く／× で閉じられるようにし、Slack タブをその方式に移行する。

**Architecture:** kernel の `plugin.Meta` に `Hidden` フラグを足し `/api/plugins` DTO で公開。シェル(app.js)は hidden タブを起動時に出さず、`openTab`/`activateLeftTab` がオンデマンドでタブボタンを生成、hidden タブは × で閉じられる。Settings のフロントが `/api/plugins` の hidden を列挙し「開く」ボタンを出す（Go 無改修）。Slack は `Meta.Hidden=true` に。

**Tech Stack:** Go 1.26、標準ライブラリ、embed.FS の実 JS/CSS（SPA/ビルド無し）。

## Global Constraints

- 言語 Go 1.26 系、標準ライブラリのみ。
- JS/CSS は `embed.FS` の実ファイル。Go const 文字列への埋め込み禁止。SPA/ビルドツール導入禁止（spec D1）。
- 1 ファイル 700 行以内。TDD（Go はテスト先行）。
- Conventional Commits（日本語・scope 付き）: `feat(plugin):` / `feat(server):` / `feat(shell):` / `feat(settings):` / `feat(slack):` / `docs:`。
- カーネル/プラグイン境界を守る。新タブ追加は Plugin 実装 + Register で完結（run.go/シェル手編集に戻さない）。
- 開閉状態は永続化しない（リロードで hidden 既定に戻る。deep-link `#tab=<id>` は openTab のオンデマンド生成で維持）。
- コア（非 hidden）タブにクローズ機能は付けない。
- JS はユニットテスト基盤なし → JS 変更は手動スモークで確認、Go は自動テスト。

---

### Task 1: Meta.Hidden 追加 + /api/plugins DTO 反映

**Files:**
- Modify: `kernel/plugin/plugin.go`（`Meta` に `Hidden bool`）
- Modify: `kernel/server/server.go`（`pluginDTO` に `Hidden`、`pluginsHandler` で設定）
- Test: `kernel/server/server_test.go`

**Interfaces:**
- Produces: `plugin.Meta.Hidden bool`; `/api/plugins` の各要素 JSON に `"hidden": true`（false のときは省略）

- [ ] **Step 1: 失敗するテストを書く**

`kernel/server/server_test.go` の `fakePlugin` に `hidden` フィールドを足し、`Meta()` に反映する。既存の `fakePlugin{id, pane}` 構築はゼロ値 false で不変。
```go
// fakePlugin の定義を差し替え
type fakePlugin struct {
	id     string
	pane   plugin.Pane
	hidden bool
}

func (f fakePlugin) Meta() plugin.Meta {
	return plugin.Meta{ID: f.id, Title: "T-" + f.id, Pane: f.pane, Order: 0, Hidden: f.hidden}
}
```
そのうえで新規テストを追加:
```go
func TestAPIPlugins_HiddenFlag(t *testing.T) {
	reg := plugin.NewRegistry()
	_ = reg.Register(fakePlugin{id: "alpha", pane: plugin.PaneLeft})
	_ = reg.Register(fakePlugin{id: "gamma", pane: plugin.PaneLeft, hidden: true})
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
	byID := map[string]map[string]any{}
	for _, g := range got {
		byID[g["id"].(string)] = g
	}
	if _, present := byID["alpha"]["hidden"]; present {
		t.Errorf("alpha should omit hidden (omitempty), got %+v", byID["alpha"])
	}
	if byID["gamma"]["hidden"] != true {
		t.Errorf("gamma hidden = %v, want true", byID["gamma"]["hidden"])
	}
}
```

- [ ] **Step 2: テストが失敗することを確認**

Run: `cd /home/ktat/git/github/agentarium-wt-hidden && go test ./kernel/server/ -run TestAPIPlugins_HiddenFlag`
Expected: FAIL（`Hidden` 未定義 or `hidden` が JSON に出ない）

- [ ] **Step 3: 実装**

`kernel/plugin/plugin.go` の `Meta` に追記:
```go
type Meta struct {
	ID     string
	Title  string
	Pane   Pane
	Order  int
	Hidden bool // true: 起動時にタブを出さない。Settings の「開けるタブ」から開く。開いたタブはクローズ可
}
```

`kernel/server/server.go` の `pluginDTO` と `pluginsHandler`:
```go
type pluginDTO struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Pane   string `json:"pane"`
	Order  int    `json:"order"`
	Hidden bool   `json:"hidden,omitempty"`
}
```
```go
		out = append(out, pluginDTO{ID: m.ID, Title: m.Title, Pane: paneString(m.Pane), Order: m.Order, Hidden: m.Hidden})
```

- [ ] **Step 4: テストが通ることを確認**

Run: `cd /home/ktat/git/github/agentarium-wt-hidden && go test ./kernel/server/ ./kernel/plugin/`
Expected: PASS（既存 `TestAPIPlugins_ReturnsMeta` 等も緑）

- [ ] **Step 5: コミット**

```bash
git add kernel/plugin/plugin.go kernel/server/server.go kernel/server/server_test.go
git commit -m "feat(plugin): Meta に Hidden を追加し /api/plugins DTO で公開"
```

---

### Task 2: シェル app.js — hidden スキップ + オンデマンド生成 + × クローズ

**Files:**
- Modify: `kernel/shell/assets/app.js`

**Interfaces:**
- Consumes: `/api/plugins` の `hidden`（Task 1）
- Produces: `openTab(pluginId, params)` が hidden タブをオンデマンド生成して開く（board-assistant/Settings が利用）

JS はユニットテスト基盤なし。**手動スモークで検証**（Step 4）。

- [ ] **Step 1: main() のタブ生成ループを差し替え**

`kernel/shell/assets/app.js` の `main()` 内、現在の
```js
  for (const p of plugins) {
    if (p.pane === 'right') continue; // 右ペインプラグインは agent タブと衝突するため左に統一
    const btn = document.createElement('button');
    btn.className = 'left-tab';
    btn.textContent = p.title;
    btn.dataset.pluginId = p.id;
    leftTabs.set(p.id, { p, btn });
    btn.addEventListener('click', () => {
      activateLeftTab(p.id);
      if (location.hash !== '#tab=' + p.id) location.hash = '#tab=' + p.id;
    });
    leftBar.appendChild(btn);
  }
```
を次に置き換える:
```js
  for (const p of plugins) {
    if (p.pane === 'right') continue; // 右ペインプラグインは agent タブと衝突するため左に統一
    const btn = makeLeftTabButton(p);
    leftTabs.set(p.id, { p, btn });
    if (!p.hidden) leftBar.appendChild(btn); // hidden はオンデマンド表示まで bar に出さない
  }
```

- [ ] **Step 2: ヘルパを追加**

`activateLeftTab` の直前あたりに追加:
```js
// makeLeftTabButton は左タブボタンを生成する。hidden プラグインには × クローズを付ける。
function makeLeftTabButton(p) {
  const btn = document.createElement('button');
  btn.className = 'left-tab';
  btn.textContent = p.title;
  btn.dataset.pluginId = p.id;
  btn.addEventListener('click', () => {
    activateLeftTab(p.id);
    if (location.hash !== '#tab=' + p.id) location.hash = '#tab=' + p.id;
  });
  if (p.hidden) {
    const x = document.createElement('span');
    x.textContent = ' ×';
    x.style.cursor = 'pointer';
    x.style.marginLeft = '6px';
    x.title = '閉じる';
    x.addEventListener('click', (e) => {
      e.stopPropagation(); // タブ切替と分離
      closeLeftTab(p.id);
    });
    btn.appendChild(x);
  }
  return btn;
}

// closeLeftTab は hidden タブを bar から取り除く（leftTabs エントリは残し、再度開けるようにする）。
function closeLeftTab(pluginId) {
  const t = leftTabs.get(pluginId);
  if (!t) return;
  t.btn.remove();
  if (currentLeftTab === pluginId) {
    currentLeftTab = null;
    if (location.hash === '#tab=' + pluginId) location.hash = '';
    const first = leftBar.querySelector('.left-tab');
    if (first) {
      activateLeftTab(first.dataset.pluginId);
    } else {
      leftPanel.replaceChildren(); // 可視タブが無ければ左ペインを空に
    }
  }
}
```

- [ ] **Step 3: activateLeftTab をオンデマンド表示対応にする**

現在の `activateLeftTab` の
```js
async function activateLeftTab(pluginId, params) {
  const t = leftTabs.get(pluginId);
  if (!t) return false;
  leftBar.querySelectorAll('.left-tab').forEach((b) => b.classList.remove('active'));
```
を次に変更（`if (!t) return false;` の直後に1行追加）:
```js
async function activateLeftTab(pluginId, params) {
  const t = leftTabs.get(pluginId);
  if (!t) return false;
  if (!t.btn.isConnected) leftBar.appendChild(t.btn); // hidden タブのオンデマンド表示
  leftBar.querySelectorAll('.left-tab').forEach((b) => b.classList.remove('active'));
```
（`openTab` は `activateLeftTab` を呼ぶだけなので追加変更不要。`#tab=<hiddenId>` の deep-link も focusFromHash→activateLeftTab 経由でオンデマンド生成される。）

- [ ] **Step 4: 手動スモーク**

Run:
```bash
cd /home/ktat/git/github/agentarium-wt-hidden && go run ./cmd/agentarium
```
ブラウザ `http://127.0.0.1:8780` で確認（cmd/agentarium が hidden プラグインを同梱しない場合は Step 5 の slack 化後 or board-assistant で確認）。最低限、既存タブが従来通り表示・切替できること（回帰なし）を確認。
Expected: 既存の左タブが表示され切替できる。エラーコンソール無し。

- [ ] **Step 5: ビルド/vet + コミット**

Run: `cd /home/ktat/git/github/agentarium-wt-hidden && go build ./... && go vet ./...`
```bash
git add kernel/shell/assets/app.js
git commit -m "feat(shell): hidden タブの非表示・オンデマンド生成・× クローズを実装"
```

---

### Task 3: Settings に「開けるタブ」セクション

**Files:**
- Modify: `kernel/settings/assets/index.js`

**Interfaces:**
- Consumes: `/api/plugins` の `hidden`（Task 1）、`globalThis.agentarium.openTab`（Task 2 / 既存ホスト API）

JS ユニットテスト基盤なし → 手動スモーク（Step 3）。

- [ ] **Step 1: 「開けるタブ」描画関数を追加**

`kernel/settings/assets/index.js` に関数を追加:
```js
// renderOpenableTabs は hidden プラグインを列挙し「開く」ボタンを出す。
// クリックで左ペインの該当タブをオンデマンドで開く（agentarium.openTab）。
async function renderOpenableTabs(root) {
  let plugins = [];
  try {
    const res = await fetch('/api/plugins');
    if (res.ok) plugins = await res.json();
  } catch (_) { return; }
  const hidden = plugins.filter((p) => p.hidden === true);
  if (hidden.length === 0) return; // 対象なしならセクションを出さない

  const card = document.createElement('div');
  card.className = 'card';
  const h = document.createElement('h3');
  h.textContent = '開けるタブ';
  card.appendChild(h);
  for (const p of hidden) {
    const row = document.createElement('div');
    row.style.margin = '6px 0';
    const label = document.createElement('span');
    label.textContent = p.title + '  ';
    const btn = document.createElement('button');
    btn.className = 'run-btn';
    btn.textContent = '開く';
    btn.addEventListener('click', () => {
      if (globalThis.agentarium && typeof globalThis.agentarium.openTab === 'function') {
        globalThis.agentarium.openTab(p.id);
      }
    });
    row.appendChild(label);
    row.appendChild(btn);
    card.appendChild(row);
  }
  root.appendChild(card);
}
```

- [ ] **Step 2: Settings のトップ描画から呼ぶ**

`index.js` の Settings 一覧を描画するエントリ（`render(root, ...)` またはスキーマ描画後の箇所。既存で `card` を `root`/コンテナに append している関数）末尾で `renderOpenableTabs` を呼ぶ。既存のスキーマ描画（Kernel / Kernel Secrets / 各プラグイン）の**後**に追加する:
```js
  await renderOpenableTabs(root); // 既存セクション描画の後に「開けるタブ」を追加
```
（`root` は既存描画が使っているルート要素に合わせる。await 可能な文脈でなければ `renderOpenableTabs(root);` として fire-and-forget でよい。）

- [ ] **Step 3: 手動スモーク**

Run: `cd /home/ktat/git/github/agentarium-wt-hidden && go build ./...`
Task 5（slack を hidden 化）後に board-assistant か cmd で起動し、Settings に「開けるタブ」→ Slack「開く」が出て、クリックで左ペインに Slack タブが開くことを確認。
Expected: hidden プラグインが「開けるタブ」に列挙され、「開く」で該当タブが開く。

- [ ] **Step 4: コミット**

```bash
git add kernel/settings/assets/index.js
git commit -m "feat(settings): hidden プラグインを開く「開けるタブ」セクションを追加"
```

---

### Task 4: Slack プラグインを Hidden に

**Files:**
- Create: `plugins/slack/slack_test.go`（Meta テスト。package `slack`）
- Modify: `plugins/slack/slack.go`

**Interfaces:**
- Consumes: `plugin.Meta.Hidden`（Task 1）、既存テストヘルパ `newTestStore(t)`（`plugins/slack/handler_test.go` 定義、同一 package `slack`）

- [ ] **Step 1: 失敗するテストを書く**

`plugins/slack/slack_test.go`（新規、`package slack`。`newTestStore` は同 package の handler_test.go に既にあるので流用）:
```go
package slack

import "testing"

func TestMeta_Hidden(t *testing.T) {
	p := New(newTestStore(t))
	if !p.Meta().Hidden {
		t.Error("slack Meta().Hidden = false, want true（既定非表示・Settings から開く）")
	}
}
```

- [ ] **Step 2: テストが失敗することを確認**

Run: `cd /home/ktat/git/github/agentarium-wt-hidden && go test ./plugins/slack/ -run TestMeta_Hidden`
Expected: FAIL（Hidden が false）

- [ ] **Step 3: 実装**

`plugins/slack/slack.go` の `Meta()`:
```go
func (p Plugin) Meta() plugin.Meta {
	return plugin.Meta{ID: pluginID, Title: "Slack", Pane: plugin.PaneLeft, Order: 5, Hidden: true}
}
```

- [ ] **Step 4: テストが通ることを確認**

Run: `cd /home/ktat/git/github/agentarium-wt-hidden && go test ./plugins/slack/`
Expected: PASS（既存 slack テストも緑）

- [ ] **Step 5: コミット**

```bash
git add plugins/slack/slack.go plugins/slack/slack_test.go
git commit -m "feat(slack): タブを既定非表示にし Settings から開く（Meta.Hidden）"
```

---

### Task 5: README 追従

**Files:**
- Modify: `README.md`（agentarium）

**Interfaces:** なし（ドキュメント）

- [ ] **Step 1: agentarium README を更新**

`README.md` の該当箇所に反映（既存表現に合わせる）:
- プラグイン IF の説明に `Meta.Hidden`（true で起動時タブ非表示・Settings の「開けるタブ」から開く・× で閉じられる）を追記。
- タブ/Settings の説明に「開けるタブ」セクションを追記。
- Slack を「既定非表示。Settings の『開けるタブ』から開く」と明記（Slack を列挙している箇所があれば更新）。

- [ ] **Step 2: コミット**

```bash
git add README.md
git commit -m "docs: Meta.Hidden と Settings「開けるタブ」、Slack 既定非表示を README に追記"
```

---

## 完了後

- `go test -race ./...` と `go vet ./...` がグリーンであることを確認。
- 手動スモーク（起動時 Slack タブ非表示 → Settings「開けるタブ」→「開く」→ 左ペイン表示 → × で閉じる → リロードで hidden 既定 → `#tab=slack` で再オープン）。
- PR 作成（CodeRabbit レビュー → 解消 → main マージ）。
- 別途 **board-assistant リポ**の `README.md` も「Slack タブは Settings から開く」に更新（agentarium とは別リポ・別 PR。この計画のスコープ外だが忘れないこと）。
