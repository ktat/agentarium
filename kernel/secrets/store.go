package secrets

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// pepper は build-time に -ldflags "-X github.com/ktat/agentarium/kernel/secrets.pepper=…"
// で埋め込む追加鍵素材。既定は空（OSS upstream にハードコード秘密を置かない）。
var pepper string

// Store は設定値の永続ストア。非 Secret は平文、Secret は AES-256-GCM 暗号化で持つ。
// 鍵は keyPath の per-install パスフレーズと build-time pepper から導出する。
type Store struct {
	mu       sync.Mutex
	dataPath string
	key      [32]byte
	m        map[string]string
}

// NewStore は dataPath（設定）と keyPath（鍵）を開く。keyPath が無ければランダム
// パスフレーズを生成して 0600 で作る。dataPath が壊れていれば error。
func NewStore(dataPath, keyPath string) (*Store, error) {
	pass, err := loadOrCreatePassphrase(keyPath)
	if err != nil {
		return nil, err
	}
	m, err := loadData(dataPath)
	if err != nil {
		return nil, err
	}
	return &Store{dataPath: dataPath, key: deriveKey(pepper, pass), m: m}, nil
}

func loadOrCreatePassphrase(keyPath string) (string, error) {
	if b, err := os.ReadFile(keyPath); err == nil {
		return string(b), nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	pass := base64.StdEncoding.EncodeToString(raw)
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(keyPath, []byte(pass), 0o600); err != nil {
		return "", err
	}
	return pass, nil
}

func loadData(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("secrets: corrupt data file %s: %w", path, err)
	}
	if m == nil {
		m = map[string]string{}
	}
	return m, nil
}

func saveData(path string, m map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Get は key の値を返す。enc: タグ付きなら復号する。復号失敗は未設定扱い ("",false)。
func (s *Store) Get(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[key]
	if !ok {
		return "", false
	}
	if isEncrypted(v) {
		plain, err := decryptValue(s.key, v)
		if err != nil {
			return "", false
		}
		return plain, true
	}
	return v, true
}

// Has は key の値が存在するか（Secret の set 判定用、復号しない）。
func (s *Store) Has(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.m[key]
	return ok
}

// Set は平文で保存し永続化する（非 Secret 項目）。
func (s *Store) Set(key, val string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = val
	return saveData(s.dataPath, s.m)
}

// SetSecret は値を暗号化して保存し永続化する（Secret 項目）。
func (s *Store) SetSecret(key, val string) error {
	enc, err := encryptValue(s.key, val)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = enc
	return saveData(s.dataPath, s.m)
}

func (s *Store) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, key)
	return saveData(s.dataPath, s.m)
}

// Scoped は prefix（plugin id）を自動付与する Store のビュー。plugin に注入する。
type Scoped struct {
	store  *Store
	prefix string
}

func Scope(s *Store, prefix string) *Scoped { return &Scoped{store: s, prefix: prefix} }

func (sc *Scoped) k(key string) string             { return sc.prefix + "." + key }
func (sc *Scoped) Get(key string) (string, bool)   { return sc.store.Get(sc.k(key)) }
func (sc *Scoped) Has(key string) bool             { return sc.store.Has(sc.k(key)) }
func (sc *Scoped) Set(key, val string) error       { return sc.store.Set(sc.k(key), val) }
func (sc *Scoped) SetSecret(key, val string) error { return sc.store.SetSecret(sc.k(key), val) }

// RekeyFile は dataPath の暗号化値を (oldPepper,passphrase)→(newPepper,newPassphrase)
// で再暗号化する。rotatePassphrase が true なら再暗号化後に新パスフレーズを生成し
// keyPath を置換する。再暗号化した件数を返す。cmd の `secrets rekey` が呼ぶ。
//
// 注意（不整合ウィンドウ）: データ保存（newKey で暗号化）に成功した後の鍵ファイル
// 書き込みに失敗すると、データは newKey・鍵ファイルは旧パスフレーズという不一致が
// 残り得る（rotatePassphrase=true 時）。発生確率は低いが、復旧には正しい newPepper +
// newPass の再投入が要る。失敗時はデータファイルのバックアップから復旧すること。
func RekeyFile(dataPath, keyPath, oldPepper, newPepper string, rotatePassphrase bool) (int, error) {
	oldPass, err := loadOrCreatePassphrase(keyPath)
	if err != nil {
		return 0, err
	}
	newPass := oldPass
	if rotatePassphrase {
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return 0, err
		}
		newPass = base64.StdEncoding.EncodeToString(raw)
	}
	m, err := loadData(dataPath)
	if err != nil {
		return 0, err
	}
	oldKey := deriveKey(oldPepper, oldPass)
	newKey := deriveKey(newPepper, newPass)
	n := rekey(m, oldKey, newKey)
	if err := saveData(dataPath, m); err != nil {
		return 0, err
	}
	if rotatePassphrase {
		if err := os.WriteFile(keyPath, []byte(newPass), 0o600); err != nil {
			return n, err
		}
	}
	return n, nil
}
