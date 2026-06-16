package wrap

import (
	"errors"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/ktat/agentarium/kernel/terminal"
)

// ObserverHooks / StateListener は terminal package の共通型のエイリアス（D8）。
// 既存の SetObserver / AddStateListener / bindObserverCallbacksLocked の signature を
// 壊さずに型を 1 本化する。
type ObserverHooks = terminal.ObserverHooks
type StateListener = terminal.StateListener

// Registry は ID で識別される複数の wrap Process を管理する。
//
// ロック順序の不変条件: r.mu を保持した状態から Process のメソッド（p.mu を取る
// SetOnInput/SetOnOutput/Resize 等）を呼ぶ経路はあるが、その逆—p.mu 保持中に r.*
// を呼ぶ経路—を作ってはならない。Process の onExit は cleanup() 内で別 goroutine
// から発火するため r.Remove が安全に r.mu を取れる。
type Registry struct {
	mu             sync.Mutex
	processes      map[string]*entry
	sessionIndex   map[string]string // SessionID → ID の逆引き
	workDir        string
	agents         *terminal.AgentRegistry // RestoreFromStore が Agent 名解決に使う
	store          *Store                  // nil なら永続化なし
	observer       ObserverHooks
	stateListeners []StateListener
	// done は Registry が持つ background goroutine（StartLazyWarmupLoop の warmup loop 等）
	// に終了を伝える channel。Close で close される。Run/main の lifecycle に紐付けたい
	// 長寿命 loop はここを listen する想定。
	done          chan struct{}
	closed        bool
	warmupStarted bool              // StartLazyWarmupLoop の冪等ガード（1 Registry につき loop は最大 1 本）
	wg            sync.WaitGroup    // warmup / persist goroutine の終了を Close で待つ
	persistReq    chan []StoreEntry // 容量 1 の coalescing channel。persistLocked が enqueue、writer goroutine が Save
}

type entry struct {
	Process     *Process
	Label       string
	WorkDir     string
	AgentName   string
	Model       string
	SessionID   string
	Cols        int
	AltRows     int
	State       terminal.SessionState
	StateSince  time.Time
	StateSource string
}

// NewRegistry は永続化なしの Registry を返す。agents は RestoreFromStore が
// Agent 名を解決するために使う（restore を使わないなら nil 可）。
func NewRegistry(workDir string, agents *terminal.AgentRegistry) *Registry {
	return NewRegistryWithStore(workDir, agents, nil)
}

// NewRegistryWithStore は Store を伴う Registry を返す。
func NewRegistryWithStore(workDir string, agents *terminal.AgentRegistry, store *Store) *Registry {
	r := &Registry{
		processes:    make(map[string]*entry),
		sessionIndex: make(map[string]string),
		workDir:      workDir,
		agents:       agents,
		store:        store,
		done:         make(chan struct{}),
	}
	if store != nil {
		r.persistReq = make(chan []StoreEntry, 1)
		r.wg.Add(1)
		go r.persistLoop()
	}
	return r
}

// persistLoop は persistReq から最新スナップショットを受けて store へ書く専用 goroutine。
// Close (r.done close) で、保留中の最後の 1 件を flush してから終了する。
func (r *Registry) persistLoop() {
	defer r.wg.Done()
	for {
		select {
		case <-r.done:
			// shutdown: 保留があれば最後に 1 回だけ書いて終了（最新状態を永続化）。
			select {
			case snap := <-r.persistReq:
				_ = r.store.Save(snap)
			default:
			}
			return
		case snap := <-r.persistReq:
			_ = r.store.Save(snap)
		}
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
			r.bindObserverCallbacksLocked(e.Process, id)
		}
	}
	r.mu.Unlock()
}

func (r *Registry) bindObserverCallbacksLocked(p *Process, id string) {
	if r.observer == nil {
		return
	}
	obs := r.observer
	termID := id
	p.SetOnInput(func(data []byte) { obs.OnInput(termID, data) })
	p.SetOnOutput(func(data []byte) { obs.OnOutput(termID, data) })
}

