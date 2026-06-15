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

ファサード `agentarium`（`New` / `Register` / `WithTerminal` / `Handler` / `Run` / `Shutdown`）と、生パッケージ（`kernel/plugin` / `kernel/server` / `kernel/shell` / `kernel/terminal` / `kernel/store`）の両方が public です。

`kernel/store` は `[]T` を JSON ファイルへ atomic に永続化する汎用ストア（`store.New[T](path)`）で、プラグインが小さな状態を保存するのに使えます（消費者 main が plugin ごとに別パスを渡す）。

## 実行（参照デモ）

```sh
go run ./cmd/agentarium
```

`cmd/agentarium` は hello + sessions + chat + manifest プラグインと xterm ターミナルを結線した参照デモです。宣言的 manifest（IF B）の例は [`examples/manifest-tab`](examples/manifest-tab) を参照。

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

### Pet 連携（外部バイナリ）

デスクトップ Pet（マスコット）は **別バイナリ**として実装し、agentarium が公開する契約に従う。
agentarium 本体に Pet 描画は含めず、Settings タブの「Pet」ブロックから binary パス・skin・
自動起動を設定し、その場で起動もできる（`App.WithPet` で opt-in）。

**Pet バイナリの CLI 契約**
- `pet --list-skin` : 利用可能スキンを 1 行 1 名で stdout に出力。
- `pet --server <host:port> [--skin <name>]` : 起動し `http://<host:port>/terminal/events` に SSE 接続。

**状態 SSE 契約** `GET /terminal/events`（`text/event-stream`）。接続時に現在状態、以後は状態変化時のみ送信、15 秒ごとに `: ping` で keepalive:

```text
event: state
data: {"sessions":[{"id":"t1","label":"...","state":"running"}],"counts":{"idle":N,"running":N,"awaiting_user":N},"highest":"awaiting_user|running|idle"}
```

`sessions[]` は全 terminal の状態（Pet の popover 用）。`highest` の優先度は awaiting_user > running > idle で、Pet はこれを表情にマップする。この契約に従えば誰でも Pet バイナリを実装できる。

**focus**: Pet の popover 項目クリックは OS の open コマンドで `http://<host:port>/#term=<id>` を開く。shell はこの `#term=<id>` を解釈して該当 Agent タブを開く/アクティブ化する。

### 同梱プラグイン

| プラグイン | 説明 |
|---|---|
| `plugins/sessions` | `~/.claude/projects/<workdir>` の claude セッション一覧 + Resume |
| `plugins/chat` | 自由入力テキストを既定エージェントの初期入力として起動 + chat 履歴の一覧/再開/archive。ルート `/plugins/chat/{start,list,update,archive}`、保存先 `<os.UserConfigDir>/agentarium/chat.json` |
| `plugins/hello` | 最小リファレンスプラグイン（Settings dogfood 付き） |

## Agent ターミナル

フレームワーク用語は **「Agent ターミナル」** で、`claude` は既定エージェントの一つにすぎません。実行バイナリは **Agent プロファイル**（`{Name, Invocation(RunRequest)}`）で可変です。コマンド起動時に `agent` / `model` を指定できます。

backend は 2 種類:

- `xterm`: 生 PTY バイト + xterm.js
- `wrap`: サーバ側 VT エミュレータ（行差分を JSON で送出）

### 状態検出（Pet 連携）

Agent ターミナルの PTY 出力を観測し、セッション状態（`running` / `awaiting_user` / `idle`）を
判定して `/terminal/events` SSE に配信する。判定パターンは **Agent プロファイルが所有**する
（`terminal.StateAware` を実装した Agent のみ対象。未実装の Agent は `idle` 固定）。

- 出力が一定時間継続 → `running`
- 許可プロンプト（claude は `do you want to proceed`）→ `awaiting_user`
- 一定時間沈黙 → `idle` 降格（`awaiting_user` は時間では降格しない）

この状態が外部 Pet バイナリの表情にマップされる。

### セッション復元（再起動越え）

`NewRegistryWithStore` で Store を渡すと、開いていた Agent ターミナルを再起動越しに復元します。
プロセス自体は引き継がず、**Agent のセッション（`claude --resume <id>`）として論理復元**します。

- 保存先（参照デモ）: `<os.UserConfigDir>/agentarium/terminal-<renderer>.json`（renderer 別）
- 復元は lazy: 起動時は pending 登録のみ、タブを開く（WS 接続）と起動。warmup が低頻度で残りを起動
- 復元可否は `terminal.ResumableAgent.ResumeArtifact` が示すファイルの存在で判定（claude なら jsonl）。
  消費者は `ServiceConfig.CanResume` に判定関数を渡す（参照デモは `sessions.CanResume` を使用）
- フロント（xterm/wrap 両 renderer）は WS 切断時に自動再接続（指数バックオフ + 「再接続中…」表示）。
  サーバ再起動後に手動リロードなしで復元セッションへ繋ぎ直る

## examples

| ディレクトリ | 説明 |
|---|---|
| `examples/consumer` | ライブラリとして import する消費者 `main` の最小例 |
| `examples/manifest-tab` | 宣言的 manifest（IF B）だけでタブを追加するサンプル |

## 開発

- 言語: Go 1.26 系
- テスト: `go test -race ./...`
