// Settings タブのフロント。/plugins/settings/schema でプラグイン一覧を取得し、
// ⚙ で個別フォームへ遷移。Save は /plugins/settings/save に POST する。
// 値は value/textContent で扱い HTML 解釈させない（XSS 回避）。共通クラスを流用。

export async function render(root) {
  await showList(root);
}

async function showList(root) {
  root.textContent = '';
  const h = document.createElement('h2');
  h.textContent = 'Settings';
  root.appendChild(h);

  let data;
  try {
    const res = await fetch('/plugins/settings/schema');
    if (!res.ok) { root.appendChild(p('読み込み失敗: HTTP ' + res.status)); return; }
    data = await res.json();
  } catch (e) {
    root.appendChild(p('読み込み失敗: ' + e));
    return;
  }
  const secretKeys = (data && data.secretKeys) || [];
  const plugins = (data && data.plugins) || [];
  if (plugins.length === 0) {
    root.appendChild(p('設定を持つプラグインはありません'));
  } else {
    const table = document.createElement('table');
    table.className = 'task-table';
    for (const pl of plugins) {
      const tr = document.createElement('tr');
      const td = document.createElement('td');
      td.textContent = pl.title || pl.id;
      tr.appendChild(td);
      const btnCell = document.createElement('td');
      const btn = document.createElement('button');
      btn.className = 'run-btn';
      btn.textContent = '⚙';
      btn.title = '設定';
      btn.addEventListener('click', () => showForm(root, pl, secretKeys));
      btnCell.appendChild(btn);
      tr.appendChild(btnCell);
      table.appendChild(tr);
    }
    root.appendChild(table);
  }
  await renderPetBlock(root);
  await renderOpenableTabs(root); // 既存セクション描画の後に「詳細設定」（別タブで開く）を追加
}

// renderOpenableTabs は hidden プラグインを列挙し「開く」ボタンを出す。
// クリックで左ペインの該当タブをオンデマンドで開く（agentarium.openTab）。
async function renderOpenableTabs(root) {
  let plugins = [];
  try {
    const res = await fetch('/api/plugins');
    if (res.ok) plugins = await res.json();
  } catch (_) { return; }
  // 左ペインの hidden プラグインのみ対象（右ペインは openTab の対象外＝開くボタンを出さない）
  const hidden = plugins.filter((p) => p.hidden === true && p.pane !== 'right');
  if (hidden.length === 0) return; // 対象なしならセクションを出さない

  const card = document.createElement('div');
  card.className = 'card';
  const h = document.createElement('h3');
  h.textContent = '詳細設定';
  card.appendChild(h);
  for (const p of hidden) {
    const row = document.createElement('div');
    row.style.margin = '6px 0';
    const label = document.createElement('span');
    label.textContent = p.title + '  ';
    const btn = document.createElement('button');
    btn.className = 'run-btn';
    btn.textContent = '開く';
    btn.addEventListener('click', () => {
      if (globalThis.agentarium && typeof globalThis.agentarium.openTab === 'function') {
        globalThis.agentarium.openTab(p.id);
      } else {
        alert('タブを開けません（agentarium.openTab が利用できません）');
      }
    });
    row.appendChild(label);
    row.appendChild(btn);
    card.appendChild(row);
  }
  root.appendChild(card);
}

