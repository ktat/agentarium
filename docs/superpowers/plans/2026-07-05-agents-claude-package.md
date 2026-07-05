# agents/claude パッケージ新設 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** claude の完成版 Agent を public パッケージ `agents/claude` に集約し、リポ内4箇所のコピペを正典化して、消費者が `agents.Register(claude.New())` だけで全任意 IF を得られるようにする。

**Architecture:** リポルート直下に public パッケージ `agents/claude` を新設する。カーネルには claude を持ち込まず（spec §7 エージェント非依存を維持）、`terminal.ConfigAgent` と同じ「同梱・opt-in の Agent ヘルパー」層に置く。`cmd/agentarium` の現行 `claudeAgent` 実装を正典として移設し、`cmd/agentarium`・`examples/consumer`・`examples/manifest-tab` のローカル定義を差し替える。

**Tech Stack:** Go 1.26、標準 `testing`、`regexp`/`time`（StatePatterns）、既存 `plugins/sessions`（`SessionsDirFor`/`SessionIDs`）。

## Global Constraints

- 言語: Go 1.26 系
- モジュールパス: `github.com/ktat/agentarium`
- 公開可視性: `agents/claude` は public（`internal/` に置かない、D7）
- カーネルに `claude` をハードコードしない（spec §7）。本パッケージはカーネル外
- 1ファイル 700 行以内
- テストは TDD（Red → Green）。`make test`（`go test -race ./...`）と `make lint` がグリーン必須
- コミットは Conventional Commits（日本語・scope 付き）
- コミットメッセージ末尾に `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`

---

### Task 1: `agents/claude` パッケージ新設（TDD）

**Files:**
- Create: `agents/claude/claude.go`
- Test: `agents/claude/claude_test.go`

**Interfaces:**
- Consumes: `github.com/ktat/agentarium/kernel/terminal`（`terminal.Agent` / `terminal.ResumableAgent` / `terminal.SessionDetector` / `terminal.StateAware` / `terminal.RunRequest` / `terminal.StatePatterns`）、`github.com/ktat/agentarium/plugins/sessions`（`sessions.SessionsDirFor(workDir string) (string, error)` / `sessions.SessionIDs(workDir string) []string`）
- Produces: `claude.New() claude.Agent`、`claude.Agent`（`Name()` / `Invocation(terminal.RunRequest) (string, []string)` / `ResumeArtifact(workDir, sessionID string) string` / `ListSessionIDs(workDir string) []string` / `StatePatterns() terminal.StatePatterns`）

- [ ] **Step 1: 失敗するテストを書く**

Create `agents/claude/claude_test.go`:

```go
package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ktat/agentarium/kernel/terminal"
)

// 任意 IF の実装をコンパイル時に保証する。
var (
	_ terminal.Agent           = Agent{}
	_ terminal.ResumableAgent  = Agent{}
	_ terminal.SessionDetector = Agent{}
	_ terminal.StateAware      = Agent{}
)

func TestName(t *testing.T) {
	if got := New().Name(); got != "claude" {
		t.Fatalf("Name() = %q, want %q", got, "claude")
	}
}

func TestInvocation(t *testing.T) {
	bin, args := New().Invocation(terminal.RunRequest{
		Model:       "opus",
		Resume:      "sess-1",
		SessionName: "demo",
	})
	if bin != "claude" {
		t.Fatalf("binary = %q, want %q", bin, "claude")
	}
	want := []string{"--model", "opus", "--resume", "sess-1", "-n", "demo"}
	if strings.Join(args, " ") != strings.Join(want, " ") {
		t.Fatalf("args = %v, want %v", args, want)
	}
}

func TestInvocationEmpty(t *testing.T) {
	bin, args := New().Invocation(terminal.RunRequest{})
	if bin != "claude" {
		t.Fatalf("binary = %q, want %q", bin, "claude")
	}
	if len(args) != 0 {
		t.Fatalf("args = %v, want empty", args)
	}
}

func TestResumeArtifact(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("UserHomeDir unavailable")
	}
	got := New().ResumeArtifact("/tmp/work", "sess-1")
	if !strings.HasSuffix(got, "sess-1.jsonl") {
		t.Fatalf("ResumeArtifact should end with <sessionID>.jsonl: %s", got)
	}
	if !strings.HasPrefix(got, filepath.Join(home, ".claude", "projects")) {
		t.Fatalf("ResumeArtifact should be under ~/.claude/projects: %s", got)
	}
	if New().ResumeArtifact("/tmp/work", "") != "" {
		t.Fatal("empty sessionID should return empty path")
	}
}

func TestStatePatterns(t *testing.T) {
	p := New().StatePatterns()
	if p.Permission == nil || !p.Permission.MatchString("Do you want to proceed?") {
		t.Fatal("Permission should match claude 許可プロンプト")
	}
	if p.SustainedRunning != 2*time.Second {
		t.Fatalf("SustainedRunning = %v, want 2s", p.SustainedRunning)
	}
	if p.IdleTimeout != 1500*time.Millisecond {
		t.Fatalf("IdleTimeout = %v, want 1500ms", p.IdleTimeout)
	}
	if p.BurstGap != time.Second {
		t.Fatalf("BurstGap = %v, want 1s", p.BurstGap)
	}
}
```

