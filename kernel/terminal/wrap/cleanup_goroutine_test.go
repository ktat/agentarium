package wrap

import (
	"runtime"
	"testing"
	"time"
)

// waitForGoroutines は cond が true になるまで最大 timeout までポーリングする。
// 固定 sleep だと環境差で起動待ち/drain 待ちが不足・過剰になりフレークするため、
// 条件ベースで待つ。
func waitForGoroutines(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// TestProcess_StopReleasesGoroutines は Stop 後に Process が起動した goroutine
// (readPump / responseLoop / flushLoop) が全て終了することを確認する。
//
// 回帰防止の対象: cleanup が emu.Close を呼ばないと responseLoop が emu.Read で
// 永久ブロックし、その goroutine が emulator (VirtualRows×2 画面の grid) を握り
// 続けて GC されない。閉じたセッションぶんメモリが漏れる。
//
// 判定は runtime.NumGoroutine() の「増分が base へ戻るか」で行う。これはプロセス
// 全体の値だが、(1) base を Start 前に取得し増分で見る、(2) パッケージ内テストは
// 逐次実行 (t.Parallel 未使用)、(3) 起動待ち・drain 待ちとも固定 sleep ではなく
// poll-retry にする、ことで他テストやランタイム内部の一時変動による false fail を
// 抑えている。
func TestProcess_StopReleasesGoroutines(t *testing.T) {
	base := runtime.NumGoroutine()

	p := NewProcess("", "sleep", "60")
	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// 固定 sleep ではなく、Process の goroutine が実際に立ち上がって goroutine 数が
	// base を超えるまでポーリングで待つ。
	if !waitForGoroutines(2*time.Second, func() bool { return runtime.NumGoroutine() > base }) {
		t.Fatalf("Process goroutines did not start: base=%d now=%d", base, runtime.NumGoroutine())
	}

	if err := p.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Stop で増えた分が base 以下へ戻ることを確認する。flushLoop は購読者ゼロだと
	// 最大 ~1s で抜けるため余裕をもってポーリングする。
	if !waitForGoroutines(3*time.Second, func() bool { return runtime.NumGoroutine() <= base }) {
		t.Fatalf("goroutines leaked after Stop: base=%d now=%d (responseLoop が emu.Read でブロックしたまま emulator を握り続けている疑い)",
			base, runtime.NumGoroutine())
	}
}
