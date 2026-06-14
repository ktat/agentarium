package chat

import (
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ktat/agentarium/kernel/plugin"
	"github.com/ktat/agentarium/kernel/server"
)

// mu は store への read-modify-write を直列化する（list 連打 + start の競合回避）。
var mu sync.Mutex

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

	mu.Lock()
	defer mu.Unlock()
	recs, err := p.store.Load()
	if err != nil {
		log.Printf("plugins/chat: load: %v", err)
		http.Error(w, "failed to load chat store", http.StatusInternalServerError)
		return
	}
	now := time.Now()
	id := "chat:" + strconv.FormatInt(now.UnixNano(), 10)
	recs = append(recs, ChatRecord{ID: id, Summary: summary, StartedAt: now.Format(time.RFC3339)})
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
	mu.Lock()
	recs, err := p.store.Load()
	mu.Unlock()
	if err != nil {
		log.Printf("plugins/chat: load: %v", err)
		http.Error(w, "failed to load chat store", http.StatusInternalServerError)
		return
	}
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].StartedAt > recs[j].StartedAt })
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"items": recs})
}

// handleUpdate / handleArchive は Task B3 で本実装。まずはコンパイルを通す仮実装。
func (p Plugin) handleUpdate(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
func (p Plugin) handleArchive(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
