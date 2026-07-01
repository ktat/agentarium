# 既定非表示・オンデマンド/クローズ可能タブ + Settings からの起動 設計

- 日付: 2026-07-01
- リポジトリ: agentarium（kernel + 同梱 slack プラグイン）
- ブランチ: `feat/hidden-tabs`

## 1. 目的

特定機能タブ（当面は Slack 連携）を**起動時のタブバーに常設せず**、Settings から必要なときだけ開けるようにする。
タブバーの常設項目を減らしつつ、既存プラグインのタブ UI をそのまま活かす。

きっかけ: Slack OAuth 連携を専用タブとして常設するより、Settings から開く形が望ましい、という UX 要望。
「Settings 内で連携を完結」ではなく「Settings を入口にオンデマンドでタブを開く」方式を採用する（利用者合意済み）。

## 2. 現状（変更前）

- `kernel/plugin.Meta{ID, Title, Pane, Order}`。`Pane` は `PaneLeft`/`PaneRight` のみ。
- `kernel/server` の `/api/plugins` は登録済み全プラグインを列挙し、`pluginDTO{ID, Title, Pane, Order}` を返す。
- `kernel/shell/assets/app.js` は起動時に `/api/plugins` から**左ペインの全プラグイン分のタブボタンを静的生成**する
  （`p.pane === 'right'` はスキップ）。左タブは**クローズ不可・オンデマンド生成なし**。`openTab`/`activateLeftTab`
  は既存タブの切替のみ。右ペイン Agent タブのみクローズ可能。
- Slack プラグイン（`plugins/slack/slack.go`）は `Meta{Pane: PaneLeft}` のため常設タブになる。

## 3. スコープ

### やること
- `plugin.Meta` に `Hidden bool` を追加。
- `/api/plugins` の DTO に `Hidden` を載せる。
- シェル: hidden プラグインは起動時タブを出さない／`openTab` でオンデマンド生成／開いた hidden タブは × でクローズ可。
- Settings 画面に「開けるタブ」セクション（hidden プラグイン一覧 + 「開く」ボタン）。
- Slack プラグインの `Meta` を `Hidden: true` に。

### やらないこと（YAGNI）
- 開閉状態の永続化（リロードで hidden 既定に戻る。deep-link `#tab=<id>` は openTab のオンデマンド生成で維持）。
- コアタブ（非 hidden）のクローズ機能。
- Settings 内へのプラグイン固有 UI 埋め込み（別案として検討したが不採用）。
- JS ユニットテスト基盤の新設（spec D1: フロントは embed.FS の実ファイル、SPA/ビルド無し）。

## 4. コンポーネント

### 4.1 `kernel/plugin/plugin.go`
```go
type Meta struct {
    ID     string
    Title  string
    Pane   Pane
    Order  int
    Hidden bool // true: 起動時タブを出さない。Settings の「開けるタブ」から開く。開いたタブはクローズ可
}
```
`Pane` は不変（Hidden は Pane と直交。hidden な左ペインプラグインを表す）。

### 4.2 `kernel/server/server.go`
```go
type pluginDTO struct {
    ID     string `json:"id"`
    Title  string `json:"title"`
    Pane   string `json:"pane"`
    Order  int    `json:"order"`
    Hidden bool   `json:"hidden,omitempty"`
}
```
`pluginsHandler` で `Hidden: m.Hidden` を設定。列挙対象は従来通り全プラグイン（hidden もルート/assets 配信は継続、フラグのみ付与）。

### 4.3 `kernel/shell/assets/app.js`
- **起動時タブバー生成**: 既存ループ（`for (const p of plugins)`）で `p.pane === 'right'` のスキップに加え、`p.hidden === true` もタブボタンを生成しない。ただし `leftTabs` へは登録しておく（`{p, btn: null}`）か、プラグイン情報を別 Map（`hiddenPlugins`）に保持して openTab がオンデマンド生成できるようにする。
  - 実装方針: `leftTabs` に全左プラグイン（hidden 含む）の `{p, btn}` を入れ、hidden は `btn` を bar に **append しない**。openTab 時に btn 未 append なら append する。
