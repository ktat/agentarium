package terminal

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// aggregateStates は SessionInfo 群を状態カウントと最優先状態に集約する。
// 優先度: awaiting_user > running > idle。pending/その他は idle 扱い。空は idle。
func aggregateStates(items []SessionInfo) (map[string]int, string) {
	counts := map[string]int{"idle": 0, "running": 0, "awaiting_user": 0}
	for _, it := range items {
		switch it.State.String() {
		case "running":
			counts["running"]++
		case "awaiting_user":
			counts["awaiting_user"]++
		default:
			counts["idle"]++
		}
	}
	highest := "idle"
	if counts["running"] > 0 {
		highest = "running"
	}
	if counts["awaiting_user"] > 0 {
		highest = "awaiting_user"
	}
	return counts, highest
}

// sessionsPayload は SessionInfo 群を SSE 用の per-session 配列に写す（Pet popover 用）。
func sessionsPayload(items []SessionInfo) []map[string]string {
	out := make([]map[string]string, 0, len(items))
	for _, it := range items {
		out = append(out, map[string]string{
			"id":    it.ID,
			"label": it.Label,
			"state": it.State.String(),
		})
	}
	return out
}

func stateEventBytes(sessions []map[string]string, counts map[string]int, highest string) []byte {
	payload, _ := json.Marshal(map[string]any{
		"sessions": sessions,
		"counts":   counts,
		"highest":  highest,
	})
	return []byte(fmt.Sprintf("event: state\ndata: %s\n\n", payload))
}

type sseHub struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

func newSSEHub() *sseHub { return &sseHub{subs: map[chan []byte]struct{}{}} }

func (h *sseHub) add() chan []byte {
	ch := make(chan []byte, 8)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *sseHub) remove(ch chan []byte) {
	h.mu.Lock()
	if _, ok := h.subs[ch]; ok {
		delete(h.subs, ch)
		close(ch)
	}
	h.mu.Unlock()
}

func (h *sseHub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

func (h *sseHub) broadcast(b []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- b:
		default:
		}
	}
}

// EventSubscriberCount は /terminal/events の現在の購読者数を返す（pet status 用）。
func (s *Service) EventSubscriberCount() int {
	if s.events == nil {
		return 0
	}
	return s.events.count()
}

// onStateChange は active backend の状態遷移ごとに呼ばれる（AddStateListener 登録）。
func (s *Service) onStateChange(id string, prev, next SessionState, source string) {
	s.broadcastStateIfChanged()
}

// broadcastStateIfChanged は現在の全端末状態を再集約し、前回配信から変化が
// あれば全 SSE 購読者へ配信する。ポーリングしない。状態遷移（onStateChange）
// に加え、端末停止（handleStop）でも呼ぶことで、端末集合の減少（タブを閉じた等）
// も購読者へ push する。
func (s *Service) broadcastStateIfChanged() {
	items := s.active.List()
	counts, highest := aggregateStates(items)
	ss := sessionsPayload(items)
	key := highest
	for _, m := range ss {
		key += "|" + m["id"] + "=" + m["state"]
	}
	s.lastAggMu.Lock()
	changed := key != s.lastAgg
	s.lastAgg = key
	s.lastAggMu.Unlock()
	if changed {
		s.events.broadcast(stateEventBytes(ss, counts, highest))
	}
}

// handleEvents は GET /terminal/events（SSE）。接続時に現在状態を即時送り、
// 以後 onStateChange からの変化を流す。15s 毎に keepalive コメント。
func (s *Service) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := s.events.add()
	defer s.events.remove(ch)

	items := s.active.List()
	counts, highest := aggregateStates(items)
	_, _ = w.Write(stateEventBytes(sessionsPayload(items), counts, highest))
	flusher.Flush()

	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case b, ok := <-ch:
			if !ok {
				return
			}
			_, _ = w.Write(b)
			flusher.Flush()
		case <-ping.C:
			_, _ = w.Write([]byte(": ping\n\n"))
			flusher.Flush()
		}
	}
}
