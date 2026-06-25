# agentarium 汎用イベントバス Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development（推奨）。チェックボックス（`- [ ]`）で進捗管理。

**Goal:** カーネルに汎用 pub/sub イベントバスを足し、任意の topic を `POST /events/publish` で流し `GET /events?topic=` (SSE) で購読、フロントは `globalThis.agentarium.subscribe(topic, cb)` で受け取れるようにする。

**Architecture:** 既存の terminal `sseHub`（kernel/terminal/events.go）の汎用版を `kernel/events` パッケージに切り出す。topic 付き購読・broadcast。server がインスタンスを生成し `POST /events/publish`・`GET /events` をマウント。shell の app.js に subscribe ヘルパを追加。

**Tech Stack:** Go 1.26（標準ライブラリのみ）、プレーン JS（embed.FS）、TDD。

## Global Constraints
- Go 1.26 系・標準ライブラリのみ。1ファイル 700 行以内。言われたこと以外はしない。
- TDD（Red→Green→Refactor）。Conventional Commits（日本語・scope 付き）。
- ローカル実行前提（127.0.0.1 バインド）。`POST /events/publish` は csrfGuard 配下（Origin 不在=ローカル curl は許可、cross-origin は拒否）。
- 既存 `/terminal/events`（状態 SSE）とは別物（用途が違うため一般化はするが置換しない）。

---

### Task 1: kernel/events — 汎用 Hub（pub/sub + SSE ハンドラ）

**Files:**
- Create: `kernel/events/events.go`
- Test: `kernel/events/events_test.go`

**Interfaces:**
- Produces: `events.New() *Hub`、`(*Hub).Publish(topic string, data []byte)`、
  `(*Hub).HandleSubscribe(w http.ResponseWriter, r *http.Request)`（GET SSE, topic は `?topic=`）、
  `(*Hub).HandlePublish(w http.ResponseWriter, r *http.Request)`（POST, body `{"topic":string,"data":<any>}`）。

- [ ] **Step 1: テストを書く（events_test.go）**
```go
package events

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPublishReachesMatchingSubscriber(t *testing.T) {
	h := New()
	srv := httptest.NewServer(http.HandlerFunc(h.HandleSubscribe))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"?topic=t1", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer resp.Body.Close()

	// 購読登録が済むまで少し待つ
	time.Sleep(50 * time.Millisecond)
	h.Publish("t1", []byte(`{"ok":1}`))

	buf := make([]byte, 256)
	done := make(chan string, 1)
	go func() {
		n, _ := resp.Body.Read(buf)
		done <- string(buf[:n])
	}()
	select {
	case got := <-done:
		if !strings.Contains(got, `data: {"ok":1}`) {
			t.Fatalf("frame=%q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event received")
	}
}

func TestPublishTopicFilter(t *testing.T) {
	h := New()
	ch := h.add("t1")
	defer h.remove(ch)
	h.Publish("other", []byte(`x`)) // 不一致 topic は届かない
	select {
	case <-ch:
		t.Fatal("should not receive for non-matching topic")
	case <-time.After(100 * time.Millisecond):
	}
	h.Publish("t1", []byte(`y`))
	select {
	case b := <-ch:
		if !strings.Contains(string(b), "y") {
			t.Fatalf("frame=%q", b)
		}
	case <-time.After(time.Second):
		t.Fatal("expected event for matching topic")
	}
}

func TestHandlePublishBroadcasts(t *testing.T) {
	h := New()
	ch := h.add("rep")
	defer h.remove(ch)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/events/publish",
		strings.NewReader(`{"topic":"rep","data":{"id":"p1","term":"2026 1H"}}`))
	h.HandlePublish(rec, req)
	if rec.Code != 204 {
		t.Fatalf("status=%d", rec.Code)
	}
	select {
	case b := <-ch:
		if !strings.Contains(string(b), `"id":"p1"`) {
			t.Fatalf("frame=%q", b)
		}
	case <-time.After(time.Second):
		t.Fatal("publish not broadcast")
	}
}

func TestHandlePublishBadJSON(t *testing.T) {
	h := New()
	rec := httptest.NewRecorder()
	h.HandlePublish(rec, httptest.NewRequest("POST", "/events/publish", strings.NewReader("not json")))
	if rec.Code != 400 {
		t.Fatalf("want 400 got %d", rec.Code)
	}
}
```

- [ ] **Step 2: 失敗を確認** Run: `cd /home/ktat/git/github/agentarium && go test ./kernel/events/`（パッケージ未実装でビルド不可）

- [ ] **Step 3: 実装（events.go）**
```go
// Package events はカーネルの汎用 pub/sub イベントバス（topic 付き SSE 配信）。
// 既存 terminal の状態 SSE とは別物で、任意の消費者が任意の topic を流せる。
package events

import (
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
```

- [ ] **Step 4: 緑を確認** Run: `cd /home/ktat/git/github/agentarium && go test ./kernel/events/ -v`
- [ ] **Step 5: コミット**
```bash
git add kernel/events/
git commit -m "feat(events): 汎用 pub/sub イベントバス（topic 付き SSE）を追加"
```

