package terminal

import (
	"sync"
	"testing"
	"time"
)

// fakeDetector は ListSessionIDs を制御するテスト用 Agent。
type fakeDetector struct {
	mu  sync.Mutex
	ids []string
}

func (f *fakeDetector) Name() string                             { return "fake" }
func (f *fakeDetector) Invocation(RunRequest) (string, []string) { return "fake", nil }
func (f *fakeDetector) set(ids ...string)                        { f.mu.Lock(); f.ids = ids; f.mu.Unlock() }
func (f *fakeDetector) ListSessionIDs(string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.ids...)
}

func withFastWatch(t *testing.T) {
	t.Helper()
	oi, ot := SessionWatchInterval, SessionWatchTimeout
	SessionWatchInterval = 5 * time.Millisecond
	SessionWatchTimeout = 2 * time.Second
	t.Cleanup(func() { SessionWatchInterval, SessionWatchTimeout = oi, ot })
}

func TestWatchNewSession_DetectsNewID(t *testing.T) {
	withFastWatch(t)
	det := &fakeDetector{}
	det.set("old-1") // 起動前から存在するセッション

	var got string
	var mu sync.Mutex
	done := make(chan struct{})
	go func() {
		WatchNewSession(det, ".", func(sid string) bool {
			mu.Lock()
			got = sid
			mu.Unlock()
			return true
		}, make(chan struct{}))
		close(done)
	}()

	// 起動後に新規セッションが出現
	time.Sleep(20 * time.Millisecond)
	det.set("old-1", "new-2")

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watcher did not finish in time")
	}
	mu.Lock()
	defer mu.Unlock()
	if got != "new-2" {
		t.Fatalf("want new-2, got %q", got)
	}
}

func TestWatchNewSession_PicksNewestUnclaimed(t *testing.T) {
	withFastWatch(t)
	det := &fakeDetector{}
	det.set() // 起動前は空

	claimed := map[string]bool{"taken": true}
	var got string
	var mu sync.Mutex
	done := make(chan struct{})
	go func() {
		WatchNewSession(det, ".", func(sid string) bool {
			if claimed[sid] {
				return false
			}
			mu.Lock()
			got = sid
			mu.Unlock()
			return true
		}, make(chan struct{}))
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	// 新しい順で taken, fresh。taken は別 terminal に割当済みなので fresh を選ぶ。
	det.set("taken", "fresh")

	<-done
	mu.Lock()
	defer mu.Unlock()
	if got != "fresh" {
		t.Fatalf("want fresh (taken is claimed), got %q", got)
	}
}

func TestWatchNewSession_StopCancels(t *testing.T) {
	withFastWatch(t)
	det := &fakeDetector{}
	det.set("old")
	stop := make(chan struct{})

	assigned := false
	done := make(chan struct{})
	go func() {
		WatchNewSession(det, ".", func(string) bool { assigned = true; return true }, stop)
		close(done)
	}()
	close(stop)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watcher did not stop on close")
	}
	if assigned {
		t.Fatal("should not assign after stop")
	}
}

func TestWatchNewSession_NonDetectorNoop(t *testing.T) {
	withFastWatch(t)
	// SessionDetector を満たさない Agent は即 return（panic/blocking しない）。
	WatchNewSession(ConfigAgent{AgentName: "x", Binary: "x"}, ".", func(string) bool {
		t.Fatal("should not assign for non-detector agent")
		return false
	}, make(chan struct{}))
}
