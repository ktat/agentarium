# reload 前に開いていた Agent タブの復元

- 日付: 2026-07-08
- ステータス: 設計承認済み（実装前）
- 対象: `kernel/shell/assets/app.js`（フロントエンドのみ。Go 側変更なし）

## 背景 / 課題

右ペインの Agent ターミナルタブは `app.js` の `rightTabs = new Map()` にメモリ保持されるだけで、
localStorage 等への永続化が無い。ブラウザを reload すると `main()` が左タブを再構築するのみで、
開いていた Agent タブは失われる（`app.js:563` にも「params はメモリ保持のみ＝リロードで失われる」と明記）。

一方サーバ側では起動中セッションを列挙する `GET /terminal/list`（`kernel/terminal/service.go:303`）が
存在し、`Registry.Start` は既存 id が `Running()` なら再起動せず既存プロセスを返す冪等実装
（`kernel/terminal/xterm/registry.go:133`）。既存の `focusFromHash`（`app.js:108`）は
`#term=<id>` deep-link で生存セッションに対し `openAgentTab({key,label})` を呼んでおり、
この「冪等 start による attach」に既に依存している。

## 目標 / 非目標

- 目標: **reload 直前に開いていたタブだけ**を、順序とアクティブタブを保って復元する
- 非目標:
  - 起動中セッション全部を無条件に復元する（重くなるため採らない）
  - サーバ再起動を越えた復元（既に `persist.go` 等が担う別レイヤ。本件はブラウザ reload のみ）
  - resume によるセッション復活（本件は「サーバに生存しているセッションへの再 attach」に限定）

## 決定事項

- stale タブ（保存されていたが reload 後 `/terminal/list` に無い id）: **静かに破棄**
- アクティブタブ: **reload 直前のアクティブタブを復元**（そのタブが破棄されていた場合は、最後に復元したタブが active のまま）

## 設計

### 1. 保存データ（localStorage）

- キー: `agentarium.rightTabs`（既存 `agentarium.layout.*` と同じ名前空間）
- 値（JSON）: `{ "keys": ["<tabKey>", ...], "active": "<tabKey>" }`
  - `keys` は配列順＝タブ表示順
  - ラベルは保存しない。復元時に `/terminal/list` の最新ラベルを使う（サーバを単一の真実源にする＝`focusFromHash` と同方針）。list に無ければそのタブは破棄されるためフォールバック不要

### 2. 保存タイミング

`persistRightTabs()` を新設し、`rightTabs` の集合・アクティブが変わる箇所すべてで呼ぶ:

- `openAgentTab`: タブ追加＋activate の後
- `closeAgentTab`: 削除＋新アクティブ確定の後
- `activateRightTab`: アクティブ切替の後

`persistRightTabs()` は現在の `rightTabs`（挿入順）から `keys` を、`active` クラスを持つタブから `active` を組み立て、
`localStorage.setItem` する。書き込みは `try/catch` で握りつぶす（`app.js:520` と同様、制限環境で throw しても動作を止めない）。

### 3. 復元フロー

`restoreRightTabs()` を新設し、`main()` 内で `focusFromHash` の前に `await` する:

1. `localStorage` から `agentarium.rightTabs` を読み JSON parse。不在・parse 失敗・`keys` が配列でない場合は何もせず return
2. `GET /terminal/list` を 1 回 fetch し `id → item` の map を作る（fetch 失敗時は return）
3. 保存 `keys` を順に走査し、**list に存在する id だけ** `await openAgentTab({ key, label: item.Label, silent: true })` を呼ぶ
   - `command` / `resume` / `agent` / `model` は渡さない → 再 inject も再起動も起きない。start は冪等で既存プロセスへ attach
   - list の項目キーは `item.ID`（`focusFromHash` は `it.ID || it.id` の両対応。ラベルは `it.Label || it.label`）を踏襲
4. 全復元後、保存 `active` が復元済みタブに含まれるなら `activateRightTab(active)`。含まれなければ何もしない（`openAgentTab` が最後に開いたタブを active 済み）
5. storage の明示掃除は不要: 復元中に走る `persistRightTabs()` が現存タブだけを書き直すため stale は自然に消える

### 4. `openAgentTab` の alert 抑制（小改修）

`openAgentTab(opts)` に内部用フラグ `silent`（省略時 false）を追加する。
start 失敗 / renderer 未結線時、`silent` が true なら `alert()` の代わりに `console.warn` に留める。
これは復元経路（list で存在確認済みだが list→start 間の競合で稀に失敗しうる）で
ユーザーに不要な alert を出さないため。公開 API `window.agentarium.openAgentTab` の
外向き挙動（`silent` 省略時）は不変。

### 5. 既存挙動との相互作用・エッジ

- **`focusFromHash` との順序**: restore → focusFromHash。`#term=<id>` deep-link が既に復元済みタブを指す場合、
  `openAgentTab` は `rightTabs.has` で activate のみ（冪等）。deep-link のアクティブ指定が復元アクティブを
  上書きするのは許容（明示リンク優先）
- **pending セッション**: `/terminal/list` に lazy 復元の pending（`Process==nil`）が含まれる場合、
  start はそれを fresh 起動する。タブ attach として妥当な挙動で許容
- **再 inject しない**: restore は `{key,label,silent}` のみ渡すため `command` 経路に入らない

### 6. ファイルサイズ

`app.js` 646 行 → 約 +40 行で ~686 行。700 行目安の範囲内。今回は分割しない。

## テスト方針

このリポに JS 単体テストのランナーは無い（SPA / ビルドツールチェーン非導入 = spec D1）。
純粋ロジックを切り出しても実行基盤が無いため、**検証は実ブラウザ駆動**（chrome-devtools MCP）で行う:

1. Agent タブを複数開く → 別タブをアクティブにする → reload
2. 確認: タブ群が同順で復元 / reload 前のアクティブタブが前面 / 端末内容が生きている（再 attach 成功）
3. 1 つ手動 close → reload → 閉じたタブが復元されない
4. サーバで 1 セッションを stop 相当にして list から消す → reload → その stale タブが静かに破棄される
5. reload 復元時に `command` 再 inject が起きない（端末に余計な入力が入らない）

Go 側は変更なしのため `make test`（`go test -race ./...`）はグリーン維持の確認のみ。

## 影響ファイル

- `kernel/shell/assets/app.js`: `persistRightTabs()` / `restoreRightTabs()` 新設、
  `openAgentTab`（`silent` 追加＋保存呼び出し）、`closeAgentTab` / `activateRightTab`（保存呼び出し）、
  `main()`（restore 呼び出し）を編集
- README.md: タブ / 環境変数 / データ保存先 / サブコマンド / プラグイン IF の追加変更に該当しないため更新不要
  （localStorage キーはユーザー向けドキュメント対象外）
