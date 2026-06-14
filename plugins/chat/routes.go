package chat

import (
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ktat/agentarium/kernel/plugin"
	"github.com/ktat/agentarium/kernel/server"
)

func (p Plugin) Routes() []plugin.Route {
	return []plugin.Route{
		{Method: "POST", Path: "/start", Handler: p.handleStart},
		{Method: "GET", Path: "/list", Handler: p.handleList},
		{Method: "POST", Path: "/update", Handler: p.handleUpdate},
		{Method: "POST", Path: "/archive", Handler: p.handleArchive},
	}
}

type startRequest struct {
	Summary string `json:"summary"`
}

// handleStart は POST /plugins/chat/start。summary を記録し採番した id を返す。
func (p Plugin) handleStart(w http.ResponseWriter, r *http.Request) {
	if !server.IsLocalOriginOrAbsent(r) {
		http.Error(w, "cross-origin request rejected", http.StatusForbidden)
		return
	}
	var body startRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	summary := strings.TrimSpace(body.Summary)
	if summary == "" {
		http.Error(w, "summary is required", http.StatusBadRequest)
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	recs, err := p.store.Load()
	if err != nil {
		log.Printf("plugins/chat: load: %v", err)
		http.Error(w, "failed to load chat store", http.StatusInternalServerError)
		return
	}
	now := time.Now().UTC()
	id := "chat-" + strconv.FormatInt(now.UnixNano(), 10)
	recs = append(recs, ChatRecord{ID: id, Summary: summary, StartedAt: now.Format(time.RFC3339Nano)})
	if err := p.store.Save(recs); err != nil {
		log.Printf("plugins/chat: save: %v", err)
		http.Error(w, "failed to save chat store", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
}

// handleList は GET /plugins/chat/list。レコードを新しい順で返す。
func (p Plugin) handleList(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	recs, err := p.store.Load()
	p.mu.Unlock()
	if err != nil {
		log.Printf("plugins/chat: load: %v", err)
		http.Error(w, "failed to load chat store", http.StatusInternalServerError)
		return
	}
	if recs == nil {
		recs = []ChatRecord{}
	}
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].StartedAt > recs[j].StartedAt }) // UTC+RFC3339Nano なので文字列比較で時系列降順になる
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"items": recs})
}

// handleUpdate は POST /plugins/chat/update?id=&session_id= で再開識別子を紐付ける。
func (p Plugin) handleUpdate(w http.ResponseWriter, r *http.Request) {
	if !server.IsLocalOriginOrAbsent(r) {
		http.Error(w, "cross-origin request rejected", http.StatusForbidden)
		return
	}
	sid := r.URL.Query().Get("session_id")
	p.mutate(w, r.URL.Query().Get("id"), func(rec *ChatRecord) {
		if sid != "" {
			rec.SessionID = sid
		}
	})
}

// handleArchive は POST /plugins/chat/archive?id= でレコードの archive 状態をトグルする
// （未archive→archive、archive済み→解除）。フロントの「archive / 戻す」ボタンに対応。
func (p Plugin) handleArchive(w http.ResponseWriter, r *http.Request) {
	if !server.IsLocalOriginOrAbsent(r) {
		http.Error(w, "cross-origin request rejected", http.StatusForbidden)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	p.mutate(w, r.URL.Query().Get("id"), func(rec *ChatRecord) {
		if rec.ArchivedAt == "" {
			rec.ArchivedAt = now
		} else {
			rec.ArchivedAt = ""
		}
	})
}

// mutate は id のレコードに fn を適用して保存する。見つからなければ 404、成功で 204。
func (p Plugin) mutate(w http.ResponseWriter, id string, fn func(*ChatRecord)) {
	if id == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	recs, err := p.store.Load()
	if err != nil {
		log.Printf("plugins/chat: load: %v", err)
		http.Error(w, "failed to load chat store", http.StatusInternalServerError)
		return
	}
	found := false
	for i := range recs {
		if recs[i].ID == id {
			fn(&recs[i])
			found = true
			break
		}
	}
	if !found {
		http.Error(w, "id not found", http.StatusNotFound)
		return
	}
	if err := p.store.Save(recs); err != nil {
		log.Printf("plugins/chat: save: %v", err)
		http.Error(w, "failed to save chat store", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
