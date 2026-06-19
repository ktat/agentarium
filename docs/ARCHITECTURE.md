# Agentarium アーキテクチャ

エージェント（Claude Code / Codex 等）をホストする汎用ランタイム + プラグイン式ダッシュボード。
本ドキュメントは現状のコード構造を俯瞰する。利用者向けの使い方は [README.md](../README.md) を参照。

## 設計原則

1. **カーネル / プラグイン境界が最重要**。カーネルは「プラグインをホストする最小ランタイム」。
   それ以外はすべてプラグイン（一部を同梱）。
2. **ライブラリとして消費される**のが主。消費者は自分のリポで `main` を書き、カーネル + 好きな
   同梱プラグイン + 自作プラグインをコンパイル時に組む。`cmd/agentarium` は参照デモにすぎない。
3. **エージェント非依存**。`claude` は既定エージェントの一つ。実行バイナリは Agent プロファイルで可変。
4. **ローカル実行前提**（既定 `127.0.0.1` バインド）。外部公開しない。
5. **フロントは `embed.FS` の実ファイル**（JS/CSS）。SPA フレームワーク・ビルドツールは導入しない。

### 3 層構造

```text
┌─────────────────────────────────────────────────────────────┐
│ アプリプラグイン（特定アプリ固有）  ← このリポには入れない／別リポ │
├─────────────────────────────────────────────────────────────┤
│ バンドルプラグイン（汎用機能・一部同梱）  plugins/sessions, hello │
├─────────────────────────────────────────────────────────────┤
│ カーネル（プラグインをホストする最小ランタイム）  kernel/...      │
└─────────────────────────────────────────────────────────────┘
```

## ディレクトリレイアウト

```text
agentarium/
├── agentarium.go            # ファサード（App: New/Register/WithTerminal/WithSecrets/WithPet/Run...）
├── kernel/
│   ├── plugin/              # プラグイン契約（Plugin/Route/Frontend/Settings IF）+ Registry + manifest ローダ
│   ├── server/              # レジストリ走査 → HTTP ルート自動マウント + CSRF
│   ├── shell/               # タブ UI シェル（index.html / app.js / app.css を embed）
│   ├── terminal/            # Agent ターミナルサービス（Service / Backend 契約 / Agent / 永続化）
│   │   ├── xterm/           #   backend: 生 PTY バイト + xterm.js
│   │   └── wrap/            #   backend: サーバ側 VT エミュレータ（行差分 JSON）
│   ├── settings/            # 組み込み Settings タブ（SettingsProvider 合流 + Kernel 設定）
│   ├── secrets/             # 暗号化シークレットストア（AES-256-GCM・鍵ファイル分離）
│   ├── viewer/              # 右ペインビューア用 Markdown 描画（gomarkdown + bluemonday）
│   └── pet/                 # 外部 Pet バイナリの設定・起動（CLI/SSE 契約）
├── plugins/
│   ├── sessions/            # claude セッション一覧（jsonl）+ Resume
│   └── hello/              # 最小リファレンスプラグイン
├── cmd/agentarium/          # 参照デモ（hello + sessions + manifest + xterm/wrap + secrets/pet）
├── examples/
│   ├── consumer/            # ライブラリ import の最小消費者 main
│   └── manifest-tab/        # 宣言的 manifest だけでタブ追加
└── docs/
```

## ファサードと起動フロー

消費者は `agentarium.App` を通じて全体を組む（細かい制御が要れば生パッケージを直接使える）。

```go
app := agentarium.New()
app.Register(hello.Plugin{}, sessions.New(wd))   // プラグインを opt-in
app.WithSecrets(sec).WithTerminal(svc).WithPet(p) // カーネル機能を opt-in
app.Run("")                                       // "" は既定 127.0.0.1:8780
```

`App.Handler()` → `server.New(registry, shell.FS(), opts...)` が `http.ServeMux` を組み立てる。
`Run(addr string)` は listener を張る（`addr == ""` なら既定 `127.0.0.1:8780`、非ループバックは `AGENTARIUM_ALLOW_PUBLIC=1` が必要）。
`AGENTARIUM_ADDR` での上書きは API ではなく参照デモ `cmd/agentarium` 側の慣習（env を読んで `Run` に渡す）。
`Shutdown` で graceful 停止（Close を実装する backend の goroutine も止める）。

