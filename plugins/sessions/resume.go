package sessions

import (
	"os"

	"github.com/ktat/agentarium/kernel/terminal"
)

// CanResume は ag が terminal.ResumableAgent なら artifact の存在で resume 可否を判定する。
// 判定材料が無い（interface 非対応 / artifact 空 / nil agent）なら true（楽観: 無効 resume は
// 起動時に自然失敗する）。Registry.RestoreFromStoreLazy の canResume として渡す想定。
//
// 注: scanner.go は kernel/terminal に依存しない方針だが、本ファイルは Agent の所在表明
// (ResumableAgent) と判定ロジックを橋渡しするため terminal を import する。
func CanResume(ag terminal.Agent, workDir, sessionID string) bool {
	ra, ok := ag.(terminal.ResumableAgent)
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
