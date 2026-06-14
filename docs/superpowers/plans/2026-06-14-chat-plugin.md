# Chat プラグイン + kernel/store 公開 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 自由入力テキストを既定エージェントの初期入力として起動し、chat 履歴を一覧・再開できる同梱プラグイン `plugins/chat` を、再利用可能な永続ストア `kernel/store` の公開と共に追加する。

**Architecture:** 既存の汎用 `JSONStore[T]`（`kernel/terminal/jsonstore.go`）を新公開パッケージ `kernel/store` へ移動し、`kernel/terminal` は型 alias + 関数委譲で後方互換を保つ。`plugins/chat` は `RouteProvider` + `FrontendProvider` を実装し、消費者 main が `store.New[chat.ChatRecord](path)` を `chat.New` に注入する（D7 消費モデル、plugin IF 無変更）。導線は既存 `window.agentarium.openAgentTab({command, autoEnter})`、resume は `/terminal/list` の `SessionID` 紐付けで実現（カーネル無変更）。

**Tech Stack:** Go 1.26、標準ライブラリ（net/http, encoding/json, embed）、httptest。フロントは embed.FS の素の JS（ビルドツールなし）。

---

## File Structure

- `kernel/store/jsonstore.go` — **新規**。`JSONStore[T]` / `New` / `Load` / `Save`（terminal から移動）
- `kernel/store/jsonstore_test.go` — **新規**。移設したラウンドトリップ/quarantine テスト
- `kernel/terminal/jsonstore.go` — **改変**。alias + 委譲のみに置換
- `kernel/terminal/jsonstore_test.go` — **削除**（store へ移設）
- `plugins/chat/chat.go` — **新規**。`ChatRecord` / `Plugin` / `New` / `Meta` / `Assets`
- `plugins/chat/routes.go` — **新規**。start / list / update / archive ハンドラ
- `plugins/chat/chat_test.go` — **新規**。Meta + ルート table test
- `plugins/chat/assets/index.js` — **新規**。Chat タブのフロント
- `cmd/agentarium/main.go` — **改変**。chat ストアのパス解決 + 登録
- `README.md` — **改変**。プラグイン表 + 公開 API に追記

---

## Part A: kernel/store

### Task A1: `kernel/store` パッケージを作成（JSONStore を移動）

**Files:**
- Create: `kernel/store/jsonstore.go`
- Create: `kernel/store/jsonstore_test.go`

- [ ] **Step 1: テストを先に作成（package store, `New` を使う）**

`kernel/store/jsonstore_test.go`:

```go
package store

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type jsTestEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func TestJSONStore_SaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "data.json")
	s := New[jsTestEntry](path)
	in := []jsTestEntry{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}}
	if err := s.Save(in); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("round trip mismatch:\n want %+v\n got  %+v", in, got)
	}
}

func TestJSONStore_LoadMissingReturnsNil(t *testing.T) {
	s := New[jsTestEntry](filepath.Join(t.TempDir(), "nope.json"))
	got, err := s.Load()
	if err != nil {
		t.Fatalf("load missing: %v", err)
	}
	if got != nil {
		t.Fatalf("want nil, got %+v", got)
	}
}

func TestJSONStore_LoadCorruptQuarantines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")
	if err := os.WriteFile(path, []byte("{bad json"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s := New[jsTestEntry](path)
	got, err := s.Load()
	if err != nil {
		t.Fatalf("corrupt load should not error (quarantine): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty, got %d", len(got))
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Fatalf("corrupt file should be quarantined to .bak: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("original should be gone: %v", err)
	}
}

func TestJSONStore_SaveDurableAfterReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	in := []jsTestEntry{{ID: "x", Name: "X"}}
	if err := New[jsTestEntry](path).Save(in); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := New[jsTestEntry](path).Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("reopen mismatch: %+v", got)
	}
}
```

- [ ] **Step 2: テストが落ちる（コンパイルエラー）ことを確認**

