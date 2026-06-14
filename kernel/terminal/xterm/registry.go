package xterm

import (
	"errors"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/ktat/agentarium/kernel/terminal"
)

// ObserverHooks / StateListener は terminal package の共通型のエイリアス（D8）。
// 既存の SetObserver / AddStateListener / bindObserverCallbacks の signature を
// 壊さずに型を 1 本化する。
type ObserverHooks = terminal.ObserverHooks
type StateListener = terminal.StateListener

// Registry は ID で識別される複数の Process を管理する xterm バックエンドの中核。
//
// ロック順序の不変条件: 本 Registry の r.mu を取得した状態から Process のメソッド
// （p.mu を取る SetOnInput/SetOnOutput 等）を呼ぶ経路はあるが、その逆—p.mu 保持中に
// r.* を呼ぶ経路—を作ってはならない。両者が混在するとデッドロックする。Process の
// onExit は cleanup() 内で別 goroutine から発火するため r.Remove が安全に r.mu を取れる。
type Registry struct {
	mu             sync.Mutex
	processes      map[string]*entry
	sessionIndex   map[string]string // SessionID → ID の逆引き
	workDir        string
	store          *terminal.Store // nil なら永続化なし
	observer       ObserverHooks
	stateListeners []StateListener
}

type entry struct {
	Process     *Process
	Label       string
	AgentName   string
	WorkDir     string
	Model       string
	SessionID   string
	Cols        int
	AltRows     int
	State       terminal.SessionState
	StateSince  time.Time
	StateSource string
}

// NewRegistry は永続化なしの Registry を返す。
func NewRegistry(workDir string) *Registry {
	return NewRegistryWithStore(workDir, nil)
}

// NewRegistryWithStore は Store を伴う Registry を返す。
func NewRegistryWithStore(workDir string, store *terminal.Store) *Registry {
	return &Registry{
		processes:    make(map[string]*entry),
		sessionIndex: make(map[string]string),
		workDir:      workDir,
		store:        store,
	}
}

// AddStateListener は状態遷移 callback を追加する（nil は無視）。
func (r *Registry) AddStateListener(l StateListener) {
	if l == nil {
		return
	}
	r.mu.Lock()
	r.stateListeners = append(r.stateListeners, l)
	r.mu.Unlock()
}

// SetObserver は Process の入出力を観測する hook を設定し、既存 Process にも再 bind する。
func (r *Registry) SetObserver(o ObserverHooks) {
	r.mu.Lock()
	r.observer = o
	for id, e := range r.processes {
		if e.Process != nil {
			r.bindObserverCallbacks(e.Process, id)
		}
	}
	r.mu.Unlock()
}

func (r *Registry) bindObserverCallbacks(p *Process, id string) {
	if r.observer == nil {
		return
	}
	obs := r.observer
	termID := id
	p.SetOnInput(func(data []byte) { obs.OnInput(termID, data) })
	p.SetOnOutput(func(data []byte) { obs.OnOutput(termID, data) })
}

// Start は id に対応する Process を起動して返す。既に Running なら再利用する。
// 起動バイナリ/引数は ag.Invocation(req) が組み立てる（command 固定なし）。
func (r *Registry) Start(id, label string, ag terminal.Agent, req terminal.RunRequest) (*Process, error) {
	if id == "" {
		return nil, errors.New("id is required")
	}
	if ag == nil {
		return nil, errors.New("agent is required")
	}
	binary, args := ag.Invocation(req)
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.processes[id]; ok && e.Process.Running() {
		if label != "" {
			e.Label = label
		}
		return e.Process, nil
	}
	p := NewProcess(r.workDir, binary, args...)
	r.bindObserverCallbacks(p, id)
	ent := &entry{
		Process:     p,
		Label:       label,
		AgentName:   ag.Name(),
		WorkDir:     r.workDir,
		Model:       req.Model,
		State:       terminal.StateIdle,
		StateSince:  time.Now(),
		StateSource: "init",
	}
	// SetOnExit BEFORE Start: prevents a race where the process exits immediately
	// (before SetOnExit is called after Start) leaving a ghost entry in the registry.
	// Also use removeIfSame so a stale onExit closure cannot delete a new entry
	// registered under the same id after Stop+Start.
	p.SetOnExit(func() { r.removeIfSame(id, ent) })
	if err := p.Start(); err != nil {
		return nil, err
	}
	r.processes[id] = ent
	return p, nil
}

// Get は id に対応する Process を返す（なければ nil）。
func (r *Registry) Get(id string) *Process {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.processes[id]; ok {
		return e.Process
	}
	return nil
}

// Label は id のラベルを返す。
func (r *Registry) Label(id string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.processes[id]; ok {
		return e.Label
	}
	return ""
}

