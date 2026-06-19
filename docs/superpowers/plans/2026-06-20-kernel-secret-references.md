# Kernel Secret 参照 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** プラグインの設定フィールドを「直接入力」と「カーネル共有シークレットへの参照」のどちらでも設定でき、プラグインが自分の設定を読むときにカーネルが参照を解決する仕組みを agentarium 本体に追加する。

**Architecture:** 既存 `secrets.Store`（暗号化 key/value）と `settings` プラグインを拡張する。カーネルシークレットは `secret.<KEY>` の独立名前空間に保存。プラグイン設定フィールドの参照は `<pluginID>.<field>.__ref` に参照先 KEY を保存し、literal キー `<pluginID>.<field>` と排他にする。読み取りは新設 `settings.Reader`（プラグイン id 束縛）が毎回オンデマンドで解決する（値をキャッシュしない）。

**Tech Stack:** Go 1.26 系、標準ライブラリのみ。フロントは `embed.FS` の素の JS（ビルドツールチェーン無し、テストランナー無し）。

## Global Constraints

- 言語は Go 1.26 系。標準ライブラリ範囲で実装する（新規依存を足さない）。
- 1 ファイル 700 行以内を目安とする。
- JS/CSS は `embed.FS` の実ファイル。Go const への埋め込みはしない。値は `value`/`textContent` で扱い HTML 解釈させない（XSS 回避）。
- カーネルシークレットのストアキー接頭辞は `secret.`、参照ポインタのキー接尾辞は `.__ref`。`secret` / `kernel` はプラグイン id の予約語。
- ダングリング参照（参照先カーネルシークレット不在）は `("", false)`（未設定扱い）。
- 暗号シークレットの値は UI に返さない（`Set` 表示のみ）。平文シークレットは値を返してよい。
- 設計の正典: `docs/superpowers/specs/2026-06-20-kernel-secret-references-design.md`。

---

### Task 1: `secrets.Store` に `IsEncrypted` と `Keys` を追加

**Files:**
- Modify: `kernel/secrets/store.go`
- Test: `kernel/secrets/store_test.go`

**Interfaces:**
- Consumes: 既存 `Store`（`s.mu`, `s.m map[string]string`）、`isEncrypted(string) bool`（crypto.go）。
- Produces:
  - `func (s *Store) IsEncrypted(key string) bool` — key の保存値が暗号化済みなら true。未設定は false。
  - `func (s *Store) Keys() []string` — 保存済みキーを昇順ソートで返す。

- [ ] **Step 1: 失敗するテストを書く**

`kernel/secrets/store_test.go` の末尾に追記:

```go
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
```

- [ ] **Step 2: テストが失敗することを確認**

Run: `go test ./kernel/secrets/ -run 'TestStore_IsEncrypted|TestStore_Keys' -v`
Expected: コンパイルエラー（`s.IsEncrypted` / `s.Keys` 未定義）。

- [ ] **Step 3: 最小実装**

`kernel/secrets/store.go` の import に `"sort"` を追加（`"path/filepath"` の隣など）:

```go
import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)
```

`Has` メソッドの直後に追加:

```go
// IsEncrypted は key の保存値が暗号化済みか返す。未設定は false。
func (s *Store) IsEncrypted(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[key]
	return ok && isEncrypted(v)
}

// Keys は保存済みキーを昇順ソートで返す。
func (s *Store) Keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.m))
	for k := range s.m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
```

- [ ] **Step 4: テストが通ることを確認**

Run: `go test ./kernel/secrets/ -run 'TestStore_IsEncrypted|TestStore_Keys' -v`
Expected: PASS

- [ ] **Step 5: コミット**

```bash
git add kernel/secrets/store.go kernel/secrets/store_test.go
git commit -m "feat(secrets): Store に IsEncrypted と Keys を追加"
```

---

### Task 2: `settings.Reader`（literal/ref 解決）を新設

**Files:**
- Create: `kernel/settings/reader.go`
- Test: `kernel/settings/reader_test.go`

**Interfaces:**
- Consumes: `secrets.Store`（`Get(key) (string,bool)`, `Has`, `Set`, `SetSecret`, `Delete`）。
- Produces:
  - `const KernelSecretPrefix = "secret."`
  - `const RefSuffix = ".__ref"`
  - `type Reader struct{ ... }`
  - `func NewReader(store *secrets.Store, pluginID string) *Reader`
  - `func (r *Reader) Get(field string) (string, bool)` — ref があればカーネルシークレット、なければ literal を解決。ダングリング/未設定は `("",false)`。値はキャッシュしない。

- [ ] **Step 1: 失敗するテストを書く**

`kernel/settings/reader_test.go` を新規作成:

