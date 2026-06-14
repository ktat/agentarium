package terminal

import (
	"regexp"
	"time"
)

// Agent はバイナリと引数の組み立てを各エージェントが知る抽象。
// claude/codex/任意エージェント固有の引数組み立ては実装の内側に閉じ、
// カーネル境界を agent-agnostic に保つ。具体的な ClaudeAgent 等は kernel 内に
// 持ち込まず、消費者リポまたは参照デモ（cmd/agentarium）側で登録する。
type Agent interface {
	Name() string
	// Invocation は RunRequest から起動バイナリと引数を組み立てて返す。
	Invocation(req RunRequest) (binary string, args []string)
}

// ResumableAgent は Agent の任意拡張。自分のセッション履歴の所在を表明する。
// claude なら ~/.claude/projects/<encoded(workDir)>/<sessionID>.jsonl。
// 満たさない Agent は復元可否を判定できない（呼び出し側は楽観的に復元を試みる）。
type ResumableAgent interface {
	Agent
	// ResumeArtifact は「存在すれば resume 可能」なファイルパスを返す（空なら判定対象なし）。
	ResumeArtifact(workDir, sessionID string) string
}

// StatePatterns は Agent ごとの PTY 出力状態検出パラメータ。
// パターンは terminal 固有でなく agent 固有（各 terminal は自分の agent のものを使う）。
type StatePatterns struct {
	Permission       *regexp.Regexp // マッチ行で awaiting_user（nil なら permission 検出なし）
	SustainedRunning time.Duration  // 出力継続がこの時間を超えたら running
	IdleTimeout      time.Duration  // 沈黙がこの時間を超えたら idle 降格
	BurstGap         time.Duration  // この無出力後の出力を新 burst 開始とみなす
}

// StateAware は Agent の任意拡張。PTY 出力から状態を検出するためのパターンを表明する。
// 実装しない Agent は状態検出の対象外（その terminal は idle 固定）。
type StateAware interface {
	StatePatterns() StatePatterns
}

// ConfigAgent は設定駆動の汎用 Agent。コードを書かずに済む任意エージェント用。
// ModelFlag が空なら RunRequest.Model は無視する。Resume/SessionName は扱わない
// （扱いたいエージェントは専用の Agent 実装を書く）。
type ConfigAgent struct {
	AgentName string
	Binary    string
	BaseArgs  []string
	ModelFlag string // 例: "--model"。空なら Model 無視
}

func (a ConfigAgent) Name() string { return a.AgentName }

func (a ConfigAgent) Invocation(req RunRequest) (string, []string) {
	args := append([]string(nil), a.BaseArgs...)
	if a.ModelFlag != "" && req.Model != "" {
		args = append(args, a.ModelFlag, req.Model)
	}
	return a.Binary, args
}

// AgentRegistry は name→Agent を保持し既定エージェントを解決する。
// 実行中の動的追加はしない（起動時に main から Register する前提・非スレッドセーフ）。
// 既定エージェント名は消費者の責務で決める（kernel に既定値は持たない）。
type AgentRegistry struct {
	agents      map[string]Agent
	defaultName string
}

// NewAgentRegistry は既定エージェント名を指定して空のレジストリを返す。
func NewAgentRegistry(defaultName string) *AgentRegistry {
	return &AgentRegistry{agents: map[string]Agent{}, defaultName: defaultName}
}

// Register は Agent を登録する（同名は上書き）。
func (r *AgentRegistry) Register(a Agent) { r.agents[a.Name()] = a }

// Resolve は name の Agent を返す（なければ nil）。
func (r *AgentRegistry) Resolve(name string) Agent { return r.agents[name] }

// Default は既定エージェントを返す（未登録なら nil）。
func (r *AgentRegistry) Default() Agent { return r.agents[r.defaultName] }