- [ ] **Step 2: テストが失敗することを確認**

Run: `cd /home/ktat/git/github/agentarium/.claude/worktrees/feat+agents-claude-package && go test ./agents/claude/`
Expected: FAIL（`undefined: Agent` / `undefined: New` によりコンパイルエラー）

- [ ] **Step 3: 実装を書く**

Create `agents/claude/claude.go`:

```go
// Package claude は claude バイナリ用の完成版 Agent を提供する同梱ヘルパー。
// 消費者は agents.Register(claude.New()) で登録し、resume / session 検出 /
// 状態検出の任意 IF をまとめて得る。カーネルには claude を持ち込まない
// （spec §7 エージェント非依存）ための、terminal.ConfigAgent と同じ層のパッケージ。
package claude

import (
	"path/filepath"
	"regexp"
	"time"

	"github.com/ktat/agentarium/kernel/terminal"
	"github.com/ktat/agentarium/plugins/sessions"
)

// Agent は claude バイナリ用の Agent。ゼロ値で利用可能。
type Agent struct{}

// New は claude Agent を返す。
func New() Agent { return Agent{} }

func (Agent) Name() string { return "claude" }

// Invocation は RunRequest を claude 固有引数（--model / --resume / -n）へ変換する。
func (Agent) Invocation(req terminal.RunRequest) (string, []string) {
	var args []string
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	if req.Resume != "" {
		args = append(args, "--resume", req.Resume)
	}
	if req.SessionName != "" {
		args = append(args, "-n", req.SessionName)
	}
	return "claude", args
}

// ResumeArtifact は claude セッション履歴 jsonl のパスを返す（terminal.ResumableAgent）。
// 存在すれば --resume 可能。sessions プラグインの projects dir 規約を再利用する。
func (Agent) ResumeArtifact(workDir, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	dir, err := sessions.SessionsDirFor(workDir)
	if err != nil {
		return ""
	}
	return filepath.Join(dir, sessionID+".jsonl")
}

// ListSessionIDs は現在の claude セッション識別子を新しい順で返す（terminal.SessionDetector）。
// カーネルが新規起動セッションの UUID 検出（再開用の紐付け）に使う。
func (Agent) ListSessionIDs(workDir string) []string {
	return sessions.SessionIDs(workDir)
}

// claudePermission は claude の許可プロンプト検出パターン。StatePatterns は高頻度で
// 呼ばれるため、正規表現はパッケージ変数に切り出して再コンパイルを避ける。
var claudePermission = regexp.MustCompile(`(?i)do you want to proceed`)

// StatePatterns は claude TUI の PTY 出力に対する状態検出パラメータ（terminal.StateAware）。
func (Agent) StatePatterns() terminal.StatePatterns {
	return terminal.StatePatterns{
		Permission:       claudePermission,
		SustainedRunning: 2 * time.Second,
		IdleTimeout:      1500 * time.Millisecond,
		BurstGap:         time.Second,
	}
}
```

