// =============================================================================
// IM Backend Test Client — app.js
//
// A simple vanilla-JS client for testing the IM backend.
// No frameworks, no build tools — open index.html in a browser.
//
// Architecture:
//   - Sessions: stored as a JSON array in localStorage (supports multiple users)
//   - API:      central apiCall() attaches JWT of the active session automatically
//   - WS:       WebSocket client wraps native WebSocket, one per active session
//   - UI:       direct DOM manipulation, no virtual DOM
// =============================================================================

// --- Session Store -----------------------------------------------------------
//
// Sessions are stored in localStorage as:
//   im_sessions:  JSON array of {id, label, userId, accessToken, refreshToken, expiresIn}
//   im_active_id: string — ID of the currently active session
//
// This allows multiple users to be logged in simultaneously across tabs.
// Each tab can independently switch its active session.

const sessionStore = {
  // Migrate old single-key format to new multi-session format.
  // Runs once on first access, then is a no-op.
  _migrate() {
    if (localStorage.getItem('_im_migrated')) return;
    const oldToken = localStorage.getItem('im_access_token');
    if (oldToken) {
      const session = {
        id: 's_' + Date.now(),
        label: localStorage.getItem('im_user_id') || 'unknown',
        userId: localStorage.getItem('im_user_id') || '',
        accessToken: oldToken,
        refreshToken: localStorage.getItem('im_refresh_token') || '',
        expiresIn: parseInt(localStorage.getItem('im_expires_in') || '0'),
      };
      ['im_access_token','im_refresh_token','im_expires_in','im_user_id'].forEach(
        k => localStorage.removeItem(k));
      localStorage.setItem('im_sessions', JSON.stringify([session]));
      localStorage.setItem('im_active_id', session.id);
    }
    localStorage.setItem('_im_migrated', '1');
  },

  /** @returns {Array<{id:string, label:string, userId:string, accessToken:string, refreshToken:string, expiresIn:number}>} */
  getAll() {
    this._migrate();
    try { return JSON.parse(localStorage.getItem('im_sessions') || '[]'); }
    catch (_) { return []; }
  },

  _saveAll(list) {
    localStorage.setItem('im_sessions', JSON.stringify(list));
  },

  /** @returns {object|null} the active session, or null if none */
  getActive() {
    const list = this.getAll();
    if (list.length === 0) return null;
    const activeId = localStorage.getItem('im_active_id');
    return list.find(s => s.id === activeId) || list[0];
  },

  /**
   * Add a new session or update an existing one for the same userId.
   * Automatically makes the session active.
   */
  upsert(label, userId, accessToken, refreshToken, expiresIn) {
    const list = this.getAll();
    const existing = list.find(s => s.userId === userId);
    if (existing) {
      existing.label = label;
      existing.accessToken = accessToken;
      existing.refreshToken = refreshToken;
      existing.expiresIn = expiresIn;
      localStorage.setItem('im_active_id', existing.id);
    } else {
      const session = {
        id: 's_' + Date.now() + '_' + Math.random().toString(36).slice(2, 6),
        label,
        userId,
        accessToken,
        refreshToken,
        expiresIn,
      };
      list.push(session);
      localStorage.setItem('im_active_id', session.id);
    }
    this._saveAll(list);
  },

  /** Remove a session by ID. If it was active, activate the most recent remaining session. */
  remove(id) {
    let list = this.getAll();
    list = list.filter(s => s.id !== id);
    this._saveAll(list);
    if (localStorage.getItem('im_active_id') === id) {
      const next = list.length > 0 ? list[list.length - 1] : null;
      localStorage.setItem('im_active_id', next ? next.id : '');
    }
  },

  /** Switch the active session to a different ID. */
  setActive(id) {
    localStorage.setItem('im_active_id', id);
  },

  /** True if there is an active session with a non-empty access token. */
  isLoggedIn() {
    const a = this.getActive();
    return !!(a && a.accessToken);
  },

  // --- Convenience getters (read from active session) ---
  get accessToken()  { return this.getActive()?.accessToken || ''; },
  get refreshToken() { return this.getActive()?.refreshToken || ''; },
  get expiresIn()    { return this.getActive()?.expiresIn || 0; },
  get userId()       { return this.getActive()?.userId || ''; },

  /** Remove all sessions. */
  clearAll() {
    localStorage.removeItem('im_sessions');
    localStorage.removeItem('im_active_id');
  },
};

