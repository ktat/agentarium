package terminal

import "time"

// SessionWatchInterval / SessionWatchTimeout は WatchNewSession のポーリング間隔と
// 諦めるまでの上限。var にしているのはテストで短縮するため（本番は既定値）。
var (
	SessionWatchInterval = 1500 * time.Millisecond
	SessionWatchTimeout  = 45 * time.Second
)

// WatchNewSession は ag が SessionDetector のとき、起動直後に出現した新規セッション
// 識別子を検出して tryAssign に渡す（出力非依存）。
//
// 仕組み: 起動前の識別子集合を控え、以後ポーリングして新規識別子を tryAssign に渡す。
// tryAssign は claim と assign を単一ロック下でアトミックに行い、成功なら true を返す。
// 検出成功・stop クローズ・SessionWatchTimeout 経過のいずれかで終了する。
//
// ベースラインを自動取得するため、呼び出し前に Process.Start() が実行されていると
// 起動直後に作られたセッション識別子を取りこぼす可能性がある。
// 起動前にベースラインを確保する必要がある場合は WatchNewSessionFromBaseline を使うこと。
func WatchNewSession(ag Agent, workDir string, tryAssign func(sessionID string) bool, stop <-chan struct{}) {
	det, ok := ag.(SessionDetector)
	if !ok {
		return
	}
	before := map[string]bool{}
	for _, s := range det.ListSessionIDs(workDir) {
		before[s] = true
	}
	WatchNewSessionFromBaseline(det, workDir, before, tryAssign, stop)
}

// WatchNewSessionFromBaseline は呼び出し元が事前に取得した before ベースラインを使って
// 新規セッション識別子を検出し tryAssign に渡す。
// Process.Start() 前にベースラインを取得することで、起動直後のセッション識別子も検出できる。
func WatchNewSessionFromBaseline(det SessionDetector, workDir string, before map[string]bool, tryAssign func(sessionID string) bool, stop <-chan struct{}) {
	ticker := time.NewTicker(SessionWatchInterval)
	defer ticker.Stop()
	deadline := time.After(SessionWatchTimeout)
	for {
		select {
		case <-stop:
			return
		case <-deadline:
			return
		case <-ticker.C:
			for _, sid := range det.ListSessionIDs(workDir) { // 新しい順
				if sid == "" || before[sid] {
					continue
				}
				if tryAssign == nil {
					return
				}
				if tryAssign(sid) {
					return
				}
			}
		}
	}
}
