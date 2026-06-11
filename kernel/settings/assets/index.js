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
  const plugins = (data && data.plugins) || [];
  if (plugins.length === 0) {
    root.appendChild(p('設定を持つプラグインはありません'));
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
    btn.addEventListener('click', () => showForm(root, pl));
    btnCell.appendChild(btn);
    tr.appendChild(btnCell);
    table.appendChild(tr);
  }
  root.appendChild(table);
}

function showForm(root, pl) {
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
  const getValue = {}; // key → () => string（フィールド型ごとに値の取り出し方が違う）
  for (const f of (pl.fields || [])) {
    const row = document.createElement('div');
    const label = document.createElement('label');
    label.textContent = f.label || f.key;
    label.style.display = 'block';
    row.appendChild(label);

    if (Array.isArray(f.options) && f.options.length > 0) {
      // 選択肢ありはラジオで描画
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
      getValue[f.key] = () => {
        const sel = radios.find((r) => r.checked);
        return sel ? sel.value : '';
      };
    } else {
      const input = document.createElement('input');
      input.type = f.secret ? 'password' : 'text';
      if (f.secret) {
        input.placeholder = f.set ? '（設定済み・変更時のみ入力）' : '';
      } else {
        input.value = f.value || '';
      }
      getValue[f.key] = () => input.value;
      row.appendChild(input);
    }
    form.appendChild(row);
  }
  const save = document.createElement('button');
  save.className = 'run-btn';
  save.textContent = 'Save';
  save.addEventListener('click', async () => {
    const values = {};
    for (const k of Object.keys(getValue)) values[k] = getValue[k]();
    try {
      const res = await fetch('/plugins/settings/save', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: pl.id, values }),
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
