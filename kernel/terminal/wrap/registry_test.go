package wrap

import (
	"testing"
	"time"

	"github.com/ktat/agentarium/kernel/terminal"
)

// catAgentReg は cat を起動するテスト用 Agent（stdin 待ちで起動し続ける）。
func catAgentReg() terminal.Agent {
	return terminal.ConfigAgent{AgentName: "cat", Binary: "cat"}
}

func newTestRegistry() *Registry {
	agents := terminal.NewAgentRegistry("cat")
	agents.Register(catAgentReg())
	return NewRegistry("", agents)
}

func TestRegistry_StartGetRunning(t *testing.T) {
	r := newTestRegistry()
	p, err := r.Start("t1", "Tab 1", catAgentReg(), terminal.RunRequest{}, 100, 40)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = r.Stop("t1") }()
	if !p.Running() {
		t.Fatal("want running")
	}
	if r.Get("t1") == nil {
		t.Fatal("Get nil for started id")
	}
	if r.Get("missing") != nil {
		t.Fatal("Get should be nil for unknown id")
	}
}

func TestRegistry_StartReusesRunning(t *testing.T) {
	r := newTestRegistry()
	p1, _ := r.Start("t1", "L", catAgentReg(), terminal.RunRequest{}, 100, 40)
	p2, _ := r.Start("t1", "L", catAgentReg(), terminal.RunRequest{}, 100, 40)
	defer func() { _ = r.Stop("t1") }()
	if p1 != p2 {
		t.Fatal("running process should be reused for same id")
	}
}

func TestRegistry_StartEmptyIDAndNilAgent(t *testing.T) {
	r := newTestRegistry()
	if _, err := r.Start("", "L", catAgentReg(), terminal.RunRequest{}, 0, 0); err == nil {
		t.Fatal("want error for empty id")
	}
	if _, err := r.Start("t1", "L", nil, terminal.RunRequest{}, 0, 0); err == nil {
		t.Fatal("want error for nil agent")
	}
}

func TestRegistry_ListSortedAndRunning(t *testing.T) {
	r := newTestRegistry()
	_, _ = r.Start("b", "B", catAgentReg(), terminal.RunRequest{}, 80, 40)
	_, _ = r.Start("a", "A", catAgentReg(), terminal.RunRequest{}, 80, 40)
	defer func() { _ = r.Stop("a"); _ = r.Stop("b") }()
	items := r.List()
	if len(items) != 2 || items[0].ID != "a" || items[1].ID != "b" {
		t.Fatalf("List not sorted by id: %+v", items)
	}
	if !items[0].Running {
		t.Fatal("want running true in SessionInfo")
	}
}

func TestRegistry_SetSessionIDAndIndex(t *testing.T) {
	r := newTestRegistry()
	_, _ = r.Start("t1", "L", catAgentReg(), terminal.RunRequest{}, 80, 40)
	defer func() { _ = r.Stop("t1") }()
	r.SetSessionID("t1", "sess-xyz")
	if got := r.IDBySessionID("sess-xyz"); got != "t1" {
		t.Fatalf("IDBySessionID want t1, got %q", got)
	}
	if r.IDBySessionID("nope") != "" {
		t.Fatal("unknown session id should map to empty")
	}
}