```go
package settings_test

import (
	"path/filepath"
	"testing"

	"github.com/ktat/agentarium/kernel/secrets"
	"github.com/ktat/agentarium/kernel/settings"
)

func newReaderStore(t *testing.T) *secrets.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := secrets.NewStore(filepath.Join(dir, "data.json"), filepath.Join(dir, "key"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

func TestReader_LiteralValue(t *testing.T) {
	s := newReaderStore(t)
	_ = s.Set("eval.NOTION_APP_TOKEN", "literal-val")
	r := settings.NewReader(s, "eval")
	if v, ok := r.Get("NOTION_APP_TOKEN"); !ok || v != "literal-val" {
		t.Fatalf("literal get = %q,%v", v, ok)
	}
}

func TestReader_RefResolvesKernelSecret(t *testing.T) {
	s := newReaderStore(t)
	_ = s.SetSecret(settings.KernelSecretPrefix+"NOTION_TOKEN", "kernel-secret")
	_ = s.Set("eval.NOTION_APP_TOKEN"+settings.RefSuffix, "NOTION_TOKEN")
	r := settings.NewReader(s, "eval")
	if v, ok := r.Get("NOTION_APP_TOKEN"); !ok || v != "kernel-secret" {
		t.Fatalf("ref get = %q,%v", v, ok)
	}
}

func TestReader_DanglingRefIsUnset(t *testing.T) {
	s := newReaderStore(t)
	_ = s.Set("eval.NOTION_APP_TOKEN"+settings.RefSuffix, "GONE")
	r := settings.NewReader(s, "eval")
	if v, ok := r.Get("NOTION_APP_TOKEN"); ok {
		t.Fatalf("dangling ref should be unset, got %q,%v", v, ok)
	}
}

func TestReader_MissingFieldIsUnset(t *testing.T) {
	s := newReaderStore(t)
	r := settings.NewReader(s, "eval")
	if _, ok := r.Get("NOPE"); ok {
		t.Fatal("missing field should be unset")
	}
}
```

- [ ] **Step 2: テストが失敗することを確認**

Run: `go test ./kernel/settings/ -run TestReader -v`
Expected: コンパイルエラー（`settings.NewReader` / 定数未定義）。

- [ ] **Step 3: 最小実装**

`kernel/settings/reader.go` を新規作成:

```go
package settings

import "github.com/ktat/agentarium/kernel/secrets"

const (
	// KernelSecretPrefix はカーネル共有シークレットのストアキー接頭辞。
	KernelSecretPrefix = "secret."
	// RefSuffix はプラグイン設定フィールドの参照ポインタを表すキー接尾辞。
	// 値は参照先カーネルシークレットの KEY（接頭辞抜き）。
	RefSuffix = ".__ref"
)

// Reader は 1 プラグインの設定を読むための、plugin id 束縛のアクセサ。
// ref（カーネルシークレット参照）を透過的に解決する。
type Reader struct {
	store    *secrets.Store
	pluginID string
}

// NewReader は pluginID に束縛した設定リーダーを返す。
func NewReader(store *secrets.Store, pluginID string) *Reader {
	return &Reader{store: store, pluginID: pluginID}
}

// Get は field の設定値を解決して返す。ref があれば参照先カーネルシークレットを、
// なければ literal を返す。未設定・ref 先不在は ("", false)。
// 注意: 値はキャッシュしないこと。ref は実行中に張り替え/削除され得る。
func (r *Reader) Get(field string) (string, bool) {
	base := r.pluginID + "." + field
	if ref, ok := r.store.Get(base + RefSuffix); ok && ref != "" {
		return r.store.Get(KernelSecretPrefix + ref)
	}
	return r.store.Get(base)
}
```

- [ ] **Step 4: テストが通ることを確認**

Run: `go test ./kernel/settings/ -run TestReader -v`
Expected: PASS（4 件）

- [ ] **Step 5: コミット**

```bash
git add kernel/settings/reader.go kernel/settings/reader_test.go
git commit -m "feat(settings): literal/ref を解決する Reader を追加"
```

---

### Task 3: プラグイン id の予約語チェック（`secret` / `kernel`）

**Files:**
- Modify: `kernel/plugin/registry.go:39-49`
- Test: `kernel/plugin/registry_test.go`

**Interfaces:**
- Consumes: 既存 `Registry.Register`。
- Produces: `Register` が id `secret` / `kernel` をエラーにする（ストアキー名前空間 `secret.` / `kernel.` との衝突防止）。

- [ ] **Step 1: 失敗するテストを書く**

`kernel/plugin/registry_test.go`（`package plugin`）の末尾に追記。既存スタブ `fakePlugin{id, order}` を使う:

```go
func TestRegister_RejectsReservedIDs(t *testing.T) {
	for _, id := range []string{"secret", "kernel"} {
		r := NewRegistry()
		err := r.Register(fakePlugin{id: id})
		if err == nil {
			t.Fatalf("id %q should be rejected as reserved", id)
		}
	}
}
```

- [ ] **Step 2: テストが失敗することを確認**

Run: `go test ./kernel/plugin/ -run TestRegister_RejectsReservedIDs -v`
Expected: FAIL（id "secret" が登録できてしまう）。

- [ ] **Step 3: 最小実装**

`kernel/plugin/registry.go`、`validID` 定義の下に予約語セットを追加:

```go
// reservedIDs はカーネルが内部で使う名前空間（secrets.Store のキー接頭辞
// "secret."、Settings の "kernel." グループ）と衝突するため禁止する plugin ID。
var reservedIDs = map[string]bool{"secret": true, "kernel": true}
```

`Register` 内、`validID` チェックの直後・重複チェックの前に追加:

```go
	if !validID.MatchString(id) {
		return fmt.Errorf("invalid plugin ID %q: must match [a-z0-9][a-z0-9_-]*", id)
	}
	if reservedIDs[id] {
		return fmt.Errorf("reserved plugin ID: %s", id)
	}
	if r.ids[id] {
		return fmt.Errorf("duplicate plugin ID: %s", id)
	}
```

- [ ] **Step 4: テストが通ることを確認**

Run: `go test ./kernel/plugin/ -run TestRegister_RejectsReservedIDs -v`
Expected: PASS

