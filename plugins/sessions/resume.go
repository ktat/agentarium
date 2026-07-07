package sessions

import "github.com/ktat/agentarium/kernel/terminal"

// CanResume は terminal.CanResume への後方互換ラッパ。実体は kernel/terminal に移設済み
// （kernel → plugin の層違反を避けつつ standard パッケージからも使えるようにするため）。
// terminal.ServiceConfig.CanResume フィールドとして渡す想定。
func CanResume(ag terminal.Agent, workDir, sessionID string) bool {
	return terminal.CanResume(ag, workDir, sessionID)
}
