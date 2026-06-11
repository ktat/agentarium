# Agentarium

エージェント（Claude Code / Codex 等）をホストする汎用ランタイム + プラグイン式ダッシュボード。
最小のカーネル（HTTP サーバ / タブ UI シェル / Agent ターミナルサービス）に、機能を **プラグイン** として足していく構成です。ローカル実行前提（既定で `127.0.0.1` にバインド）。

## 消費モデル

Agentarium は **ライブラリ** として消費者リポから import されるのが主な使い方です。消費者は自分のワークフローアプリのために自分のリポで `main` を書き、カーネル + 好きな同梱プラグイン + 自作プラグインをコンパイル時に組みます。

```go
package main

import (
	"log"

	"github.com/ktat/agentarium"
	"github.com/ktat/agentarium/plugins/hello"
)

func main() {
	app := agentarium.New()
	if err := app.Register(hello.Plugin{}); err != nil { // 使うものだけ opt-in
		log.Fatal(err)
	}
	log.Fatal(app.Run("")) // "" は既定 127.0.0.1:8780
}
```

ファサード `agentarium`（`New` / `Register` / `WithTerminal` / `Handler` / `Run` / `Shutdown`）と、生パッケージ（`kernel/plugin` / `kernel/server` / `kernel/shell` / `kernel/terminal`）の両方が public です。

## 実行（参照デモ）

```sh
go run ./cmd/agentarium
```

`cmd/agentarium` は hello + sessions プラグインと xterm ターミナルを結線した参照デモです。宣言的 manifest（IF B）の例は [`examples/manifest-tab`](examples/manifest-tab) を参照。

### 環境変数

| 変数 | 既定 | 説明 |
|---|---|---|
| `AGENTARIUM_ADDR` | `127.0.0.1:8780` | listen アドレス |
| `AGENTARIUM_TERMINAL_RENDERER` | `xterm` | ターミナル backend（`xterm` / `wrap`）。Settings タブの「Kernel」からも設定でき、保存値が env より優先される（変更は再起動で反映） |
| `AGENTARIUM_ALLOW_PUBLIC` | （未設定） | `1` で非ループバックアドレスへのバインドを許可（既定はループバックのみ） |

## プラグイン

新タブの追加は「`Plugin` を実装して `Register` する」だけで完結します（ルータやシェルテンプレートの手編集は不要）。

### コンパイル時プラグイン（IF A）

必須契約は `Plugin`（`Meta()`）のみ。能力はオプトインの interface で、カーネルが型アサーションで拾います。

| interface | 役割 | 自動マウント先 |
|---|---|---|
| `Plugin` | `Meta() Meta`（ID / Title / Pane / Order）。必須 | タブボタン生成 |
| `RouteProvider` | `Routes() []Route` | `/plugins/<id>/...` |
| `FrontendProvider` | `Assets() fs.FS`（ルートに `index.js`） | `/plugins/<id>/assets/` |
| `SettingsProvider` | `SettingsSchema() []Field` | Settings 合流（S2 で本格利用） |

JS / CSS は `embed.FS` の実ファイルとして持ちます（Go const 文字列への埋め込みはしません）。

### 宣言的 manifest プラグイン（IF B）

「API → 一覧 → 行ボタンで Agent 起動」の定型タブは Go コード不要。manifest（JSON）を
`plugin.NewManifestPlugin([]byte)` で読み、通常どおり `Register` するだけで追加できます。
カーネル同梱の共有 `list` レンダラがテーブルを描画し、行ボタンが Agent ターミナルを起動します。

```go
//go:embed mytab.json
var manifestJSON []byte

p, err := plugin.NewManifestPlugin(manifestJSON)
if err != nil {
	log.Fatal(err)
}
app.Register(p)
```

manifest フィールド:

| フィールド | 必須 | 説明 |
|---|---|---|
| `id` | ○ | プラグイン ID（`[a-z0-9][a-z0-9_-]*`）。ルート名前空間に使う |
| `title` | ○ | タブ表示名 |
| `pane` | | `left`（既定）/ `right` |
| `order` | | タブ表示順 |
| `dataURL` | ○ | 一覧データ取得先。`/` 始まりの同一オリジンのみ（JSON 配列を返す API） |
| `render` | ○ | `list` のみ |
| `list.columns[]` | ○ | `{label, value}`。`value` は `{{.field}}` で行の値を参照 |
| `rowAction` | | `{label, type:"openAgent", agent, model?, resume?, command?, autoEnter?, key?, tabLabel?}`。テンプレート可。省略時は読み取り専用一覧 |