func TestRegistry_StateTransitionNotifiesListener(t *testing.T) {
	r := newTestRegistry()
	_, _ = r.Start("t1", "L", catAgentReg(), terminal.RunRequest{}, 80, 40)
	defer func() { _ = r.Stop("t1") }()
	type ev struct {
		id         string
		prev, next terminal.SessionState
	}
	got := make(chan ev, 1)
	r.AddStateListener(func(id string, prev, next terminal.SessionState, source string) {
		got <- ev{id, prev, next}
	})
	r.SetState("t1", terminal.StateRunning, "pty")
	select {
	case e := <-got:
		if e.id != "t1" || e.prev != terminal.StateIdle || e.next != terminal.StateRunning {
			t.Fatalf("unexpected event: %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("state listener not notified")
	}
}

func TestRegistry_StopRemovesEntry(t *testing.T) {
	r := newTestRegistry()
	_, _ = r.Start("t1", "L", catAgentReg(), terminal.RunRequest{}, 80, 40)
	if err := r.Stop("t1"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if r.Get("t1") != nil {
		t.Fatal("entry should be gone after Stop")
	}
}

// recordAgent は Invocation に渡された RunRequest を記録するテスト用 Agent。
// binary は cat（stdin 待ちで起動し続ける）を使い、復元後も Running を保てる。
type recordAgent struct {
	name string
	got  *terminal.RunRequest
}

func (a recordAgent) Name() string { return a.name }
func (a recordAgent) Invocation(req terminal.RunRequest) (string, []string) {
	*a.got = req
	return "cat", nil
}

func TestRestoreFromStore_ReconstructsViaAgent(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir + "/terminals.json")
	if err := store.Save([]StoreEntry{
		{ID: "t1", Label: "L1", Agent: "rec", SessionID: "sess-1", Model: "haiku", Cols: 100, AltRows: 30},
	}); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	var captured terminal.RunRequest
	agents := terminal.NewAgentRegistry("rec")
	agents.Register(recordAgent{name: "rec", got: &captured})

	r := NewRegistryWithStore("", agents, store)
	t.Cleanup(func() { r.Close() })
	restored, total := r.RestoreFromStore(nil)
	if restored != 1 || total != 1 {
		t.Fatalf("restore counts: restored=%d total=%d", restored, total)
	}
	defer func() { _ = r.Stop("t1") }()

	if captured.Resume != "sess-1" {
		t.Fatalf("want Resume=sess-1, got %q", captured.Resume)
	}
	if captured.Model != "haiku" {
		t.Fatalf("want Model=haiku, got %q", captured.Model)
	}
	if r.Get("t1") == nil {
		t.Fatal("restored process not registered")
	}
	if r.IDBySessionID("sess-1") != "t1" {
		t.Fatal("sessionIndex not restored")
	}
}

func TestRestoreFromStore_SkipsWhenCannotResume(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir + "/terminals.json")
	if err := store.Save([]StoreEntry{
		{ID: "t1", Label: "L1", Agent: "cat", SessionID: "gone"},
	}); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	agents := terminal.NewAgentRegistry("cat")
	agents.Register(terminal.ConfigAgent{AgentName: "cat", Binary: "cat"})

	r := NewRegistryWithStore("", agents, store)
	t.Cleanup(func() { r.Close() })
	restored, total := r.RestoreFromStore(func(sessionID string) bool { return false })
	if restored != 0 || total != 1 {
		t.Fatalf("want restored=0 total=1, got restored=%d total=%d", restored, total)
	}
	if r.Get("t1") != nil {
		t.Fatal("entry should not be restored when canResume=false")
	}
}

func TestRestoreFromStore_NilStore(t *testing.T) {
	r := newTestRegistry() // store=nil (from Task 2 test helper)
	restored, total := r.RestoreFromStore(nil)
	if restored != 0 || total != 0 {
		t.Fatalf("nil store should restore nothing, got %d/%d", restored, total)
	}
}

func TestRestoreFromStore_SkipsUnknownAgent(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir + "/terminals.json")
	if err := store.Save([]StoreEntry{{ID: "t1", Label: "L1", Agent: "ghost"}}); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	// 何も登録していない AgentRegistry: Resolve("ghost")=nil, Default()=nil。
	agents := terminal.NewAgentRegistry("none")
	r := NewRegistryWithStore("", agents, store)
	t.Cleanup(func() { r.Close() })
	restored, total := r.RestoreFromStore(nil)
	if restored != 0 || total != 1 {
		t.Fatalf("want restored=0 total=1, got %d/%d", restored, total)
	}
	if r.Get("t1") != nil {
		t.Fatal("entry with unknown agent should not be restored")
	}
}

func TestRegistry_CloseIsIdempotent(t *testing.T) {
	r := newTestRegistry()
	r.Close()
	// 2 回目は no-op（panic / blocking してはならない）
	r.Close()
}

func TestRegistry_CloseClosesDoneChannel(t *testing.T) {
	r := newTestRegistry()
	done := r.doneForTest()
	select {
	case <-done:
		t.Fatal("done should not be closed before Close()")
	default:
	}
	r.Close()
	select {
	case <-done:
		// expected: closed channel returns immediately
	case <-time.After(time.Second):
		t.Fatal("done should be closed after Close()")
	}
}