Run: `go test ./kernel/store/`
Expected: FAIL（`undefined: New` / package が無い）

- [ ] **Step 3: 実装を作成（terminal から移動した本体）**

`kernel/store/jsonstore.go`:

```go
// Package store はプラグイン/カーネルが小さな状態を JSON ファイルへ
// atomic に永続化するための汎用ストアを提供する。消費者 main が
// plugin ごとに別パスを渡して名前空間を分ける（D7 消費モデル）。
package store

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// JSONStore[T] は []T を JSON ファイルへ atomic に永続化する汎用ストア。
//
// 設計:
//   - Save: tmp ファイルへ書いて rename（atomic）、さらに親ディレクトリを fsync して
//     rename の耐久性を上げる（kernel crash 後も rename が残る可能性を高める）
//   - Load: パース失敗時は破損ファイルを <path>.bak へ退避して空で続行（quarantine）
type JSONStore[T any] struct {
	path string
	mu   sync.Mutex
}

// New は path をバッキングファイルとする JSONStore を返す。
func New[T any](path string) *JSONStore[T] {
	return &JSONStore[T]{path: path}
}

// Load は永続化済みの []T を読み出す。ファイルが無ければ (nil, nil)。
// パース失敗時は破損ファイルを <path>.bak へ退避し、log を出して (nil, nil) を返す。
func (s *JSONStore[T]) Load() ([]T, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []T
	if err := json.Unmarshal(b, &entries); err != nil {
		bak := s.path + ".bak"
		log.Printf("store: corrupt %s (%v); quarantining to %s and starting empty", s.path, err, bak)
		if rerr := os.Rename(s.path, bak); rerr != nil {
			log.Printf("store: failed to quarantine %s: %v", s.path, rerr)
		}
		return nil, nil
	}
	return entries, nil
}

// Save は entries を atomic（tmp→rename）に書き出し、親ディレクトリを fsync する。
func (s *JSONStore[T]) Save(entries []T) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	// 親ディレクトリの fsync（rename の durability 向上。失敗は best-effort で無視）。
	if d, derr := os.Open(dir); derr == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
```

- [ ] **Step 4: テストが通ることを確認**

Run: `go test ./kernel/store/`
Expected: PASS（4 テスト）

- [ ] **Step 5: コミット**

```bash
git add kernel/store/
git commit -m "feat(store): 汎用 JSONStore を公開パッケージ kernel/store として追加"
```

---

### Task A2: `kernel/terminal` を alias + 委譲に置換

**Files:**
- Modify: `kernel/terminal/jsonstore.go`（全置換）
- Delete: `kernel/terminal/jsonstore_test.go`

- [ ] **Step 1: terminal の jsonstore.go を alias + 委譲へ置換**

`kernel/terminal/jsonstore.go` の全内容を以下に置換:

```go
package terminal

import "github.com/ktat/agentarium/kernel/store"

// JSONStore[T] は kernel/store.JSONStore[T] の後方互換 alias。
// 実体は kernel/store へ移動した（プラグインからも使える公開ストア）。
type JSONStore[T any] = store.JSONStore[T]

// NewJSONStore は store.New への委譲（後方互換）。
func NewJSONStore[T any](path string) *store.JSONStore[T] {
	return store.New[T](path)
}
```

- [ ] **Step 2: 旧テストを削除**

```bash
git rm kernel/terminal/jsonstore_test.go
```

- [ ] **Step 3: terminal パッケージがビルド・テストできることを確認**

Run: `go build ./... && go test ./kernel/terminal/...`
Expected: PASS（`terminal.NewStore` / `persist.go` の `JSONStore[SessionRecord]` が alias 経由で従来通り動く）

- [ ] **Step 4: 全体ビルド確認（cmd / wrap の terminal.NewStore 利用箇所）**

Run: `go build ./...`
Expected: エラーなし

- [ ] **Step 5: コミット**