動く例は [`examples/manifest-tab`](examples/manifest-tab)（sessions 一覧を manifest だけで再現）。

### シェル提供の共通クラス

プラグインの DOM はシェルと同一ドキュメントを共有するため、`kernel/shell/assets/app.css` が定義するクラスをそのまま使えます。これはフレームワークとしての意図的な契約です。

| クラス | 用途 |
|---|---|
| `.card` / `.card-head` | カード枠と見出し |
| `.task-table`（`th`/`td`） | 一覧テーブル |
| `.resume-btn` / `.run-btn` / `.running-btn` | pill 操作ボタン |
| `.badge` | ステータスバッジ |
| `code` / `.mono` | 等幅表示 |
| `.muted` | 補助テキスト（淡色） |
| `h2` | セクション見出し |

デザイントークンは `app.css` 冒頭の `:root` 変数（`--accent` 等）で、テーマ差し替えの土台。

### 設定とシークレット（Settings）

プラグインが `SettingsProvider`（`SettingsSchema() []Field`）を実装すると、Settings タブに
その設定フォームが現れる。`App.WithSecrets(store)` で opt-in する。

```go
sec, _ := secrets.NewStore(dataPath, keyPath) // データと鍵は別ファイル
app.WithSecrets(sec)
app.Register(myplugin.New(secrets.Scope(sec, "myplugin"))) // plugin は sc.Get("token") で読む
```

- `Field.Secret=true` の項目は AES-256-GCM で暗号化保存。非 Secret は平文 JSON。
- 鍵 = `SHA-256(pepper ‖ passphrase)`。`passphrase` は初回生成のランダム値で**別ファイル**に 0600 保存。
  `pepper` はビルド時に注入する追加鍵素材（既定は空）。
- **脅威モデル**: データファイル単体の漏洩（バックアップ・誤コミット・平文 grep）から Secret を守る。
  ローカル FS 全読（鍵ファイル同時取得）は防がない。鍵ファイルは同期・共有対象から外すこと。
- 鍵ファイル / pepper を失うと既存 Secret は復号不能（`Get` は未設定扱い）。

ビルド時に pepper を焼き込む（自分用の private ビルド向け。公開不要）:

```sh
make build PEPPER=$(openssl rand -hex 16)
```

pepper を変えるときは、先にデータを変換してから再ビルドする:

```sh
agentarium secrets rekey --old-pepper=<旧> --new-pepper=<新>
make build PEPPER=<新>
```

### 右ペインビューア

プラグインのフロントは `agentarium.openViewer(...)` で右ペイン上部にタブ式ビューアを開ける。
未使用時はターミナルが全高、最初の表示で上下分割される（境界はドラッグでリサイズ）。

```js
agentarium.openViewer({ key: 'doc', title: 'Doc', type: 'markdown', content: '# Hello' });
agentarium.openViewer({ key: 'log', title: 'Log', type: 'text', url: '/plugins/foo/raw' });
agentarium.closeViewer('doc');
```

- `type: 'markdown'`（既定）はサーバ側（gomarkdown + bluemonday）で描画・サニタイズして表示。
- `type: 'text'` は `<pre>` でそのまま表示（安全）。
- `content`（インライン）または `url`（同一オリジンを fetch）でソースを渡す。

**Notion / Slack 等のアプリ固有ビューアは upstream に含めない。** 消費者リポのアプリプラグインが
`openViewer` を呼んで実装する（社内 ID・ホスト・認証に依存するため。消費モデルの節を参照）。

### 同梱プラグイン

| プラグイン | 説明 |
|---|---|
| `plugins/sessions` | `~/.claude/projects/<workdir>` の claude セッション一覧 + Resume |
| `plugins/hello` | 最小リファレンスプラグイン（Settings dogfood 付き） |

## Agent ターミナル

フレームワーク用語は **「Agent ターミナル」** で、`claude` は既定エージェントの一つにすぎません。実行バイナリは **Agent プロファイル**（`{Name, Invocation(RunRequest)}`）で可変です。コマンド起動時に `agent` / `model` を指定できます。

backend は 2 種類:

- `xterm`: 生 PTY バイト + xterm.js
- `wrap`: サーバ側 VT エミュレータ（行差分を JSON で送出）

## examples

| ディレクトリ | 説明 |
|---|---|
| `examples/consumer` | ライブラリとして import する消費者 `main` の最小例 |
| `examples/manifest-tab` | 宣言的 manifest（IF B）だけでタブを追加するサンプル |

## 開発

- 言語: Go 1.26 系
- テスト: `go test -race ./...`
