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
