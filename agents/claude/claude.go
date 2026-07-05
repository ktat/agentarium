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

// 任意 IF（ResumableAgent 等）の実装をコンパイル時に保証する。ここに置くことで
// consumer の go build 時にも署名ドリフトを検出できる（本番コードでの静的アサート）。
var (
	_ terminal.Agent           = Agent{}
	_ terminal.ResumableAgent  = Agent{}
	_ terminal.SessionDetector = Agent{}
	_ terminal.StateAware      = Agent{}
)

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
