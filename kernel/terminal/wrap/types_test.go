package wrap

import "testing"

func TestVirtualRows_DefaultAndOverride(t *testing.T) {
	if got := VirtualRows(); got != 5000 {
		t.Fatalf("default VirtualRows want 5000, got %d", got)
	}
	SetVirtualRows(8000)
	if got := VirtualRows(); got != 8000 {
		t.Fatalf("after set want 8000, got %d", got)
	}
	SetVirtualRows(VirtualRowsMin - 1)
	if got := VirtualRows(); got != 8000 {
		t.Fatalf("below-min ignored: want 8000, got %d", got)
	}
	SetVirtualRows(VirtualRowsMax + 1)
	if got := VirtualRows(); got != 8000 {
		t.Fatalf("above-max ignored: want 8000, got %d", got)
	}
	SetVirtualRows(5000)
}

func TestWSMessageZeroValue(t *testing.T) {
	var m WSMessage
	if m.Type != "" || len(m.Lines) != 0 {
		t.Fatalf("zero WSMessage unexpected: %+v", m)
	}
}
