// Package events はカーネルの汎用 pub/sub イベントバス（topic 付き SSE 配信）。
// 既存 terminal の状態 SSE とは別物で、任意の消費者が任意の topic を流せる。
package events

import (
	"bytes"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// Hub は topic ごとの購読チャネルを束ね、Publish を一致 topic の購読者へ配信する。
type Hub struct {
	mu   sync.Mutex
	subs map[chan []byte]string // チャネル → 購読 topic
}

func New() *Hub { return &Hub{subs: map[chan []byte]string{}} }

func (h *Hub) add(topic string) chan []byte {
	ch := make(chan []byte, 8)
	h.mu.Lock()
	h.subs[ch] = topic
	h.mu.Unlock()
	return ch
}

func (h *Hub) remove(ch chan []byte) {
	h.mu.Lock()
	if _, ok := h.subs[ch]; ok {
		delete(h.subs, ch)
		close(ch)
	}
	h.mu.Unlock()
}

// Publish は topic 一致の購読者へ data を SSE フレーム（data: <json>\n\n）として送る。
func (h *Hub) Publish(topic string, data []byte) {
	frame := append(append([]byte("data: "), data...), '\n', '\n')
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch, t := range h.subs {
		if t != topic {
			continue
		}
		select {
		case ch <- frame:
		default: // バッファ満杯の遅い購読者はドロップ
		}
	}
}

// HandlePublish は POST /events/publish。body {"topic":string,"data":<any>}。data を再 marshal して配信。
func (h *Hub) HandlePublish(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Topic string          `json:"topic"`
		Data  json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Topic == "" {
		http.Error(w, "invalid publish body", http.StatusBadRequest)
		return
	}
	data := body.Data
	if len(data) == 0 {
		data = []byte("null")
	} else {
		var buf bytes.Buffer
		if err := json.Compact(&buf, data); err != nil {
			http.Error(w, "invalid publish body", http.StatusBadRequest)
			return
		}
		data = buf.Bytes()
	}
	h.Publish(body.Topic, data)
	w.WriteHeader(http.StatusNoContent)
}

// HandleSubscribe は GET /events?topic=<t>（SSE）。接続中、topic 一致の Publish を流す。
func (h *Hub) HandleSubscribe(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	topic := r.URL.Query().Get("topic")
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	ch := h.add(topic)
	defer h.remove(ch)
	flusher.Flush()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	ctx := r.Context()
	for {
		select {
		case b, ok := <-ch:
			if !ok {
				return
			}
			_, _ = w.Write(b)
			flusher.Flush()
		case <-ticker.C:
			_, _ = w.Write([]byte(": keepalive\n\n"))
			flusher.Flush()
		case <-ctx.Done():
			return
		}
	}
}
