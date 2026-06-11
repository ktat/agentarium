package terminal

import "sync"

// RingBuffer は固定サイズの循環バッファ。書き込みは常に成功し、容量を超えると
// 古いデータが上書きされる。ブラウザリロード時の過去出力 replay 用に PTY 出力を保持する。
type RingBuffer struct {
	mu   sync.Mutex
	data []byte
	next int
	full bool
}

// NewRingBuffer は size バイトの循環バッファを返す（size<=0 なら 1）。
func NewRingBuffer(size int) *RingBuffer {
	if size <= 0 {
		size = 1
	}
	return &RingBuffer{data: make([]byte, size)}
}

// Write はデータをバッファに書き込む。常に len(p), nil を返す。
func (r *RingBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, b := range p {
		r.data[r.next] = b
		r.next++
		if r.next == len(r.data) {
			r.next = 0
			r.full = true
		}
	}
	return len(p), nil
}

// Bytes はバッファ内容を「古いものから順に」コピーして返す。
func (r *RingBuffer) Bytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.full {
		out := make([]byte, r.next)
		copy(out, r.data[:r.next])
		return out
	}
	size := len(r.data)
	out := make([]byte, size)
	copy(out, r.data[r.next:])
	copy(out[size-r.next:], r.data[:r.next])
	return out
}