- [ ] **Step 5: コミット**

```bash
git add kernel/plugin/registry.go kernel/plugin/registry_test.go
git commit -m "feat(plugin): secret/kernel を予約 plugin ID として拒否"
```

---

### Task 4: Settings schema に Kernel Secrets グループ・secretKeys・ref を追加

**Files:**
- Modify: `kernel/settings/settings.go`（`fieldDTO`, `handleSchema`）
- Test: `kernel/settings/settings_test.go`

**Interfaces:**
- Consumes: Task 1 の `store.Keys()` / `store.IsEncrypted()`、Task 2 の `KernelSecretPrefix` / `RefSuffix`。
- Produces:
  - `fieldDTO` に `Encrypted bool` と `Ref string` を追加。
  - `handleSchema` レスポンスに以下を追加:
    - `plugins` 配列の先頭付近に id `"secret"` / title `"Kernel Secrets"` のグループ。各 field は `{key: KEY, label: KEY, encrypted, set:true, value: 平文なら値・暗号なら""}`。
    - トップレベル `secretKeys []string`（接頭辞を除いたカーネルシークレット KEY 一覧。ドロップダウン候補）。
    - 各プラグイン field の `ref`（`<id>.<field>.__ref` が存在すればその値）。

- [ ] **Step 1: 失敗するテストを書く**

`kernel/settings/settings_test.go` の末尾に追記。`newTestEnv` / `findRoute` は既存ヘルパを使う:

```go
func TestSchema_KernelSecretsAndRef(t *testing.T) {
	_, store, sp := newTestEnv(t)
	// 平文・暗号のカーネルシークレットを 1 件ずつ
	_ = store.Set(settings.KernelSecretPrefix+"PLAIN_KEY", "plainval")
	_ = store.SetSecret(settings.KernelSecretPrefix+"SECRET_KEY", "secretval")
	// alpha.token を SECRET_KEY 参照に
	_ = store.Set("alpha.token"+settings.RefSuffix, "SECRET_KEY")

	h := findRoute(t, sp, "GET", "/schema")
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("GET", "/schema", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp struct {
		Plugins []struct {
			ID     string `json:"id"`
			Title  string `json:"title"`
			Fields []struct {
				Key       string `json:"key"`
				Value     string `json:"value"`
				Set       bool   `json:"set"`
				Encrypted bool   `json:"encrypted"`
				Ref       string `json:"ref"`
			} `json:"fields"`
		} `json:"plugins"`
		SecretKeys []string `json:"secretKeys"`
	}
	body, _ := io.ReadAll(rec.Body)
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, body)
	}
	// secretKeys は接頭辞抜きの 2 件
	if len(resp.SecretKeys) != 2 {
		t.Fatalf("secretKeys = %v, want 2", resp.SecretKeys)
	}
	for _, k := range resp.SecretKeys {
		if strings.HasPrefix(k, settings.KernelSecretPrefix) {
			t.Fatalf("secretKeys should be prefix-stripped, got %q", k)
		}
	}
	// Kernel Secrets グループ: 平文は value を返し、暗号は返さない
	var foundSecret bool
	for _, pl := range resp.Plugins {
		if pl.ID == "secret" {
			foundSecret = true
			for _, f := range pl.Fields {
				if f.Key == "PLAIN_KEY" && f.Value != "plainval" {
					t.Fatalf("plain kernel secret value = %q, want plainval", f.Value)
				}
				if f.Key == "SECRET_KEY" && f.Value != "" {
					t.Fatalf("encrypted kernel secret value should be hidden, got %q", f.Value)
				}
				if f.Key == "SECRET_KEY" && !f.Encrypted {
					t.Fatal("SECRET_KEY should be marked encrypted")
				}
			}
		}
		if pl.ID == "alpha" {
			for _, f := range pl.Fields {
				if f.Key == "token" && f.Ref != "SECRET_KEY" {
					t.Fatalf("alpha.token ref = %q, want SECRET_KEY", f.Ref)
				}
			}
		}
	}
	if !foundSecret {
		t.Fatal("Kernel Secrets group (id=secret) missing")
	}
}
```

- [ ] **Step 2: テストが失敗することを確認**

Run: `go test ./kernel/settings/ -run TestSchema_KernelSecretsAndRef -v`
Expected: FAIL（secretKeys が無い / secret グループが無い等）。

- [ ] **Step 3: 最小実装**

`kernel/settings/settings.go` の `fieldDTO` に 2 フィールド追加:

```go
type fieldDTO struct {
	Key       string   `json:"key"`
	Label     string   `json:"label"`
	Secret    bool     `json:"secret"`
	Value     string   `json:"value,omitempty"`
	Set       bool     `json:"set,omitempty"`
	Options   []string `json:"options,omitempty"`
	Encrypted bool     `json:"encrypted,omitempty"` // Kernel Secrets: 暗号化されているか
	Ref       string   `json:"ref,omitempty"`       // プラグイン field: 参照先カーネルシークレット KEY
}
```

`handleSchema` を次のように書き換える（カーネルグループ追加 → Kernel Secrets グループ → secretKeys 収集 → プラグイン field に ref 付与 → レスポンスに secretKeys 同梱）:

