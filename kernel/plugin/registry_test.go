package plugin

import "testing"

type fakePlugin struct {
	id    string
	order int
}

func (f fakePlugin) Meta() Meta {
	return Meta{ID: f.id, Title: f.id, Pane: PaneLeft, Order: f.order}
}

func TestRegister_OK(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(fakePlugin{id: "a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := len(r.Plugins()); got != 1 {
		t.Fatalf("want 1 plugin, got %d", got)
	}
}

func TestRegister_EmptyID(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(fakePlugin{id: ""}); err == nil {
		t.Fatal("want error for empty ID, got nil")
	}
}

func TestRegister_DuplicateID(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(fakePlugin{id: "a"})
	if err := r.Register(fakePlugin{id: "a"}); err == nil {
		t.Fatal("want error for duplicate ID, got nil")
	}
}

func TestPlugins_SortedByOrderThenID(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(fakePlugin{id: "b", order: 1})
	_ = r.Register(fakePlugin{id: "a", order: 1})
	_ = r.Register(fakePlugin{id: "c", order: 0})
	got := r.Plugins()
	want := []string{"c", "a", "b"} // order 0 first; tie broken by ID
	for i, p := range got {
		if p.Meta().ID != want[i] {
			t.Fatalf("position %d: want %s, got %s", i, want[i], p.Meta().ID)
		}
	}
}

type routedPlugin struct {
	id     string
	routes []Route
}

func (p routedPlugin) Meta() Meta      { return Meta{ID: p.id, Title: p.id, Pane: PaneLeft} }
func (p routedPlugin) Routes() []Route { return p.routes }

func TestRegister_RejectsInvalidID(t *testing.T) {
	bad := []string{"Bad", "a b", "a/b", "a.b", "{x}", "-lead", "_lead", ""}
	for _, id := range bad {
		r := NewRegistry()
		if err := r.Register(fakePlugin{id: id}); err == nil {
			t.Errorf("id %q should be rejected", id)
		}
	}
}

func TestRegister_AcceptsValidID(t *testing.T) {
	good := []string{"a", "sessions", "my-tab", "tab_2", "x9"}
	for _, id := range good {
		r := NewRegistry()
		if err := r.Register(fakePlugin{id: id}); err != nil {
			t.Errorf("id %q should be accepted, got %v", id, err)
		}
	}
}

func TestRegister_RejectsBadRoutePath(t *testing.T) {
	r := NewRegistry()
	err := r.Register(routedPlugin{id: "p", routes: []Route{{Method: "GET", Path: "noslash"}}})
	if err == nil {
		t.Fatal("Route.Path without leading slash should be rejected")
	}
}

// TestRegister_RejectsUnsafeRoutePath は ServeMux パターンを壊す/曖昧にする文字
// （`{`/`}`/空白）を含む Path を弾くことを検証する（R3）。
func TestRegister_RejectsUnsafeRoutePath(t *testing.T) {
	bad := []string{"/a{id}", "/a}", "/a b", "/a\tb", "/{wild}", "/a\n"}
	for _, p := range bad {
		r := NewRegistry()
		if err := r.Register(routedPlugin{id: "p", routes: []Route{{Method: "GET", Path: p}}}); err == nil {
			t.Errorf("path %q should be rejected", p)
		}
	}
}

func TestRegister_AcceptsGoodRoutePath(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(routedPlugin{id: "p", routes: []Route{{Method: "GET", Path: "/list"}}}); err != nil {
		t.Fatalf("valid route should be accepted: %v", err)
	}
}

func TestRegister_RejectsReservedIDs(t *testing.T) {
	for _, id := range []string{"secret", "kernel"} {
		r := NewRegistry()
		err := r.Register(fakePlugin{id: id})
		if err == nil {
			t.Fatalf("id %q should be rejected as reserved", id)
		}
	}
}

func TestSetOrder_OverridesSortPosition(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(fakePlugin{id: "chat", order: 0})
	_ = r.Register(fakePlugin{id: "topics", order: 20})
	r.SetOrder("chat", 25) // chat を topics の後ろへ
	got := r.Plugins()
	want := []string{"topics", "chat"}
	for i, p := range got {
		if p.Meta().ID != want[i] {
			t.Fatalf("position %d: want %s, got %s", i, want[i], p.Meta().ID)
		}
	}
}

func TestEffectiveOrder(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(fakePlugin{id: "topics", order: 20})
	r.SetOrder("chat", 25)
	if got := r.EffectiveOrder("chat"); got != 25 { // override（未登録でも保持）
		t.Errorf("chat: want 25, got %d", got)
	}
	if got := r.EffectiveOrder("topics"); got != 20 { // override 無し → Meta.Order
		t.Errorf("topics: want 20, got %d", got)
	}
	if got := r.EffectiveOrder("nope"); got != 0 { // 未登録かつ override 無し
		t.Errorf("nope: want 0, got %d", got)
	}
}
