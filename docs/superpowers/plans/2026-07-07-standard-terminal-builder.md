# 標準ターミナル配線ビルダ 実装計画

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** demo にハードコードされていた「両 backend＋settings 駆動 Active＋renderer 別 Store＋CanResume」の標準ターミナル配線を再利用ヘルパ `kernel/terminal/standard.NewService` に切り出し、board-assistant / demo 双方をそれ経由に統一して、Settings の `terminal_renderer`（wrap）が確実に反映されるようにする。

**Architecture:** `kernel/terminal` に純粋関数 `CanResume` を移設（層の整合）し、`kernel/terminal/standard` パッケージが xterm/wrap 両 backend を組み立て active を解決した `*terminal.Service` を返す。ファサード（`package agentarium`）は無変更。消費者は返り値を既存の `app.WithTerminal(svc)` に渡すだけ。

**Tech Stack:** Go 1.26 / 標準ライブラリ / 既存 kernel パッケージ（terminal, terminal/xterm, terminal/wrap, settings, secrets）。

## Global Constraints

- 言語: Go 1.26 系。
- **ランタイム切替は非スコープ**。active は起動時に確定、Settings 変更は再起動で反映。
- カーネルは `internal/` に入れず public に保つ。**kernel → plugin の import は層違反**（`standard` は `plugins/sessions` を import しない）。
- 過剰な抽象化を避け、言われたこと以外はしない。
- 2リポにまたがる: agentarium リポ（`/home/ktat/git/github/agentarium`）と board-assistant リポ（`/home/ktat/git/github/board-assistant`）。各タスクの commit は該当リポで行う。
- board-assistant は go.mod の `replace github.com/ktat/agentarium => ../agentarium` でローカル参照するため、agentarium 側の変更は即座に反映される。

---

## File Structure

- `kernel/terminal/resume.go`（新規, agentarium）— `CanResume` 実体
- `kernel/terminal/resume_test.go`（新規, agentarium）— `CanResume` テスト
- `plugins/sessions/resume.go`（変更, agentarium）— `terminal.CanResume` へ委譲
- `kernel/terminal/standard/standard.go`（新規, agentarium）— `Config` / `NewService`
- `kernel/terminal/standard/standard_test.go`（新規, agentarium）— `NewService` テスト
- `cmd/agentarium/main.go`（変更, agentarium）— demo を `standard.NewService` へ
- `README.md`（変更, agentarium）— 標準配線と store パス注記
- `main.go`（変更, board-assistant）— `standard.NewService` へ
- `README.md`（変更, board-assistant）— データ保存先に terminal store 追記

---

## Task 1: `CanResume` を `kernel/terminal` へ移設

**作業リポ:** agentarium（`/home/ktat/git/github/agentarium`）

**Files:**
- Create: `kernel/terminal/resume.go`
- Create: `kernel/terminal/resume_test.go`
- Modify: `plugins/sessions/resume.go`

**Interfaces:**
- Produces: `func CanResume(ag terminal.Agent, workDir, sessionID string) bool`（パッケージ `terminal`）。ag が `terminal.ResumableAgent` で `ResumeArtifact` が非空パスを返し、そのファイルが存在すれば true。判定材料が無い（interface 非対応 / artifact 空 / nil agent）なら true。

- [ ] **Step 1: 失敗するテストを書く**

`kernel/terminal/resume_test.go`:

```go
package terminal_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ktat/agentarium/kernel/terminal"
)

type fakeResumableAgent struct{ artifact string }

func (fakeResumableAgent) Name() string                                      { return "fake" }
func (fakeResumableAgent) Invocation(terminal.RunRequest) (string, []string) { return "fake", nil }
func (f fakeResumableAgent) ResumeArtifact(workDir, sessionID string) string { return f.artifact }

type fakePlainAgent struct{}

func (fakePlainAgent) Name() string                                      { return "plain" }
func (fakePlainAgent) Invocation(terminal.RunRequest) (string, []string) { return "plain", nil }

func TestCanResume(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "s1.jsonl")
	if err := os.WriteFile(existing, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "nope.jsonl")

	if !terminal.CanResume(fakeResumableAgent{artifact: existing}, dir, "s1") {
		t.Fatal("existing artifact should be resumable")
	}
	if terminal.CanResume(fakeResumableAgent{artifact: missing}, dir, "nope") {
		t.Fatal("missing artifact should NOT be resumable")
	}
	if !terminal.CanResume(fakePlainAgent{}, dir, "s1") {
		t.Fatal("non-ResumableAgent should default to true")
	}
	if !terminal.CanResume(fakeResumableAgent{artifact: ""}, dir, "s1") {
		t.Fatal("empty artifact should default to true")
	}
	if !terminal.CanResume(nil, dir, "s1") {
		t.Fatal("nil agent should default to true")
	}
}
```