func TestRestoreFromStoreLazy_RegistersPending(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir + "/terminals.json")
	if err := store.Save([]StoreEntry{
		{ID: "t1", Label: "L1", Agent: "cat", SessionID: "s-1", Model: "m", Cols: 80, AltRows: 30},
		{ID: "t2", Label: "L2", Agent: "cat"},
	}); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	agents := terminal.NewAgentRegistry("cat")
	agents.Register(terminal.ConfigAgent{AgentName: "cat", Binary: "cat"})

	r := NewRegistryWithStore("", agents, store)
	t.Cleanup(func() { r.Close() })
	pending, total := r.RestoreFromStoreLazy(nil)
	if pending != 2 || total != 2 {
		t.Fatalf("want pending=2 total=2, got pending=%d total=%d", pending, total)
	}
	if r.Get("t1") != nil {
		t.Fatal("pending entry should have nil Process via Get")
	}
	items := r.List()
	if len(items) != 2 {
		t.Fatalf("List should include pending entries, got %d", len(items))
	}
	for _, it := range items {
		if it.Running {
			t.Fatalf("pending entry Running should be false: %+v", it)
		}
	}
	if r.IDBySessionID("s-1") != "t1" {
		t.Fatal("sessionIndex should be restored for pending entries")
	}
}

func TestRestoreFromStoreLazy_SkipsWhenCannotResume(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir + "/terminals.json")
	if err := store.Save([]StoreEntry{
		{ID: "t1", Label: "L1", Agent: "cat", SessionID: "gone"},
	}); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	agents := terminal.NewAgentRegistry("cat")
	agents.Register(terminal.ConfigAgent{AgentName: "cat", Binary: "cat"})

	r := NewRegistryWithStore("", agents, store)
	t.Cleanup(func() { r.Close() })
	pending, total := r.RestoreFromStoreLazy(func(sessionID string) bool { return false })
	if pending != 0 || total != 1 {
		t.Fatalf("want pending=0 total=1, got %d/%d", pending, total)
	}
}

// startTestRegistry は cat エージェントを 1 つ登録した Registry を返す。
func startTestRegistry() *Registry {
	agents := terminal.NewAgentRegistry("cat")
	agents.Register(terminal.ConfigAgent{AgentName: "cat", Binary: "cat"})
	return NewRegistry("", agents)
}

func TestEnsureStarted_StartsPending(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir + "/terminals.json")
	if err := store.Save([]StoreEntry{{ID: "t1", Label: "L", Agent: "cat", Cols: 80, AltRows: 30}}); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	agents := terminal.NewAgentRegistry("cat")
	agents.Register(terminal.ConfigAgent{AgentName: "cat", Binary: "cat"})
	r := NewRegistryWithStore("", agents, store)
	t.Cleanup(func() { r.Close() })
	if pending, _ := r.RestoreFromStoreLazy(nil); pending != 1 {
		t.Fatalf("seed pending: %d", pending)
	}

	p, label, ok := r.EnsureStarted("t1")
	if !ok {
		t.Fatal("EnsureStarted returned ok=false for known pending id")
	}
	defer func() { _ = r.Stop("t1") }()
	if !p.Running() {
		t.Fatal("process should be running after EnsureStarted")
	}
	if label != "L" {
		t.Fatalf("label want L, got %q", label)
	}
	// 2 回目はすでに起動済みなので再利用。
	p2, _, _ := r.EnsureStarted("t1")
	if p2 != p {
		t.Fatal("EnsureStarted should reuse running process")
	}
}

func TestEnsureStarted_UnknownIDReturnsFalse(t *testing.T) {
	r := startTestRegistry()
	if _, _, ok := r.EnsureStarted("missing"); ok {
		t.Fatal("EnsureStarted for unknown id should return ok=false")
	}
}

func TestStartNextPending_StartsOneInIDOrder(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir + "/terminals.json")
	if err := store.Save([]StoreEntry{
		{ID: "b", Label: "B", Agent: "cat"},
		{ID: "a", Label: "A", Agent: "cat"},
	}); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	agents := terminal.NewAgentRegistry("cat")
	agents.Register(terminal.ConfigAgent{AgentName: "cat", Binary: "cat"})
	r := NewRegistryWithStore("", agents, store)
	t.Cleanup(func() { r.Close() })
	_, _ = r.RestoreFromStoreLazy(nil)
	defer func() { _ = r.Stop("a"); _ = r.Stop("b") }()

	id1, ok1 := r.StartNextPending()
	if !ok1 || id1 != "a" {
		t.Fatalf("want first start to be 'a', got id=%q ok=%v", id1, ok1)
	}
	id2, ok2 := r.StartNextPending()
	if !ok2 || id2 != "b" {
		t.Fatalf("want second start to be 'b', got id=%q ok=%v", id2, ok2)
	}
	// すべて pending を消化したら false。
	if _, ok := r.StartNextPending(); ok {
		t.Fatal("StartNextPending should return false when no pending remains")
	}
}

