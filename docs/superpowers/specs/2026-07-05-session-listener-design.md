# SessionListener（セッション ID 割当の consumer 通知）— 設計

## 背景・目的

ターミナルの Agent（claude 等）が起動すると、`WatchNewSession` が workDir の
セッション履歴を監視し、新規に出現した識別子を検出して `SetSessionID(id, sessionID)` で
端末レコードへ紐付ける。consumer プラグインが「この端末の再開識別子」を**永続化して覚えたい**
場合、現状は次のいずれかしかない:

- `Service.SessionID(terminalID)` を後からポーリングして引く（端末が live な間しか返らない）。
- フロントで `/terminal/list` をポーリングする（タブが開いている間しか捕捉できない）。

どちらも「端末が生きている / タブが開いている」ことに依存し、割当直後にタブを閉じる・
端末を Stop する・アプリを再起動するといった操作で取りこぼす。

割当イベントを**検出時点で server 側 consumer に push** する仕組みを足し、consumer が
自前ストアへ即永続化できるようにする。既存の `StateListener`（状態遷移通知）と対称の設計。

## スコープ

やること:
- `terminal.SessionListener` 型と `TerminalBackend.AddSessionListener` を追加。
- `xterm.Registry` / `wrap.Registry` に `sessionListeners` と `AddSessionListener` を追加し、
  `SetSessionID` が **空→非空（かつ変化）** に遷移したときだけロック外で発火。
- `xterm.Backend` / `wrap.Backend` は registry へ委譲。
- `Service.AddSessionListener` を追加し active backend へ委譲。

やらないこと:
- SSE イベントバスへの publish（フロント依存＝取りこぼしの元なので採らない）。
- 割当解除（非空→空）の通知（現状 SetSessionID に空を渡す経路は index 掃除用途で、
  consumer 通知の必要が無い）。
- 既存 `StateListener` の変更。

## 用語・前提
- 「割当（assign）」= `SetSessionID` で端末の `SessionID` が空から非空になる、または別値に変わること。
- listener は **active backend のもの**だけが呼ばれる（Service は active backend に委譲）。
- listener は全端末について発火する。特定端末だけ関心がある consumer は id で自前フィルタする。

## コンポーネント

### 1. `kernel/terminal/terminal.go`
```go
// SessionListener は端末に再開識別子が割り当てられたとき呼ばれる callback。
// id は terminal id、sessionID は割り当てられた識別子（空文字では呼ばれない）。
type SessionListener func(id, sessionID string)
```
`TerminalBackend` IF に追加:
```go
AddSessionListener(SessionListener)
```

### 2. `xterm.Registry` / `wrap.Registry`
- フィールド `sessionListeners []SessionListener`（`stateListeners` と同居）。
- `AddSessionListener(l SessionListener)`: `nil` は無視、mu ロックで append（`AddStateListener` と同形）。
- `SetSessionID` の発火条件:
  - 現状の実装は「旧 index を消す→`e.SessionID = sessionID`→非空なら index 追加」。
  - **旧値と異なり、かつ `sessionID != ""`** のときだけ、ロック解放後に listeners を順に呼ぶ
    （`SetState` の「listeners はロック外で呼ぶ」作法を踏襲。deadlock/再入防止）。
  - listeners のスナップショットをロック内で取り、ロック外で呼ぶ。

### 3. `xterm.Backend` / `wrap.Backend`
```go
func (b *Backend) AddSessionListener(l terminal.SessionListener) { b.Registry.AddSessionListener(l) }
```

### 4. `Service`
```go
// AddSessionListener は active backend のセッション割当 listener を登録する。
func (s *Service) AddSessionListener(l SessionListener) { s.active.AddSessionListener(l) }
```
`onStateChange` を service が自分で AddStateListener しているのと異なり、これは
**consumer が任意に登録する公開口**（Service 内部からは登録しない）。

## データフロー
```
端末起動 → WatchNewSession が新規セッション検出 → registry.SetSessionID(id, uuid)
  → (空→非空 遷移) → sessionListeners 発火（ロック外）
    → consumer callback（例: 評価プラグインが id→自前ストアへ永続化）
```

## エラーハンドリング
- listener が panic した場合の隔離は行わない（`StateListener` と同方針。consumer 責務）。
- 端末が存在しない id への `SetSessionID` は現状どおり no-op（listener も呼ばれない）。

## テスト
- `xterm`/`wrap` registry: `AddSessionListener` 登録後 `SetSessionID(id, "s1")` で 1 回呼ばれる。
  同値の再 `SetSessionID` では呼ばれない。空文字 `SetSessionID(id, "")` では呼ばれない。
  別値へ変えると再度呼ばれる。存在しない id では呼ばれない。
- `Service.AddSessionListener` が active backend の登録に委譲されること（fake backend で確認）。

## リスク・トレードオフ
1. kernel/ターミナル IF に 1 メソッド増える。既存 `StateListener` と対称なので学習コストは低いが、
   `TerminalBackend` 実装者（xterm/wrap 以外の将来 backend）に実装義務が増える。
2. listener は全端末で発火する。関心のない端末は consumer 側でフィルタする必要がある
   （フレームワークは端末種別を知らないため、ここでの絞り込みはしない）。
3. 同期呼び出し（発火スレッドで listener 実行）。重い listener は SetSessionID を遅延させる
   （consumer は軽い永続化に留める前提）。
