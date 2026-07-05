# `agents/claude` パッケージ新設 設計

- 日付: 2026-07-05
- 状態: 承認済み（実装前）
- 起点: board-assistant で Chat タブの「再開」が非活性・「実行中」状態が出ない

## 背景と根本原因

board-assistant（agentarium の消費者リポ）で Chat の「再開」ボタンが常に非活性になる。
根本原因は board-assistant の `claudeAgent` が `Name()` / `Invocation()` しか実装しておらず、
`terminal.SessionDetector`（`ListSessionIDs`）を実装していないこと。

失敗の連鎖:

1. `terminal.WatchNewSession` は `det, ok := ag.(SessionDetector); if !ok { return }` で即 return する
2. 起動後のセッション UUID が一切検出されない
3. `svc.SessionID(id)` は常に空を返す
4. chat の `WithSessionLookup(svc.SessionID)` バックフィルも、フロントの `/terminal/list` ポーリングも SessionID を得られない
5. `ChatRecord.SessionID` が空のまま → フロントが再開ボタンを `disabled` にする（`plugins/chat/assets/index.js`）

同様に `StateAware`（`StatePatterns`）未実装のため状態検出（実行中/待機）も動かない。

### 構造的な問題（落とし穴）

`claudeAgent` はリポ内外に4回コピペされ、実装している任意 IF がバラバラ:

| 場所 | Name/Invocation | ListSessionIDs | ResumeArtifact | StatePatterns |
|---|:-:|:-:|:-:|:-:|
| `cmd/agentarium` | ✓ | ✓ | ✓ | ✓ |
| `examples/consumer` | ✓ | ✓ | ✗ | ✗ |
| `examples/manifest-tab` | ✓ | ✗ | ✗ | ✗ |
| board-assistant | ✓ | ✗ | ✗ | ✗ |

任意 IF を落とすと再開・状態表示が黙って壊れる。単一の正典が必要。

## 目的

claude の完成版 Agent を単一の public パッケージに集約し、コピペ由来の IF 欠落を構造的に解消する。
消費者は `agents.Register(claude.New())` するだけで全 IF を得る。

## 方針決定（合意事項）

- **プラグイン化のレベル**: (A) 同梱 import パッケージ方式。`plugin.Plugin` にはしない
  （Agent はタブではなく、Meta/Pane/Routes/Assets を持たないため空実装契約になるのを避ける）
- **同梱範囲**: 今回は **claude のみ**。Codex は CLI 仕様（session 検出・resume パス・状態パターン）
  が実機依存で、推測実装は同じ落とし穴を再生産するため別タスク
- **スコープ**: 今回は **agentarium 側のみ**（パッケージ新設 + リポ内重複の移行）。
  board-assistant の差し替えは後続タスク
- **スコープ外**: Chat の「実行中」バッジ表示。Chat の Agent セレクタ UI
  （選択肢が claude 1つの間は YAGNI。Codex 同梱時にまとめて設計）

### 補足: 複数 Agent はフレームワークとしては既にサポート済み

`AgentRegistry` は name→Agent の map で複数登録でき、`openAgentTab({agent})` →
`/terminal/start?agent=` → `service.resolveAgent(q.Get("agent"))` で name 解決される。
本設計はその複数 Agent 化の前提整備（`claude.New()` / 将来の `codex.New()` を並べる形）にあたる。

## アーキテクチャ

- リポルート直下に public パッケージ `agents/claude` を新設（D7 の可視性: `internal/` に置かない）
- カーネルには claude を持ち込まない（spec §7「エージェント非依存」を維持）。
  これは同梱・opt-in の Agent ヘルパーであり、`terminal.ConfigAgent` と同じ層

## API

