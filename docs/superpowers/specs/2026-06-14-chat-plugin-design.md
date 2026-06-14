# Chat プラグイン（同梱）＋ `kernel/store` 公開 — 設計

- 日付: 2026-06-14
- ブランチ: `worktree-feat+chat-plugin`
- 移植参考元: backlog-worker `management-server/internal/dashboard/scripts_chat.go`（173行）+ `references/workflow-chat.md`

## 背景・目的

backlog-worker の「Chat タブ」は、自由入力テキストを既定エージェントの **最初の入力**
として送って会話を始める特化型ランチャー＋ `category=chat` 履歴である。agentarium に
同等の汎用機能を**同梱プラグイン**として持たせる。

Chat の価値は「導線（自由入力→起動）」と「カテゴリ履歴（chat だけ一覧・再開）」の両方。
agentarium は既に汎用 Agent ターミナルを持つため、Chat の差分価値は
**(1) ターミナルを意識させず自由入力を初期プロンプトとして注入する手間削減**と
**(2) chat 起動分だけを束ねた履歴・再開**にある。

## 既存基盤の調査結果（重要）

- カーネルのシェルは `window.agentarium.openAgentTab({key,label,agent,model,resume,command,autoEnter})`
  を公開済み。`command` は起動直後に `/terminal/inject` で PTY に注入される。
  → **導線（入力→起動＋初期入力注入）はカーネル無変更・プラグインフロントだけで実装可能**。
- `GET /terminal/list` は `SessionInfo{ID, Label, SessionID, State, Running}` を JSON で返す
  （フィールド名そのまま）。`SessionID` は再開識別子（旧 UUID 相当）。
  → ターミナル ID と再開識別子が同一行に並ぶため、backlog-worker が抱えた
  「起動→UUID 確定の検知（409 の複雑さ）」は agentarium では不要。
- カーネルには汎用 `JSONStore[T]`（`kernel/terminal/jsonstore.go`）が既にある。
  atomic save（tmp→rename＋親dir fsync）/ load 時 quarantine 付き。
  `terminal.NewStore(SessionRecord)` はこの別名。
  → 「プラグインから使える記憶領域」の中核は新規実装不要。公開すればよい。

## スコープ分割

| # | 部分 | 内容 | カーネル境界 |
|---|------|------|------|
| (a) | `kernel/store` | 既存 `JSONStore[T]` を公開パッケージへ昇格 | 既存機構の公開のみ。新規ロジックなし |
| (b) | `plugins/chat` | 入力導線＋カテゴリ履歴のバンドルプラグイン | プラグインのみ。plugin IF 無変更 |

実装順序は (a) → (b)。

## (a) `kernel/store`

- `kernel/terminal/jsonstore.go` の `JSONStore[T]` を新パッケージ `kernel/store` へ移動（公開）。
- `kernel/terminal` 側は後方互換のため型 alias + 関数委譲を残す:
  - `type JSONStore[T any] = store.JSONStore[T]`
  - `func NewJSONStore[T any](path string) *store.JSONStore[T] { return store.New[T](path) }`
  - 既存 `terminal.NewStore(path) *Store`（SessionRecord 用）は無変更で維持。
- 公開 API は現状の3メソッドのみ:
  - `func New[T any](path string) *JSONStore[T]`
  - `func (s *JSONStore[T]) Load() ([]T, error)`（無ければ `(nil, nil)`、破損は `.bak` 退避）
  - `func (s *JSONStore[T]) Save(entries []T) error`（atomic + 親dir fsync）
- **作らない（YAGNI）**: クエリ層 / マイグレーション / コード上の namespacing 機構。
- namespacing は「消費者 main が plugin ごとに別パスを渡す」で実現する
  （`secrets.NewStore(dataPath, keyPath)` / `sessions.New(workDir)` と同じ D7 消費モデル）。

## (b) `plugins/chat`

### レコード型

```go
type ChatRecord struct {
    ID         string `json:"id"`          // 採番した chat id（= terminal key）
    Summary    string `json:"summary"`     // ユーザー入力本文（クリーン）
    StartedAt  string `json:"started_at"`  // RFC3339
    SessionID  string `json:"session_id,omitempty"`  // 再開識別子。未取得は空
    ArchivedAt string `json:"archived_at,omitempty"` // archive 時刻。空=非archive
}
```