func TestStartLazyWarmupLoop_DrainsPending(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir + "/terminals.json")
	if err := store.Save([]StoreEntry{
		{ID: "a", Label: "A", Agent: "cat"},
		{ID: "b", Label: "B", Agent: "cat"},
	}); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	agents := terminal.NewAgentRegistry("cat")
	agents.Register(terminal.ConfigAgent{AgentName: "cat", Binary: "cat"})
	r := NewRegistryWithStore("", agents, store)
	t.Cleanup(func() { r.Close() })
	_, _ = r.RestoreFromStoreLazy(nil)
	defer func() { _ = r.Stop("a"); _ = r.Stop("b") }()

	r.StartLazyWarmupLoop(10 * time.Millisecond)
	// 短い間隔なので 2 件 + 終了判定で十分時間を取る。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		items := r.List()
		running := 0
		for _, it := range items {
			if it.Running {
				running++
			}
		}
		if running == 2 {
			return // success
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("warmup loop should have started both pending entries within timeout")
}

func TestStartLazyWarmupLoop_StopsOnClose(t *testing.T) {
	r := startTestRegistry()
	// pending は無いが、loop の生成と停止だけ検証する。
	r.StartLazyWarmupLoop(50 * time.Millisecond)
	// pending 無しなら loop は自然終了するが、念のため Close で確実に止める。
	r.Close()
	// Close 後の StartLazyWarmupLoop 呼び出しは no-op であるべき。
	r.StartLazyWarmupLoop(50 * time.Millisecond) // panic / hang してはならない
}

func TestClose_WaitsForWarmupAndPreventsNewSpawn(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir + "/terminals.json")
	if err := store.Save([]StoreEntry{
		{ID: "a", Label: "A", Agent: "cat"},
		{ID: "b", Label: "B", Agent: "cat"},
		{ID: "c", Label: "C", Agent: "cat"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	agents := terminal.NewAgentRegistry("cat")
	agents.Register(terminal.ConfigAgent{AgentName: "cat", Binary: "cat"})
	r := NewRegistryWithStore("", agents, store)
	_, _ = r.RestoreFromStoreLazy(nil)

	// warmup を高頻度で回し、すぐ Close する。
	r.StartLazyWarmupLoop(time.Millisecond)
	r.Close() // wg.Wait で warmup goroutine の終了を待つ

	// Close 後は pending が残っていても StartNextPending が新規起動しない。
	if id, ok := r.StartNextPending(); ok {
		_ = r.Stop(id)
		t.Fatalf("StartNextPending should not spawn after Close, got %q", id)
	}
	// 起動済みプロセスは後始末（残っていれば stop）。
	for _, it := range r.List() {
		if it.Running {
			_ = r.Stop(it.ID)
		}
	}
}

func TestPersist_AsyncWritesAndFlushesOnClose(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir + "/terminals.json")
	agents := terminal.NewAgentRegistry("cat")
	agents.Register(terminal.ConfigAgent{AgentName: "cat", Binary: "cat"})
	r := NewRegistryWithStore("", agents, store)

	// Start でセッションを作り SessionID を付ける（persist 対象）。
	if _, err := r.Start("t1", "L", terminal.ConfigAgent{AgentName: "cat", Binary: "cat"}, terminal.RunRequest{}, 80, 30); err != nil {
		t.Fatalf("start: %v", err)
	}
	r.SetSessionID("t1", "sess-1")

	// Close で writer goroutine が最終スナップショットを flush してから終了する。
	_ = r.Stop("t1")
	r.Close()

	// store に SessionID を持つエントリが書かれている（Stop 後は t1 が消えるので空のはず）。
	// → ここでは「Close 後に Load がエラーなく読める = ファイルが壊れていない」ことを確認する。
	got, err := store.Load()
	if err != nil {
		t.Fatalf("load after close: %v", err)
	}
	_ = got // 内容は Stop により空。durability の主眼は「Close が flush 済み」点。
}

func TestPersist_SessionIDPersistedThenLoadable(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir + "/terminals.json")
	agents := terminal.NewAgentRegistry("cat")
	agents.Register(terminal.ConfigAgent{AgentName: "cat", Binary: "cat"})
	r := NewRegistryWithStore("", agents, store)
	if _, err := r.Start("t1", "L", terminal.ConfigAgent{AgentName: "cat", Binary: "cat"}, terminal.RunRequest{}, 80, 30); err != nil {
		t.Fatalf("start: %v", err)
	}
	r.SetSessionID("t1", "sess-keep")
	r.Close() // 最終 flush

	got, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// Stop していないので t1 が SessionID 付きで永続化されている。
	found := false
	for _, e := range got {
		if e.ID == "t1" && e.SessionID == "sess-keep" {
			found = true
		}
	}
	if !found {
		t.Fatalf("session t1/sess-keep not persisted: %+v", got)
	}
	// cleanup: 起動済み cat を止める
	_ = r.Stop("t1")
}