---

### Task 2: server に /events をマウント

**Files:**
- Modify: `kernel/server/server.go`（New の中で Hub 生成・2 route マウント）
- Test: `kernel/server/server_test.go`

**Interfaces:**
- Consumes: `events.New()` / `(*Hub).HandlePublish` / `(*Hub).HandleSubscribe`（Task 1）。
- Produces: `POST /events/publish`（csrfGuard 配下）、`GET /events`（SSE）。

- [ ] **Step 1: テストを書く（server_test.go に追記）**
```go
func TestEventsPublishSubscribe(t *testing.T) {
	reg := plugin.NewRegistry()
	srv := New(reg, newTestShellFS())
	// publish は 204
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"topic":"x","data":{"a":1}}`)
	srv.ServeHTTP(rec, httptest.NewRequest("POST", "/events/publish", body))
	if rec.Code != 204 {
		t.Fatalf("publish status=%d", rec.Code)
	}
	// subscribe エンドポイントが存在し SSE ヘッダを返す（即時 flush 後に context 終了で抜ける）
	req := httptest.NewRequest("GET", "/events?topic=x", nil)
	ctx, cancel := context.WithTimeout(req.Context(), 100*time.Millisecond)
	defer cancel()
	rec2 := httptest.NewRecorder()
	srv.ServeHTTP(rec2, req.WithContext(ctx))
	if ct := rec2.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type=%q", ct)
	}
}
```
（`context`/`time`/`strings` を server_test の import に必要なら追加。`httptest.NewRecorder` は Flusher を実装するため SSE ハンドラは即 return せず context timeout で抜ける。）

- [ ] **Step 2: 失敗を確認** Run: `cd /home/ktat/git/github/agentarium && go test ./kernel/server/ -run TestEvents`

- [ ] **Step 3: 実装（server.go）**

import に `"github.com/ktat/agentarium/kernel/events"` を追加。`New` 内、`mux.HandleFunc("GET /{$}", ...)` の近くに:
```go
	hub := events.New()
	mux.Handle("POST /events/publish", csrfGuard(http.HandlerFunc(hub.HandlePublish)))
	mux.HandleFunc("GET /events", hub.HandleSubscribe)
```

- [ ] **Step 4: 緑を確認** Run: `cd /home/ktat/git/github/agentarium && go test ./kernel/server/`
- [ ] **Step 5: コミット**
```bash
git add kernel/server/server.go kernel/server/server_test.go
git commit -m "feat(server): /events/publish と /events(SSE) をマウント"
```

---

### Task 3: shell に subscribe ヘルパ

**Files:**
- Modify: `kernel/shell/assets/app.js`

**Interfaces:**
- Produces: `globalThis.agentarium.subscribe(topic, onMessage)` → `EventSource` を返す（`.close()` 可能）。onMessage には JSON.parse 済みの data を渡す。

- [ ] **Step 1: 実装**

`globalThis.agentarium = { ... }` の定義に `subscribe` を追加し、ヘルパ関数を定義:
```js
// subscribe は topic の汎用イベント（/events）を購読し、各 data(JSON) を onMessage に渡す。
// 戻り値の EventSource は呼び出し側が .close() できる。
function subscribe(topic, onMessage) {
  const es = new EventSource('/events?topic=' + encodeURIComponent(topic));
  es.onmessage = (e) => {
    let data = null;
    try { data = JSON.parse(e.data); } catch (_) { data = e.data; }
    try { onMessage(data); } catch (_) { /* 購読側のエラーは握りつぶす */ }
  };
  return es;
}
```
公開オブジェクトに `subscribe,` を追加（openTab 等と並べる）。

- [ ] **Step 2: ビルド/構文確認** Run: `cd /home/ktat/git/github/agentarium && go build ./... && node --check kernel/shell/assets/app.js`
- [ ] **Step 3: 手動確認（推奨）**: ブラウザ devtools で `const es=agentarium.subscribe('x',d=>console.log(d)); fetch('/events/publish',{method:'POST',body:JSON.stringify({topic:'x',data:{hi:1}})})` → `{hi:1}` がログされる。
- [ ] **Step 4: コミット**
```bash
git add kernel/shell/assets/app.js
git commit -m "feat(shell): agentarium.subscribe(topic,cb) で汎用イベントを購読"
```

---

## Self-Review
- Spec 対応: 汎用 pub/sub（publish/subscribe SSE）→T1/T2。フロント subscribe→T3。
- Placeholder: なし（全コード記載）。
- 型整合: `events.New()/Publish([]byte)/HandlePublish/HandleSubscribe`、JS `subscribe(topic,cb)`。
- 注意: `POST /events/publish` は csrfGuard 配下（Origin 不在のローカル curl=許可、cross-origin=拒否）。

## 開発フロー注意（agentarium）
- worktree ブランチで作業（main の未コミット WIP を隔離）。push 前に `make test`/`make lint`（deno 未インストール環境では `lint-js` が失敗するため `--no-verify`、Go test + golangci-lint + `node --check` で代替）。PR 作成 → CodeRabbit 監視 → 指摘解消で main マージ。
