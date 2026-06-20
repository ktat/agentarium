# Kernel Secret 参照 — 設計

## 背景・目的

board-assistant（agentarium を import する消費者リポ）で、Notion トークン等の
シークレットを複数プラグインから共有して使いたい。現状 agentarium には:

- `secrets.Store`（暗号化 key/value）と `settings` プラグイン（Secret フィールドを
  UI から暗号化保存）は存在する。
- しかし**一般プラグインが自分の設定値をカーネル経由で読み戻す経路が無い**。
  `secrets.Store` を持つのは `settings`（書き込み側）と `pet` だけで、消費者 main が
  直接コンストラクタ注入している。`secrets.Scope`/`Scoped` は定義済みだが未配線。

目的は、プラグインの設定フィールドに対し **「直接入力した値」と「カーネルが持つ
共有シークレットへの参照」のどちらでも設定でき、プラグインは自分の設定を読むだけで
カーネルが参照を解決する** 仕組みを agentarium 本体に追加すること。

## スコープ

- **対象は agentarium 本体のみ。** board-assistant の評価プラグインや Notion 連携は
  別フェーズ（別 spec）。
- 本 spec は「カーネルシークレットの CRUD」「設定フィールドの literal/ref 切替」
  「プラグイン向け設定読み取り I/F」の3点を追加する。

## 非目標（YAGNI）

- 別途のグラント表・`SecretNeeds()` 宣言・プラグイン id 束縛リーダーの新 IF は**作らない**
  （検討の上、後述の「参照＝認可」モデルに単純化した）。
- ref が指せる先はカーネルシークレットのみ。プラグイン間で互いの設定値を参照する機能は作らない。
- `secrets.Scope`/`Scoped` は今回使わない（必要になったら別途）。

## モデル

### 用語
- **カーネルシークレット**: カーネルが管理する共有 key/value。例 `NOTION_TOKEN`。
  常に暗号化保存する。プラグイン id で prefix しない独立名前空間。
- **設定フィールド**: 既存の `SettingsProvider.SettingsSchema()` が返す per-plugin の項目。
  例: プラグイン `evaluation` の `NOTION_APP_TOKEN`。
- **参照 (ref)**: 「フィールド `evaluation.NOTION_APP_TOKEN` の値は、カーネルシークレット
  `NOTION_TOKEN` を指す」という記録。**この参照の存在自体が、当該プラグインが当該
  カーネルシークレットを読む認可を兼ねる**（別建ての許可リストは持たない）。

### UI から見た動き
- per-plugin Settings フォームの**全フィールド**で、admin は次のどちらかを選べる:
  - (a) テキストを直接入力（= literal、従来通り）
  - (b) カーネルシークレットを選択（= ref。ドロップダウンにカーネルシークレット key 一覧）
- 見た目は「プラグインのフィールドにカーネル値を当て込む」が、実体はカーネル側に
  `(pluginID, field) → secretKey` の参照が記録される。

### 読み取り
- プラグインは自分の設定を読むだけ。カーネルが解決する:
  - フィールドが literal → その値を返す。
  - フィールドが ref → 参照先カーネルシークレットを復号して返す。
  - ref 先が削除済み/不在 → **未設定扱い `("", false)`**（ダングリング参照の決定）。

## ストレージ・エンコーディング

`secrets.Store`（`map[string]string`、`enc:` タグで暗号化を表現）をそのまま使い、
キー規約を拡張する:

| 用途 | ストアキー | 値 | 暗号化 |
|---|---|---|---|
| 既存: カーネル設定（renderer 等） | `kernel.<field>` | 値 | 非暗号 |
| 既存: プラグイン設定 literal | `<pluginID>.<field>` | 値 | フィールドが Secret なら暗号 |
| 追加: カーネルシークレット | `secret.<KEY>` | 値 | `encrypted` フラグ次第（暗号 or 平文） |
| 追加: プラグイン設定 ref | `<pluginID>.<field>.__ref` | 参照先 `<KEY>` | 非暗号 |

- ref の有無で literal/ref を判定する。`__ref` が存在すれば ref、無ければ literal。
  既存の literal 読み取り経路は変更不要（`__ref` が無いだけ）。
- admin が ref を設定したら literal キー `<pluginID>.<field>` を削除し `__ref` を立てる。
  literal を設定したら `__ref` を削除する。両者は排他。
- `secret.` プレフィックスは予約。プラグイン id に `secret`/`kernel` は使わせない
  （`plugin.Registry.Register` で既に id 重複チェックがあるなら、予約語チェックを追加）。

## プラグイン向け読み取り I/F

カーネルが、プラグイン id に束縛した設定リーダーを提供する。消費者モデルに合わせ、
**消費者 main がファサード経由で取得してプラグインのコンストラクタに渡す**（chat/pet と同方式。
新しいプラグイン・ライフサイクル IF はカーネルに追加しない）。

```go
// kernel/settings
type Reader struct { /* store + pluginID を保持 */ }

// Get は field の設定値を解決して返す。literal はそのまま、ref はカーネルシークレットを
// 復号して返す。未設定・ref 先不在は ("", false)。
func (r *Reader) Get(field string) (string, bool)

// ファサード（agentarium.go）
// PluginSettings は pluginID に束縛した設定リーダーを返す。WithSecrets 未設定なら nil。
func (a *App) PluginSettings(pluginID string) *settings.Reader
```