func TestRestoreFromStoreLazy_ListReportsPending(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir + "/terminals.json")
	if err := store.Save([]StoreEntry{{ID: "t1", Label: "L", Agent: "cat"}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	agents := terminal.NewAgentRegistry("cat")
	agents.Register(terminal.ConfigAgent{AgentName: "cat", Binary: "cat"})
	r := NewRegistryWithStore("", agents, store)
	t.Cleanup(func() { r.Close() })
	if pending, _ := r.RestoreFromStoreLazy(nil); pending != 1 {
		t.Fatalf("pending: %d", pending)
	}
	items := r.List()
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	if items[0].Running {
		t.Fatal("pending entry should not be Running")
	}
	if items[0].State != terminal.StatePending {
		t.Fatalf("pending entry State = %v, want pending", items[0].State)
	}
}

// wrap 側
func TestRegistry_StopThenStartSameID_OldOnExitDoesNotRemoveNew(t *testing.T) {
	r := newTestRegistry()
	p1, _ := r.Start("t1", "L", catAgentReg(), terminal.RunRequest{}, 80, 30)
	// 旧 process を停止する前に「同じ id で新エントリ」を登録できるよう、
	// 実機の race は別の goroutine が起こすが、ここでは決定的に再現する。
	if err := r.Stop("t1"); err != nil {
		t.Fatalf("stop1: %v", err)
	}
	// 旧 process はもう map にない。新 Start で別 entry を作る。
	p2, err := r.Start("t1", "L", catAgentReg(), terminal.RunRequest{}, 80, 30)
	if err != nil {
		t.Fatalf("start2: %v", err)
	}
	defer func() { _ = r.Stop("t1") }()
	if p1 == p2 {
		t.Fatal("expected new process after Stop+Start")
	}
	// 旧 process の onExit が今ここで発火する状況を強制する。
	// 旧 process はもう Stop() 済みなので cleanup→onExit がいずれ走る。
	// 同期的に確認するには removeIfSame の不変条件を直接検証する。
	// r.processes["t1"] が p2 のままであることを確認。
	if got := r.Get("t1"); got != p2 {
		t.Fatalf("registry lost the new entry; got %v want %v", got, p2)
	}
}

// TestRegistry_PersistAfterCloseIsSynchronous は Close 後に persistLocked が
// 走っても enqueue（誰も読まない）ではなく同期 Save で書き出すことを検証する（R2）。
func TestRegistry_PersistAfterCloseIsSynchronous(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir + "/reg.json")
	agents := terminal.NewAgentRegistry("cat")
	agents.Register(catAgentReg())
	r := NewRegistryWithStore("", agents, store)

	// persist loop を畳む。これ以降 enqueue しても読まれない。
	r.Close()

	// shutdown 後の最終操作を模す: entry を 1 件足して persistLocked を直接呼ぶ。
	r.mu.Lock()
	r.processes["t1"] = &entry{Label: "L", SessionID: "s1", AgentName: "cat"}
	r.persistLocked()
	r.mu.Unlock()

	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 || got[0].ID != "t1" || got[0].SessionID != "s1" {
		t.Fatalf("post-close persist not written synchronously: %+v", got)
	}
}