| App メソッド | 役割 |
|---|---|
| `New()` | 空の App（同梱プラグインは自動登録しない） |
| `Register(plugins...)` | プラグインを Registry に登録 |
| `WithTerminal(*terminal.Service)` | Agent ターミナルを opt-in |
| `WithSecrets(*secrets.Store)` | 暗号化設定ストア + Settings タブを opt-in |
| `WithPet(*pet.Supervisor)` | 外部 Pet 連携を opt-in |
| `Handler()` / `Run()` / `Shutdown()` | http.Handler 取得 / 起動 / 停止 |
| `Registry()` | プラグインレジストリ（拡張用） |

## プラグイン契約（kernel/plugin）

必須は `Plugin`（`Meta()`）のみ。能力はオプトインの interface で、`server` が**型アサーション**で拾う。

| interface | メソッド | 自動マウント先 |
|---|---|---|
| `Plugin`（必須） | `Meta() Meta`（ID / Title / Pane / Order） | タブボタン生成（`GET /api/plugins`） |
| `RouteProvider` | `Routes() []Route` | `POST/GET /plugins/<id><path>`（csrfGuard 付き） |
| `FrontendProvider` | `Assets() fs.FS`（ルートに `index.js`） | `GET /plugins/<id>/assets/...`（dir listing 禁止） |
| `SettingsProvider` | `SettingsSchema() []Field` | Settings タブに設定フォーム合流 |

- **新タブの追加 = 「`Plugin` を実装して `Register` する」だけ**。ルータやシェルテンプレートの手編集は不要。
- `Pane` は `left`（機能タブ）/ `right`（Agent ターミナル系）。
- ID は `^[a-z0-9][a-z0-9_-]*$`。ルート名前空間 `/plugins/<id>/` の安全性のため厳格。

### 宣言的 manifest プラグイン（IF B）

「API → 一覧 → 行ボタンで Agent 起動」の定型タブは Go コード不要。`plugin.NewManifestPlugin(json)` が
`Plugin`+`RouteProvider`+`FrontendProvider` を満たすプラグインを返す。カーネル同梱の共有 `list`
レンダラ（`kernel/plugin/manifest_assets/index.js`）がテーブルを描画し、行ボタンが Agent ターミナルを起動する。

## HTTP ルート構成（server.New）

`server.New` は Registry を走査して以下を 1 つの mux に組む。

| ルート | 供給元 | 備考 |
|---|---|---|
| `GET /{$}` | shell | index.html |
| `GET /assets/...` | shell | app.js / app.css（dir listing 禁止） |
| `GET /api/plugins` | server | タブ一覧（Meta を JSON 化） |
| `GET/POST /plugins/<id><path>` | 各 RouteProvider | csrfGuard |
| `GET /plugins/<id>/assets/...` | 各 FrontendProvider | dir listing 禁止 |
| `POST /viewer/render` | viewer | Markdown→安全 HTML（csrfGuard） |
| `/terminal/...` | terminal.Service.MountOn | 下記参照（opt-in） |
| `/pet/...` | pet.Supervisor.MountOn | opt-in |

CSRF 等価防御は `server.IsLocalOriginOrAbsent`（Origin/Referer が public host なら 403）。
状態変更系（plugin POST・terminal start/stop/inject・viewer render・pet 制御）に適用。

## カーネル各コンポーネント

| パッケージ | 役割 |
|---|---|
| `kernel/plugin` | プラグイン契約・`Registry`・manifest ローダ・共有 list レンダラ |
| `kernel/server` | mux 組み立て・CSRF・`Mountable`（terminal/pet の組み込み口） |
| `kernel/shell` | フロントシェル（index.html / app.js / app.css）を embed で公開 |
| `kernel/terminal` | Agent ターミナルサービス（次節） |
| `kernel/settings` | 組み込み Settings タブ。`SettingsProvider` を列挙し値を secrets に保存。Kernel 設定（`terminal_renderer` 等）も扱う |
| `kernel/secrets` | 暗号化ストア。`Field.Secret` を AES-256-GCM で保存。鍵 = `SHA-256(pepper‖passphrase)`、passphrase は別ファイル 0600。`Scope` で名前空間分離 |
| `kernel/viewer` | Markdown を gomarkdown でレンダリングし bluemonday でサニタイズ |
| `kernel/pet` | 外部 Pet バイナリ（別リポ）の skin 列挙・起動。`/pet/*` を MountOn。本体に Pet 描画は持たない |

## Agent ターミナルサービス（kernel/terminal）

### 中核