```bash
git add kernel/terminal/jsonstore.go
git commit -m "refactor(terminal): JSONStore を kernel/store へ移し alias で後方互換維持"
```

---

## Part B: plugins/chat

### Task B1: `ChatRecord` と Plugin スケルトン（Meta / New）

**Files:**
- Create: `plugins/chat/chat.go`
- Create: `plugins/chat/chat_test.go`
- Create: `plugins/chat/assets/index.js`（embed 用に空でないファイルが必要なので最小プレースホルダを先に置く）

- [ ] **Step 1: Meta テストを作成**

`plugins/chat/chat_test.go`:

```go
package chat

import (
	"path/filepath"
	"testing"

	"github.com/ktat/agentarium/kernel/plugin"
	"github.com/ktat/agentarium/kernel/store"
)

func newTestPlugin(t *testing.T) Plugin {
	t.Helper()
	st := store.New[ChatRecord](filepath.Join(t.TempDir(), "chat.json"))
	return New(st)
}

func TestPlugin_Meta(t *testing.T) {
	p := newTestPlugin(t)
	var _ plugin.Plugin = p
	if p.Meta().ID != "chat" {
		t.Fatalf("want id chat, got %s", p.Meta().ID)
	}
	if p.Meta().Pane != plugin.PaneLeft {
		t.Fatalf("want pane left, got %v", p.Meta().Pane)
	}
}
```

- [ ] **Step 2: テストが落ちる（package 無し）ことを確認**

Run: `go test ./plugins/chat/`
Expected: FAIL（package が無い / `New` undefined）

- [ ] **Step 3: assets プレースホルダと chat.go を作成**

`plugins/chat/assets/index.js`（最小、後続 Task B4 で本実装。シェルは `mod.render(panel,{pluginId})` を呼ぶ規約なので `render` をエクスポートする）:

```js
// Chat タブ（実装は Task B4）
export async function render(root) {
  root.textContent = 'chat (未実装)';
}
```

`plugins/chat/chat.go`:

```go
// Package chat は自由入力テキストを既定エージェントの初期入力として起動し、
// chat 起動分の履歴を一覧・再開する同梱プラグイン。
package chat

import (
	"embed"
	"io/fs"

	"github.com/ktat/agentarium/kernel/plugin"
	"github.com/ktat/agentarium/kernel/store"
)

//go:embed assets/*
var assetsFS embed.FS

// ChatRecord は 1 回の chat 起動を表す永続レコード。
type ChatRecord struct {
	ID         string `json:"id"`                    // 採番した chat id（= terminal key）
	Summary    string `json:"summary"`               // ユーザー入力本文（クリーン）
	StartedAt  string `json:"started_at"`            // RFC3339
	SessionID  string `json:"session_id,omitempty"`  // 再開識別子。未取得は空
	ArchivedAt string `json:"archived_at,omitempty"` // archive 時刻。空=非archive
}

// Plugin は chat 履歴ストアを保持する同梱プラグイン。
type Plugin struct {
	store *store.JSONStore[ChatRecord]
}

// New は chat レコードストアを注入して Plugin を構築する。
// 消費者 main で `chat.New(store.New[chat.ChatRecord](path))` を Register する想定。
func New(st *store.JSONStore[ChatRecord]) Plugin { return Plugin{store: st} }

func (p Plugin) Meta() plugin.Meta {
	return plugin.Meta{ID: "chat", Title: "Chat", Pane: plugin.PaneLeft, Order: 0}
}

func (p Plugin) Assets() fs.FS {
	sub, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		panic(err) // embed パス固定なので起こり得ない
	}
	return sub
}
```

- [ ] **Step 4: テストが通ることを確認**

Run: `go test ./plugins/chat/`
Expected: PASS

- [ ] **Step 5: コミット**

```bash
git add plugins/chat/
git commit -m "feat(chat): ChatRecord と Plugin スケルトン（Meta/Assets）を追加"
```