- [ ] **Step 4: テストが通ることを確認**

Run: `go test ./agents/claude/`
Expected: PASS（全テスト ok）

- [ ] **Step 5: コミット**

```bash
git add agents/claude/claude.go agents/claude/claude_test.go
git commit -m "$(cat <<'EOF'
feat(agents/claude): claude 完成版 Agent を public パッケージとして新設

resume / session 検出 / 状態検出の任意 IF をまとめて実装。消費者は
agents.Register(claude.New()) で全 IF を得る。カーネルには入れない
（spec §7 エージェント非依存を維持）。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: `cmd/agentarium` を新パッケージへ移行

**Files:**
- Modify: `cmd/agentarium/main.go`（ローカル `claudeAgent` 型と4メソッド、`claudePermission` 変数を削除。`import` に `agents/claude` 追加、不要になった `regexp` を削除、`time` は他で使われていなければ削除。`agents.Register(claudeAgent{})` → `agents.Register(claude.New())`）
- Modify: `cmd/agentarium/main_test.go`（`TestClaudeAgent_ResumeArtifact` を削除。挙動は Task 1 の `agents/claude/claude_test.go` に集約済み）

**Interfaces:**
- Consumes: Task 1 の `claude.New()`
- Produces: なし（アプリ配線のみ）

- [ ] **Step 1: ローカル claudeAgent を削除**

`cmd/agentarium/main.go` から以下を削除する:
- `type claudeAgent struct{}` とその全メソッド（`Name` / `Invocation` / `ResumeArtifact` / `ListSessionIDs` / `StatePatterns`）
- `var claudePermission = regexp.MustCompile(...)` 行

- [ ] **Step 2: import と登録を差し替え**

`import` ブロックに追加:

```go
	"github.com/ktat/agentarium/agents/claude"
```

`regexp` の import 行を削除する。登録箇所を差し替える:

```go
	agents := terminal.NewAgentRegistry("claude")
	agents.Register(claude.New())
```

- [ ] **Step 3: テストを削除**

`cmd/agentarium/main_test.go` から `TestClaudeAgent_ResumeArtifact` 関数全体を削除する。未使用になった import（`os` は他テストで使用中なので残す。`filepath`/`strings` は他テストで使用中か確認し、未使用なら削除）は次ステップのビルドで検出する。

- [ ] **Step 4: ビルドして未使用 import を解消**

Run: `cd /home/ktat/git/github/agentarium/.claude/worktrees/feat+agents-claude-package && go build ./cmd/... && go vet ./cmd/...`
Expected: 成功。失敗する場合はエラーが指す未使用 import（`regexp` / `time` / `filepath` / `strings` 等）を削除して再実行

- [ ] **Step 5: テスト実行**

Run: `go test ./cmd/...`
Expected: PASS

- [ ] **Step 6: コミット**

```bash
git add cmd/agentarium/main.go cmd/agentarium/main_test.go
git commit -m "$(cat <<'EOF'
refactor(cmd/agentarium): claudeAgent を agents/claude に移行

ローカルの claudeAgent 定義を削除し agents.Register(claude.New()) を使う。
ResumeArtifact のテストは agents/claude 側へ集約済み。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: `examples/*` を新パッケージへ移行

**Files:**
- Modify: `examples/consumer/main.go`（ローカル `claudeAgent`（`Name`/`Invocation`/`ListSessionIDs`）を削除、`agents/claude` を import、`agents.Register(claudeAgent{})` → `agents.Register(claude.New())`、未使用 import を解消）
- Modify: `examples/manifest-tab/main.go`（ローカル `claudeAgent`（`Name`/`Invocation`）を削除、`agents/claude` を import、`agents.Register(claudeAgent{})` → `agents.Register(claude.New())`、未使用 import を解消）

**Interfaces:**
- Consumes: Task 1 の `claude.New()`
- Produces: なし（サンプル配線のみ）

- [ ] **Step 1: examples/consumer を差し替え**