```go
package claude

// Agent は claude バイナリ用の完成版 Agent。ゼロ値で利用可能。
type Agent struct{}

// New は claude Agent を返す。消費者は agents.Register(claude.New()) で登録する。
func New() Agent { return Agent{} }

func (Agent) Name() string                                          // "claude"
func (Agent) Invocation(req terminal.RunRequest) (string, []string) // --model/--resume/-n
func (Agent) ResumeArtifact(workDir, sessionID string) string       // terminal.ResumableAgent
func (Agent) ListSessionIDs(workDir string) []string               // terminal.SessionDetector
func (Agent) StatePatterns() terminal.StatePatterns                // terminal.StateAware
```

コンパイル時のIF実装保証:

```go
var (
    _ terminal.Agent           = Agent{}
    _ terminal.ResumableAgent  = Agent{}
    _ terminal.SessionDetector = Agent{}
    _ terminal.StateAware      = Agent{}
)
```

## 挙動仕様（`cmd/agentarium` の現行実装を正典として移設）

- `Name()` → `"claude"`
- `Invocation(req)`:
  - `req.Model != ""` → `--model <model>`
  - `req.Resume != ""` → `--resume <resume>`
  - `req.SessionName != ""` → `-n <name>`
  - バイナリは `"claude"`
- `ResumeArtifact(workDir, sessionID)`:
  - `sessionID == ""` → `""`
  - それ以外 → `sessions.SessionsDirFor(workDir)` 配下の `<sessionID>.jsonl`
  - `SessionsDirFor` がエラー → `""`
- `ListSessionIDs(workDir)` → `sessions.SessionIDs(workDir)`
- `StatePatterns()`:
  - `Permission`: `(?i)do you want to proceed`（パッケージ変数で1回だけコンパイル）
  - `SustainedRunning`: 2s / `IdleTimeout`: 1500ms / `BurstGap`: 1s

## 依存

`kernel/terminal`（RunRequest / StatePatterns）と `plugins/sessions`（`SessionsDirFor` / `SessionIDs`
= claude の `~/.claude/projects` 規約）。後者は `cmd/agentarium` が既に import しており、
新規の層違反は生じない（agent ヘルパー → 同梱プラグインの public ヘルパー参照は許容）。

## 移行（リポ内の重複解消・正典化）

- `cmd/agentarium/main.go`: ローカル `claudeAgent`（型定義と4メソッド）を削除し、
  `agents.Register(claude.New())` に置換。`canResume`（`sessions.CanResume(agents.Resolve(...))`）
  の配線はそのまま
- `examples/consumer/main.go` / `examples/manifest-tab/main.go`: ローカル定義を `claude.New()` に置換
  （Invocation は3コピーとも完全一致を確認済み。examples はフル実装への昇格で
  session 検出/状態検出を新たに獲得する＝改善であり退行なし）
- `cmd/agentarium/main_test.go` の `TestClaudeAgent_ResumeArtifact` は新パッケージ側テストへ集約

## テスト（TDD, Red → Green）

`agents/claude/claude_test.go`:

- `Name()` が `"claude"`
- `Invocation`: model / resume / SessionName 各指定時の引数、無指定時に空、バイナリ `"claude"`
- `ResumeArtifact`: 空 sid で `""`、非空 sid で `~/.claude/projects` 配下の `<sid>.jsonl`
- `ListSessionIDs`: `sessions.SessionIDs` へ委譲（存在確認レベル）
- `StatePatterns`: `Permission` が `do you want to proceed` にマッチ、各 duration が期待値
- IF 実装の静的アサート（上記 `var _ = ...`）

## README

`## Agent ターミナル` 節に、claude の完成版 Agent が `agents/claude` として同梱され、
消費者は `agents.Register(claude.New())` で全 IF（resume/session 検出/状態検出）を得る旨を追記。
`## examples` 近辺の同梱一覧にも `agents/claude` を追加。

## 今回スコープ外（後続タスク）

- board-assistant の `claudeAgent` を `agents/claude` へ差し替え、実機で再開・状態検出を verify
- Chat の「実行中」バッジ表示（問題2b）
- `agents/codex` の同梱（Codex CLI 仕様確定後）
- Chat の Agent セレクタ UI（選択肢が2つ以上になってから）
