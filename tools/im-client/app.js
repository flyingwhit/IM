// =============================================================================
// IM Backend Test Client — app.js
//
// A simple vanilla-JS client for testing the IM backend.
// No frameworks, no build tools — open index.html in a browser.
//
// Architecture:
//   - State: stored in localStorage (tokens, server URL) and module globals
//   - API:   central apiCall() function attaches JWT automatically
//   - WS:    WebSocket client wraps native WebSocket with reconnection logic
//   - UI:    direct DOM manipulation, no virtual DOM
// =============================================================================

// --- State -------------------------------------------------------------------

const store = {
  // Persisted (localStorage keys)
  get serverUrl()  { return localStorage.getItem('im_server_url') || 'http://localhost:8080'; },
  set serverUrl(v) { localStorage.setItem('im_server_url', v); },
  get accessToken()  { return localStorage.getItem('im_access_token') || ''; },
  set accessToken(v) { localStorage.setItem('im_access_token', v); },
  get refreshToken()  { return localStorage.getItem('im_refresh_token') || ''; },
  set refreshToken(v) { localStorage.setItem('im_refresh_token', v); },
  get expiresIn()  { return parseInt(localStorage.getItem('im_expires_in') || '0'); },
  set expiresIn(v) { localStorage.setItem('im_expires_in', String(v)); },
  get userId()  { return localStorage.getItem('im_user_id') || ''; },
  set userId(v) { localStorage.setItem('im_user_id', v); },

  isLoggedIn() { return !!this.accessToken; },

  clear() {
    ['im_access_token','im_refresh_token','im_expires_in','im_user_id'].forEach(k =>
      localStorage.removeItem(k));
  }
};

// --- Tab Switching -----------------------------------------------------------

function initTabs() {
  document.querySelectorAll('.tab-btn').forEach(btn => {
    btn.addEventListener('click', () => {
      document.querySelectorAll('.tab-btn').forEach(b => b.classList.remove('active'));
      document.querySelectorAll('.tab-content').forEach(c => c.classList.remove('active'));
      btn.classList.add('active');
      document.getElementById('tab-' + btn.dataset.tab).classList.add('active');
    });
  });
}

// --- Server Ping -------------------------------------------------------------

async function pingServer() {
  const url = store.serverUrl;
  const start = performance.now();
  try {
    const resp = await fetch(url + '/health');
    const ms = Math.round(performance.now() - start);
    if (resp.ok) {
      setServerStatus('online', `Connected (${ms}ms)`);
    } else {
      setServerStatus('offline', `Health check failed: ${resp.status}`);
    }
  } catch (e) {
    setServerStatus('offline', `Cannot reach server: ${e.message}`);
  }
}

function setServerStatus(status, label) {
  const dot = document.getElementById('server-indicator');
  const lbl = document.getElementById('server-label');
  dot.className = 'status-dot ' + status;
  lbl.textContent = label;
}

// --- API Client --------------------------------------------------------------

/**
 * Make an authenticated API call.
 *
 * Automatically attaches the Bearer token if the user is logged in.
 * Logs result to the response panel on the REST tab.
 *
 * @param {string} method  - HTTP method
 * @param {string} path    - URL path (e.g. "/api/v1/users/me")
 * @param {object|FormData|null} body - request body (JSON-serialized if plain object)
 * @returns {Promise<{ok: boolean, status: number, data: any, ms: number}>}
 */
async function apiCall(method, path, body = null) {
  const url = store.serverUrl + path;
  const headers = {};

  if (store.isLoggedIn()) {
    headers['Authorization'] = 'Bearer ' + store.accessToken;
  }

  if (body && !(body instanceof FormData)) {
    headers['Content-Type'] = 'application/json';
  }

  const opts = { method, headers };
  if (body) {
    opts.body = body instanceof FormData ? body : JSON.stringify(body);
  }

  const start = performance.now();
  let resp, data, error;
  try {
    resp = await fetch(url, opts);
  } catch (e) {
    error = e;
  }
  const ms = Math.round(performance.now() - start);

  if (error) {
    showResponse(-1, ms, { error: error.message });
    return { ok: false, status: -1, data: { error: error.message }, ms };
  }

  const text = await resp.text();
  try { data = JSON.parse(text); } catch (_) { data = text; }

  showResponse(resp.status, ms, data);
  return { ok: resp.ok, status: resp.status, data, ms };
}

