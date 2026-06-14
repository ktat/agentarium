package terminal

import (
	"regexp"
	"sync"
	"testing"
	"time"
)

type fakeSetter struct {
	mu     sync.Mutex
	states map[string]SessionState
	calls  []string
}

func newFakeSetter() *fakeSetter {
	return &fakeSetter{states: map[string]SessionState{}}
}

func (f *fakeSetter) SetState(id string, s SessionState, source string) {
	f.mu.Lock()
	f.states[id] = s
	f.calls = append(f.calls, id+"="+s.String())
	f.mu.Unlock()
}

func (f *fakeSetter) snapshot() map[string]SessionState {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]SessionState, len(f.states))
	for k, v := range f.states {
		out[k] = v
	}
	return out
}

func testPatterns() StatePatterns {
	return StatePatterns{
		Permission:       regexp.MustCompile(`(?i)do you want to proceed`),
		SustainedRunning: 2 * time.Second,
		IdleTimeout:      1500 * time.Millisecond,
		BurstGap:         1 * time.Second,
	}
}

func newTestDetector(f *fakeSetter, now *time.Time) *detector {
	return newDetector(f, f.snapshot,
		func(string) (StatePatterns, bool) { return testPatterns(), true },
		func() time.Time { return *now })
}

func TestDetector_PermissionSetsAwaiting(t *testing.T) {
	f := newFakeSetter()
	now := time.Unix(0, 0)
	d := newTestDetector(f, &now)
	d.onLine("t1", "Do you want to proceed?")
	if f.snapshot()["t1"] != StateAwaitingUser {
		t.Fatalf("want awaiting_user, got %v", f.snapshot()["t1"])
	}
}

func TestDetector_SustainedBurstSetsRunning(t *testing.T) {
	f := newFakeSetter()
	now := time.Unix(100, 0)
	d := newTestDetector(f, &now)
	// 連続出力（各ギャップ 0.8s < BurstGap=1s）を 2s 以上継続させる
	d.onLine("t1", "l1") // first=100
	now = now.Add(800 * time.Millisecond)
	d.onLine("t1", "l2")
	now = now.Add(800 * time.Millisecond)
	d.onLine("t1", "l3")
	now = now.Add(800 * time.Millisecond) // now=102.4, first 保持=100, span=2.4s
	d.onLine("t1", "l4")
	d.tick() // 2.4 >= SustainedRunning(2s) → running
	if f.snapshot()["t1"] != StateRunning {
		t.Fatalf("want running, got %v", f.snapshot()["t1"])
	}
}

func TestDetector_SilenceDemotesRunningToIdle(t *testing.T) {
	f := newFakeSetter()
	now := time.Unix(200, 0)
	d := newTestDetector(f, &now)
	// まず連続 burst で running にする
	d.onLine("t1", "l1") // first=200
	now = now.Add(800 * time.Millisecond)
	d.onLine("t1", "l2")
	now = now.Add(800 * time.Millisecond)
	d.onLine("t1", "l3")
	now = now.Add(800 * time.Millisecond) // now=202.4
	d.onLine("t1", "l4")
	d.tick() // running
	if f.snapshot()["t1"] != StateRunning {
		t.Fatalf("setup: want running first, got %v", f.snapshot()["t1"])
	}
	// 沈黙させて idle 降格
	now = now.Add(1600 * time.Millisecond) // silent 1.6s > IdleTimeout(1.5s)
	d.tick()
	if f.snapshot()["t1"] != StateIdle {
		t.Fatalf("want idle, got %v", f.snapshot()["t1"])
	}
}

func TestDetector_IntermittentOutputDoesNotRun(t *testing.T) {
	f := newFakeSetter()
	now := time.Unix(400, 0)
	d := newTestDetector(f, &now)
	// BurstGap(1s) を超えるギャップの散発出力 → 毎回 burst リセット → 決して running にならない
	for i := 0; i < 5; i++ {
		d.onLine("t1", "tick")
		now = now.Add(1200 * time.Millisecond) // gap 1.2s > BurstGap
		d.tick()
	}
	if f.snapshot()["t1"] == StateRunning {
		t.Fatalf("intermittent output must not be running")
	}
}

func TestDetector_AwaitingNotDemotedBySilence(t *testing.T) {
	f := newFakeSetter()
	now := time.Unix(300, 0)
	d := newTestDetector(f, &now)
	d.onLine("t1", "Do you want to proceed?")
	now = now.Add(5 * time.Second)
	d.tick()
	if f.snapshot()["t1"] != StateAwaitingUser {
		t.Fatalf("awaiting must persist, got %v", f.snapshot()["t1"])
	}
}

func TestDetector_NoPatternsNoOp(t *testing.T) {
	f := newFakeSetter()
	now := time.Unix(0, 0)
	d := newDetector(f, f.snapshot,
		func(string) (StatePatterns, bool) { return StatePatterns{}, false },
		func() time.Time { return now })
	d.onLine("t1", "Do you want to proceed?")
	d.tick()
	if len(f.calls) != 0 {
		t.Fatalf("want no SetState calls, got %v", f.calls)
	}
}
