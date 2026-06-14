package terminal

import (
	"regexp"
	"sync"
	"time"

	"github.com/ktat/agentarium/kernel/terminal/observer"
)

// reAnyOutput はどんな非空行にもマッチする catch-all。行テキストを取得して
// per-agent のパターンを onLine 内で適用する（permission は agent 固有のため
// グローバル登録しない）。
var reAnyOutput = regexp.MustCompile(`.`)

// detector は PTY 出力から SessionState を判定し StateSetter.SetState を駆動する。
// 移植元: backlog-worker internal/sessionoverview/ptyfallback.go。
type detector struct {
	setter      StateSetter
	states      func() map[string]SessionState
	patternsFor func(id string) (StatePatterns, bool)
	now         func() time.Time

	mu            sync.Mutex
	lastOutput    map[string]time.Time
	firstOutputAt map[string]time.Time
	ptyRunning    map[string]bool

	stop chan struct{}
	once sync.Once
}

func newDetector(setter StateSetter, states func() map[string]SessionState,
	patternsFor func(id string) (StatePatterns, bool), now func() time.Time) *detector {
	return &detector{
		setter:        setter,
		states:        states,
		patternsFor:   patternsFor,
		now:           now,
		lastOutput:    map[string]time.Time{},
		firstOutputAt: map[string]time.Time{},
		ptyRunning:    map[string]bool{},
		stop:          make(chan struct{}),
	}
}

// register は Observer に catch-all 行ハンドラを登録する。
func (d *detector) register(o *observer.Observer) {
	o.Register(observer.DirectionOutput, reAnyOutput, func(m observer.MatchInfo) {
		d.onLine(m.TerminalID, m.Line)
	})
}

// onLine は 1 確定行を処理する。burst 更新と permission 判定を行う。
func (d *detector) onLine(id, line string) {
	pat, ok := d.patternsFor(id)
	if !ok {
		return
	}
	now := d.now()
	d.mu.Lock()
	last, seen := d.lastOutput[id]
	if !seen || now.Sub(last) > pat.BurstGap {
		// 初出力、または BurstGap を超える沈黙後の出力 → 新しい burst の開始
		d.firstOutputAt[id] = now
	}
	d.lastOutput[id] = now
	d.mu.Unlock()

	if pat.Permission != nil && pat.Permission.MatchString(line) {
		d.setter.SetState(id, StateAwaitingUser, "pty")
	}
}

// tick は各 terminal の burst/idle を判定し状態遷移させる（ticker から 500ms 周期で）。
func (d *detector) tick() {
	now := d.now()
	cur := d.states()

	type tr struct {
		id string
		to SessionState
	}
	var trs []tr

	d.mu.Lock()
	for id, last := range d.lastOutput {
		pat, ok := d.patternsFor(id)
		if !ok {
			continue
		}
		silent := now.Sub(last)
		switch {
		case silent > pat.IdleTimeout:
			if d.ptyRunning[id] {
				if cur[id] == StateRunning {
					trs = append(trs, tr{id, StateIdle})
				}
				d.ptyRunning[id] = false
				delete(d.firstOutputAt, id)
			}
		default:
			first, ok := d.firstOutputAt[id]
			if ok && now.Sub(first) >= pat.SustainedRunning && !d.ptyRunning[id] {
				trs = append(trs, tr{id, StateRunning})
				d.ptyRunning[id] = true
			}
		}
	}
	d.mu.Unlock()

	for _, t := range trs {
		d.setter.SetState(t.id, t.to, "pty")
	}
}

// start は 500ms 周期の tick goroutine を起動する。stop が閉じたら停止。
func (d *detector) start() {
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-d.stop:
				return
			case <-ticker.C:
				d.tick()
			}
		}
	}()
}

// close は tick goroutine を停止する（冪等）。
func (d *detector) close() {
	d.once.Do(func() { close(d.stop) })
}

// forget は terminal 終了時に内部マップを掃除する。
func (d *detector) forget(id string) {
	d.mu.Lock()
	delete(d.lastOutput, id)
	delete(d.firstOutputAt, id)
	delete(d.ptyRunning, id)
	d.mu.Unlock()
}