/**
 * Display API response in the response panel.
 */
function showResponse(status, ms, body) {
  const panel = document.querySelector('.response-card');
  const statusEl = document.getElementById('resp-status');
  const timeEl = document.getElementById('resp-time');
  const bodyEl = document.getElementById('resp-body');

  // Clear previous status classes
  panel.classList.remove('success', 'client-error', 'server-error', 'network-error');

  if (status === -1) {
    statusEl.textContent = 'NETWORK ERROR';
    panel.classList.add('network-error');
  } else {
    statusEl.textContent = status;
    if (status >= 200 && status < 300) panel.classList.add('success');
    else if (status >= 400 && status < 500) panel.classList.add('client-error');
    else panel.classList.add('server-error');
  }

  timeEl.textContent = ms + 'ms';
  bodyEl.textContent = typeof body === 'string' ? body : JSON.stringify(body, null, 2);

  // Switch to REST tab to show the result
  document.querySelector('.tab-btn[data-tab="rest"]').click();
}

// --- Auth Handlers -----------------------------------------------------------

async function handleRegister(e) {
  e.preventDefault();
  const form = e.target;
  const body = {
    username: form.username.value.trim(),
    email: form.email.value.trim(),
    password: form.password.value,
  };
  const { ok, data } = await apiCall('POST', '/api/v1/auth/register', body);
  if (ok) {
    form.reset();
    // Pre-fill login form
    document.querySelector('#form-login [name="username"]').value = body.username;
  }
}

async function handleLogin(e) {
  e.preventDefault();
  const form = e.target;
  const body = {
    username: form.username.value.trim(),
    password: form.password.value,
  };
  const { ok, data } = await apiCall('POST', '/api/v1/auth/login', body);
  if (ok) {
    store.accessToken = data.access_token;
    store.refreshToken = data.refresh_token;
    store.expiresIn = data.expires_in;
    // Decode JWT to extract user ID (the payload is at index 1 of "xxx.yyy.zzz")
    try {
      const payload = JSON.parse(atob(data.access_token.split('.')[1]));
      store.userId = payload.user_id;
    } catch (_) { /* ignore decode failure */ }
    form.reset();
    updateSessionUI();
  }
}

async function handleLogout() {
  const body = { refresh_token: store.refreshToken };
  await apiCall('POST', '/api/v1/auth/logout', body);
  store.clear();
  updateSessionUI();
  updateWsUI();
}

function updateSessionUI() {
  const card = document.getElementById('session-card');
  const info = document.getElementById('session-info');

  if (store.isLoggedIn()) {
    card.style.display = '';
    document.getElementById('sess-user-id').textContent = store.userId || '(unknown)';
    document.getElementById('sess-access-token').textContent =
      store.accessToken.substring(0, 30) + '...';
    document.getElementById('sess-refresh-token').textContent =
      store.refreshToken.substring(0, 30) + '...';
    document.getElementById('sess-expires').textContent = store.expiresIn + 's';
    info.textContent = 'Logged in: ' + (store.userId || '?');
  } else {
    card.style.display = 'none';
    info.textContent = '';
  }
}

// --- REST API Button Handlers ------------------------------------------------

function initApiButtons() {
  document.querySelectorAll('.api-btn').forEach(btn => {
    btn.addEventListener('click', () => handleApiClick(btn));
  });
}