---

### Task B2: ルート start / list

**Files:**
- Create: `plugins/chat/routes.go`
- Modify: `plugins/chat/chat_test.go`（テスト追加）

- [ ] **Step 1: start→list の往復テストを追加**

`plugins/chat/chat_test.go` に追記（既存 import に `encoding/json` `net/http/httptest` `strings` を足す）:

```go
// routeOf は指定 method/path のハンドラを返す。
func routeOf(t *testing.T, p Plugin, method, path string) func(*httptest.ResponseRecorder, *http.Request) {
	t.Helper()
	for _, rt := range p.Routes() {
		if rt.Method == method && rt.Path == path {
			h := rt.Handler
			return func(rec *httptest.ResponseRecorder, req *http.Request) { h(rec, req) }
		}
	}
	t.Fatalf("route %s %s not found", method, path)
	return nil
}

func TestStartThenList(t *testing.T) {
	p := newTestPlugin(t)

	// start
	start := routeOf(t, p, "POST", "/start")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/start", strings.NewReader(`{"summary":"hello world"}`))
	start(rec, req)
	if rec.Code != 200 {
		t.Fatalf("start status %d body=%s", rec.Code, rec.Body.String())
	}
	var sr struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &sr); err != nil {
		t.Fatalf("decode start resp: %v", err)
	}
	if sr.ID == "" {
		t.Fatal("start should return non-empty id")
	}

	// list
	list := routeOf(t, p, "GET", "/list")
	rec = httptest.NewRecorder()
	list(rec, httptest.NewRequest("GET", "/list", nil))
	if rec.Code != 200 {
		t.Fatalf("list status %d", rec.Code)
	}
	var lr struct {
		Items []ChatRecord `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &lr); err != nil {
		t.Fatalf("decode list resp: %v", err)
	}
	if len(lr.Items) != 1 || lr.Items[0].Summary != "hello world" {
		t.Fatalf("want 1 item 'hello world', got %+v", lr.Items)
	}
	if lr.Items[0].ID != sr.ID {
		t.Fatalf("list id %s != start id %s", lr.Items[0].ID, sr.ID)
	}
}

func TestStartRejectsEmptySummary(t *testing.T) {
	p := newTestPlugin(t)
	start := routeOf(t, p, "POST", "/start")
	rec := httptest.NewRecorder()
	start(rec, httptest.NewRequest("POST", "/start", strings.NewReader(`{"summary":"   "}`)))
	if rec.Code != 400 {
		t.Fatalf("want 400 for empty summary, got %d", rec.Code)
	}
}
```

import 行を以下へ更新:

```go
import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ktat/agentarium/kernel/plugin"
	"github.com/ktat/agentarium/kernel/store"
)
```

- [ ] **Step 2: テストが落ちる（Routes 未定義）ことを確認**

Run: `go test ./plugins/chat/`
Expected: FAIL（`p.Routes undefined`）

- [ ] **Step 3: routes.go に start / list を実装**

`plugins/chat/routes.go`:

```go
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
```

- [ ] **Step 4: テストが通ることを確認**

Run: `go test ./plugins/chat/`
Expected: PASS（Routes に update/archive ハンドラ参照があるが Task B3 で実装。**この時点でコンパイルさせるため Step 3 で handleUpdate/handleArchive を仮実装する必要がある**ので注意）

> 注: Step 3 の `Routes()` は `handleUpdate`/`handleArchive` を参照するため、コンパイルを通すには本タスク内で最小の仮実装を置く。`routes.go` 末尾に追加:

```go
// handleUpdate / handleArchive は Task B3 で本実装。まずはコンパイルを通す仮実装。
func (p Plugin) handleUpdate(w http.ResponseWriter, r *http.Request)  { http.Error(w, "not implemented", http.StatusNotImplemented) }
func (p Plugin) handleArchive(w http.ResponseWriter, r *http.Request) { http.Error(w, "not implemented", http.StatusNotImplemented) }
```

- [ ] **Step 5: コミット**

```bash
git add plugins/chat/routes.go plugins/chat/chat_test.go
git commit -m "feat(chat): /start /list ルートを追加（update/archive は仮実装）"
```

---

### Task B3: ルート update / archive

**Files:**
- Modify: `plugins/chat/routes.go`（仮実装を本実装に置換）
- Modify: `plugins/chat/chat_test.go`（テスト追加）

- [ ] **Step 1: update / archive のテストを追加**

`plugins/chat/chat_test.go` に追記:

```go
// seedOne は 1 件 start して id を返す。
func seedOne(t *testing.T, p Plugin, summary string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	routeOf(t, p, "POST", "/start")(rec, httptest.NewRequest("POST", "/start", strings.NewReader(`{"summary":"`+summary+`"}`)))
	if rec.Code != 200 {
		t.Fatalf("seed start status %d", rec.Code)
	}
	var sr struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &sr)
	return sr.ID
}

