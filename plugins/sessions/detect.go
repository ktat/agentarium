package sessions

// SessionIDs は workDir に対応する claude セッション識別子（jsonl の UUID）を
// 新しい順（mtime 降順）で返す。ディレクトリが無ければ空。
//
// kernel/terminal.SessionDetector を満たす claude Agent 実装が
// ListSessionIDs から呼ぶ想定（出力非依存のセッション検出に使う）。
func SessionIDs(workDir string) []string {
	dir, err := SessionsDirFor(workDir)
	if err != nil {
		return nil
	}
	list, err := ListSessions(dir)
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(list))
	for _, s := range list {
		ids = append(ids, s.UUID)
	}
	return ids
}
