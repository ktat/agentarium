package observer

import (
	"regexp"
	"sync"
)

// Direction は観測対象の方向。
type Direction int

const (
	DirectionInput Direction = iota
	DirectionOutput
)

// MatchInfo はハンドラに渡される情報。
type MatchInfo struct {
	TerminalID string
	Direction  Direction
	Line       string // 行末文字を含まない確定行
}

// Handler はパターンがマッチしたとき同期で呼ばれる。Observer のロックは保持しない。
type Handler func(MatchInfo)

type rule struct {
	pattern *regexp.Regexp
	handler Handler
}

// Observer は (direction, pattern) → handler の登録 registry。確定行ごとに照合する。
// nil レシーバでも安全（no-op）。OnInput/OnOutput/Forget で terminal.ObserverHooks を満たす。
type Observer struct {
	mu        sync.RWMutex
	rules     map[Direction][]rule
	buffers   map[bufferKey]*lineBuffer
	buffersMu sync.Mutex
}

type bufferKey struct {
	terminalID string
	direction  Direction
}

func New() *Observer {
	return &Observer{
		rules:   map[Direction][]rule{},
		buffers: map[bufferKey]*lineBuffer{},
	}
}

// Register はパターンとハンドラの組を登録する。同じ direction に複数登録すると順次呼ばれる。
func (o *Observer) Register(dir Direction, pattern *regexp.Regexp, handler Handler) {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.rules[dir] = append(o.rules[dir], rule{pattern: pattern, handler: handler})
	o.mu.Unlock()
}

func (o *Observer) OnInput(terminalID string, data []byte) {
	if o == nil {
		return
	}
	o.feed(terminalID, DirectionInput, data)
}

func (o *Observer) OnOutput(terminalID string, data []byte) {
	if o == nil {
		return
	}
	o.feed(terminalID, DirectionOutput, data)
}

func (o *Observer) feed(terminalID string, dir Direction, data []byte) {
	key := bufferKey{terminalID: terminalID, direction: dir}
	o.buffersMu.Lock()
	buf, ok := o.buffers[key]
	if !ok {
		buf = newLineBuffer(func(line string) { o.dispatch(terminalID, dir, line) })
		o.buffers[key] = buf
	}
	o.buffersMu.Unlock()
	buf.Feed(data)
}

func (o *Observer) dispatch(terminalID string, dir Direction, line string) {
	o.mu.RLock()
	rules := o.rules[dir]
	o.mu.RUnlock()
	for _, r := range rules {
		if r.pattern.MatchString(line) {
			r.handler(MatchInfo{TerminalID: terminalID, Direction: dir, Line: line})
		}
	}
}

// Forget は terminal ID に紐づくバッファを破棄する。PTY 終了時に呼ぶ。
func (o *Observer) Forget(terminalID string) {
	if o == nil {
		return
	}
	o.buffersMu.Lock()
	delete(o.buffers, bufferKey{terminalID: terminalID, direction: DirectionInput})
	delete(o.buffers, bufferKey{terminalID: terminalID, direction: DirectionOutput})
	o.buffersMu.Unlock()
}
