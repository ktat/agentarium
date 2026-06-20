package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTempStore(t *testing.T) (*Store, string, string) {
	t.Helper()
	dir := t.TempDir()
	data := filepath.Join(dir, "settings.json")
	key := filepath.Join(dir, "secret.key")
	s, err := NewStore(data, key)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s, data, key
}

func TestStore_PlainRoundTrip(t *testing.T) {
	s, _, _ := newTempStore(t)
	if err := s.Set("a.x", "hello"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if v, ok := s.Get("a.x"); !ok || v != "hello" {
		t.Fatalf("get = %q,%v", v, ok)
	}
}

func TestStore_SecretEncryptedOnDisk(t *testing.T) {
	s, data, _ := newTempStore(t)
	if err := s.SetSecret("a.token", "p@ss"); err != nil {
		t.Fatalf("setsecret: %v", err)
	}
	if v, ok := s.Get("a.token"); !ok || v != "p@ss" {
		t.Fatalf("get secret = %q,%v", v, ok)
	}
	b, _ := os.ReadFile(data)
	if strings.Contains(string(b), "p@ss") {
		t.Fatalf("plaintext secret on disk: %s", b)
	}
	if !strings.Contains(string(b), "enc:v1:") {
		t.Fatalf("expected enc tag on disk: %s", b)
	}
}

func TestStore_KeyFileCreated0600(t *testing.T) {
	_, _, key := newTempStore(t)
	info, err := os.Stat(key)
	if err != nil {
		t.Fatalf("key file missing: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key file perm = %o, want 600", info.Mode().Perm())
	}
}

func TestStore_ReopenDecrypts(t *testing.T) {
	s, data, key := newTempStore(t)
	_ = s.SetSecret("a.token", "keepme")
	s2, err := NewStore(data, key)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if v, ok := s2.Get("a.token"); !ok || v != "keepme" {
		t.Fatalf("reopened get = %q,%v", v, ok)
	}
}

func TestStore_WrongKeyFileGivesUnset(t *testing.T) {
	s, data, _ := newTempStore(t)
	_ = s.SetSecret("a.token", "keepme")
	other := filepath.Join(t.TempDir(), "other.key")
	s2, err := NewStore(data, other)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if v, ok := s2.Get("a.token"); ok {
		t.Fatalf("wrong key should yield unset, got %q,%v", v, ok)
	}
	_ = s.Set("a.plain", "v")
	s3, _ := NewStore(data, other)
	if v, ok := s3.Get("a.plain"); !ok || v != "v" {
		t.Fatalf("plain get = %q,%v", v, ok)
	}
}

func TestStore_HasAndDelete(t *testing.T) {
	s, _, _ := newTempStore(t)
	_ = s.SetSecret("a.t", "x")
	if !s.Has("a.t") {
		t.Fatal("Has should be true")
	}
	_ = s.Delete("a.t")
	if s.Has("a.t") {
		t.Fatal("Has should be false after delete")
	}
}

func TestStore_DataFile0600(t *testing.T) {
	s, data, _ := newTempStore(t)
	if err := s.Set("a.x", "v"); err != nil {
		t.Fatalf("set: %v", err)
	}
	info, err := os.Stat(data)
	if err != nil {
		t.Fatalf("data file missing: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("data file perm = %o, want 600", info.Mode().Perm())
	}
}

func TestNewStore_CorruptDataErrors(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(data, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := NewStore(data, filepath.Join(dir, "k.key")); err == nil {
		t.Fatal("corrupt data file should error")
	}
}

func TestScoped_Prefixes(t *testing.T) {
	s, _, _ := newTempStore(t)
	sc := Scope(s, "myplugin")
	_ = sc.Set("k", "v")
	if v, ok := s.Get("myplugin.k"); !ok || v != "v" {
		t.Fatalf("scoped set should write myplugin.k, got %q,%v", v, ok)
	}
	_ = sc.SetSecret("tok", "sec")
	if v, ok := sc.Get("tok"); !ok || v != "sec" {
		t.Fatalf("scoped secret round-trip = %q,%v", v, ok)
	}
}

func TestRekeyFile_MigratesSecrets(t *testing.T) {
	s, data, key := newTempStore(t)
	_ = s.SetSecret("a.tok", "v")
	_ = s.Set("a.plain", "p")
	n, err := RekeyFile(data, key, "", "newpepper", false)
	if err != nil {
		t.Fatalf("rekey: %v", err)
	}
	if n != 1 {
		t.Fatalf("reencrypted = %d want 1", n)
	}
	old := pepper
	pepper = ""
	if sOld, _ := NewStore(data, key); func() bool { _, ok := sOld.Get("a.tok"); return ok }() {
		pepper = old
		t.Fatal("old pepper should no longer decrypt")
	}
	pepper = "newpepper"
	sNew, _ := NewStore(data, key)
	if v, ok := sNew.Get("a.tok"); !ok || v != "v" {
		pepper = old
		t.Fatalf("new pepper decrypt = %q,%v", v, ok)
	}
	pepper = old
}

func TestStore_IsEncrypted(t *testing.T) {
	s, _, _ := newTempStore(t)
	_ = s.SetSecret("a.tok", "x")
	_ = s.Set("a.plain", "y")
	if !s.IsEncrypted("a.tok") {
		t.Fatal("secret should be encrypted")
	}
	if s.IsEncrypted("a.plain") {
		t.Fatal("plain should not be encrypted")
	}
	if s.IsEncrypted("a.missing") {
		t.Fatal("missing should be false")
	}
}

func TestStore_Keys(t *testing.T) {
	s, _, _ := newTempStore(t)
	_ = s.Set("b.x", "1")
	_ = s.Set("a.y", "2")
	got := s.Keys()
	if len(got) != 2 || got[0] != "a.y" || got[1] != "b.x" {
		t.Fatalf("Keys = %v, want sorted [a.y b.x]", got)
	}
}