// Start は id に対応する Process を起動して返す。既に Running なら再利用する
// （cols/altRows>0 のときだけ resize）。起動バイナリ/引数は ag.Invocation(req) が
// 組み立てる。wrap 固有の初期サイズは Process.SetInitialSize 経由で渡す。
func (r *Registry) Start(id, label string, ag terminal.Agent, req terminal.RunRequest, cols, altRows int) (*Process, error) {
	if id == "" {
		return nil, errors.New("id is required")
	}
	if ag == nil {
		return nil, errors.New("agent is required")
	}
	binary, args := ag.Invocation(req)
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.reuseExistingLocked(id, label, cols, altRows); ok {
		return e, nil
	}
	p := NewProcess(r.workDir, binary, args...)
	p.SetInitialSize(cols, altRows)
	r.bindObserverCallbacksLocked(p, id)
	ent := &entry{
		Process:     p,
		Label:       label,
		WorkDir:     r.workDir,
		AgentName:   ag.Name(),
		Model:       req.Model,
		Cols:        cols,
		AltRows:     altRows,
		State:       terminal.StateIdle,
		StateSince:  time.Now(),
		StateSource: "init",
	}
	// resume 起動なら session 識別子は既知。即設定して /terminal/list へ反映し逆引きも張る。
	if req.Resume != "" {
		ent.SessionID = req.Resume
		r.sessionIndex[req.Resume] = id
	}
	// stop は SetOnExit でクローズし、session 検出ウォッチャを停止させる。
	stop := make(chan struct{})
	// SetOnExit BEFORE Start: prevents a race where the process exits immediately
	// (before SetOnExit is called after Start) leaving a ghost entry in the registry.
	// Also use removeIfSame so a stale onExit closure cannot delete a new entry
	// registered under the same id after Stop+Start.
	p.SetOnExit(func() { close(stop); r.removeIfSame(id, ent) })
	if err := p.Start(); err != nil {
		return nil, err
	}
	r.processes[id] = ent
	r.persistLocked()
	// fresh 起動かつ Agent が SessionDetector なら、新規セッション識別子を検出して紐付ける。
	if req.Resume == "" {
		if _, ok := ag.(terminal.SessionDetector); ok {
			go terminal.WatchNewSession(ag, r.workDir,
				func(s string) bool { return r.IDBySessionID(s) != "" },
				func(s string) { r.SetSessionID(id, s) },
				stop)
		}
	}
	return p, nil
}

