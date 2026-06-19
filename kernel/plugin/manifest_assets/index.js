// 汎用 manifest list レンダラ。全 manifest プラグインで共有される単一実装。
// shell の activate() が render(root, {pluginId}) で呼ぶ。自分の manifest と
// dataURL を取得してテーブル描画し、行ボタンで window.agentarium.openAgentTab を呼ぶ。
// 値は textContent で挿入し HTML として解釈させない（XSS 回避）。

export async function render(root, opts) {
  const pluginId = opts && opts.pluginId;
  root.textContent = '';
  if (!pluginId) {
    root.appendChild(textEl('manifest renderer: pluginId が渡されていません'));
    return;
  }

  let manifest;
  try {
    const res = await fetch('/plugins/' + pluginId + '/manifest');
    if (!res.ok) { root.appendChild(textEl('manifest 取得失敗: HTTP ' + res.status)); return; }
    manifest = await res.json();
  } catch (e) {
    root.appendChild(textEl('manifest 取得失敗: ' + e));
    return;
  }

  const h = document.createElement('h2');
  h.textContent = manifest.title || pluginId;
  root.appendChild(h);

  let rows;
  try {
    const res = await fetch(manifest.dataURL);
    if (!res.ok) { root.appendChild(textEl('データ取得失敗: HTTP ' + res.status)); return; }
    rows = await res.json();
  } catch (e) {
    root.appendChild(textEl('データ取得失敗: ' + e));
    return;
  }

  if (!Array.isArray(rows) || rows.length === 0) {
    root.appendChild(textEl('データがありません'));
    return;
  }

  const columns = (manifest.list && manifest.list.columns) || [];
  const action = manifest.rowAction || null;

  const table = document.createElement('table');
  table.style.borderCollapse = 'collapse';
  table.style.width = '100%';

  const header = document.createElement('tr');
  for (const c of columns) header.appendChild(thEl(c.label));
  if (action) header.appendChild(thEl(''));
  table.appendChild(header);

  rows.forEach((row, index) => {
    const tr = document.createElement('tr');
    for (const c of columns) tdEl(tr, subst(c.value, row));
    if (action) {
      const cell = document.createElement('td');
      cell.style.padding = '4px 8px';
      cell.style.borderBottom = '1px solid #eee';
      const btn = document.createElement('button');
      btn.textContent = action.label || 'Open';
      btn.addEventListener('click', () => openAgent(pluginId, action, row, index));
      cell.appendChild(btn);
      tr.appendChild(cell);
    }
    table.appendChild(tr);
  });
  root.appendChild(table);
}

function openAgent(pluginId, action, row, index) {
  const api = globalThis.agentarium;
  if (!api || typeof api.openAgentTab !== 'function') {
    alert('agentarium host API が利用できません');
    return;
  }
  const key = action.key ? subst(action.key, row) : (pluginId + '-' + index);
  const opts = {
    key: key,
    label: action.tabLabel ? subst(action.tabLabel, row) : (action.label || key),
    agent: action.agent,
  };
  if (action.model)   opts.model   = subst(action.model, row);
  if (action.resume)  opts.resume  = subst(action.resume, row);
  if (action.command) opts.command = subst(action.command, row);
  if (action.autoEnter) opts.autoEnter = true;
  api.openAgentTab(opts);
}

// subst は "{{.key}}"（前後空白許容）を String(row[key] ?? '') に置換する。
// 条件・ループ・パイプは扱わない（spec の最小スコープ）。
function subst(tmpl, row) {
  if (typeof tmpl !== 'string') return '';
  return tmpl.replace(/\{\{\s*\.([a-zA-Z0-9_]+)\s*\}\}/g, (_, key) => {
    const v = row[key];
    return v === undefined || v === null ? '' : String(v);
  });
}

function textEl(s) { const p = document.createElement('p'); p.textContent = s; return p; }
function thEl(s) {
  const th = document.createElement('th');
  th.textContent = s;
  th.style.textAlign = 'left';
  th.style.padding = '4px 8px';
  th.style.borderBottom = '1px solid #cbd5e1';
  return th;
}
function tdEl(tr, s) {
  const td = document.createElement('td');
  td.textContent = s; // textContent で XSS 回避
  td.style.padding = '4px 8px';
  td.style.borderBottom = '1px solid #eee';
  tr.appendChild(td);
  return td;
}