```go
func (p *settingsPlugin) handleSchema(w http.ResponseWriter, r *http.Request) {
	out := make([]pluginDTO, 0)
	// カーネル自身の設定グループ（プラグインではない）。
	rendererDTO := fieldDTO{Key: rendererField, Label: "Terminal renderer（変更は再起動で反映）", Options: rendererOptions}
	if v, ok := p.store.Get(KeyTerminalRenderer); ok {
		rendererDTO.Value = v
	}
	out = append(out, pluginDTO{ID: kernelGroupID, Title: "Kernel", Fields: []fieldDTO{rendererDTO}})

	// Kernel Secrets グループと secretKeys 候補を収集する。
	secretKeys := make([]string, 0)
	secretFields := make([]fieldDTO, 0)
	for _, k := range p.store.Keys() {
		if !strings.HasPrefix(k, KernelSecretPrefix) {
			continue
		}
		name := strings.TrimPrefix(k, KernelSecretPrefix)
		secretKeys = append(secretKeys, name)
		f := fieldDTO{Key: name, Label: name, Set: true, Encrypted: p.store.IsEncrypted(k)}
		if !f.Encrypted { // 平文は値を返してよい
			if v, ok := p.store.Get(k); ok {
				f.Value = v
			}
		}
		secretFields = append(secretFields, f)
	}
	out = append(out, pluginDTO{ID: secretGroupID, Title: "Kernel Secrets", Fields: secretFields})

	for _, pl := range p.reg.Plugins() {
		sp, ok := pl.(plugin.SettingsProvider)
		if !ok {
			continue
		}
		m := pl.Meta()
		if m.ID == "settings" {
			continue
		}
		fields := make([]fieldDTO, 0)
		for _, f := range sp.SettingsSchema() {
			d := fieldDTO{Key: f.Key, Label: f.Label, Secret: f.Secret}
			storeKey := m.ID + "." + f.Key
			if ref, ok := p.store.Get(storeKey + RefSuffix); ok && ref != "" {
				d.Ref = ref // ref 設定時は literal を表示しない
			} else if f.Secret {
				d.Set = p.store.Has(storeKey)
			} else if v, ok := p.store.Get(storeKey); ok {
				d.Value = v
			}
			fields = append(fields, d)
		}
		out = append(out, pluginDTO{ID: m.ID, Title: m.Title, Fields: fields})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"plugins": out, "secretKeys": secretKeys})
}
```

`settings.go` の const ブロックに `secretGroupID` を追加（`kernelGroupID` の隣）:

```go
const (
	kernelGroupID = "kernel"
	secretGroupID = "secret"
	rendererField = "terminal_renderer"
	KeyTerminalRenderer = kernelGroupID + "." + rendererField
)
```

`strings` が未 import なら import に追加する。

- [ ] **Step 4: テストが通ることを確認**

Run: `go test ./kernel/settings/ -run TestSchema -v`
Expected: PASS（既存の schema テストも含めて緑）

- [ ] **Step 5: コミット**

```bash
git add kernel/settings/settings.go kernel/settings/settings_test.go
git commit -m "feat(settings): schema に Kernel Secrets グループと ref を追加"
```

---

### Task 5: Settings save に Kernel Secrets CRUD とプラグイン literal/ref を追加

**Files:**
- Modify: `kernel/settings/settings.go`（`saveReq`, `handleSave`）
- Test: `kernel/settings/settings_test.go`

**Interfaces:**
- Consumes: Task 2 の `KernelSecretPrefix` / `RefSuffix`、`store.Has`。
- Produces:
  - `saveReq` に `Refs map[string]string` と `Secrets []kernelSecretInput` を追加。
  - `kernelSecretInput{Key, Value string; Encrypted, Delete bool}`。
  - `handleSave`:
    - ID == `secret`: 各 `kernelSecretInput` を処理。`Delete` → `Delete("secret."+Key)`。暗号 → 空でなければ `SetSecret`。平文 → `Set`（空も許可）。`Key` 空は無視。
    - プラグイングループ: `Refs[k]` があれば参照先存在を検証（無ければ 400）→ `Set(storeKey+RefSuffix, ref)` + `Delete(storeKey)`。`Values[k]` は従来の literal 保存 + `Delete(storeKey+RefSuffix)`。schema に無い key は無視。

- [ ] **Step 1: 失敗するテストを書く**

`kernel/settings/settings_test.go` の末尾に追記:

