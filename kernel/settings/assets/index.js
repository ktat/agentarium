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
    await renderPetBlock(root);
    return;
  }
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
  await renderPetBlock(root);
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
      await showList(root);
    } catch (e) {
      alert('保存失敗: ' + e);
    }
  });
  form.appendChild(save);
  root.appendChild(form);
}

function p(s) { const el = document.createElement('p'); el.className = 'muted'; el.textContent = s; return el; }