// SetSessionID は id のエントリにセッション識別子を紐付け、逆引き index を更新し永続化する。
func (r *Registry) SetSessionID(id, sessionID string) {
	r.mu.Lock()
	e, ok := r.processes[id]
	if ok {
		if e.SessionID != "" {
			delete(r.sessionIndex, e.SessionID)
		}
		e.SessionID = sessionID
		if sessionID != "" {
			r.sessionIndex[sessionID] = id
		}
	}
	r.mu.Unlock()
	if ok {
		r.persist()
	}
}

// IDBySessionID は SessionID から terminal ID を逆引きする（なければ ""）。
func (r *Registry) IDBySessionID(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessionIndex[sessionID]
}

// SetState は id の状態を更新し、state または source が変化したら stateListeners を呼ぶ。
// source は "hook"|"pty"|"init"。同じ state でも source が変われば通知する
// （例: (idle,"init")→(idle,"pty")）。hook と PTY は対等な情報源で、後発の信号が常に上書きする。
func (r *Registry) SetState(id string, next terminal.SessionState, source string) {
	r.mu.Lock()
	e, ok := r.processes[id]
	if !ok {
		r.mu.Unlock()
		return
	}
	prev := e.State
	if prev == next && e.StateSource == source {
		r.mu.Unlock()
		return
	}
	e.State = next
	e.StateSince = time.Now()
	e.StateSource = source
	listeners := append([]StateListener(nil), r.stateListeners...)
	r.mu.Unlock()
	for _, l := range listeners {
		l(id, prev, next, source)
	}
}

// Stop は id の Process を停止し Registry から取り除く（ユーザ操作起点）。
// 注意: Process が自然終了済み（readLoop→onExit→Remove）で既に取り除かれている場合、
// ここでは ok=false となり no-op で返る。そのため observer.Forget はこの経路では呼ばれない
// （Remove 側で呼ばれる）。observer を結線する S1-P3 では両経路の Forget が漏れないか確認すること。
func (r *Registry) Stop(id string) error {
	r.mu.Lock()
	e, ok := r.processes[id]
	if ok {
		if e.SessionID != "" {
			delete(r.sessionIndex, e.SessionID)
		}
		delete(r.processes, id)
	}
	obs := r.observer
	r.mu.Unlock()
	if !ok {
		return nil
	}
	r.persist()
	if obs != nil {
		obs.Forget(id)
	}
	return e.Process.Stop()
}

// Remove は Registry からのみ取り除く（Process は終了済み前提。PTY EOF の onExit から呼ばれる）。
// 後方互換のため残す。無条件削除が必要な呼び元（テスト等）はこちらを使う。
// onExit callback からは removeIfSame を使うこと（Stop+Start の race 防止）。
func (r *Registry) Remove(id string) {
	r.mu.Lock()
	e, ok := r.processes[id]
	if ok {
		if e.SessionID != "" {
			delete(r.sessionIndex, e.SessionID)
		}
		delete(r.processes, id)
	}
	obs := r.observer
	r.mu.Unlock()
	if ok {
		r.persist()
		if obs != nil {
			obs.Forget(id)
		}
	}
}

// removeIfSame は id のエントリが ent と同一オブジェクトの場合のみ削除する。
// onExit closure から呼ばれ、Stop → 同 id で Start された新エントリを誤って
// 削除するのを防ぐ（旧 onExit が遅れて発火しても新 entry は保護される）。
func (r *Registry) removeIfSame(id string, ent *entry) {
	r.mu.Lock()
	e, ok := r.processes[id]
	if !ok || e != ent {
		r.mu.Unlock()
		return
	}
	if e.SessionID != "" {
		delete(r.sessionIndex, e.SessionID)
	}
	delete(r.processes, id)
	obs := r.observer
	r.mu.Unlock()
	r.persist()
	if obs != nil {
		obs.Forget(id)
	}
}

// persist は store があれば SessionID を持つ entry を SessionRecord として書き出す。
func (r *Registry) persist() {
	if r.store == nil {
		return
	}
	r.mu.Lock()
	out := make([]terminal.SessionRecord, 0, len(r.processes))
	for id, e := range r.processes {
		if e.SessionID == "" {
			continue
		}
		out = append(out, terminal.SessionRecord{
			ID:        id,
			Label:     e.Label,
			WorkDir:   e.WorkDir,
			Agent:     e.AgentName,
			SessionID: e.SessionID,
			Model:     e.Model,
			Cols:      e.Cols,
			AltRows:   e.AltRows,
		})
	}
	r.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if err := r.store.Save(out); err != nil {
		log.Printf("terminal/xterm: persist failed: %v", err)
	}
}

// List は管理中の全 Process を ID 昇順で SessionInfo として返す。
func (r *Registry) List() []terminal.SessionInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]terminal.SessionInfo, 0, len(r.processes))
	for id, e := range r.processes {
		out = append(out, terminal.SessionInfo{
			ID:        id,
			Label:     e.Label,
			SessionID: e.SessionID,
			State:     e.State,
			Running:   e.Process.Running(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
