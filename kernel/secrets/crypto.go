package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"strings"
)

// encPrefix は暗号化済み値の接頭辞（バージョン付き）。
const encPrefix = "enc:v1:"

// deriveKey は pepper と passphrase から 32byte の KEK を導出する。
// passphrase は高エントロピー乱数前提のため SHA-256 で十分（scrypt 不要）。
func deriveKey(pepper, passphrase string) [32]byte {
	return sha256.Sum256([]byte(pepper + "\x00" + passphrase))
}

func isEncrypted(stored string) bool { return strings.HasPrefix(stored, encPrefix) }

// encryptValue は平文を AES-256-GCM で暗号化し "enc:v1:"+base64(nonce‖ct) を返す。
func encryptValue(key [32]byte, plain string) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return encPrefix + base64.StdEncoding.EncodeToString(ct), nil
}

// decryptValue は encryptValue の出力を復号する。タグ不一致・鍵不一致は error。
func decryptValue(key [32]byte, stored string) (string, error) {
	if !isEncrypted(stored) {
		return "", errors.New("secrets: value is not encrypted")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, encPrefix))
	if err != nil {
		return "", err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	ns := gcm.NonceSize()
	if len(raw) < ns {
		return "", errors.New("secrets: ciphertext too short")
	}
	plain, err := gcm.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func newGCM(key [32]byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// rekey は map 内の暗号化済み値だけを oldKey→newKey で再暗号化し、件数を返す。
// 平文値は触らない。復号に失敗した値は据え置く（防御的）。
func rekey(m map[string]string, oldKey, newKey [32]byte) int {
	n := 0
	for k, v := range m {
		if !isEncrypted(v) {
			continue
		}
		plain, err := decryptValue(oldKey, v)
		if err != nil {
			continue
		}
		enc, err := encryptValue(newKey, plain)
		if err != nil {
			continue
		}
		m[k] = enc
		n++
	}
	return n
}