- **契約**: リーダーは保持してよいが、解決した**値はキャッシュしない**こと。
  ref は実行中に張り替え/削除され得るため、毎回 `Get` で解決する（リスク1の対処）。
- 値はオンデマンド解決なので、リーダーは内部で `*secrets.Store` を参照するのみ。

## Settings UI / API 変更

### スキーマ (`GET /plugins/settings/schema`)
- 新グループ **"Kernel Secrets"**（`secret`）を返す。`store` 内の `secret.<KEY>` を列挙し、
  key 一覧・`encrypted`（暗号化されているか）・「設定済み」フラグを返す。
  **暗号化シークレットの値は返さない**（既存 Secret と同様）。平文は値を返してよい。
- 各プラグインの `fieldDTO` に追加:
  - `ref string`（このフィールドが参照している `<KEY>`、無ければ空）
  - 既存 `value`/`set` は literal 用。
- レスポンスに `secretKeys []string`（ドロップダウン候補）を含める。

### 保存 (`POST /plugins/settings/save`)
- **Kernel Secrets グループ**: key/value の追加・更新・削除を受ける（動的キー）。
  リクエストの `encrypted` フラグで `SetSecret`（暗号）/`Set`（平文）を選ぶ。
  - 追加/更新: 空値は暗号項目では「空は既存保持」。平文項目は空値で上書き可。
  - 削除: save リクエスト内で key ごとに `delete: true` を受けて `Delete("secret.<KEY>")` する
    （専用エンドポイントは設けない）。
- **プラグイングループ**: 各フィールドにつき literal か ref かを受ける。
  - ref 指定時: `<pluginID>.<field>.__ref = <KEY>` を `Set`、literal キーを `Delete`。
  - literal 指定時: literal を保存（Secret なら `SetSecret`、空は既存保持）、`__ref` を `Delete`。
  - 不正な ref（存在しない `<KEY>`）は 400。

### フロント (`assets/index.js`)
- per-plugin フォームの各フィールドに「直接入力 / カーネルシークレット参照」トグルを追加。
  ref 選択時は `secretKeys` のドロップダウンを出す。XSS 回避の既存方針（value/textContent）を踏襲。
- Kernel Secrets グループ用に、key 追加・編集・削除の最小 UI を追加。

## エッジケースと振る舞い

1. **ダングリング参照**: ref 先が無い → `Get` は `("", false)`。プラグインは未設定として扱う。
2. **削除の連鎖**: カーネルシークレット削除時、参照していたフィールドは自動でダングリングになる
   （参照カウントは持たない＝削除をブロックしない。決定済み）。
3. **予約プレフィックス**: `secret.` / `kernel.` はカーネル用。プラグイン id にこれらを禁止。
4. **WithSecrets 未設定**: `PluginSettings` は nil を返す。Settings タブの Kernel Secrets も出ない。
5. **暗号化トグル**: カーネルシークレットは作成時に暗号/平文を選べる（`encrypted` フラグ）。
   暗号項目は値を UI に返さない（`Set` 表示のみ）。平文項目は値を返してよい。
   暗号/平文の判別には `secrets.Store` に判定用ヘルパ（例 `IsEncrypted(key) bool` と、平文値を
   読む既存 `Get`）が要る。`Get` は暗号値を透過復号するため、UI へ「平文だけ値を返す」には
   暗号判定が必要。

## テスト

- `secrets.Store`: 新規キー規約は settings 側でテスト。追加する `IsEncrypted` は下記で別途。
- `kernel/settings`:
  - `Reader.Get`: literal 解決 / ref 解決 / ダングリング → `("",false)` / 未設定。
  - literal⇄ref 切替で `__ref` と literal キーが排他になること。
  - schema が暗号値を漏らさないこと（Secret 値・暗号カーネルシークレット値を返さない）。
    平文カーネルシークレットは値を返すこと。
  - save: 不正 ref で 400、Kernel Secret の add/update/delete、`encrypted` フラグで
    暗号/平文の保存が分岐すること。
- `secrets.Store`: `IsEncrypted(key)` が暗号/平文を正しく判定すること。
- ファサード: `PluginSettings(id)` が id 束縛で正しく解決し、WithSecrets 未設定で nil。

## README 追従

- Settings タブの説明に「Kernel Secrets グループ」「フィールドの literal/ref 参照」を追記。
- データ保存先（`secret.<KEY>`、`<pluginID>.<field>.__ref` の規約）を該当表に反映。
- 公開 API 表に `App.PluginSettings` / `settings.Reader` を追加。

## リスク・トレードオフ（再掲）

1. **動的参照とキャッシュ**: リーダーが値をキャッシュすると失効が効かない →
   「値をキャッシュしない、毎回 Get で解決」を契約として明記（上記 I/F）。
2. **key 名の露出**: schema はカーネルシークレットの key 名一覧をドロップダウン用に全プラグイン
   設定画面へ返す。**値は返さない**。key 名は秘匿情報でない前提（ローカル単一管理者運用）。
3. **消費者モデルでの強制力**: コンパイル時に組み込むプラグインは元々プロセスを信頼されている。
   本機構は対 main の防御ではなく、設定の一元管理・使い回し・どのフィールドがどの共有値を
   指すかの明示が価値。