async function handleApiClick(btn) {
  const method = btn.dataset.method;
  let path = btn.dataset.path;
  let body = null;

  // Handle path parameter substitution: {id} → value from input
  if (btn.dataset.pathParam) {
    const input = document.getElementById(btn.dataset.pathParam);
    if (!input || !input.value.trim()) {
      showResponse(-1, 0, { error: 'Please fill in the required field.' });
      return;
    }
    path = path.replace('{id}', input.value.trim());
  }

  // Handle query parameters
  if (btn.dataset.query === 'msg-query') {
    const peer = document.getElementById('msg-peer').value.trim();
    const before = document.getElementById('msg-before').value.trim();
    const limit = document.getElementById('msg-limit').value;
    if (!peer) {
      showResponse(-1, 0, { error: '"peer" query parameter is required.' });
      return;
    }
    const params = new URLSearchParams({ peer });
    if (before) params.set('before', before);
    params.set('limit', limit || '50');
    path = path + '?' + params.toString();
  }

  // Handle request body
  if (btn.dataset.body === 'update-profile') {
    const nickname = document.getElementById('update-nickname').value.trim();
    const avatar = document.getElementById('update-avatar').value.trim();
    body = {};
    if (nickname) body.nickname = nickname;
    if (avatar) body.avatar_url = avatar;
    if (Object.keys(body).length === 0) {
      showResponse(-1, 0, { error: 'Fill at least one field (nickname or avatar_url).' });
      return;
    }
  } else if (btn.dataset.body === 'friend-request') {
    const targetId = document.getElementById('friend-target-id').value.trim();
    if (!targetId) {
      showResponse(-1, 0, { error: 'Target user ID is required.' });
      return;
    }
    body = { target_id: targetId };
  }

  await apiCall(method, path, body);
}

// --- WebSocket Client --------------------------------------------------------

let ws = null;
let wsReconnectTimer = null;

function getWsUrl() {
  const httpUrl = store.serverUrl;
  // Replace http:// → ws:// and https:// → wss://
  const wsUrl = httpUrl.replace(/^http/, 'ws');
  return wsUrl + '/ws?token=' + store.accessToken;
}

function connectWs() {
  if (!store.isLoggedIn()) {
    wsLog('system', 'Cannot connect: not logged in. Please login first.');
    return;
  }

  if (ws && ws.readyState === WebSocket.OPEN) {
    wsLog('system', 'Already connected.');
    return;
  }

  const url = getWsUrl();
  wsLog('system', 'Connecting to ' + url.replace(/\?token=.*/, '?token=***'));

  try {
    ws = new WebSocket(url);
  } catch (e) {
    wsLog('error', 'Failed to create WebSocket: ' + e.message);
    return;
  }

  ws.onopen = () => {
    wsLog('system', 'WebSocket connected.');
    updateWsUI();
    // Start heartbeat
    startHeartbeat();
  };

  ws.onmessage = (event) => {
    let env;
    try {
      env = JSON.parse(event.data);
    } catch (_) {
      wsLog('error', 'Received invalid JSON: ' + event.data);
      return;
    }
    handleWsMessage(env);
  };

  ws.onclose = (event) => {
    wsLog('system', `WebSocket closed (code=${event.code}, reason="${event.reason}")`);
    stopHeartbeat();
    ws = null;
    updateWsUI();
  };

  ws.onerror = () => {
    wsLog('error', 'WebSocket error occurred.');
  };
}

function disconnectWs() {
  if (wsReconnectTimer) {
    clearInterval(wsReconnectTimer);
    wsReconnectTimer = null;
  }
  stopHeartbeat();
  if (ws) {
    ws.close(1000, 'User disconnected');
    ws = null;
  }
  wsLog('system', 'Disconnected.');
  updateWsUI();
}

/**
 * Handle an incoming WebSocket envelope.
 */
function handleWsMessage(env) {
  switch (env.type) {
    case 'message.new':
      wsLog('received',
        `[from: ${env.payload.from}] ${env.payload.content}` +
        ` (id=${env.payload.id})`);
      break;
    case 'message.ack':
      wsLog('ack',
        `Message ${env.payload.id}: ${env.payload.status}`);
      break;
    case 'pong':
      // Silently update heartbeat — only log if verbose
      break;
    case 'error':
      wsLog('error',
        `[${env.payload.code}] ${env.payload.message}`);
      break;
    default:
      wsLog('system', `Unknown message type: ${env.type}`);
  }
}

