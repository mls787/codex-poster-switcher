const token = document.querySelector('meta[name="poster-token"]').content;
const grid = document.getElementById('theme-grid');
const status = document.getElementById('status');
const fileInput = document.getElementById('image-input');
const dropzone = document.getElementById('dropzone');

function show(message, error = false) {
  status.textContent = message;
  status.classList.toggle('error', error);
}

async function request(path, body) {
  const options = { method: 'POST', headers: { 'X-Poster-Token': token } };
  if (body instanceof FormData) options.body = body;
  else {
    options.headers['Content-Type'] = 'application/json';
    options.body = JSON.stringify(body || {});
  }
  const response = await fetch(path, options);
  const result = await response.json();
  if (!response.ok) throw new Error(result.error || '操作失败');
  return result;
}

async function busy(button, task) {
  button.disabled = true;
  try { await task(); }
  catch (error) { show(error.message, true); }
  finally { button.disabled = false; }
}

function renderTheme(theme, active) {
  const card = document.createElement('article');
  card.className = 'poster' + (active ? ' active' : '');
  const image = document.createElement('img');
  image.src = '/image/' + encodeURIComponent(theme.id);
  image.alt = theme.name;
  image.loading = 'lazy';
  const body = document.createElement('div');
  body.className = 'poster-body';
  const name = document.createElement('span');
  name.className = 'poster-name';
  name.textContent = theme.name;
  const actions = document.createElement('div');
  actions.className = 'poster-actions';
  const select = document.createElement('button');
  select.className = 'select-button';
  select.type = 'button';
  select.textContent = active ? '使用中' : '切换';
  select.disabled = active;
  select.addEventListener('click', () => busy(select, async () => {
    const result = await request('/api/activate', { id: theme.id });
    show(result.message);
    await refresh(false);
  }));
  const remove = document.createElement('button');
  remove.type = 'button';
  remove.textContent = '删除';
  remove.setAttribute('aria-label', '删除海报 ' + theme.name);
  remove.addEventListener('click', () => {
    if (!confirm('从本机图库删除“' + theme.name + '”？此操作无法撤销。')) return;
    busy(remove, async () => {
      const result = await request('/api/delete', { id: theme.id });
      show(result.message);
      await refresh(false);
    });
  });
  actions.append(select, remove);
  body.append(name, actions);
  card.append(image, body);
  return card;
}

async function refresh(updateMessage = true) {
  const response = await fetch('/api/state');
  const state = await response.json();
  document.getElementById('connection-dot').classList.toggle('connected', state.connected);
  document.getElementById('connection-label').textContent = state.connected ? '已连接 Codex' : '等待启动 Codex';
  const themes = state.library.themes || [];
  document.getElementById('theme-count').textContent = themes.length + ' 张';
  grid.replaceChildren(...themes.map(t => renderTheme(t, t.id === state.library.active)));
  if (updateMessage) show(state.message);
}

async function importFile(file) {
  if (!file) return;
  const data = new FormData();
  data.append('image', file, file.name);
  show('正在保存海报…');
  try {
    const result = await request('/api/import', data);
    show(result.message);
    await refresh(false);
  } catch (error) { show(error.message, true); }
  fileInput.value = '';
}

fileInput.addEventListener('change', () => importFile(fileInput.files[0]));
dropzone.addEventListener('keydown', event => {
  if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); fileInput.click(); }
});
for (const eventName of ['dragenter', 'dragover']) {
  dropzone.addEventListener(eventName, event => { event.preventDefault(); dropzone.classList.add('dragover'); });
}
for (const eventName of ['dragleave', 'drop']) {
  dropzone.addEventListener(eventName, event => { event.preventDefault(); dropzone.classList.remove('dragover'); });
}
dropzone.addEventListener('drop', event => importFile(event.dataTransfer.files[0]));

document.getElementById('launch-button').addEventListener('click', event => busy(event.currentTarget, async () => {
  show('正在等待 Codex 打开，最多 15 秒…');
  const result = await request('/api/launch');
  show(result.message);
  await refresh(false);
}));
document.getElementById('restore-button').addEventListener('click', event => busy(event.currentTarget, async () => {
  const result = await request('/api/restore');
  show(result.message);
  await refresh(false);
}));
document.getElementById('quit-button').addEventListener('click', event => busy(event.currentTarget, async () => {
  const result = await request('/api/quit');
  show(result.message);
}));

refresh().catch(error => show(error.message, true));