- [ ] **Step 2: テストが失敗することを確認**

Run: `cd /home/ktat/git/github/agentarium && go test ./kernel/terminal/ -run TestCanResume`
Expected: FAIL（`terminal.CanResume` undefined でコンパイルエラー）

- [ ] **Step 3: 実体を実装**

`kernel/terminal/resume.go`:

```go
package terminal

import "os"

// CanResume は ag が ResumableAgent なら artifact の存在で resume 可否を判定する。
// 判定材料が無い（interface 非対応 / artifact 空 / nil agent）なら true（楽観: 無効 resume は
// 起動時に自然失敗する）。ServiceConfig.CanResume フィールドとして渡す想定。
func CanResume(ag Agent, workDir, sessionID string) bool {
	ra, ok := ag.(ResumableAgent)
	if !ok {
		return true
	}
	art := ra.ResumeArtifact(workDir, sessionID)
	if art == "" {
		return true
	}
	_, err := os.Stat(art)
	return err == nil
}
```

- [ ] **Step 4: `plugins/sessions/resume.go` を委譲に変更**

`plugins/sessions/resume.go` を全置換:

```go
package sessions

import "github.com/ktat/agentarium/kernel/terminal"

// CanResume は terminal.CanResume への後方互換ラッパ。実体は kernel/terminal に移設済み
// （kernel → plugin の層違反を避けつつ standard パッケージからも使えるようにするため）。
// terminal.ServiceConfig.CanResume フィールドとして渡す想定。
func CanResume(ag terminal.Agent, workDir, sessionID string) bool {
	return terminal.CanResume(ag, workDir, sessionID)
}
```

- [ ] **Step 5: テストが通ることを確認（新旧両方）**

Run: `cd /home/ktat/git/github/agentarium && go test ./kernel/terminal/ ./plugins/sessions/`
Expected: PASS（新 `TestCanResume` と既存 `plugins/sessions` の `TestCanResume` の両方）

- [ ] **Step 6: commit**

```bash
cd /home/ktat/git/github/agentarium
git add kernel/terminal/resume.go kernel/terminal/resume_test.go plugins/sessions/resume.go
git commit -m "$(cat <<'EOF'
refactor(terminal): CanResume を kernel/terminal へ移設し sessions は委譲

standard パッケージから層違反なく使えるよう純粋関数を kernel 側へ寄せる。
plugins/sessions.CanResume は後方互換ラッパとして残す。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: `kernel/terminal/standard.NewService` を追加

**作業リポ:** agentarium（`/home/ktat/git/github/agentarium`）

**Files:**
- Create: `kernel/terminal/standard/standard.go`
- Create: `kernel/terminal/standard/standard_test.go`

**Interfaces:**
- Consumes: `terminal.CanResume`（Task 1）、`terminal.NewService`/`ServiceConfig`、`terminal.EnvActiveBackend`、`terminal.NewStore`、`settings.TerminalRenderer`、`xterm.NewRegistry`/`xterm.NewRegistryWithStore`/`xterm.Backend`、`wrap.NewRegistry`/`wrap.NewRegistryWithStore`/`wrap.NewStore`/`wrap.Backend`。
- Produces:
  - `type Config struct { WorkDir string; Agents *terminal.AgentRegistry; Secrets *secrets.Store; StoreDir string }`
  - `func NewService(cfg Config) (*terminal.Service, error)`

補足: `settings.TerminalRenderer(nil)` は `""` を返す（nil セーフ）。`terminal.EnvActiveBackend()` は env 未設定時に `"xterm"` を返すため、active は常に具体名に解決される。

- [ ] **Step 1: 失敗するテストを書く**

`kernel/terminal/standard/standard_test.go`:

```go
package standard_test

import (
	"path/filepath"
	"testing"

	"github.com/ktat/agentarium/kernel/secrets"
	"github.com/ktat/agentarium/kernel/settings"
	"github.com/ktat/agentarium/kernel/terminal"
	"github.com/ktat/agentarium/kernel/terminal/standard"
)

