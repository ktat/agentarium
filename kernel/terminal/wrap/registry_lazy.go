package wrap

import (
	"errors"
	"log"
	"sort"
	"time"

	"github.com/ktat/agentarium/kernel/terminal"
)

// このファイルは「遅延復元」(lazy restore) を実装する。
//
// RestoreFromStore (eager, registry.go) は起動時に全 entry のプロセスを一斉に
// spawn するため、各 TUI の履歴再描画が重なって CPU / メモリのスパイクになる。
// 遅延復元では entry を Process 未起動（entry.Process == nil = pending）のまま
// 登録し、
//   - タブが開かれた（WS subscribe）タイミングで EnsureStarted が起動する
//   - 誰も開かない entry も serve 側の warmup ループが StartNextPending で
//     一定間隔ごとに 1 件ずつ起動する
// ことで、復元コストを時間軸に分散する。pending entry は一覧 (List) に
// Running=false で載り、grid (emulator) も未確保なのでメモリも消費しない。

// RestoreFromStoreLazy は store の entry を起動せず pending として登録する。
// canResume の規約は RestoreFromStore と同じ（claude なら jsonl 存在チェック等を渡す）。
// 戻り値は (pending として登録できた件数, store の総件数)。
func (r *Registry) RestoreFromStoreLazy(canResume func(rec terminal.SessionRecord) bool) (pending int, total int) {
	if r.store == nil {
		return 0, 0
	}
	entries, err := r.store.Load()
	if err != nil || len(entries) == 0 {
		return 0, 0
	}
	total = len(entries)
	for _, e := range entries {
		if r.resolveAgent(e.Agent) == nil {
			log.Printf("terminal/wrap lazy restore: skip id=%s (unknown agent %q)", e.ID, e.Agent)
			continue
		}
		if e.SessionID != "" && canResume != nil && !canResume(e) {
			log.Printf("terminal/wrap lazy restore: skip id=%s (cannot resume %s)", e.ID, e.SessionID)
			continue
		}
		workDir := e.WorkDir
		if workDir == "" {
			workDir = r.workDir
		}
		r.mu.Lock()
		if _, ok := r.processes[e.ID]; ok {
			r.mu.Unlock()
			continue
		}
		ent := newEntryFromStore(e, workDir)
		// Process は nil のまま（pending）。lazy warmup が後で起動する。
		ent.State = terminal.StatePending
		ent.StateSince = time.Now()
		ent.StateSource = "init"
		r.processes[e.ID] = ent
		if e.SessionID != "" {
			r.sessionIndex[e.SessionID] = e.ID
		}
		r.mu.Unlock()
		pending++
	}
	// 復元結果を 1 回だけ永続化する（成功登録の最新スナップショット + skip された
	// stale entry の除去をまとめて反映）。ループ内で per-entry に呼ぶと O(N) 回の
	// enqueue になるため末尾 1 回に集約する。
	r.mu.Lock()
	r.persistLocked()
	r.mu.Unlock()
	return pending, total
}

// errPendingNoAgent は startPendingLocked が agent を解決できなかったときに返す。
// RestoreFromStoreLazy が登録時にチェック済みなので原則発生しないが、agents が
// 途中で nil 化された等の稀ケース用の最終防御。
var errPendingNoAgent = errors.New("pending entry: agent could not be resolved")

// errClosed は Registry が Close 済みのとき startPendingLocked が返す。
var errClosed = errors.New("registry closed")

// EnsureStarted は id の entry が pending なら起動してから返す。
// 起動済みなら既存 Process を返す（Find + 遅延起動）。WS attach 経路で使う。
// 起動に失敗した pending entry は登録から外す（永久 retry 防止）。
func (r *Registry) EnsureStarted(id string) (*Process, string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.processes[id]
	if !ok {
		return nil, "", false
	}
	if e.Process != nil {
		return e.Process, e.Label, true
	}
	if err := r.startPendingLocked(id, e); err != nil {
		log.Printf("terminal/wrap lazy start: id=%s failed: %v", id, err)
		r.removePendingLocked(id, e)
		return nil, "", false
	}
	return e.Process, e.Label, true
}

// startPendingLocked は pending entry の Process を生成・起動する（r.mu 保持前提）。
// entry には登録時に保存された AgentName / Model / SessionID / WorkDir / Cols / AltRows
// が入っており、それらから RunRequest と Agent を再構成して起動する。
func (r *Registry) startPendingLocked(id string, e *entry) error {
	if r.closed {
		return errClosed
	}
	ag := r.resolveAgent(e.AgentName)
	if ag == nil {
		return errPendingNoAgent
	}
	req := terminal.RunRequest{Model: e.Model}
	if e.SessionID != "" {
		req.Resume = e.SessionID
	}
	binary, args := ag.Invocation(req)
	p := NewProcess(e.WorkDir, binary, args...)
	p.SetInitialSize(e.Cols, e.AltRows)
	r.bindObserverCallbacksLocked(p, id)
	// SetOnExit BEFORE Start: prevents race where process exits immediately
	// (before SetOnExit runs after Start) leaving a ghost entry.
	// Use removeIfSame so a stale closure cannot delete a new entry registered
	// under the same id after Stop+Start.
	p.SetOnExit(func() { r.removeIfSame(id, e) })
	if err := p.Start(); err != nil {
		return err
	}
	e.Process = p
	e.State = terminal.StateIdle
	e.StateSince = time.Now()
	e.StateSource = "init"
	return nil
}

// removePendingLocked は起動に失敗した pending entry を登録から外す（r.mu 保持前提）。
func (r *Registry) removePendingLocked(id string, e *entry) {
	if e.SessionID != "" {
		delete(r.sessionIndex, e.SessionID)
	}
	delete(r.processes, id)
	r.persistLocked()
}

// StartNextPending は pending entry を ID 昇順で 1 件だけ起動する。
// 起動できたら (id, true)。pending が残っていなければ ("", false)。
// 起動に失敗した entry は登録から外して次の候補へ進む（永久 retry 防止）。
func (r *Registry) StartNextPending() (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.processes))
	for id, e := range r.processes {
		if e.Process == nil {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		e := r.processes[id]
		if err := r.startPendingLocked(id, e); err != nil {
			log.Printf("terminal/wrap warmup: id=%s start failed: %v", id, err)
			r.removePendingLocked(id, e)
			continue
		}
		return id, true
	}
	return "", false
}

// StartLazyWarmupLoop は pending entry を interval 間隔で 1 件ずつ起動する
// goroutine を立ち上げる。pending が無くなれば自然終了、Registry.Close で
// done channel が closed されても終了する。重複起動を避けるため Registry に
// 紐づく warmup loop は最大 1 本だけ走らせる（warmupStarted フラグ）。
func (r *Registry) StartLazyWarmupLoop(interval time.Duration) {
	r.mu.Lock()
	if r.warmupStarted || r.closed {
		r.mu.Unlock()
		return
	}
	r.warmupStarted = true
	r.wg.Add(1)
	done := r.done
	r.mu.Unlock()

	go func() {
		defer r.wg.Done()
		// ループ終了（pending 枯渇 / done 受信）でガードを戻し、pending が
		// 再投入されたとき StartLazyWarmupLoop の再呼び出しで warmup を再開
		// できるようにする。
		defer func() {
			r.mu.Lock()
			r.warmupStarted = false
			r.mu.Unlock()
		}()
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				id, ok := r.StartNextPending()
				if !ok {
					return
				}
				log.Printf("terminal/wrap warmup: started %s", id)
			}
		}
	}()
}