- **`openTab(pluginId, params)` 拡張**: 対象が hidden かつタブボタン未表示なら、ボタンをオンデマンドで生成・`leftBar` に append（× クローズ付き）してから `activateLeftTab`。既存の可視タブはそのまま切替。
- **クローズ**: hidden 由来で開いたタブのボタンに × を付ける。クリックで：
  - `leftBar` からボタンを除去（`leftTabs` の該当 `btn` を null 化 or hiddenに戻す）、
  - アクティブだったら別タブ（先頭の可視タブ）へ切替、
  - `location.hash === '#tab=' + id` なら hash を空にする。
  - コア（非 hidden）タブには × を付けない。
- **リロード挙動**: 永続化なし。`focusFromHash` が `#tab=<hiddenId>` を検出したら `openTab` 経由でオンデマンド生成して開く（deep-link 維持）。

### 4.4 `kernel/settings/assets/index.js`
Settings 画面に「開けるタブ」セクションを追加。
- `/api/plugins` を fetch し `hidden === true` を抽出。
- 各プラグインに `Title` ラベル + 「開く」ボタン。クリックで `globalThis.agentarium.openTab(id)`（Settings は左ペイン。openTab で左ペインの当該タブへ切替わる）。
- hidden プラグインが 0 件ならセクション自体を出さない。
- Go 側 `settings.go` は**無改修**。

### 4.5 `plugins/slack/slack.go`
```go
func (p Plugin) Meta() plugin.Meta {
    return plugin.Meta{ID: pluginID, Title: "Slack", Pane: plugin.PaneLeft, Order: 5, Hidden: true}
}
```
Slack のタブ UI（`assets/index.js` の連携ボタン・workspace 一覧）は不変。

### 4.6 board-assistant
無改修。`slack.New` 登録済みのため、hidden 化で自動的にタブが消え Settings から開ける。

## 5. データフロー
```
起動 → /api/plugins（slack: hidden:true）→ シェルは slack タブボタンを bar に出さない
Settings タブ →「開けるタブ」に Slack「開く」ボタン
「開く」→ agentarium.openTab('slack') → openTab が slack タブをオンデマンド生成・左ペインに表示
Slack タブ内で従来通り「Slack 連携」→ /plugins/slack/start → OAuth → /plugins/slack/callback
× → タブ消滅（再度 Settings から開ける）。リロードでも hidden 既定に戻る
```

## 6. エラーハンドリング / エッジ
- `openTab('slack')` を既に開いている状態で再度呼ぶ → 二重生成せず既存タブをアクティブ化（`leftTabs` の btn 有無で判定）。
- 未知 pluginId / 右ペインプラグインの openTab → 従来通り false。
- hidden プラグインがルート/assets を持たない場合でも「開く」は tab 生成を試みる（assets 無ければ従来の activate 失敗ハンドリングに従う）。

## 7. テスト
- **Go（自動）**:
  - `kernel/plugin` or `kernel/server`: `Meta{Hidden:true}` が `/api/plugins` DTO に `hidden:true` として反映される（httptest でハンドラを叩き JSON を検証）。`Hidden` 未設定は `hidden` 省略（omitempty）。
  - 既存の server/settings/slack テストの非回帰（`go test -race ./...`）。
- **手動スモーク（JS はユニット基盤なし）**:
  - `go run ./cmd/agentarium`（または board-assistant）で、起動時に Slack タブが出ないこと、Settings の「開けるタブ」に Slack があること、「開く」で左ペインに Slack タブが出ること、× で閉じられること、リロードで消えること、`#tab=slack` で再度開くこと。

## 8. ドキュメント追従
- agentarium `README.md`: `Meta.Hidden` の説明、Settings「開けるタブ」、Slack が既定非表示で Settings から開く旨をプラグイン IF / タブ / Slack の該当箇所に追記。
- board-assistant `README.md`: Slack タブが「Settings から開く」形に変わった旨（既存の Slack 記述を更新）。

## 9. 段階的実装の単位（plan で分割）
1. `plugin.Meta.Hidden` 追加 + `server` DTO/ハンドラ反映 + Go テスト。
2. シェル app.js: 起動時 hidden スキップ + openTab オンデマンド生成 + × クローズ。
3. settings assets: 「開けるタブ」セクション。
4. slack Meta を Hidden:true に。
5. README 追従（agentarium + board-assistant）。
