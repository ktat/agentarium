package terminal

import "time"

// SessionWatchInterval / SessionWatchTimeout は WatchNewSession のポーリング間隔と
// 諦めるまでの上限。var にしているのはテストで短縮するため（本番は既定値）。
var (
	SessionWatchInterval = 1500 * time.Millisecond
	SessionWatchTimeout  = 45 * time.Second
)

// WatchNewSession は ag が SessionDetector のとき、起動直後に出現した新規セッション
// 識別子を検出して assign に渡す（出力非依存）。
//
// 仕組み: 起動前の識別子集合を控え、以後ポーリングして「起動前に無く、かつ未割当
// （claimed が false）」な識別子のうち最も新しいものを 1 つ確定する。検出成功・
// stop クローズ・SessionWatchTimeout 経過のいずれかで終了する。
//
//   - claimed: 既に別 terminal に割当済みの識別子を弾く述語（nil 可）。同一 workDir で
//     並行起動したときに取り違えないためのガード。
//   - assign:  確定した識別子で 1 度だけ呼ばれる（通常 registry.SetSessionID へ橋渡し）。
func WatchNewSession(ag Agent, workDir string, claimed func(string) bool, assign func(sessionID string), stop <-chan struct{}) {
	det, ok := ag.(SessionDetector)
	if !ok {
		return
	}
	before := map[string]bool{}
	for _, s := range det.ListSessionIDs(workDir) {
		before[s] = true
	}
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
				if claimed != nil && claimed(sid) {
					continue
				}
				assign(sid)
				return
			}
		}
	}
}