- **`Service`**: 制御層。`AgentRegistry` で agent 名を解決し、複数 `Backend` から active を 1 つ選んで
  start/stop/inject/list/renderer を提供。`/terminal/{start,stop,inject,list,renderer,events,ws,assets}` を MountOn。
  構築時に active backend の `Restore(CanResume)` を駆動する。
- **`Backend` 契約** = `TerminalBackend`（domain: Start/Stop/Inject/SetSessionID/List/AddStateListener/**Restore**）
  + `TransportBackend`（front: Renderer/Routes/Assets）。
- **`Agent`**: `{Name(), Invocation(RunRequest) → (binary, args)}`。`ResumableAgent` は任意拡張で
  `ResumeArtifact(workDir, sessionID)`（存在すれば resume 可能なファイル）を表明する。
- **永続化**: `SessionRecord`（ID/Label/WorkDir/Agent/SessionID/Model/Cols/AltRows）を `Store`
  = `JSONStore[SessionRecord]`（atomic 書き込み + 破損 quarantine）に保存。
- **イベント**: 状態 SSE（`/terminal/events`）を `sseHub` が配信（Pet 等が購読）。
- 補助: `ringbuf`（xterm の replay）、`ids`（terminal ID 検証）、`wskeepalive`（WS ping/pong）。

### backend は 2 種類

| | xterm | wrap |
|---|---|---|
| 描画 | 生 PTY バイトを xterm.js に流す | サーバ側 VT エミュレータが grid 行差分を JSON 送出 |
| 再接続復元 | ring buffer を replay | init snapshot で grid 再構築 |
| 用途 | 汎用・軽量 | CJK 等幅整列・差分描画が要るとき |

両 backend の Registry は **lazy 復元**を持つ:
1. 起動時 `Restore` が `SessionRecord` を **pending**（Process 未起動）で登録。
2. タブを開く（WS attach）と `EnsureStarted` が `Agent.Invocation(RunRequest{Resume: SessionID})`
   で起動（claude なら `--resume`）。
3. 誰も開かない pending は warmup ループが低頻度で 1 件ずつ起動（CPU/メモリスパイク回避）。
4. 復元可否は注入された `CanResume`（参照デモは `sessions.CanResume` → `ResumableAgent.ResumeArtifact`
   の jsonl 存在チェック）で判定。不可なら skip + store から除去。

### フロント自動再接続

`xterm`/`wrap` 両 renderer の `index.js` は WS 切断時に指数バックオフで自動再接続し、切断中は
「再接続中…」バナーを出す。サーバ再起動後に手動リロードなしで復元セッションへ繋ぎ直る
（reflex 等での開発時フリーズを解消）。

## データ保存先

| パス | 内容 | 書き手 |
|---|---|---|
| `<UserConfigDir>/agentarium/settings.json` | 設定 + 暗号化シークレット | secrets.Store |
| `<UserConfigDir>/agentarium/secret.key` | passphrase（0600） | secrets.Store |
| `<UserConfigDir>/agentarium/terminal-<renderer>.json` | セッション復元レコード | terminal Registry |
| `~/.claude/projects/<encoded-workdir>/<uuid>.jsonl` | claude セッション履歴（読み取り） | claude 本体（sessions プラグイン / ResumeArtifact が参照） |

## セキュリティモデル

- 既定 `127.0.0.1` バインド。非ループバックは `AGENTARIUM_ALLOW_PUBLIC=1` 明示が必要。
- CSRF: 状態変更エンドポイントは `IsLocalOriginOrAbsent` で cross-origin を 403。
  WS は `Upgrade` の `CheckOrigin` で検証（プロセス起動などの副作用は Upgrade 通過後に行う）。
- アセット配信はディレクトリ一覧を 404（情報漏洩防止）。
- シークレットはファイル単体漏洩（バックアップ・誤コミット・平文 grep）に耐える暗号化。
  鍵ファイル同時取得は防がない（鍵ファイルは同期・共有対象から外す）。

## 消費モデル（パッケージ可視性）

- カーネル（`kernel/...`）・同梱プラグイン（`plugins/...`）・ファサード（ルートの `package agentarium`）は
  **public（import 可能）**。`internal/` には消費者に見せない実装詳細だけを置く。
- 公開 API は二系統を維持: ファサード `agentarium.New()/Register()/Run()/Handler()/Registry()` と、
  生パッケージ `kernel/plugin`・`kernel/server`・`kernel/shell`・`kernel/terminal`。
- アプリ固有プラグイン（Notion/Slack 等）は upstream に入れず、消費者リポで実装する。
