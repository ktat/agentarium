package slack

import "testing"

func TestMeta_Hidden(t *testing.T) {
	p := New(newTestStore(t))
	if !p.Meta().Hidden {
		t.Error("slack Meta().Hidden = false, want true（既定非表示・Settings から開く）")
	}
}