// renderPetBlock は Pet 設定ブロックを描画する。/pet/config が無い（pet 未結線）なら何も出さない。
async function renderPetBlock(root) {
  let cfg;
  try {
    const res = await fetch('/pet/config');
    if (!res.ok) return;
    cfg = await res.json();
  } catch (_) {
    return;
  }
  const h = document.createElement('h2');
  h.textContent = 'Pet';
  root.appendChild(h);
  const card = document.createElement('div');
  card.className = 'card';

  const binRow = document.createElement('div');
  const binLabel = document.createElement('label');
  binLabel.textContent = 'Pet バイナリのパス';
  binLabel.style.display = 'block';
  const binInput = document.createElement('input');
  binInput.type = 'text';
  binInput.value = cfg.binary || '';
  binInput.style.width = '100%';
  binRow.appendChild(binLabel);
  binRow.appendChild(binInput);
  card.appendChild(binRow);

  const autoRow = document.createElement('label');
  autoRow.style.display = 'block';
  autoRow.style.margin = '8px 0';
  const autoInput = document.createElement('input');
  autoInput.type = 'checkbox';
  autoInput.checked = !!cfg.autostart;
  autoRow.appendChild(autoInput);
  autoRow.appendChild(document.createTextNode(' アプリ起動時に自動起動'));
  card.appendChild(autoRow);

  let skinValue = cfg.skin || '';
  const skinWrap = document.createElement('div');
  const skinBtn = document.createElement('button');
  skinBtn.className = 'run-btn';
  skinBtn.textContent = 'skin を取得';
  const skinList = document.createElement('div');
  skinList.style.margin = '6px 0';
  skinBtn.addEventListener('click', async () => {
    skinList.textContent = '取得中…';
    try {
      const res = await fetch('/pet/skins');
      if (!res.ok) { skinList.textContent = '取得失敗: ' + (await res.text()); return; }
      const data = await res.json();
      skinList.textContent = '';
      for (const sk of (data.skins || [])) {
        const rl = document.createElement('label');
        rl.style.marginRight = '12px';
        const radio = document.createElement('input');
        radio.type = 'radio';
        radio.name = 'pet-skin';
        radio.value = sk;
        if (sk === skinValue) radio.checked = true;
        radio.addEventListener('change', () => { skinValue = sk; });
        rl.appendChild(radio);
        rl.appendChild(document.createTextNode(' ' + sk));
        skinList.appendChild(rl);
      }
      if (!(data.skins || []).length) skinList.textContent = '(skin なし)';
    } catch (e) {
      skinList.textContent = '取得失敗: ' + e;
    }
  });
  skinWrap.appendChild(skinBtn);
  skinWrap.appendChild(skinList);
  card.appendChild(skinWrap);

  const actions = document.createElement('div');
  actions.style.marginTop = '8px';
  const saveBtn = document.createElement('button');
  saveBtn.className = 'run-btn';
  saveBtn.textContent = '保存';
  saveBtn.addEventListener('click', async () => {
    try {
      const res = await fetch('/pet/config', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ binary: binInput.value, skin: skinValue, autostart: autoInput.checked }),
      });
      if (res.status !== 204) { alert('保存失敗: HTTP ' + res.status); return; }
      alert('保存しました');
    } catch (e) { alert('保存失敗: ' + e); }
  });
  const launchBtn = document.createElement('button');
  launchBtn.className = 'run-btn';
  launchBtn.style.marginLeft = '8px';
  launchBtn.textContent = '起動';
  launchBtn.addEventListener('click', async () => {
    try {
      const res = await fetch('/pet/launch', { method: 'POST' });
      if (!res.ok) { alert('起動失敗: ' + (await res.text())); return; }
      alert('起動しました');
    } catch (e) { alert('起動失敗: ' + e); }
  });
  actions.appendChild(saveBtn);
  actions.appendChild(launchBtn);
  card.appendChild(actions);

  root.appendChild(card);
}

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
      if (pl.id === 'kernel' && 'theme' in values) applyTheme(values.theme);
      await showList(root);
    } catch (e) {
      alert('保存失敗: ' + e);
    }
  });
  form.appendChild(save);
  root.appendChild(form);
}

// applyTheme は選択されたテーマを即時反映する（リロード不要）。
// 'light'/'dark' は data-theme を立て、'system' は属性を外して OS 追従に戻す。
function applyTheme(theme) {
  const el = document.documentElement;
  if (theme === 'light' || theme === 'dark') {
    el.dataset.theme = theme;
  } else {
    delete el.dataset.theme;
  }
}

function p(s) { const el = document.createElement('p'); el.className = 'muted'; el.textContent = s; return el; }

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

    // 暗号化済み既存項目は値が伏せられている。表示ボタンで都度復号して見る。
    // revealedValue は「表示で入れた復号値」。未編集ならこの値と一致し、Save 時に再送しない。
    let revealedValue = null;
    let revealBtn = null;
    if (existing && enc) {
      revealBtn = document.createElement('button');
      revealBtn.type = 'button';
      revealBtn.className = 'run-btn';
      revealBtn.style.marginLeft = '8px';
      revealBtn.textContent = '👁 表示';
      let revealed = false;
      revealBtn.addEventListener('click', async () => {
        if (revealed) {
          valInput.value = '';
          valInput.type = 'password';
          valInput.placeholder = '（設定済み・変更時のみ入力）';
          revealBtn.textContent = '👁 表示';
          revealed = false;
          revealedValue = null;
          return;
        }
        try {
          const res = await fetch('/plugins/settings/reveal', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ key: existing.key }),
          });
          if (!res.ok) { alert('表示失敗: HTTP ' + res.status); return; }
          const data = await res.json();
          revealedValue = data.value || '';
          valInput.value = revealedValue;
          valInput.type = 'text';
          revealBtn.textContent = '🙈 隠す';
          revealed = true;
        } catch (e) { alert('表示失敗: ' + e); }
      });
    }

    wrap.appendChild(keyInput);
    wrap.appendChild(valInput);
    wrap.appendChild(encLabel);
    if (revealBtn) wrap.appendChild(revealBtn);
    if (existing) wrap.appendChild(delLabel);
    card.appendChild(wrap);
    rows.push({ keyInput, valInput, encInput, delInput, existing: !!existing, getRevealed: () => revealedValue });
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
      // 既存・暗号で「値未入力」または「表示しただけで未編集（復号値のまま）」なら送らない（既存保持）。
      // 表示後に編集していれば revealedValue と一致しないので更新として送る。
      const revealed = r.getRevealed();
      if (r.existing && r.encInput.checked &&
          (r.valInput.value === '' || (revealed !== null && r.valInput.value === revealed))) continue;
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
