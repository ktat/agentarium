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