```go
func postSave(t *testing.T, sp plugin.Plugin, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := findRoute(t, sp, "POST", "/save")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/save", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h(rec, req)
	return rec
}

func TestSave_KernelSecretCRUD(t *testing.T) {
	_, store, sp := newTestEnv(t)
	// 暗号 1 件・平文 1 件を追加
	rec := postSave(t, sp, `{"id":"secret","secrets":[
		{"key":"NOTION_TOKEN","value":"tok","encrypted":true},
		{"key":"REGION","value":"jp","encrypted":false}
	]}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
	if v, ok := store.Get(settings.KernelSecretPrefix + "NOTION_TOKEN"); !ok || v != "tok" {
		t.Fatalf("notion token = %q,%v", v, ok)
	}
	if !store.IsEncrypted(settings.KernelSecretPrefix + "NOTION_TOKEN") {
		t.Fatal("NOTION_TOKEN should be encrypted")
	}
	if store.IsEncrypted(settings.KernelSecretPrefix + "REGION") {
		t.Fatal("REGION should be plaintext")
	}
	// 削除
	rec = postSave(t, sp, `{"id":"secret","secrets":[{"key":"REGION","delete":true}]}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d", rec.Code)
	}
	if store.Has(settings.KernelSecretPrefix + "REGION") {
		t.Fatal("REGION should be deleted")
	}
}

func TestSave_PluginRefAndLiteralExclusive(t *testing.T) {
	_, store, sp := newTestEnv(t)
	_ = store.SetSecret(settings.KernelSecretPrefix+"NOTION_TOKEN", "tok")

	// alpha.token を NOTION_TOKEN 参照に設定
	rec := postSave(t, sp, `{"id":"alpha","refs":{"token":"NOTION_TOKEN"}}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("ref save status = %d", rec.Code)
	}
	if v, ok := store.Get("alpha.token" + settings.RefSuffix); !ok || v != "NOTION_TOKEN" {
		t.Fatalf("ref pointer = %q,%v", v, ok)
	}
	if store.Has("alpha.token") {
		t.Fatal("literal alpha.token should be cleared when ref is set")
	}

	// literal に戻すと __ref が消える
	rec = postSave(t, sp, `{"id":"alpha","values":{"token":"plainsecret"}}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("literal save status = %d", rec.Code)
	}
	if store.Has("alpha.token" + settings.RefSuffix) {
		t.Fatal("__ref should be cleared when literal is set")
	}
	if v, ok := store.Get("alpha.token"); !ok || v != "plainsecret" {
		t.Fatalf("literal token = %q,%v", v, ok)
	}
}

func TestSave_InvalidRefIs400(t *testing.T) {
	_, _, sp := newTestEnv(t)
	rec := postSave(t, sp, `{"id":"alpha","refs":{"token":"NOPE"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid ref status = %d, want 400", rec.Code)
	}
}
```

- [ ] **Step 2: テストが失敗することを確認**

Run: `go test ./kernel/settings/ -run TestSave_ -v`
Expected: FAIL（refs/secrets 未対応）。

- [ ] **Step 3: 最小実装**

`kernel/settings/settings.go` の `saveReq` を拡張し、`kernelSecretInput` を追加:

```go
type saveReq struct {
	ID      string              `json:"id"`
	Values  map[string]string   `json:"values"`
	Refs    map[string]string   `json:"refs,omitempty"`    // プラグイン field → カーネルシークレット KEY
	Secrets []kernelSecretInput `json:"secrets,omitempty"` // Kernel Secrets グループ用
}

type kernelSecretInput struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	Encrypted bool   `json:"encrypted"`
	Delete    bool   `json:"delete"`
}
```

`handleSave` の、`kernelGroupID` 分岐の直後に Kernel Secrets 分岐を追加:

```go
	// Kernel Secrets グループ（動的キー）。
	if body.ID == secretGroupID {
		for _, e := range body.Secrets {
			if e.Key == "" {
				continue
			}
			storeKey := KernelSecretPrefix + e.Key
			var err error
			switch {
			case e.Delete:
				err = p.store.Delete(storeKey)
			case e.Encrypted:
				if e.Value == "" {
					continue // 空 Secret は既存保持
				}
				err = p.store.SetSecret(storeKey, e.Value)
			default:
				err = p.store.Set(storeKey, e.Value) // 平文（空も許可）
			}
			if err != nil {
				http.Error(w, "save failed", http.StatusInternalServerError)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
```

`handleSave` の、`sp := p.findProvider(body.ID)` 以降の literal 保存ループを、ref 対応に書き換える。`schema` map 構築の後に次のように:

```go
	// 参照 (ref) の保存: schema にある field のみ。参照先カーネルシークレットの存在を検証。
	for k, ref := range body.Refs {
		if _, ok := schema[k]; !ok {
			continue
		}
		if ref == "" {
			continue
		}
		if !p.store.Has(KernelSecretPrefix + ref) {
			http.Error(w, "unknown kernel secret: "+ref, http.StatusBadRequest)
			return
		}
		storeKey := body.ID + "." + k
		if err := p.store.Set(storeKey+RefSuffix, ref); err != nil {
			http.Error(w, "save failed", http.StatusInternalServerError)
			return
		}
		if err := p.store.Delete(storeKey); err != nil {
			http.Error(w, "save failed", http.StatusInternalServerError)
			return
		}
	}
	// literal の保存: ref と排他にするため __ref を消す。
	for k, v := range body.Values {
		f, ok := schema[k]
		if !ok {
			continue // 未知 key 無視
		}
		storeKey := body.ID + "." + k
		if err := p.store.Delete(storeKey + RefSuffix); err != nil {
			http.Error(w, "save failed", http.StatusInternalServerError)
			return
		}
		var err error
		if f.Secret {
			if v == "" {
				continue // 空 Secret は既存保持
			}
			err = p.store.SetSecret(storeKey, v)
		} else {
			err = p.store.Set(storeKey, v)
		}
		if err != nil {
			http.Error(w, "save failed", http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
```

注意: 既存の literal ループ（`for k, v := range body.Values { ... }`）は上の新ループで置き換える（重複させない）。`Delete` が `__ref` 不在でもエラーにならないことを確認すること（`map` の `delete` はキー不在でも no-op、`saveData` も成功する）。

- [ ] **Step 4: テストが通ることを確認**

Run: `go test ./kernel/settings/ -v`
Expected: PASS（既存の save テスト含め全て緑）

- [ ] **Step 5: コミット**

```bash
git add kernel/settings/settings.go kernel/settings/settings_test.go
git commit -m "feat(settings): save に Kernel Secrets CRUD と literal/ref 切替を追加"
```

---

### Task 6: ファサード `App.PluginSettings`

**Files:**
- Modify: `agentarium.go`
- Test: `agentarium_test.go`

**Interfaces:**
- Consumes: `a.secrets *secrets.Store`、Task 2 の `settings.NewReader`。
- Produces: `func (a *App) PluginSettings(pluginID string) *settings.Reader` — `WithSecrets` 未設定なら nil。

- [ ] **Step 1: 失敗するテストを書く**

`agentarium_test.go`（`package agentarium`。`secrets` / `filepath` は既に import 済み）の末尾に追記:

```go
func TestApp_PluginSettings(t *testing.T) {
	app := New()
	// WithSecrets 前は nil
	if app.PluginSettings("eval") != nil {
		t.Fatal("PluginSettings should be nil before WithSecrets")
	}
	dir := t.TempDir()
	store, err := secrets.NewStore(filepath.Join(dir, "data.json"), filepath.Join(dir, "key"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	app.WithSecrets(store)
	_ = store.Set("eval.NOTION_APP_TOKEN", "v")
	r := app.PluginSettings("eval")
	if r == nil {
		t.Fatal("PluginSettings should be non-nil after WithSecrets")
	}
	if v, ok := r.Get("NOTION_APP_TOKEN"); !ok || v != "v" {
		t.Fatalf("reader get = %q,%v", v, ok)
	}
}
```

- [ ] **Step 2: テストが失敗することを確認**

Run: `go test . -run TestApp_PluginSettings -v`
Expected: コンパイルエラー（`PluginSettings` 未定義）。

- [ ] **Step 3: 最小実装**

`agentarium.go` の `WithSecrets` の直後に追加:

```go
// PluginSettings は pluginID に束縛した設定リーダーを返す。設定フィールドの
// literal/カーネルシークレット参照を透過的に解決する。WithSecrets 未設定なら nil。
// 消費者は自作プラグインのコンストラクタにこれを渡して設定値を読む。
func (a *App) PluginSettings(pluginID string) *settings.Reader {
	if a.secrets == nil {
		return nil
	}
	return settings.NewReader(a.secrets, pluginID)
}
```

- [ ] **Step 4: テストが通ることを確認**

Run: `go test . -run TestApp_PluginSettings -v`
Expected: PASS

- [ ] **Step 5: コミット**

```bash
git add agentarium.go agentarium_test.go
git commit -m "feat: App.PluginSettings で設定リーダーを公開"
```

---

### Task 7: フロント — プラグイン field の literal/ref トグル

**Files:**
- Modify: `kernel/settings/assets/index.js`（`render`/`showList`/`showForm`）

**Interfaces:**
- Consumes: schema レスポンスの `secretKeys`、各 field の `ref`。
- Produces: save POST に、参照フィールドは `refs:{field:KEY}`、直接入力フィールドは `values:{field:val}` を載せる。

注: このリポに JS テストランナーは無い。検証は `go build` とブラウザ手動確認で行う。

- [ ] **Step 1: 実装（schema の secretKeys を保持して showForm に渡す）**

`kernel/settings/assets/index.js` の冒頭、`render`/`showList` を、取得した `secretKeys` を `showForm` に渡せるよう書き換える。`showList` 内の `data` 取得後に `const secretKeys = (data && data.secretKeys) || [];` を追加し、各プラグイン行の `⚙` ハンドラを `() => showForm(root, pl, secretKeys)` に変更する。

- [ ] **Step 2: 実装（showForm に literal/ref トグルを追加）**

`showForm(root, pl)` のシグネチャを `showForm(root, pl, secretKeys)` に変更し、各 field 描画の `else`（input 描画）ブロックを次に置き換える。`getValue` は値だけでなく「literal か ref か」を返すよう、`getField[f.key] = () => ({mode, value})` 形式にする:

```js
  const form = document.createElement('div');
  form.className = 'card';
  const getField = {}; // key → () => {mode:'literal'|'ref', value:string}
  const keys = secretKeys || [];
  for (const f of (pl.fields || [])) {
    const row = document.createElement('div');
    const label = document.createElement('label');
    label.textContent = f.label || f.key;
    label.style.display = 'block';
    row.appendChild(label);

    if (Array.isArray(f.options) && f.options.length > 0) {
      // 選択肢ありはラジオで描画（従来通り、ref 非対応）
      const name = 'opt-' + f.key;
      const radios = [];
      for (const opt of f.options) {
        const rl = document.createElement('label');
        rl.style.marginRight = '12px';
        const radio = document.createElement('input');
        radio.type = 'radio';
        radio.name = name;
        radio.value = opt;
        if (f.value === opt) radio.checked = true;
        rl.appendChild(radio);
        rl.appendChild(document.createTextNode(' ' + opt));
        row.appendChild(rl);
        radios.push(radio);
      }
      getField[f.key] = () => {
        const sel = radios.find((r) => r.checked);
        return { mode: 'literal', value: sel ? sel.value : '' };
      };
    } else {
      // モード選択: 直接入力 / カーネルシークレット参照
      const modeSel = document.createElement('select');
      const optLiteral = document.createElement('option');
      optLiteral.value = 'literal';
      optLiteral.textContent = '直接入力';
      const optRef = document.createElement('option');
      optRef.value = 'ref';
      optRef.textContent = 'カーネルシークレット参照';
      modeSel.appendChild(optLiteral);
      modeSel.appendChild(optRef);

      const textInput = document.createElement('input');
      textInput.type = f.secret ? 'password' : 'text';
      if (f.secret) {
        textInput.placeholder = f.set ? '（設定済み・変更時のみ入力）' : '';
      } else {
        textInput.value = f.value || '';
      }

      const refSel = document.createElement('select');
      const blank = document.createElement('option');
      blank.value = '';
      blank.textContent = '（選択）';
      refSel.appendChild(blank);
      for (const k of keys) {
        const o = document.createElement('option');
        o.value = k;
        o.textContent = k;
        if (f.ref === k) o.selected = true;
        refSel.appendChild(o);
      }

      const applyMode = () => {
        const ref = modeSel.value === 'ref';
        refSel.style.display = ref ? '' : 'none';
        textInput.style.display = ref ? 'none' : '';
      };
      modeSel.value = f.ref ? 'ref' : 'literal';
      modeSel.addEventListener('change', applyMode);
      applyMode();

      row.appendChild(modeSel);
      row.appendChild(textInput);
      row.appendChild(refSel);
      getField[f.key] = () => (modeSel.value === 'ref'
        ? { mode: 'ref', value: refSel.value }
        : { mode: 'literal', value: textInput.value });
    }
    form.appendChild(row);
  }
```

save ハンドラを `values` と `refs` を振り分ける形に書き換える:

```js
  const save = document.createElement('button');
  save.className = 'run-btn';
  save.textContent = 'Save';
  save.addEventListener('click', async () => {
    const values = {};
    const refs = {};
    for (const k of Object.keys(getField)) {
      const r = getField[k]();
      if (r.mode === 'ref') {
        if (r.value) refs[k] = r.value;
      } else {
        values[k] = r.value;
      }
    }
    try {
      const res = await fetch('/plugins/settings/save', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: pl.id, values, refs }),
      });
      if (res.status !== 204) { alert('保存失敗: HTTP ' + res.status); return; }
      await showList(root);
    } catch (e) {
      alert('保存失敗: ' + e);
    }
  });
  form.appendChild(save);
  root.appendChild(form);
}
```

- [ ] **Step 3: ビルドと手動確認**

Run: `go build ./...`
Expected: 成功。

手動確認（任意・推奨）: `go run ./cmd/agentarium` で起動 → Settings タブ → 任意のプラグインの ⚙ →
各フィールドに「直接入力 / カーネルシークレット参照」セレクトが出ること、参照に切替えるとドロップダウンが出ることを確認。

- [ ] **Step 4: コミット**

```bash
git add kernel/settings/assets/index.js
git commit -m "feat(settings ui): プラグイン field に literal/ref トグルを追加"
```

---

### Task 8: フロント — Kernel Secrets グループの CRUD UI

**Files:**
- Modify: `kernel/settings/assets/index.js`（`showForm` に id==='secret' 特例を追加）

**Interfaces:**
- Consumes: schema の id `"secret"` グループ（fields: `{key, encrypted, set, value}`）。
- Produces: save POST `{id:'secret', secrets:[{key,value,encrypted,delete}]}`。

- [ ] **Step 1: 実装（showForm に Kernel Secrets 専用描画を分岐）**

`showForm(root, pl, secretKeys)` の先頭（`back` ボタン追加の後、通常フォーム描画の前）に、id==='secret' 用の分岐を追加して早期 return する:

```js
function showForm(root, pl, secretKeys) {
  root.textContent = '';
  const back = document.createElement('button');
  back.className = 'run-btn';
  back.textContent = '← 戻る';
  back.addEventListener('click', () => showList(root));
  root.appendChild(back);

  const h = document.createElement('h2');
  h.textContent = pl.title || pl.id;
  root.appendChild(h);

  if (pl.id === 'secret') {
    renderKernelSecrets(root, pl);
    return;
  }
  // …（Task 7 の通常フォーム描画が続く）…
```

ファイル末尾に `renderKernelSecrets` を追加。既存値の編集（暗号は値非表示・変更時のみ）、削除チェック、新規行追加に対応:

```js
// renderKernelSecrets は Kernel Secrets グループ専用 UI。既存キーの更新・削除、新規追加。
function renderKernelSecrets(root, pl) {
  const card = document.createElement('div');
  card.className = 'card';
  const rows = []; // {keyInput, valInput, encInput, delInput, existing}

  function addRow(existing) {
    const wrap = document.createElement('div');
    wrap.style.margin = '6px 0';

    const keyInput = document.createElement('input');
    keyInput.type = 'text';
    keyInput.placeholder = 'KEY';
    keyInput.value = existing ? existing.key : '';
    if (existing) keyInput.readOnly = true;

    const valInput = document.createElement('input');
    const enc = existing ? !!existing.encrypted : true;
    valInput.type = enc ? 'password' : 'text';
    if (existing && enc) {
      valInput.placeholder = '（設定済み・変更時のみ入力）';
    } else if (existing) {
      valInput.value = existing.value || '';
    } else {
      valInput.placeholder = 'value';
    }

    const encLabel = document.createElement('label');
    encLabel.style.margin = '0 8px';
    const encInput = document.createElement('input');
    encInput.type = 'checkbox';
    encInput.checked = enc;
    if (existing) encInput.disabled = true; // 既存項目の暗号区分は変更不可
    encLabel.appendChild(encInput);
    encLabel.appendChild(document.createTextNode(' 暗号化'));

    const delLabel = document.createElement('label');
    delLabel.style.marginLeft = '8px';
    const delInput = document.createElement('input');
    delInput.type = 'checkbox';
    delLabel.appendChild(delInput);
    delLabel.appendChild(document.createTextNode(' 削除'));

    wrap.appendChild(keyInput);
    wrap.appendChild(valInput);
    wrap.appendChild(encLabel);
    if (existing) wrap.appendChild(delLabel);
    card.appendChild(wrap);
    rows.push({ keyInput, valInput, encInput, delInput, existing: !!existing });
  }

  for (const f of (pl.fields || [])) {
    addRow({ key: f.key, encrypted: f.encrypted, value: f.value });
  }

  const addBtn = document.createElement('button');
  addBtn.className = 'run-btn';
  addBtn.textContent = '+ 追加';
  addBtn.addEventListener('click', () => addRow(null));

  const save = document.createElement('button');
  save.className = 'run-btn';
  save.style.marginLeft = '8px';
  save.textContent = 'Save';
  save.addEventListener('click', async () => {
    const secrets = [];
    for (const r of rows) {
      const key = r.keyInput.value.trim();
      if (!key) continue;
      if (r.existing && r.delInput.checked) {
        secrets.push({ key, delete: true });
        continue;
      }
      const entry = { key, value: r.valInput.value, encrypted: r.encInput.checked };
      // 既存・暗号で値未入力なら送らない（空は既存保持）
      if (r.existing && r.encInput.checked && r.valInput.value === '') continue;
      secrets.push(entry);
    }
    try {
      const res = await fetch('/plugins/settings/save', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: 'secret', secrets }),
      });
      if (res.status !== 204) { alert('保存失敗: HTTP ' + res.status); return; }
      await showList(root);
    } catch (e) { alert('保存失敗: ' + e); }
  });

  card.appendChild(addBtn);
  card.appendChild(save);
  root.appendChild(card);
}
```

- [ ] **Step 2: ビルドと手動確認**

Run: `go build ./...`
Expected: 成功。

手動確認（任意・推奨）: 起動 → Settings → 「Kernel Secrets」⚙ → 新規追加で KEY/value/暗号化を入れて Save →
再表示で暗号項目は値非表示・設定済みになること、平文は値が見えること、削除チェック+Save で消えること、
別プラグインの参照ドロップダウンにそのキーが出ることを確認。

- [ ] **Step 3: コミット**

```bash
git add kernel/settings/assets/index.js
git commit -m "feat(settings ui): Kernel Secrets グループの CRUD UI を追加"
```

---

### Task 9: README とドキュメント追従

**Files:**
- Modify: `README.md`

**Interfaces:**
- Consumes: なし（ドキュメントのみ）。

- [ ] **Step 1: README を更新**

`README.md` の該当箇所を更新する:
- Settings タブの説明に「Kernel Secrets グループ（共有シークレットの登録・暗号/平文選択）」と
  「各プラグイン設定フィールドは直接入力 or カーネルシークレット参照を選べる」を追記。
- データ保存先の表（あれば）に `secret.<KEY>`（カーネルシークレット）と
  `<pluginID>.<field>.__ref`（参照ポインタ）の規約を追記。
- 公開 API 一覧（あれば）に `App.PluginSettings(pluginID) *settings.Reader` と `settings.NewReader` を追記。

具体的な追記文言は既存 README の構成に合わせる。表が無ければ「Settings」節に箇条書きで追加する。

- [ ] **Step 2: 全テスト + build を確認**

Run: `go test ./... && go build ./...`
Expected: 全て PASS / 成功。

- [ ] **Step 3: コミット**

```bash
git add README.md
git commit -m "docs: Kernel Secrets と設定フィールド参照を README に反映"
```

---

## Self-Review

**Spec coverage:**
- カーネルシークレット CRUD（暗号/平文） → Task 4(schema 列挙), Task 5(save), Task 8(UI)。
- 設定フィールドの literal/ref トグル（全フィールド） → Task 4(ref schema), Task 5(save), Task 7(UI)。
- プラグイン向け読み取り I/F（参照解決、キャッシュしない契約） → Task 2(Reader), Task 6(facade)。
- ストレージ規約 `secret.<KEY>` / `<pluginID>.<field>.__ref` → Task 2 の定数, Task 4/5 で使用。
- 予約プレフィックス（plugin id `secret`/`kernel` 禁止） → Task 3。
- ダングリング参照 → 未設定 → Task 2 のテスト `TestReader_DanglingRefIsUnset`。
- `secrets.Store.IsEncrypted`（平文判定） → Task 1。
- 暗号値を UI に返さない / 平文は返す → Task 4 のテスト `TestSchema_KernelSecretsAndRef`。
- README 追従 → Task 9。

**Placeholder scan:** プレースホルダ無し。各コード手順は実コードを記載。Task 9 のみ既存 README 構成依存のため文言を実装者判断とした（追記対象は明示済み）。

**Type consistency:** `KernelSecretPrefix`(="secret.") / `RefSuffix`(=".__ref") は Task 2 で定義し Task 4/5/6 で一貫使用。`fieldDTO.Encrypted`/`Ref`、`saveReq.Refs`/`Secrets`、`kernelSecretInput{Key,Value,Encrypted,Delete}`、`Reader.Get`、`App.PluginSettings` の名称・型は全タスクで一致。`secretGroupID`="secret" は Task 4 で定義し Task 5 で使用。
