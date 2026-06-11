package terminal

import (
	"bytes"
	"testing"
)

func TestRingBuffer_NotFull(t *testing.T) {
	r := NewRingBuffer(8)
	_, _ = r.Write([]byte("abc"))
	if got := r.Bytes(); !bytes.Equal(got, []byte("abc")) {
		t.Fatalf("want abc, got %q", got)
	}
}

func TestRingBuffer_Overwrite(t *testing.T) {
	r := NewRingBuffer(4)
	_, _ = r.Write([]byte("abcdef")) // 容量 4 を超え、古いものが上書きされる
	if got := r.Bytes(); !bytes.Equal(got, []byte("cdef")) {
		t.Fatalf("want cdef, got %q", got)
	}
}

func TestRingBuffer_ZeroSizeBecomesOne(t *testing.T) {
	r := NewRingBuffer(0)
	_, _ = r.Write([]byte("xy"))
	if got := r.Bytes(); !bytes.Equal(got, []byte("y")) {
		t.Fatalf("want y, got %q", got)
	}
}