// reuseExistingLocked は r.mu 保持前提で「id が既に running なら label 更新 +
// cols/altRows>0 のときだけ resize」して既存 Process を返す。第 2 戻り値 false なら新規生成。
func (r *Registry) reuseExistingLocked(id, label string, cols, altRows int) (*Process, bool) {
	e, ok := r.processes[id]
	if !ok || e.Process == nil || !e.Process.Running() {
		return nil, false
	}
	changed := false
	if label != "" && label != e.Label {
		e.Label = label
		changed = true
	}
	if cols > 0 || altRows > 0 {
		c := cols
		if c <= 0 {
			c = e.Cols
		}
		ar := altRows
		if ar <= 0 {
			ar = e.AltRows
		}
		_ = e.Process.Resize(c, ar)
		if cols > 0 && e.Cols != cols {
			e.Cols = cols
			changed = true
		}
		if altRows > 0 && e.AltRows != altRows {
			e.AltRows = altRows
			changed = true
		}
	}
	if changed {
		r.persistLocked()
	}
	return e.Process, true
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

// Find は id の Process と Label を返す（なければ ok=false）。
func (r *Registry) Find(id string) (*Process, string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.processes[id]
	if !ok || e.Process == nil {
		return nil, "", false
	}
	return e.Process, e.Label, true
}

// SetSessionID は id のエントリにセッション識別子を紐付け、逆引き index を更新し永続化する。
func (r *Registry) SetSessionID(id, sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.processes[id]
	if !ok {
		return
	}
	if e.SessionID != "" {
		delete(r.sessionIndex, e.SessionID)
	}
	e.SessionID = sessionID
	if sessionID != "" {
		r.sessionIndex[sessionID] = id
	}
	r.persistLocked()
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
// source は "hook"|"pty"|"init"。同じ state でも source が変われば通知する。
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
func (r *Registry) Stop(id string) error {
	r.mu.Lock()
	e, ok := r.processes[id]
	if ok {
		if e.SessionID != "" {
			delete(r.sessionIndex, e.SessionID)
		}
		delete(r.processes, id)
		r.persistLocked()
	}
	obs := r.observer
	r.mu.Unlock()
	if !ok {
		return nil
	}
	if obs != nil {
		obs.Forget(id)
	}
	if e.Process == nil {
		return nil
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
		r.persistLocked()
	}
	obs := r.observer
	r.mu.Unlock()
	if ok && obs != nil {
		obs.Forget(id)
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
	r.persistLocked()
	obs := r.observer
	r.mu.Unlock()
	if obs != nil {
		obs.Forget(id)
	}
}

// persistLocked は r.mu 保持前提で現在の全 entry のスナップショットを構築し、
// writer goroutine (persistLoop) へ非同期で渡す。実 disk I/O は r.mu の外で行われる。
// store=nil なら no-op。容量 1 channel を coalescing して、バースト時は最新だけ書く。
func (r *Registry) persistLocked() {
	if r.store == nil {
		return
	}
	out := make([]StoreEntry, 0, len(r.processes))
	for id, e := range r.processes {
		out = append(out, toStoreEntry(id, e))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	// Close 後は persistLoop が既に終了しているため enqueue しても誰も読まず取りこぼす。
	// shutdown 中の最終操作（Stop/Remove 経由）も確実に永続化するため同期 Save に
	// フォールバックする（R2）。r.closed は r.mu 下でのみ更新され、本関数も常に
	// r.mu 下で呼ばれるので判定は安全。disk I/O を mu 下で行うが Close 後の低頻度経路に限る。
	if r.closed {
		if err := r.store.Save(out); err != nil {
			log.Printf("terminal/wrap: post-close persist failed: %v", err)
		}
		return
	}
	// 非ブロッキング coalescing enqueue。persistLocked は常に r.mu 下で呼ばれるため
	// producer は 1 つだけ。channel が埋まっていれば古いスナップショットを捨てて最新で置く。
	select {
	case r.persistReq <- out:
	default:
		select {
		case <-r.persistReq:
		default:
		}
		r.persistReq <- out
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
			Running:   e.Process != nil && e.Process.Running(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// resolveAgent は name から Agent を解決する。name が空 / 未登録なら既定 Agent。
// agents 未設定（nil）なら nil を返す。
func (r *Registry) resolveAgent(name string) terminal.Agent {
	if r.agents == nil {
		return nil
	}
	if name != "" {
		if ag := r.agents.Resolve(name); ag != nil {
			return ag
		}
	}
	return r.agents.Default()
}

// RestoreFromStore は Store に保存された entry を読み出し、Agent を名前で解決して
// Agent.Invocation(RunRequest{Resume,Model}) で起動コマンドを再構成し再起動する。
// SessionID を持つ entry は、canResume が非 nil かつ false を返す場合は skip する
// （resume 不能なセッションの復元失敗を避ける。claude なら jsonl 存在チェック等を渡す）。
// canResume が nil なら常に resume を試みる。戻り値は (復元できた件数, store 総件数)。
func (r *Registry) RestoreFromStore(canResume func(rec terminal.SessionRecord) bool) (restored int, total int) {
	if r.store == nil {
		return 0, 0
	}
	entries, err := r.store.Load()
	if err != nil || len(entries) == 0 {
		return 0, 0
	}
	total = len(entries)
	for _, e := range entries {
		ag := r.resolveAgent(e.Agent)
		if ag == nil {
			log.Printf("terminal/wrap restore: skip id=%s (unknown agent %q)", e.ID, e.Agent)
			continue
		}
		req := terminal.RunRequest{Model: e.Model}
		if e.SessionID != "" {
			if canResume != nil && !canResume(e) {
				log.Printf("terminal/wrap restore: skip id=%s (cannot resume %s)", e.ID, e.SessionID)
				continue
			}
			req.Resume = e.SessionID
		}
		binary, args := ag.Invocation(req)
		workDir := e.WorkDir
		if workDir == "" {
			workDir = r.workDir
		}
		r.mu.Lock()
		if _, ok := r.processes[e.ID]; ok {
			r.mu.Unlock()
			continue
		}
		p := NewProcess(workDir, binary, args...)
		p.SetInitialSize(e.Cols, e.AltRows)
		id := e.ID
		r.bindObserverCallbacksLocked(p, id)
		ent := newEntryFromStore(e, workDir)
		ent.Process = p
		ent.State = terminal.StateIdle
		ent.StateSince = time.Now()
		ent.StateSource = "init"
		// SetOnExit BEFORE Start (same reasoning as Registry.Start).
		p.SetOnExit(func() { r.removeIfSame(id, ent) })
		if err := p.Start(); err != nil {
			r.mu.Unlock()
			log.Printf("terminal/wrap restore: id=%s start failed: %v", id, err)
			continue
		}
		r.processes[id] = ent
		if e.SessionID != "" {
			r.sessionIndex[e.SessionID] = id
		}
		r.mu.Unlock()
		restored++
	}
	// 復元結果を 1 回だけ永続化する（成功登録の最新スナップショット + skip された
	// stale entry の除去をまとめて反映）。ループ内で per-entry に呼ぶと O(N) 回の
	// enqueue になるため末尾 1 回に集約する。
	r.mu.Lock()
	r.persistLocked()
	r.mu.Unlock()
	return restored, total
}

// Close は Registry が持つ background goroutine（warmup loop 等）に終了を通知し、
// 終了を待つ。冪等。呼び出し元は cmd の graceful shutdown とテスト。
func (r *Registry) Close() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	close(r.done)
	r.mu.Unlock()
	r.wg.Wait() // warmup goroutine が StartNextPending を抜けるまで待つ
}

// doneForTest はテスト用に done channel を返す（実 API ではない）。
func (r *Registry) doneForTest() <-chan struct{} { return r.done }
