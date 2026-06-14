package xterm

import (
	"errors"
	"log"
	"sort"
	"time"

	"github.com/ktat/agentarium/kernel/terminal"
)

// このファイルは xterm backend の「遅延復元」(lazy restore) を実装する。
// 設計と理由は wrap/registry_lazy.go と同一: 起動時に全 entry を一斉 spawn せず
// pending（Process==nil）で登録し、WS attach (EnsureStarted) か warmup ループが
// 1 件ずつ起動して復元コストを時間軸に分散する。xterm は xterm.js 側でサイズ管理
// するため、復元時の Cols/AltRows は使わない（attach 時の resize に委ねる）。

// errPendingNoAgent は startPendingLocked が agent を解決できなかったときに返す。
var errPendingNoAgent = errors.New("pending entry: agent could not be resolved")

// errClosed は Registry が Close 済みのとき startPendingLocked が返す。
var errClosed = errors.New("registry closed")

// RestoreFromStoreLazy は store の entry を起動せず pending として登録する。
// SessionID を持つ entry は canResume が false なら skip。戻り値は (pending 件数, 総件数)。
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
			log.Printf("terminal/xterm lazy restore: skip id=%s (unknown agent %q)", e.ID, e.Agent)
			continue
		}
		if e.SessionID != "" && canResume != nil && !canResume(e) {
			log.Printf("terminal/xterm lazy restore: skip id=%s (cannot resume %s)", e.ID, e.SessionID)
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
		ent := &entry{
			Label:       e.Label,
			AgentName:   e.Agent,
			WorkDir:     workDir,
			Model:       e.Model,
			SessionID:   e.SessionID,
			Cols:        e.Cols,
			AltRows:     e.AltRows,
			State:       terminal.StatePending,
			StateSince:  time.Now(),
			StateSource: "init",
		}
		r.processes[e.ID] = ent
		if e.SessionID != "" {
			r.sessionIndex[e.SessionID] = e.ID
		}
		r.mu.Unlock()
		pending++
	}
	// 復元結果を 1 回だけ永続化（成功登録 + skip された stale entry の除去を反映）。
	r.persist()
	return pending, total
}

// EnsureStarted は id の entry が pending なら起動してから返す。起動済みなら既存を返す。
// 起動に失敗した pending は登録から外す（永久 retry 防止）。WS attach 経路で使う。
func (r *Registry) EnsureStarted(id string) (*Process, bool) {
	r.mu.Lock()
	e, ok := r.processes[id]
	if !ok {
		r.mu.Unlock()
		return nil, false
	}
	if e.Process != nil {
		p := e.Process
		r.mu.Unlock()
		return p, true
	}
	if err := r.startPendingLocked(id, e); err != nil {
		r.removePendingLocked(id, e)
		r.mu.Unlock()
		log.Printf("terminal/xterm lazy start: id=%s failed: %v", id, err)
		r.persist()
		return nil, false
	}
	p := e.Process
	r.mu.Unlock()
	return p, true
}

// startPendingLocked は pending entry の Process を生成・起動する（r.mu 保持前提）。
// SessionRecord 由来の AgentName/Model/SessionID から RunRequest と Agent を再構成する。
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
	r.bindObserverCallbacks(p, id)
	// SetOnExit BEFORE Start: 即時終了による ghost entry を防ぐ。removeIfSame で
	// Stop+Start の race も防ぐ。
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

// removePendingLocked は起動に失敗した pending entry を map から外す（r.mu 保持前提）。
// 永続化は呼び出し側が mu 解放後に行う。
func (r *Registry) removePendingLocked(id string, e *entry) {
	if e.SessionID != "" {
		delete(r.sessionIndex, e.SessionID)
	}
	delete(r.processes, id)
}

// StartNextPending は pending entry を ID 昇順で 1 件だけ起動する。
// 起動できたら (id, true)。pending が無ければ ("", false)。失敗 entry は外して次へ。
func (r *Registry) StartNextPending() (string, bool) {
	r.mu.Lock()
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
			r.removePendingLocked(id, e)
			log.Printf("terminal/xterm warmup: id=%s start failed: %v", id, err)
			continue
		}
		r.mu.Unlock()
		return id, true
	}
	r.mu.Unlock()
	return "", false
}

// StartLazyWarmupLoop は pending entry を interval 間隔で 1 件ずつ起動する goroutine を
// 立ち上げる。pending が尽きるか Close で終了。1 Registry につき loop は最大 1 本。
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
				log.Printf("terminal/xterm warmup: started %s", id)
			}
		}
	}()
}