// Server URL — not per-user, stored separately.
const serverUrlStore = {
  get() { return localStorage.getItem('im_server_url') || 'http://localhost:8080'; },
  set(v) { localStorage.setItem('im_server_url', v); },
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
  const url = serverUrlStore.get();
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
 * Automatically attaches the Bearer token of the active session.
 * Logs result to the response panel on the REST tab.
 */
async function apiCall(method, path, body = null) {
  const url = serverUrlStore.get() + path;
  const headers = {};

  if (sessionStore.isLoggedIn()) {
    headers['Authorization'] = 'Bearer ' + sessionStore.accessToken;
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

function showResponse(status, ms, body) {
  const panel = document.querySelector('.response-card');
  const statusEl = document.getElementById('resp-status');
  const timeEl = document.getElementById('resp-time');
  const bodyEl = document.getElementById('resp-body');

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
    document.querySelector('#form-login [name="username"]').value = body.username;
  }
}

async function handleLogin(e) {
  e.preventDefault();
  const form = e.target;
  const username = form.username.value.trim();
  const password = form.password.value;

  const { ok, data } = await apiCall('POST', '/api/v1/auth/login', { username, password });
  if (!ok) return;

  // Decode JWT to extract user ID
  let userId = '';
  try {
    const payload = JSON.parse(atob(data.access_token.split('.')[1]));
    userId = payload.user_id;
  } catch (_) { /* ignore decode failure */ }

  // Upsert — updates existing session for this user, or adds a new one
  sessionStore.upsert(username, userId, data.access_token, data.refresh_token, data.expires_in);

  form.reset();
  refreshSessionUI();
}

async function handleLogout() {
  const active = sessionStore.getActive();
  if (!active) return;

  // Best-effort: tell the server to invalidate the refresh token
  try {
    await apiCall('POST', '/api/v1/auth/logout', { refresh_token: active.refreshToken });
  } catch (_) { /* server may be down or token expired — still clear locally */ }

  sessionStore.remove(active.id);
  disconnectWs();
  refreshSessionUI();
  updateWsUI();
}

function switchSession(id) {
  sessionStore.setActive(id);
  disconnectWs();
  refreshSessionUI();
  updateWsUI();
}

function removeSession(id) {
  const active = sessionStore.getActive();
  const wasActive = active && active.id === id;
  sessionStore.remove(id);
  if (wasActive) disconnectWs();
  refreshSessionUI();
  updateWsUI();
}

function refreshSessionUI() {
  const container = document.getElementById('session-list');
  const active = sessionStore.getActive();
  const all = sessionStore.getAll();

  if (all.length === 0) {
    container.innerHTML = '<p class="hint">No active sessions. Register or login above.</p>';
    document.getElementById('session-info').textContent = '';
    return;
  }

  let html = '';
  for (const s of all) {
    const isActive = active && s.id === active.id;
    html += `
      <div class="session-row ${isActive ? 'active' : ''}">
        <div class="session-main">
          <span class="session-label">${escapeHtml(s.label)}</span>
          <span class="session-userid">${escapeHtml(s.userId.substring(0, 8))}...</span>
          <span class="session-token">${escapeHtml(s.accessToken.substring(0, 16))}...</span>
        </div>
        <div class="session-actions">
          ${isActive
            ? '<span class="badge-active">active</span>'
            : `<button class="btn btn-sm" onclick="switchSession('${s.id}')">Switch</button>`
          }
          <button class="btn btn-sm btn-danger-outline" onclick="removeSession('${s.id}')">✕</button>
        </div>
      </div>`;
  }
  container.innerHTML = html;

  document.getElementById('session-info').textContent =
    active ? 'Active: ' + active.label : '';
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

  if (btn.dataset.pathParam) {
    const input = document.getElementById(btn.dataset.pathParam);
    if (!input || !input.value.trim()) {
      showResponse(-1, 0, { error: 'Please fill in the required field.' });
      return;
    }
    path = path.replace('{id}', input.value.trim());
  }

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

function getWsUrl() {
  const httpUrl = serverUrlStore.get();
  const wsUrl = httpUrl.replace(/^http/, 'ws');
  return wsUrl + '/ws?token=' + sessionStore.accessToken;
}

function connectWs() {
  if (!sessionStore.isLoggedIn()) {
    wsLog('system', 'Cannot connect: no active session. Please login first.');
    return;
  }

  // Disconnect any existing WebSocket before opening a new one
  if (ws) disconnectWs();

  const url = getWsUrl();
  wsLog('system', 'Connecting to ' + url.replace(/\?token=.*/, '?token=***'));

  try {
    ws = new WebSocket(url);
  } catch (e) {
    wsLog('error', 'Failed to create WebSocket: ' + e.message);
    return;
  }

  ws.onopen = () => {
    wsLog('system', 'WebSocket connected as ' + sessionStore.getActive()?.label);
    updateWsUI();
    startHeartbeat();
  };

  ws.onmessage = (event) => {
    let env;
    try { env = JSON.parse(event.data); } catch (_) {
      wsLog('error', 'Received invalid JSON: ' + event.data);
      return;
    }
    handleWsMessage(env);
  };

  ws.onclose = (event) => {
    wsLog('system', `WebSocket closed (code=${event.code}, reason="${event.reason || ''}")`);
    stopHeartbeat();
    ws = null;
    updateWsUI();
  };

  ws.onerror = () => {
    wsLog('error', 'WebSocket error occurred.');
  };
}

function disconnectWs() {
  stopHeartbeat();
  if (ws) {
    const wasOpen = ws.readyState === WebSocket.OPEN;
    ws.close(1000, 'User disconnected');
    ws = null;
    if (wasOpen) wsLog('system', 'Disconnected.');
  }
  updateWsUI();
}

function handleWsMessage(env) {
  switch (env.type) {
    case 'message.new':
      wsLog('received',
        `[from: ${env.payload.from}] ${env.payload.content}` +
        ` (id=${env.payload.id})`);
      break;
    case 'message.ack':
      wsLog('ack', `Message ${env.payload.id}: ${env.payload.status}`);
      break;
    case 'pong':
      break;
    case 'error':
      wsLog('error', `[${env.payload.code}] ${env.payload.message}`);
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

  ws.send(JSON.stringify({
    type: 'message.send',
    payload: { to, content, content_type: 'text' },
  }));
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
  document.getElementById('ws-status-dot').className =
    'status-dot ' + (connected ? 'connected' : 'disconnected');
  document.getElementById('ws-status-text').textContent =
    connected ? 'Connected' : 'Disconnected';
  document.getElementById('btn-ws-connect').disabled = connected || !sessionStore.isLoggedIn();
  document.getElementById('btn-ws-disconnect').disabled = !connected;
  document.getElementById('btn-ws-send').disabled = !connected;
}

// --- WebSocket Log -----------------------------------------------------------

function wsLog(category, message) {
  const container = document.getElementById('ws-log');
  const time = new Date().toLocaleTimeString();

  const entry = document.createElement('div');
  entry.className = 'log-entry ' + category;
  entry.innerHTML = `<span class="log-time">${time}</span>${escapeHtml(message)}`;
  container.appendChild(entry);

  if (document.getElementById('ws-auto-scroll').checked) {
    container.scrollTop = container.scrollHeight;
  }
}

function clearWsLog() {
  document.getElementById('ws-log').innerHTML = '';
  wsLog('system', 'Log cleared.');
}

function escapeHtml(str) {
  const div = document.createElement('div');
  div.textContent = str;
  return div.innerHTML;
}

// --- Cross-tab Sync ----------------------------------------------------------
//
// When another tab changes sessions or switches the active user, this tab
// picks up the change via the storage event and refreshes its UI.
window.addEventListener('storage', (e) => {
  if (e.key === 'im_sessions' || e.key === 'im_active_id') {
    refreshSessionUI();
    updateWsUI();
  }
});

// --- Initialization ----------------------------------------------------------

function init() {
  document.getElementById('server-url').value = serverUrlStore.get();

  initTabs();

  document.getElementById('btn-ping').addEventListener('click', pingServer);
  document.getElementById('server-url').addEventListener('change', function() {
    serverUrlStore.set(this.value.trim());
    setServerStatus('offline', 'Server URL changed — ping to verify');
  });

  document.getElementById('form-register').addEventListener('submit', handleRegister);
  document.getElementById('form-login').addEventListener('submit', handleLogin);

  initApiButtons();

  document.getElementById('btn-ws-connect').addEventListener('click', connectWs);
  document.getElementById('btn-ws-disconnect').addEventListener('click', disconnectWs);
  document.getElementById('form-ws-send').addEventListener('submit', handleWsSend);
  document.getElementById('btn-clear-log').addEventListener('click', clearWsLog);

  refreshSessionUI();
  updateWsUI();
  pingServer();

  console.log('IM Test Client initialized.');
  console.log('Server:', serverUrlStore.get());
  console.log('Sessions:', sessionStore.getAll().length);
}

document.addEventListener('DOMContentLoaded', init);
