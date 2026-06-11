// hello プラグインのフロント。/plugins/hello/ping を叩き、加えて Agent タブを開く
// ボタンを置いて agentarium.openAgentTab ホスト API のデモにする。
export async function render(root) {
  const res = await fetch('/plugins/hello/ping');
  const data = await res.json();

  const card = document.createElement('div');
  card.className = 'card';

  const head = document.createElement('div');
  head.className = 'card-head';
  const h = document.createElement('h2');
  h.textContent = 'Hello plugin';
  head.appendChild(h);

  const body = document.createElement('div');
  body.style.padding = '12px 14px';

  const p = document.createElement('p');
  p.className = 'muted';
  p.textContent = 'ping → ' + data.message;

  const btn = document.createElement('button');
  btn.className = 'run-btn';
  btn.style.marginTop = '10px';
  btn.textContent = 'Open Agent tab (demo)';
  btn.addEventListener('click', () => {
    const key = 'agent-demo-' + Date.now();
    window.agentarium.openAgentTab({
      key: key,
      label: 'Agent',
      agent: 'claude',
    });
  });

  const docBtn = document.createElement('button');
  docBtn.className = 'run-btn';
  docBtn.style.marginTop = '10px';
  docBtn.style.marginLeft = '8px';
  docBtn.textContent = 'ドキュメント表示';
  docBtn.addEventListener('click', () => {
    if (!window.agentarium || typeof window.agentarium.openViewer !== 'function') {
      alert('viewer API が利用できません');
      return;
    }
    window.agentarium.openViewer({
      key: 'hello-doc',
      title: 'Hello Doc',
      type: 'markdown',
      content: '# Hello\n\n**bold** と `code`、\n\n- item 1\n- item 2\n\n[link](https://example.com)',
    });
  });

  body.appendChild(p);
  body.appendChild(btn);
  body.appendChild(docBtn);
  card.appendChild(head);
  card.appendChild(body);
  root.appendChild(card);
}