func newSecrets(t *testing.T) *secrets.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := secrets.NewStore(filepath.Join(dir, "data.json"), filepath.Join(dir, "key"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

func newAgents() *terminal.AgentRegistry { return terminal.NewAgentRegistry("claude") }

func TestNewService_ActiveFromSettings(t *testing.T) {
	sec := newSecrets(t)
	if err := sec.Set(settings.KeyTerminalRenderer, "wrap"); err != nil {
		t.Fatal(err)
	}
	svc, err := standard.NewService(standard.Config{
		WorkDir: t.TempDir(), Agents: newAgents(), Secrets: sec, StoreDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if got := svc.Active().Name(); got != "wrap" {
		t.Fatalf("active = %q, want wrap", got)
	}
}

func TestNewService_DefaultsToXterm(t *testing.T) {
	t.Setenv("AGENTARIUM_TERMINAL_RENDERER", "")
	svc, err := standard.NewService(standard.Config{
		WorkDir: t.TempDir(), Agents: newAgents(), Secrets: newSecrets(t), StoreDir: "",
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if got := svc.Active().Name(); got != "xterm" {
		t.Fatalf("active = %q, want xterm", got)
	}
}

func TestNewService_EnvFallback(t *testing.T) {
	t.Setenv("AGENTARIUM_TERMINAL_RENDERER", "wrap")
	svc, err := standard.NewService(standard.Config{
		WorkDir: t.TempDir(), Agents: newAgents(), Secrets: nil, StoreDir: "",
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if got := svc.Active().Name(); got != "wrap" {
		t.Fatalf("active = %q, want wrap (env fallback)", got)
	}
}

func TestNewService_Validation(t *testing.T) {
	if _, err := standard.NewService(standard.Config{WorkDir: "wd", Agents: nil}); err == nil {
		t.Fatal("nil Agents should error")
	}
	if _, err := standard.NewService(standard.Config{WorkDir: "", Agents: newAgents()}); err == nil {
		t.Fatal("empty WorkDir should error")
	}
}
```

- [ ] **Step 2: テストが失敗することを確認**

Run: `cd /home/ktat/git/github/agentarium && go test ./kernel/terminal/standard/`
Expected: FAIL（`standard` パッケージが存在せずコンパイルエラー）

- [ ] **Step 3: `standard.NewService` を実装**

`kernel/terminal/standard/standard.go`:

```go
// Package standard は agentarium の標準ターミナル配線を再利用ヘルパとして提供する。
// xterm/wrap 両 backend を登録し、Settings(kernel.terminal_renderer) → env → 既定 xterm の
// 順で active を選び、renderer 別 Store でセッション復元を有効化した *terminal.Service を返す。
// active は起動時に確定する（ランタイム切替は非対応。Settings 変更は再起動で反映）。
package standard

import (
	"errors"
	"path/filepath"

	"github.com/ktat/agentarium/kernel/secrets"
	"github.com/ktat/agentarium/kernel/settings"
	"github.com/ktat/agentarium/kernel/terminal"
	"github.com/ktat/agentarium/kernel/terminal/wrap"
	"github.com/ktat/agentarium/kernel/terminal/xterm"
)

// Config は標準ターミナル配線の入力。
type Config struct {
	WorkDir  string                  // 端末 cwd（必須）
	Agents   *terminal.AgentRegistry // 登録済みレジストリ（必須）
	Secrets  *secrets.Store          // active 解決用。nil 可（nil なら env → 既定 xterm）
	StoreDir string                  // renderer 別 Store の置き場。空なら Store 無し（復元なし）
}

// NewService は xterm/wrap 両 backend を登録し、Settings → env → 既定 xterm の順で
// active を決め、CanResume を配線した *terminal.Service を返す。
func NewService(cfg Config) (*terminal.Service, error) {
	if cfg.Agents == nil {
		return nil, errors.New("standard: Agents is required")
	}
	if cfg.WorkDir == "" {
		return nil, errors.New("standard: WorkDir is required")
	}

	var xtermBackend *xterm.Backend
	var wrapBackend *wrap.Backend
	if cfg.StoreDir != "" {
		xtermBackend = &xterm.Backend{Registry: xterm.NewRegistryWithStore(
			cfg.WorkDir, cfg.Agents, terminal.NewStore(filepath.Join(cfg.StoreDir, "terminal-xterm.json")))}
		wrapBackend = &wrap.Backend{Registry: wrap.NewRegistryWithStore(
			cfg.WorkDir, cfg.Agents, wrap.NewStore(filepath.Join(cfg.StoreDir, "terminal-wrap.json")))}
	} else {
		xtermBackend = &xterm.Backend{Registry: xterm.NewRegistry(cfg.WorkDir, cfg.Agents)}
		wrapBackend = &wrap.Backend{Registry: wrap.NewRegistry(cfg.WorkDir, cfg.Agents)}
	}

	// active: Settings(nil セーフ) → env(未設定なら "xterm") の順。常に具体名に解決される。
	active := settings.TerminalRenderer(cfg.Secrets)
	if active == "" {
		active = terminal.EnvActiveBackend()
	}

	canResume := func(rec terminal.SessionRecord) bool {
		return terminal.CanResume(cfg.Agents.Resolve(rec.Agent), rec.WorkDir, rec.SessionID)
	}

	return terminal.NewService(terminal.ServiceConfig{
		Agents:    cfg.Agents,
		Backends:  []terminal.Backend{xtermBackend, wrapBackend},
		Active:    active,
		CanResume: canResume,
	})
}
```

- [ ] **Step 4: テストが通ることを確認**

Run: `cd /home/ktat/git/github/agentarium && go test ./kernel/terminal/standard/`
Expected: PASS（4 テストすべて）

- [ ] **Step 5: commit**

```bash
cd /home/ktat/git/github/agentarium
git add kernel/terminal/standard/
git commit -m "$(cat <<'EOF'
feat(terminal): 標準ターミナル配線ビルダ standard.NewService を追加

xterm/wrap 両 backend + settings 駆動 active + renderer 別 Store + CanResume を
1 コンストラクタに集約。消費者は app.WithTerminal に渡すだけで済む。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: board-assistant `main.go` を `standard.NewService` 経由に

**作業リポ:** board-assistant（`/home/ktat/git/github/board-assistant`）

**Files:**
- Modify: `main.go`（board-assistant, 現 16 行目の xterm import と 64-73 行目の端末配線）

**Interfaces:**
- Consumes: `standard.NewService`/`standard.Config`（Task 2）。

- [ ] **Step 1: import を差し替え**

`main.go` の import ブロックで、次の行を削除:

```go
	"github.com/ktat/agentarium/kernel/terminal/xterm"
```

次の行を追加（import ブロック内、アルファベット順で `kernel/terminal` の直後）:

```go
	"github.com/ktat/agentarium/kernel/terminal/standard"
```

- [ ] **Step 2: 端末配線ブロックを置換**

`main.go` の次のブロック（コメント「最小 xterm ターミナル…」から `app.WithTerminal(svc)` まで）:

```go
	// 最小 xterm ターミナル（シェル init 健全化 + 後続の評価実行用）。
	agents := terminal.NewAgentRegistry("claude")
	agents.Register(claude.New())
	svc, err := terminal.NewService(terminal.ServiceConfig{
		Agents:   agents,
		Backends: []terminal.Backend{&xterm.Backend{Registry: xterm.NewRegistry(wd, agents)}},
	})
	if err != nil {
		log.Fatalf("terminal service: %v", err)
	}
	app.WithTerminal(svc)
```

を次に置換:

```go
	// xterm / wrap 両 backend を登録し、Settings(kernel.terminal_renderer)→env→既定 xterm の
	// 順で active を選ぶ。renderer 別 Store（<UserConfigDir>/board-assistant/terminal-<r>.json）で
	// 再起動越えのセッション復元も有効化する。active の変更は再起動で反映。
	agents := terminal.NewAgentRegistry("claude")
	agents.Register(claude.New())
	svc, err := standard.NewService(standard.Config{
		WorkDir:  wd,
		Agents:   agents,
		Secrets:  sec,
		StoreDir: base,
	})
	if err != nil {
		log.Fatalf("terminal service: %v", err)
	}
	app.WithTerminal(svc)
```

- [ ] **Step 3: ビルドと vet で検証**

Run: `cd /home/ktat/git/github/board-assistant && go build ./... && go vet ./...`
Expected: エラーなし（xterm import 未使用エラーが出ないこと＝差し替え成功）

- [ ] **Step 4: 動作検証（手動）**

Run: `cd /home/ktat/git/github/board-assistant && go run . &`（別ターミナルで）
手順: ブラウザで Settings → Kernel → `terminal_renderer` を `wrap` に保存 → プロセス再起動 → 端末タブを開き、`<UserConfigDir>/board-assistant/terminal-wrap.json` が生成され wrap レンダラ（サーバ側 VT）で描画されることを確認。確認後プロセス停止。

- [ ] **Step 5: commit**

```bash
cd /home/ktat/git/github/board-assistant
git add main.go
git commit -m "$(cat <<'EOF'
fix(terminal): Settings の terminal_renderer を反映するよう standard.NewService へ

従来は xterm 単独登録で settings を参照しておらず wrap を選んでも xterm 固定だった。
両 backend 登録 + settings 駆動 active + renderer 別 Store に切替。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: 参照デモ `cmd/agentarium/main.go` を `standard.NewService` 経由に

**作業リポ:** agentarium（`/home/ktat/git/github/agentarium`）

**Files:**
- Modify: `cmd/agentarium/main.go`（import、`terminalStorePath` ヘルパ削除、runServer の端末配線ブロック）

**Interfaces:**
- Consumes: `standard.NewService`/`standard.Config`（Task 2）。

- [ ] **Step 1: import を差し替え**

`cmd/agentarium/main.go` の import ブロックで次の 3 行を削除:

```go
	"github.com/ktat/agentarium/kernel/settings"
	"github.com/ktat/agentarium/kernel/terminal/wrap"
	"github.com/ktat/agentarium/kernel/terminal/xterm"
```

次の 1 行を追加（`kernel/terminal` の直後）:

```go
	"github.com/ktat/agentarium/kernel/terminal/standard"
```

（`kernel/terminal` と `plugins/sessions` は引き続き使用するため残す。`sessions` は `sessions.New(wd)` のタブ登録で使用。）

- [ ] **Step 2: `terminalStorePath` ヘルパを削除**

次の関数定義（コメント 2 行含む）を丸ごと削除:

```go
// terminalStorePath は renderer 別のセッション永続化ファイルパスを返す
// （UserConfigDir/agentarium/terminal-<renderer>.json）。
func terminalStorePath(renderer string) (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "agentarium", "terminal-"+renderer+".json"), nil
}
```

- [ ] **Step 3: runServer の端末配線ブロックを置換**

`runServer` 内の次のブロック（`wrapStorePath, err := terminalStorePath("wrap")` から `svc, err := terminal.NewService(...)` の `}` まで、下記コメント群含む）:

```go
	wrapStorePath, err := terminalStorePath("wrap")
	if err != nil {
		return err
	}
	xtermStorePath, err := terminalStorePath("xterm")
	if err != nil {
		return err
	}
	// xterm / wrap 両 backend をコンパイルに含めて登録し、実行時に active を選ぶ。
	// Store 付きにすることで再起動越えのセッション復元（lazy restore）が有効になる。
	xtermBackend := &xterm.Backend{Registry: xterm.NewRegistryWithStore(wd, agents, terminal.NewStore(xtermStorePath))}
	wrapBackend := &wrap.Backend{Registry: wrap.NewRegistryWithStore(wd, agents, wrap.NewStore(wrapStorePath))}
	// active backend は Settings（kernel.terminal_renderer）→ env → 既定 xterm の順で決定。
	active := settings.TerminalRenderer(sec)
	if active == "" {
		active = terminal.EnvActiveBackend()
	}
	// canResume: 永続レコードの Agent を解決し、その Agent が表明する artifact の存在で
	// resume 可否を判定する（claude なら jsonl 存在チェック）。
	// 永続レコードは常に具体的な Agent 名を持つ前提。agents.Resolve が nil を返す
	// （空名 / 未登録）場合は CanResume が楽観的に true を返す（resolveAgent の Default
	// fallback とは非対称だが、未知 agent は復元側で既に skip 済みのため実害なし）。
	canResume := func(rec terminal.SessionRecord) bool {
		return sessions.CanResume(agents.Resolve(rec.Agent), rec.WorkDir, rec.SessionID)
	}
	svc, err := terminal.NewService(terminal.ServiceConfig{
		Agents:    agents,
		Backends:  []terminal.Backend{xtermBackend, wrapBackend},
		Active:    active,
		CanResume: canResume,
	})
	if err != nil {
		return err
	}
```

を次に置換:

```go
	// 標準ターミナル配線（両 backend + settings 駆動 active + renderer 別 Store + CanResume）。
	// Store は <UserConfigDir>/agentarium/terminal-<renderer>.json。active 変更は再起動で反映。
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	svc, err := standard.NewService(standard.Config{
		WorkDir:  wd,
		Agents:   agents,
		Secrets:  sec,
		StoreDir: filepath.Join(cfgDir, "agentarium"),
	})
	if err != nil {
		return err
	}
```

- [ ] **Step 4: ビルド・vet・全テストで検証**

Run: `cd /home/ktat/git/github/agentarium && go build ./... && go vet ./... && go test ./...`
Expected: エラー・失敗なし（未使用 import が残っていないこと）

- [ ] **Step 5: commit**

```bash
cd /home/ktat/git/github/agentarium
git add cmd/agentarium/main.go
git commit -m "$(cat <<'EOF'
refactor(cmd): 参照デモの端末配線を standard.NewService に統一

demo-local の terminalStorePath 重複を解消し、標準ヘルパをドッグフーディング。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: README 追従（agentarium / board-assistant）

**作業リポ:** agentarium と board-assistant の両方（別リポなので commit も別々）

**Files:**
- Modify: `README.md`（agentarium, 63 行目付近と store パス記述 324 行目付近）
- Modify: `README.md`（board-assistant, データ保存先 133-136 行目付近）

- [ ] **Step 1: agentarium README — 標準配線の案内を追記**

`README.md`（agentarium）の 324 行目付近、`- 保存先（参照デモ）: ...terminal-<renderer>.json（renderer 別）` の行を次に更新:

```
- 保存先: `<StoreDir>/terminal-<renderer>.json`（renderer 別）。標準ヘルパ `kernel/terminal/standard.NewService` に `StoreDir` を渡すと有効になる（参照デモ・board-assistant いずれも `<os.UserConfigDir>/<app>/`）
```

- [ ] **Step 2: agentarium README — ターミナル配線の推奨手段を追記**

`README.md`（agentarium）の 297-298 行目付近（`- xterm: ...` / `- wrap: ...` の直前）に次の一文を追加:

```
標準的な配線（両 backend 登録 + Settings 駆動の active 選択 + renderer 別 Store + CanResume）は `kernel/terminal/standard.NewService(cfg)` にまとまっている。消費者はその返り値を `app.WithTerminal(svc)` に渡すだけでよい（active の変更は再起動で反映）。
```

- [ ] **Step 3: board-assistant README — データ保存先に terminal store を追記**

`README.md`（board-assistant）の 135 行目 `- chat.json: ...` の直後に次の 2 行を追加:

```
- `terminal-xterm.json` / `terminal-wrap.json`: 端末セッションの再開用レコード（renderer 別）
```

- [ ] **Step 4: 記述の妥当性を目視確認**

Run: `cd /home/ktat/git/github/agentarium && grep -n "standard.NewService\|terminal-<renderer>" README.md`
Run: `cd /home/ktat/git/github/board-assistant && grep -n "terminal-xterm.json" README.md`
Expected: 追記した行が表示される

- [ ] **Step 5: commit（両リポ）**

```bash
cd /home/ktat/git/github/agentarium
git add README.md
git commit -m "$(cat <<'EOF'
docs: 標準ターミナル配線 standard.NewService と store パスを追記

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"

cd /home/ktat/git/github/board-assistant
git add README.md
git commit -m "$(cat <<'EOF'
docs: データ保存先に terminal セッション store を追記

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Self-Review

- **Spec coverage:** §1 API → Task 2 / §2 内部 → Task 2 / §3 CanResume 移設 → Task 1 / §4 board-assistant → Task 3・demo → Task 4・README → Task 5 / §5 テスト → Task 1 Step1・Task 2 Step1。全項目にタスクあり。
- **Placeholder scan:** 各コード step に完全なコードを記載。TBD/TODO 無し。
- **Type consistency:** `standard.Config`/`NewService` の署名は Task 2 定義と Task 3・4 の呼び出しで一致。`terminal.CanResume(ag Agent, workDir, sessionID string) bool` は Task 1 定義と Task 2 内部利用で一致。`settings.KeyTerminalRenderer`・`terminal.EnvActiveBackend`・`svc.Active().Name()` は実在 API を確認済み。