### バック（`RouteProvider`、`chat.New(store)` で注入）

- `chat.New(store *store.JSONStore[ChatRecord]) Plugin`。消費者 main が path を与えて構築。
- ルート（先頭スラッシュ、`/plugins/chat` 配下にマウント）:
  - `POST /start` — body `{summary}`。id を時刻ベースで採番し
    `{ID, Summary, StartedAt}` を append。`{id}` を返す。
  - `GET /list` — レコードを新しい順で返す（`{items: [...]}`）。
  - `POST /update?id=&session_id=` — 該当レコードに `SessionID` をセット（再開用紐付け）。
  - `POST /archive?id=` — `ArchivedAt` をセット（表示/非表示トグル用）。
- 書き込み系（start/update/archive）は `server.IsLocalOriginOrAbsent` で CSRF ガード
  （カーネルの handleStart/handleInject と同方針）。

### フロント（`FrontendProvider`、`assets/index.js`）

- 左ペインタブ「Chat」。
- 上部: 入力欄（テキストエリア＋送信ボタン、Enter送信 / Shift+Enter改行）。
- 下部: 履歴テーブル（テキスト / 作成時刻 / 操作）。
- 送信時:
  1. `POST /plugins/chat/start` に `{summary:text}` → 返った `id` を受け取る。
  2. `window.agentarium.openAgentTab({ key:id, label, command:text, autoEnter:true })`。
  3. `/terminal/list` をポーリングし、`row.ID === id` の `SessionID` が埋まったら
     `POST /plugins/chat/update?id=&session_id=` で紐付け。
- 履歴行の「↪ 再開」: `openAgentTab({ key:id, label, resume: record.SessionID })`。
  `SessionID` 未取得のレコードは再開ボタンを無効表示。
- archive トグル: archive 済みの表示/非表示を切り替え（localStorage で状態保持）。

### 配線（参照デモ）

`cmd/agentarium/main.go` で、既存の `terminalStorePath` と同じ流儀
（`os.UserConfigDir()/agentarium/` 配下）で chat.json のパスを解決し、
既存の `app.Register(...)`（可変長）に追加する:

```go
// chatStorePath() は os.UserConfigDir()/agentarium/chat.json を返す（terminalStorePath と同型）
chatStore := store.New[chat.ChatRecord](chatPath)
app.Register(
    hello.Plugin{},
    sessions.New(wd),
    chat.New(chatStore),
    manifestPlugin,
)
```

## resume の扱い（MVP の線引き）

- 再開は `/terminal/list` の `SessionID` を `ChatRecord.SessionID` に紐付けて行う
  （カーネル無変更・409 検知不要）。
- **やらないこと**:
  - 再開可否の事前チェック（`sessions.CanResume` 連携）は v1 では行わない。
    無効 resume は起動時に自然失敗させる（既存ターミナルの楽観方針に合わせる）。
  - claude jsonl からの要約取り込み、MR リンク、category 複数種、絵文字プレフィックス、
    slash プレフィックス（`prefix`）は入れない（特定アプリ固有 / OSS 非依存方針）。

## テスト

- `kernel/store`: 移動に伴い既存 jsonstore のテストを移設（Load/Save/quarantine/atomic）。
  `kernel/terminal` の alias 経由でも従来テストが通ることを確認。
- `plugins/chat`: ルートの table test（start→list→update→archive の往復、CSRF 拒否、
  不正 id）＋ store 往復テスト。
- フロント: embed 配信（`/plugins/chat/assets/index.js` 200）の存在確認程度。

## README 追従

- プラグイン表に Chat（タブ / ルート / データ保存先 `chat.json`）を追記。
- 公開 API として `kernel/store` を消費者向けに記載。

## 非目標（Out of Scope）

- MR リンク、category 複数種、絵文字、slash ルーティング prefix（アプリプラグイン側の責務）。
- claude jsonl 走査・要約取り込み（sessions プラグインの責務）。
- 再開可否の事前判定、UUID 自動検知の高度化。
- 1ファイル 700 行超（各ファイル 700 行以内を維持）。
