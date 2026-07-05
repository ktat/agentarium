# Favicon 指定オプション 設計

- 日付: 2026-07-05
- ブランチ: `feat/favicon`

## 背景 / 課題

消費者アプリ（Agentarium を import して自分の `main` を書く側）が、ブラウザタブの
favicon を指定する手段がない。現状 `kernel/shell/assets/index.html` の `<head>` には
favicon の `<link>` が一切なく、ブラウザは `/favicon.ico` を探して 404 になる。

`<title>` は既に `server.WithTitle` / `App.WithTitle` で上書き可能。favicon も同じ
「配信時に `indexHandler` が文字列置換で注入する」パターンにきれいに乗る。

## スコープ

消費者が **href 文字列**を渡し、カーネルが `<head>` に `<link rel="icon">` を注入する。
favicon 実体の配信は消費者責任（data URI / 自作プラグイン資産パス / 絶対URL のいずれか）。

## 公開 API（D7: ファサード + 生パッケージの両方を維持）

```go
// kernel/server
func WithFavicon(href string) Option // config.favicon に格納

// ファサード（agentarium.go）
func (a *App) WithFavicon(href string) *App // → server.WithFavicon にマップ
```

## 注入方式

- `index.html` の `<head>` 内にプレースホルダコメント `<!--favicon-->` を1行置く。
- `WithFavicon` が設定された時だけ、`indexHandler` が `<!--favicon-->` を
  `<link rel="icon" href="{escaped href}">` に置換する。
- 未設定なら無置換（コメントのまま = 実質何もなし。現状挙動を維持）。
- href は消費者コード由来だが、`WithTitle` と同様 `html.EscapeString` で属性値を
  エスケープする（衛生）。

## スコープ外（YAGNI）

- `type` 属性の指定（ブラウザが拡張子 / data URI の mediatype から推論。必要時に追加）。
- カーネルでの favicon 実体配信（消費者責任）。
- ヘッダ左上 `span.title` への反映（favicon はブラウザタブのみ）。

## 変更ファイル

- `kernel/server/server.go` — `config.favicon`, `WithFavicon`, `indexHandler` 置換ロジック
- `agentarium.go` — `App.favicon`, `App.WithFavicon`, `Run` での opts 追加
- `kernel/shell/assets/index.html` — `<!--favicon-->` プレースホルダ追加
- `kernel/server/server_test.go`, `agentarium_test.go` — テスト追加
- `README.md` — 公開オプション表があれば追従

## テスト（TDD）

- `WithFavicon("...")` 設定時: レスポンス HTML に `<link rel="icon" href="...">` が注入される。
- 未設定時: `<link rel="icon"` が含まれない（`<!--favicon-->` のまま）。
- href に `"` 等を含む場合: `html.EscapeString` でエスケープされる。