func listItems(t *testing.T, p Plugin) []ChatRecord {
	t.Helper()
	rec := httptest.NewRecorder()
	routeOf(t, p, "GET", "/list")(rec, httptest.NewRequest("GET", "/list", nil))
	var lr struct {
		Items []ChatRecord `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &lr); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	return lr.Items
}

func TestUpdateSetsSessionID(t *testing.T) {
	p := newTestPlugin(t)
	id := seedOne(t, p, "hi")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/update?id="+id+"&session_id=abc-123", nil)
	routeOf(t, p, "POST", "/update")(rec, req)
	if rec.Code != 204 {
		t.Fatalf("update status %d body=%s", rec.Code, rec.Body.String())
	}
	items := listItems(t, p)
	if len(items) != 1 || items[0].SessionID != "abc-123" {
		t.Fatalf("want session_id abc-123, got %+v", items)
	}
}

func TestArchiveSetsTimestamp(t *testing.T) {
	p := newTestPlugin(t)
	id := seedOne(t, p, "hi")

	rec := httptest.NewRecorder()
	routeOf(t, p, "POST", "/archive")(rec, httptest.NewRequest("POST", "/archive?id="+id, nil))
	if rec.Code != 204 {
		t.Fatalf("archive status %d", rec.Code)
	}
	items := listItems(t, p)
	if len(items) != 1 || items[0].ArchivedAt == "" {
		t.Fatalf("want archived_at set, got %+v", items)
	}
}

func TestUpdateUnknownIDReturns404(t *testing.T) {
	p := newTestPlugin(t)
	rec := httptest.NewRecorder()
	routeOf(t, p, "POST", "/update")(rec, httptest.NewRequest("POST", "/update?id=nope&session_id=x", nil))
	if rec.Code != 404 {
		t.Fatalf("want 404, got %d", rec.Code)
	}
}
```

- [ ] **Step 2: テストが落ちる（仮実装が 501 を返す）ことを確認**

Run: `go test ./plugins/chat/ -run 'TestUpdate|TestArchive'`
Expected: FAIL（501 / want 204）

- [ ] **Step 3: 仮実装を本実装に置換**

`plugins/chat/routes.go` の仮 `handleUpdate`/`handleArchive` を以下に置換:

```go
// handleUpdate は POST /plugins/chat/update?id=&session_id= で再開識別子を紐付ける。
func (p Plugin) handleUpdate(w http.ResponseWriter, r *http.Request) {
	if !server.IsLocalOriginOrAbsent(r) {
		http.Error(w, "cross-origin request rejected", http.StatusForbidden)
		return
	}
	p.mutate(w, r.URL.Query().Get("id"), func(rec *ChatRecord) {
		rec.SessionID = r.URL.Query().Get("session_id")
	})
}

// handleArchive は POST /plugins/chat/archive?id= でレコードを archive する。
func (p Plugin) handleArchive(w http.ResponseWriter, r *http.Request) {
	if !server.IsLocalOriginOrAbsent(r) {
		http.Error(w, "cross-origin request rejected", http.StatusForbidden)
		return
	}
	p.mutate(w, r.URL.Query().Get("id"), func(rec *ChatRecord) {
		rec.ArchivedAt = time.Now().Format(time.RFC3339)
	})
}

// mutate は id のレコードに fn を適用して保存する。見つからなければ 404、成功で 204。
func (p Plugin) mutate(w http.ResponseWriter, id string, fn func(*ChatRecord)) {
	if id == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
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
```

- [ ] **Step 4: 全テストが通ることを確認**

Run: `go test ./plugins/chat/`
Expected: PASS（全 7 テスト）

- [ ] **Step 5: コミット**

```bash
git add plugins/chat/routes.go plugins/chat/chat_test.go
git commit -m "feat(chat): /update /archive ルートを実装"
```

---

### Task B4: フロント（assets/index.js）

**Files:**
- Modify: `plugins/chat/assets/index.js`（本実装に置換）
- Modify: `plugins/chat/chat_test.go`（embed 配信テスト追加）

- [ ] **Step 1: embed に index.js が含まれることのテストを追加**

`plugins/chat/chat_test.go` に追記:

```go
func TestAssetsServesIndexJS(t *testing.T) {
	p := newTestPlugin(t)
	f, err := p.Assets().Open("index.js")
	if err != nil {
		t.Fatalf("open index.js: %v", err)
	}
	defer f.Close()
	info, _ := f.Stat()
	if info.Size() == 0 {
		t.Fatal("index.js is empty")
	}
}
```

- [ ] **Step 2: テスト実行（現プレースホルダでも size>0 なので通るはず）**

Run: `go test ./plugins/chat/ -run TestAssetsServesIndexJS`
Expected: PASS（プレースホルダでも通る。本タスクの意図は Step 3 の本実装後も配信が壊れないことの回帰防止）

- [ ] **Step 3: index.js を本実装に置換**

`plugins/chat/assets/index.js` の全内容を置換（シェル規約に従い `render(root)` をエクスポート。DOM 要素は `root` スコープで取得する）:

```js
// Chat タブ: 自由入力 → 既定エージェント起動（初期入力注入）+ chat 履歴の一覧/再開。
// シェルは /plugins/chat/assets/index.js を動的 import し render(root, {pluginId}) を呼ぶ。
function esc(s) {
  return String(s).replace(/[<>&"']/g, c => ({ '<': '&lt;', '>': '&gt;', '&': '&amp;', '"': '&quot;', "'": '&#39;' }[c]));
}

const ARCHIVE_KEY = 'chat-show-archived';

function showArchived() {
  return localStorage.getItem(ARCHIVE_KEY) === '1';
}

export async function render(root) {
  root.innerHTML =
    '<div class="chat-form">' +
    '<textarea id="chatInput" rows="3" placeholder="メッセージを入力（Enterで送信 / Shift+Enterで改行）"></textarea>' +
    '<button id="chatStart">送信</button></div>' +
    '<div id="chatHistory"></div>';

  const input = root.querySelector('#chatInput');
  const startBtn = root.querySelector('#chatStart');
  const hist = root.querySelector('#chatHistory');

  async function refreshHistory() {
    try {
      const res = await fetch('/plugins/chat/list');
      if (!res.ok) throw new Error('HTTP ' + res.status);
      const data = await res.json();
      const all = data.items || [];
      const items = all.filter(r => showArchived() || !r.archived_at);
      const archivedCount = all.filter(r => r.archived_at).length;
      const toggle = '<label class="chat-archive-toggle"><input type="checkbox" id="chatArchiveToggle"' +
        (showArchived() ? ' checked' : '') + '> archive 済み (' + archivedCount + ') を表示</label>';
      if (!items.length) {
        hist.innerHTML = toggle + '<p class="empty-section">履歴なし</p>';
        bindToggle();
        return;
      }
      const rows = items.map(r => {
        const created = (r.started_at || '').replace('T', ' ').substring(0, 19);
        const rowCls = r.archived_at ? ' class="row-archived"' : '';
        const resumeAttrs = r.session_id
          ? 'data-id="' + esc(r.id) + '" data-sid="' + esc(r.session_id) + '" data-summary="' + esc(r.summary) + '"'
          : 'disabled';
        return '<tr' + rowCls + '><td>' + esc(r.summary) + '</td>' +
          '<td>' + esc(created) + '</td>' +
          '<td><button class="chat-resume-btn" ' + resumeAttrs + '>↪ 再開</button> ' +
          '<button class="chat-archive-btn" data-id="' + esc(r.id) + '">' +
          (r.archived_at ? '戻す' : 'archive') + '</button></td></tr>';
      }).join('');
      hist.innerHTML = toggle +
        '<table class="task-table"><thead><tr><th>テキスト</th><th>作成</th><th></th></tr></thead><tbody>' +
        rows + '</tbody></table>';
      bindToggle();
      bindRowButtons();
    } catch (err) {
      hist.innerHTML = '<p class="empty-section">取得失敗: ' + esc(String(err)) + '</p>';
    }
  }

  function bindToggle() {
    const t = hist.querySelector('#chatArchiveToggle');
    if (t && !t.dataset.bound) {
      t.dataset.bound = '1';
      t.addEventListener('change', () => {
        localStorage.setItem(ARCHIVE_KEY, t.checked ? '1' : '0');
        refreshHistory();
      });
    }
  }

  function bindRowButtons() {
    hist.querySelectorAll('button.chat-resume-btn[data-sid]:not([data-bound])').forEach(btn => {
      btn.dataset.bound = '1';
      btn.addEventListener('click', () => {
        const summary = btn.dataset.summary || '';
        const label = '↪ ' + (summary.length > 28 ? summary.slice(0, 28) + '…' : summary);
        window.agentarium.openAgentTab({ key: btn.dataset.id, label: label, resume: btn.dataset.sid });
      });
    });
    hist.querySelectorAll('button.chat-archive-btn:not([data-bound])').forEach(btn => {
      btn.dataset.bound = '1';
      btn.addEventListener('click', async () => {
        btn.disabled = true;
        try {
          await fetch('/plugins/chat/archive?id=' + encodeURIComponent(btn.dataset.id), { method: 'POST' });
        } catch (_) {}
        refreshHistory();
      });
    });
  }

  // 起動後 /terminal/list をポーリングして SessionID を ChatRecord に紐付ける。
  function trackSessionID(id) {
    let tries = 0;
    const timer = setInterval(async () => {
      tries++;
      if (tries > 20) { clearInterval(timer); return; } // 最大 ~20*1.5s
      try {
        const res = await fetch('/terminal/list');
        if (!res.ok) return;
        const data = await res.json();
        const row = (data.items || []).find(it => it.ID === id);
        if (row && row.SessionID) {
          clearInterval(timer);
          await fetch('/plugins/chat/update?id=' + encodeURIComponent(id) +
            '&session_id=' + encodeURIComponent(row.SessionID), { method: 'POST' });
          refreshHistory();
        }
      } catch (_) {}
    }, 1500);
  }

  async function startChat() {
    const text = (input.value || '').trim();
    if (!text) { alert('テキストを入力してください'); return; }
    let id;
    try {
      const res = await fetch('/plugins/chat/start', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ summary: text }),
      });
      if (!res.ok) { alert('start failed (' + res.status + '): ' + await res.text()); return; }
      id = (await res.json()).id;
    } catch (err) { alert('start error: ' + err); return; }

    const label = text.length > 28 ? text.slice(0, 28) + '…' : text;
    window.agentarium.openAgentTab({ key: id, label: label, command: text, autoEnter: true });
    input.value = '';
    trackSessionID(id);
    setTimeout(refreshHistory, 800);
  }

  startBtn.addEventListener('click', startChat);
  input.addEventListener('keydown', e => {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); startChat(); }
  });

  refreshHistory();
}
```

- [ ] **Step 4: テストとビルドが通ることを確認**

Run: `go test ./plugins/chat/ && go build ./...`
Expected: PASS

- [ ] **Step 5: コミット**

```bash
git add plugins/chat/assets/index.js plugins/chat/chat_test.go
git commit -m "feat(chat): フロント（入力導線・履歴・再開・archive）を実装"
```

---

### Task B5: 参照デモへの配線 + README

**Files:**
- Modify: `cmd/agentarium/main.go`
- Modify: `README.md`

- [ ] **Step 1: chat ストアのパス解決ヘルパを追加**

`cmd/agentarium/main.go` の `terminalStorePath` の下に追加:

```go
// chatStorePath は chat 履歴の永続化ファイルパスを返す
// （UserConfigDir/agentarium/chat.json）。
func chatStorePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "agentarium", "chat.json"), nil
}
```

- [ ] **Step 2: import と登録を追加**

`cmd/agentarium/main.go` の import に追加:

```go
	"github.com/ktat/agentarium/kernel/store"
	"github.com/ktat/agentarium/plugins/chat"
```

`app.Register(...)` 呼び出しの直前に追加し、Register 引数に `chat.New(...)` を足す:

```go
	chatPath, err := chatStorePath()
	if err != nil {
		return err
	}
	chatStore := store.New[chat.ChatRecord](chatPath)
```

`app.Register(` の引数リストへ `chat.New(chatStore),` を追加（`sessions.New(wd),` の次の行）:

```go
	if err := app.Register(
		hello.Plugin{},
		sessions.New(wd),
		chat.New(chatStore),
		manifestPlugin,
	); err != nil {
		return err
	}
```

- [ ] **Step 3: ビルドと全テストを確認**

Run: `go build ./... && go test ./...`
Expected: PASS（全パッケージ）

- [ ] **Step 4: README 追記**

`README.md` のプラグイン一覧表に行を追加（既存表のフォーマットに合わせる。着手時に該当表を Read して列構成を確認）:

- 同梱プラグイン表: `Chat | chat | 自由入力→エージェント起動 + 履歴/再開 | /plugins/chat/{start,list,update,archive} | UserConfigDir/agentarium/chat.json`

公開 API セクションに `kernel/store`（汎用 JSONStore[T]）を消費者向け公開パッケージとして1行追記。

- [ ] **Step 5: コミット**

```bash
git add cmd/agentarium/main.go README.md
git commit -m "feat(chat): 参照デモへ chat プラグインを配線 + README 追記"
```

---

## Self-Review 結果

- **spec coverage**: (a) kernel/store=Task A1/A2、(b) plugins/chat の ChatRecord/Meta=B1、start/list=B2、update/archive=B3、フロント導線+resume+archive=B4、配線+README=B5。resume の SessionID 紐付け=B4 の `trackSessionID`。非目標（MR/category/prefix）は計画に含めず。すべて対応タスクあり。
- **placeholder scan**: コード step は全て実コードを記載。プレースホルダ無し。Task B4 の DOM 規約は `kernel/shell/assets/app.js`（`mod.render(panel,{pluginId})` を呼ぶ）と `plugins/sessions/assets/index.js`（`export async function render(root)`）で確認済み。計画は実規約に合わせ `render(root)` をエクスポートする形に確定。
- **type consistency**: `store.New[T]` / `JSONStore[T]`、`ChatRecord{ID,Summary,StartedAt,SessionID,ArchivedAt}`、`Plugin.store`、ルートパス `/start /list /update /archive`、`/terminal/list` の `ID`/`SessionID`（大文字）— 全タスクで一致。
