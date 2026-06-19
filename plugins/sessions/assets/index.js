// sessions プラグインのフロント。/plugins/sessions/list を叩いて一覧表示し、
// 各行に Resume ボタンを置いて agentarium.openAgentTab({resume:<uuid>}) を呼ぶ。
export async function render(root) {
  root.textContent = '';
  const h = document.createElement('h2');
  h.textContent = 'Sessions';
  root.appendChild(h);

  let sessions = [];
  try {
    const res = await fetch('/plugins/sessions/list');
    if (!res.ok) {
      root.appendChild(text('failed to load: HTTP ' + res.status));
      return;
    }
    sessions = await res.json();
  } catch (e) {
    root.appendChild(text('failed to load: ' + e));
    return;
  }

  if (!sessions || sessions.length === 0) {
    root.appendChild(text('セッションがありません（~/.claude/projects/<workdir> が空 / 未存在）'));
    return;
  }

  const table = document.createElement('table');
  table.className = 'task-table';
  const header = document.createElement('tr');
  for (const t of ['UUID', '更新時刻', '要約', '']) {
    const th = document.createElement('th');
    th.textContent = t;
    header.appendChild(th);
  }
  table.appendChild(header);

  for (const s of sessions) {
    const tr = document.createElement('tr');

    // UUID cell: mono font via <code>, tooltip shows full uuid
    const uuidTd = document.createElement('td');
    const uuidCode = document.createElement('code');
    uuidCode.textContent = shortUUID(s.uuid);
    uuidCode.title = s.uuid;
    uuidTd.appendChild(uuidCode);
    tr.appendChild(uuidTd);

    addCell(tr, formatTime(s.mod_time));
    addCell(tr, s.summary || '');

    const btnCell = document.createElement('td');
    const btn = document.createElement('button');
    btn.textContent = 'Resume';
    btn.className = 'resume-btn';
    btn.addEventListener('click', () => {
      const key = 'session-' + s.uuid;
      globalThis.agentarium.openAgentTab({
        key: key,
        label: shortUUID(s.uuid),
        agent: 'claude',
        resume: s.uuid,
      });
    });
    btnCell.appendChild(btn);
    tr.appendChild(btnCell);
    table.appendChild(tr);
  }
  root.appendChild(table);
}

function text(s) {
  const p = document.createElement('p');
  p.textContent = s;
  return p;
}

function addCell(tr, value) {
  const td = document.createElement('td');
  td.textContent = value;
  tr.appendChild(td);
  return td;
}

function shortUUID(u) {
  if (!u) return '';
  if (u.length <= 12) return u;
  return u.slice(0, 8) + '…' + u.slice(-3);
}

function formatTime(iso) {
  if (!iso) return '';
  try {
    const d = new Date(iso);
    return d.toLocaleString();
  } catch (_) {
    return iso;
  }
}
