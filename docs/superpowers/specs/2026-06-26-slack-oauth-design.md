# Slack OAuth プラグイン設計

- 日付: 2026-06-26
- ブランチ: `feat/slack-oauth`
- 移植元: `backlog-worker/management-server/internal/{slack,slackhttp}`

## 1. 目的

消費者の Slack ワークスペースに対し OAuth (v2) でユーザートークンを取得・保存し、そのトークンで
Slack API（メッセージ/スレッド/ユーザー取得）を呼べるようにする。取得トークンは他プラグインや
消費者 `main` から利用できる形で公開する。

利用シナリオ: 「Slack API 用のユーザートークンを OAuth で取得する」。localhost 実行が前提。

## 2. 配置とパッケージ境界

- カーネルではなく**バンドルプラグイン** `plugins/slack/`（public パッケージ）として実装する。
  - 理由: 「カーネル = プラグインをホストする最小ランタイム。それ以外は全部プラグイン」（CLAUDE.md）。
    OAuth + API クライアントは機能でありプラグインに属する。
- 消費者は自分の `main` で `slack.New(secretsStore)` を `Register` して opt-in する。
  既定では登録しない（ファサードのデフォルト方針に従う）。

## 3. ファイル構成

```
plugins/slack/
├── slack.go      # Plugin 本体: Meta/Routes/Assets/SettingsSchema, New(store)
├── oauth.go      # OAuthClient (AuthorizeURL/Exchange) ― 移植
├── handler.go    # Start/Callback ハンドラ, CSRF state(TTL付), redirect_uri を Host から生成
├── tokenstore.go # SecretTokenStore: secrets ストアに slack.tokens(JSON) を暗号化保存
├── client.go     # APIClient (GetMessage/GetThread/ListMessagesSince/GetUser) ― 移植
├── types.go      # 値オブジェクト: WorkspaceID / AccessToken(マスク) / OAuthState / Token / Message / User
├── urlparser.go  # 移植
├── assets/       # タブ UI: 接続状態 + 「Slack 連携」ボタン + workspace 一覧
└── *_test.go
```

1 ファイル 700 行以内を維持する（最大の client.go でも移植元 231 行）。

## 4. 配線と公開 API

- `func New(store *secrets.Store) Plugin`
  - `secrets.Scope(store, "slack")` で scoped ストアを作り、`SLACK_CLIENT_ID` / `SLACK_CLIENT_SECRET`
    を読み、トークン JSON を `tokens` キーへ `SetSecret`（暗号化）で保存する。
- **SettingsProvider**: `SLACK_CLIENT_ID` / `SLACK_CLIENT_SECRET` を secret フィールドとして
  Settings タブに合流させる（kernel の汎用 secret 管理 UI を再利用）。
- **RouteProvider**（`/plugins/slack/...` に自動マウント）:
  - `GET /start` — state を発行して Slack 認可 URL へ 302 リダイレクト
  - `GET /callback` — state 検証 → code を token に交換 → SecretTokenStore に保存 → 完了 HTML
  - `GET /tokens` — タブ用に接続済み workspace の概要 JSON（access token は返さない）
- **FrontendProvider**: 接続状態と「Slack 連携」ボタン（`/plugins/slack/start` へのリンク）、
  接続済み workspace 一覧を表示する独自タブ。
- **消費者向けアクセサ**: `func (p Plugin) Client() (*APIClient, error)`
  保存済みトークン（`GetAny` 相当）から APIClient を生成して返す。0 件なら `ErrNoToken`。
  他プラグイン/消費者 `main` がこれ経由で Slack API を呼ぶ。

## 5. backlog-worker から変更する点

| 項目 | 移植元 | agentarium |
|------|--------|-----------|
| トークン保存 | 平文 JSON ファイル (`FileTokenStore`) | 暗号化 secrets ストア（`slack.tokens` キーに workspace 単位 JSON）。`SecretTokenStore` として読み書きを secrets.Store 経由に置換 |
| redirect_uri | listen addr から固定生成 | `/start` ハンドラ内で `r.Host` + scheme（TLS/`X-Forwarded-Proto` 判定）から動的生成し `/plugins/slack/callback` を付与。設定不要 |
| CSRF state | in-memory map（無期限） | in-memory map + TTL（既定 10 分）。期限切れエントリは検証時に掃除。移植元のメモリリーク懸念への小改善 |
| ルートパス | `/oauth/slack/{start,callback}` | `/plugins/slack/{start,callback,tokens}`（プラグイン規約） |

トークンのデータ構造（`tokenFile` / `storedToken`）と OAuth ロジック（`AuthorizeURL`/`Exchange`）、
APIClient（`GetMessage`/`GetThread`/`ListMessagesSince`/`GetUser`）、`urlparser` は移植元をほぼそのまま移植する。

## 6. データフロー

```
[Settings] SLACK_CLIENT_ID/SECRET を保存
      │
[タブ] 「Slack 連携」クリック → GET /plugins/slack/start
      │  state 発行・保持、redirect_uri を Host から生成
      ▼
Slack 認可画面（user_scope）→ 同意
      │
GET /plugins/slack/callback?code=...&state=...
      │  state 検証 → oauth.v2.access で code→token 交換
      ▼
SecretTokenStore.Save(token)（暗号化 secrets へ）
      │
[消費者/他プラグイン] slack.Plugin.Client() → APIClient で Slack API 呼び出し
```

## 7. エラーハンドリング

- CLIENT_ID/SECRET 未設定: `/start` は 503 + 「Settings で設定してください」。タブは未設定表示。
- state 不正/期限切れ: `/callback` は 400。
- token 交換失敗（Slack API エラー）: 502 + Slack の error コードを表示。
- `Client()` でトークン 0 件: `ErrNoToken`。
- `AccessToken.String()` は `***` を返しログ/fmt 漏洩を防ぐ（移植元踏襲）。

## 8. テスト（TDD: Red → Green → Refactor）

- `OAuthState` 生成と一意性。
- `OAuthClient.AuthorizeURL` が client_id/user_scope/redirect_uri/state を含む。
- `OAuthClient.Exchange`: httptest で `oauth.v2.access` をモックし、token 抽出と `ok:false`/空 token のエラー。
- `SecretTokenStore`: Save → GetAny/GetAll/Get の往復、空時 `ErrNoToken`、secrets が暗号化保存されること。
- `redirect_uri` 生成: TLS/非 TLS、`X-Forwarded-Proto` での scheme 切替。
- ハンドラ: `/start` の 302 と state 保持、`/callback` の state 検証分岐、未設定時 503。
- `urlparser` の移植テスト。

## 9. ドキュメント追従

`README.md` の該当表を更新する: バンドルプラグイン一覧、環境変数（`SLACK_CLIENT_ID` /
`SLACK_CLIENT_SECRET`）、データ保存先（secrets ストア内 `slack.tokens`）、ルート
（`/plugins/slack/{start,callback,tokens}`）。

## 10. 非対象（YAGNI）

- メッセージ送信 API（移植元クライアントは履歴/ユーザー取得の読み取り中心。送信は移植元に無いため入れない）。
- 複数 workspace の選択 UI（保存は複数対応、利用は `GetAny`。選択は必要になってから）。
- トークンの自動リフレッシュ（Slack user token は失効しない運用前提。必要時に追加）。
- `SLACK_REDIRECT_URL` の明示設定によるフォールバック（今回は Host 自動生成のみ）。