`examples/consumer/main.go` から `type claudeAgent struct{}` と全メソッドを削除。`import` に `"github.com/ktat/agentarium/agents/claude"` を追加。登録を差し替え:

```go
	agents := terminal.NewAgentRegistry("claude")
	agents.Register(claude.New())
```

- [ ] **Step 2: examples/manifest-tab を差し替え**

`examples/manifest-tab/main.go` から `type claudeAgent struct{}` と全メソッドを削除。`import` に `"github.com/ktat/agentarium/agents/claude"` を追加。登録を差し替え:

```go
	agents := terminal.NewAgentRegistry("claude")
	agents.Register(claude.New())
```

- [ ] **Step 3: ビルドして未使用 import を解消**

Run: `cd /home/ktat/git/github/agentarium/.claude/worktrees/feat+agents-claude-package && go build ./examples/...`
Expected: 成功。失敗する場合はエラーが指す未使用 import を削除して再実行（`examples/consumer` は `sessions` を ListSessionIDs 削除後も sessions プラグイン登録で使う可能性があるため、コンパイラの指示に厳密に従う）

- [ ] **Step 4: テスト実行**

Run: `go test ./examples/...`
Expected: PASS（テストが無いパッケージは `no test files` で可）

- [ ] **Step 5: コミット**

```bash
git add examples/consumer/main.go examples/manifest-tab/main.go
git commit -m "$(cat <<'EOF'
refactor(examples): claudeAgent を agents/claude に移行

examples/consumer と examples/manifest-tab のローカル claudeAgent を削除し
agents.Register(claude.New()) を使う。フル実装への昇格で session 検出/状態
検出を獲得する（退行なし）。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: README 追記と全体検証

**Files:**
- Modify: `README.md`（`## Agent ターミナル` 節と同梱一覧に `agents/claude` を追記）

**Interfaces:**
- Consumes: なし
- Produces: なし

- [ ] **Step 1: README を追記**

`README.md` の `## Agent ターミナル` 節（現在 268 行目付近）の直後に、claude 完成版 Agent の同梱を説明する段落を追加する:

```markdown
claude 用の完成版 Agent は `agents/claude` として同梱している。消費者は
`agents.Register(claude.New())` で登録するだけで、`--model` / `--resume` / `-n`
の引数組み立てに加え、セッション検出（`SessionDetector`）・復元判定
（`ResumableAgent`）・状態検出（`StateAware`）の任意 IF をまとめて得る。
自作エージェント（codex 等）を使う場合は同じ IF を実装した Agent を登録する。
```

- [ ] **Step 2: 全テストと lint を実行**

Run: `cd /home/ktat/git/github/agentarium/.claude/worktrees/feat+agents-claude-package && make test && make lint`
Expected: すべて PASS（`go test -race ./...` グリーン、lint 指摘なし）

- [ ] **Step 3: 重複が消えたことを確認**

Run: `grep -rn "type claudeAgent" --include='*.go' . | grep -v worktrees`
Expected: 出力なし（リポ内のローカル claudeAgent 定義が全消滅）

- [ ] **Step 4: コミット**

```bash
git add README.md
git commit -m "$(cat <<'EOF'
docs(readme): agents/claude 同梱 Agent の説明を追記

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Self-Review

**Spec coverage:**
- 新パッケージ `agents/claude`（API・挙動・IF アサート）→ Task 1 ✓
- `cmd/agentarium` 移行 + テスト集約 → Task 2 ✓
- `examples/consumer` / `examples/manifest-tab` 移行 → Task 3 ✓
- README 追記 → Task 4 ✓
- 依存（terminal / plugins/sessions）→ Task 1 の import ✓
- スコープ外（board-assistant・実行中バッジ・codex・セレクタ）→ 本計画に含めず（spec と一致）✓

**Placeholder scan:** プレースホルダなし。未使用 import の解消はコンパイラ出力に従う手順として明示。

**Type consistency:** `claude.New()` / `claude.Agent` / 各メソッドシグネチャは Task 1〜3 で一貫。`sessions.SessionsDirFor`（`(string, error)`）・`sessions.SessionIDs`（`[]string`）は実在シグネチャと一致。
