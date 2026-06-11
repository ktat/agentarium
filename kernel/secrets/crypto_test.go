package secrets

import "testing"

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	key := deriveKey("", "passphrase-abc")
	enc, err := encryptValue(key, "s3cr3t")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !isEncrypted(enc) {
		t.Fatalf("expected enc prefix, got %q", enc)
	}
	if enc == "s3cr3t" || containsPlain(enc, "s3cr3t") {
		t.Fatalf("ciphertext leaks plaintext: %q", enc)
	}
	got, err := decryptValue(key, enc)
	if err != nil || got != "s3cr3t" {
		t.Fatalf("decrypt = %q,%v want s3cr3t", got, err)
	}
}

func TestDecrypt_WrongKeyFails(t *testing.T) {
	enc, _ := encryptValue(deriveKey("", "pass-1"), "x")
	if _, err := decryptValue(deriveKey("", "pass-2"), enc); err == nil {
		t.Fatal("decrypt with wrong key should fail")
	}
}

func TestDeriveKey_PepperMatters(t *testing.T) {
	if deriveKey("p1", "pass") == deriveKey("p2", "pass") {
		t.Fatal("different pepper must yield different key")
	}
}

func TestRekey_ReencryptsEncryptedOnly(t *testing.T) {
	oldK := deriveKey("", "old")
	newK := deriveKey("", "new")
	enc, _ := encryptValue(oldK, "v")
	m := map[string]string{"a.secret": enc, "a.plain": "keepme"}
	n := rekey(m, oldK, newK)
	if n != 1 {
		t.Fatalf("reencrypted = %d, want 1", n)
	}
	if m["a.plain"] != "keepme" {
		t.Fatal("plain value must be untouched")
	}
	if got, err := decryptValue(newK, m["a.secret"]); err != nil || got != "v" {
		t.Fatalf("after rekey newK decrypt = %q,%v", got, err)
	}
	if _, err := decryptValue(oldK, m["a.secret"]); err == nil {
		t.Fatal("after rekey oldK must not decrypt")
	}
}

func containsPlain(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) && stringIndex(haystack, needle) >= 0
}
func stringIndex(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