function handleWsSend(e) {
  e.preventDefault();
  if (!ws || ws.readyState !== WebSocket.OPEN) {
    wsLog('error', 'Not connected.');
    return;
  }

  const to = document.getElementById('ws-msg-to').value.trim();
  const content = document.getElementById('ws-msg-content').value.trim();
  if (!to || !content) return;

  const env = {
    type: 'message.send',
    payload: {
      to: to,
      content: content,
      content_type: 'text',
    },
  };

  ws.send(JSON.stringify(env));
  wsLog('sent', `[to: ${to}] ${content}`);
  document.getElementById('ws-msg-content').value = '';
  document.getElementById('ws-msg-content').focus();
}

// --- Heartbeat (client-initiated ping every 30s) -----------------------------
let heartbeatTimer = null;

function startHeartbeat() {
  stopHeartbeat();
  heartbeatTimer = setInterval(() => {
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ type: 'ping' }));
    }
  }, 30000);
}

function stopHeartbeat() {
  if (heartbeatTimer) {
    clearInterval(heartbeatTimer);
    heartbeatTimer = null;
  }
}

function updateWsUI() {
  const connected = ws && ws.readyState === WebSocket.OPEN;
  const dot = document.getElementById('ws-status-dot');
  const text = document.getElementById('ws-status-text');
  const btnConnect = document.getElementById('btn-ws-connect');
  const btnDisconnect = document.getElementById('btn-ws-disconnect');
  const btnSend = document.getElementById('btn-ws-send');

  if (connected) {
    dot.className = 'status-dot connected';
    text.textContent = 'Connected';
    btnConnect.disabled = true;
    btnDisconnect.disabled = false;
    btnSend.disabled = false;
  } else {
    dot.className = 'status-dot disconnected';
    text.textContent = 'Disconnected';
    btnConnect.disabled = !store.isLoggedIn();
    btnDisconnect.disabled = true;
    btnSend.disabled = true;
  }
}

// --- WebSocket Log -----------------------------------------------------------

function wsLog(category, message) {
  const container = document.getElementById('ws-log');
  const time = new Date().toLocaleTimeString();

  const entry = document.createElement('div');
  entry.className = 'log-entry ' + category;
  entry.innerHTML = `<span class="log-time">${time}</span>${escapeHtml(message)}`;
  container.appendChild(entry);

  // Auto-scroll if enabled
  if (document.getElementById('ws-auto-scroll').checked) {
    container.scrollTop = container.scrollHeight;
  }
}

function clearWsLog() {
  const container = document.getElementById('ws-log');
  container.innerHTML = '';
  wsLog('system', 'Log cleared.');
}

function escapeHtml(str) {
  const div = document.createElement('div');
  div.textContent = str;
  return div.innerHTML;
}

// --- Initialization ----------------------------------------------------------

function init() {
  // Set server URL from store
  document.getElementById('server-url').value = store.serverUrl;

  // Tab switching
  initTabs();

  // Server events
  document.getElementById('btn-ping').addEventListener('click', pingServer);
  document.getElementById('server-url').addEventListener('change', function() {
    store.serverUrl = this.value.trim();
    setServerStatus('offline', 'Server URL changed — ping to verify');
  });

  // Auth events
  document.getElementById('form-register').addEventListener('submit', handleRegister);
  document.getElementById('form-login').addEventListener('submit', handleLogin);
  document.getElementById('btn-logout').addEventListener('click', handleLogout);

  // REST API buttons
  initApiButtons();

  // WebSocket events
  document.getElementById('btn-ws-connect').addEventListener('click', connectWs);
  document.getElementById('btn-ws-disconnect').addEventListener('click', disconnectWs);
  document.getElementById('form-ws-send').addEventListener('submit', handleWsSend);
  document.getElementById('btn-clear-log').addEventListener('click', clearWsLog);

  // Initial UI state
  updateSessionUI();
  updateWsUI();

  // Ping server on load
  pingServer();

  console.log('IM Test Client initialized.');
  console.log('Server:', store.serverUrl);
  console.log('Logged in:', store.isLoggedIn());
}

// Boot
document.addEventListener('DOMContentLoaded', init);
