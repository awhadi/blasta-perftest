'use strict';
const $ = (id) => document.getElementById(id);
// Pages have real addresses (/templates/auth0). A route is still written the short way in this
// file, '#/templates/auth0'; the address bar shows the path. <base> in index.html says where
// the app starts, so it works at the root of a host or under a path such as /blasta.
const BASE = new URL(document.baseURI).pathname;
const toUrl = (route) => BASE + String(route).replace(/^#\/?/, '');
const routeStr = () => '#/' + location.pathname.slice(BASE.length) + location.search;
const HOME_TITLE = 'BLASTA: Load and Performance Testing Platform by AWHADI';
// Every test starts with a User-Agent header row naming BLASTA (and its version), so the
// systems under test can tell its traffic. It is an ordinary row: people can change or remove it.
const APP_VERSION = (document.querySelector('meta[name="blasta-version"]') || {}).content || '';
const BLASTA_UA = 'BLASTA' + (APP_VERSION ? '/' + APP_VERSION : '');
// A template may bring its own agent (the crawler test sends a Googlebot one): keep it, tagged.
const taggedAgent = (ua) => !ua ? BLASTA_UA : /blasta/i.test(ua) ? ua : ua + ' ' + BLASTA_UA;
const isAgentHeader = (k) => String(k).trim().toLowerCase() === 'user-agent';
const ROUTE_RE = /^(test|jobs|templates|history|running|login|admin|account|reset|confirm|confirm-email)(\/|$)/;
// Addresses from before pages had paths (/#/templates/auth0, and links in emails) still work.
if (/^#\/./.test(location.hash)) history.replaceState(null, '', toUrl(location.hash));
const api = async (path, opts = {}) => {
  // The API requires this marker so a page on another origin cannot drive the
  // load generator through the user's browser.
  opts.headers = Object.assign({ 'X-Requested-With': 'blasta' }, opts.headers || {});
  const r = await fetch('api' + path, opts);
  if (r.status === 401 && authCfg) {
    let code = '';
    try { code = (await r.clone().json()).code; } catch (e) {}
    if (code === 'unauthenticated') {
      if (guestMode) { openGate('login'); throw new Error('Please sign in'); }
      sessionEnded(); throw new Error('Please sign in');
    }
  }
  if (!r.ok) {
    let msg = r.statusText, code = '';
    try { const j = await r.json(); msg = j.error || msg; code = j.code || ''; } catch (e) {}
    const err = new Error(msg);
    err.status = r.status; err.code = code;
    throw err;
  }
  return r.status === 204 ? null : r.json();
};
// Go marshals time.Duration as nanoseconds.
const ns = 1e9;
const fmtLat = (us) => {
  if (us == null) return '-';
  if (us < 1000) return Math.round(us) + 'us';
  if (us < 1e6) return (us / 1000).toFixed(2) + 'ms';
  return (us / 1e6).toFixed(2) + 's';
};
const fmtInt = (n) => (n == null ? '-' : Math.round(n).toLocaleString());
const store = {
  get(k) { try { return localStorage.getItem(k); } catch (e) { return null; } },
  set(k, v) { try { localStorage.setItem(k, v); } catch (e) {} },
};

// The running (or last) test is remembered by id only, never its job, so no
// credential is ever kept in the browser. It lets a reload or a return visit
// reconnect to the live test.
const SESSION_KEY = 'blasta.session';
const saveSession = (s) => store.set(SESSION_KEY, JSON.stringify(s));
const loadSession = () => { try { return JSON.parse(store.get(SESSION_KEY) || 'null'); } catch (e) { return null; } };
const markRunning = () => {};   // running jobs are shown in the left panel now

let authCfg = null, me = null, appStarted = false, endingSession = false;   // sign-in state
let resendFor = '', appVersion = '';

// The version shows bottom right, for people who are signed in (or when sign-in is off).
function paintVersion() {
  const el = $('appVersion');
  if (!el) return;
  el.textContent = appVersion ? 'v' + appVersion : '';
  el.hidden = !appVersion || !!(authCfg && !me);
}
let guestMode = false, guestInfo = null, guestSkew = 0, guestTimer = null;   // a visitor without an account, on a short trial
let runId = null, es = null, series = [], lastSnap = null, healthy = true, activeSLO = null;

/* ---- Small UI helpers ------------------------------------------------- */

// A notification that slides in from the left and leaves the same way. `lines` are
// optional detail rows under the title (what exactly changed).
function toast(msg, kind, lines) {
  const t = document.createElement('div');
  t.className = 'toast ' + (kind || '');
  t.setAttribute('role', kind === 'error' ? 'alert' : 'status');
  const title = document.createElement('div');
  title.className = 'toast-title';
  title.textContent = msg;
  t.appendChild(title);
  if (lines && lines.length) {
    const ul = document.createElement('ul');
    ul.className = 'toast-lines';
    lines.forEach((l) => { const li = document.createElement('li'); li.textContent = l; ul.appendChild(li); });
    t.appendChild(ul);
  }
  $('toasts').appendChild(t);
  const life = (kind === 'error' ? 8000 : 4000) + (lines ? lines.length * 700 : 0);
  const leave = () => {
    if (!t.isConnected || t.classList.contains('leaving')) return;
    t.classList.add('leaving');
    t.addEventListener('animationend', () => t.remove(), { once: true });
    setTimeout(() => t.remove(), 600);              // in case animations are off
  };
  t.onclick = leave;
  setTimeout(leave, life);
}

/* ---- Short, specific messages for what a save did ---- */

const SETTINGS_TITLE = { general: 'General settings', guest: 'Free trial', smtp: 'SMTP connection', sso: 'Single sign-on', captcha: 'Bot protection', analytics: 'Analytics', privacy: 'Privacy settings', notifications: 'Notifications' };

// Which fields of a settings section differ from what was saved before.
function changedKeys(before, after) {
  before = before || {};
  return Object.keys(after).filter((k) => {
    const a = before[k], b = after[k];
    if (b === undefined) return false;
    if (['password', 'clientSecret', 'secretKey'].includes(k)) return !!b;       // a secret is only ever "replaced"
    return JSON.stringify(a == null ? (Array.isArray(b) ? [] : typeof b === 'boolean' ? false : '') : a) !== JSON.stringify(b);
  });
}

const turned = (what, on) => what + ' turned ' + (on ? 'on' : 'off');

// One line saying what was saved, in plain words.
function savedMessage(key, before, after) {
  before = before || {};
  const ch = changedKeys(before, after), has = (k) => ch.includes(k);
  if (!ch.length) return SETTINGS_TITLE[key] + ' unchanged';
  switch (key) {
    case 'general': {
      if (ch.length > 1) return 'General settings saved';
      if (has('registration')) return after.registration === 'closed' ? 'Registration turned off' : before.registration === 'closed' ? 'Registration turned on' : 'Registration settings saved';
      if (has('otpLogin')) return turned('One-time code sign-in', after.otpLogin);
      if (has('disableReset')) return turned('Password reset by email', !after.disableReset);
      if (has('allowedDomains')) return 'Allowed email domains saved';
      if (has('publicUrl')) return 'Public URL saved';
      return 'General settings saved';
    }
    case 'guest':
      return ch.length === 1 && has('enabled') ? turned('Free trial', after.enabled) : 'Free trial limits saved';
    case 'smtp':
      if (!before.host && after.host) return 'SMTP connection configured';
      if (ch.length === 1 && has('enabled')) return turned('Email delivery', after.enabled);
      return 'SMTP connection saved';
    case 'captcha':
      return has('enabled') ? turned('Bot protection', after.enabled) : 'Bot protection settings saved';
    case 'notifications':
      return ch.length === 1 && has('enabled') ? turned('Notifications', after.enabled) : 'Notification settings saved';
    case 'privacy':
      return has('mode') && ch.length === 1 ? 'Cookie consent set to \u201c' + after.mode + '\u201d' : 'Privacy settings saved';
    case 'analytics':
      return ch.length === 1 && has('enabled') ? turned('Analytics', after.enabled) : 'Analytics settings saved';
    case 'sso':
      return 'Single sign-on provider saved';
  }
  return 'Saved';
}

function announceSaved(key, before, after) { toast(savedMessage(key, before, after), 'ok'); }

function showFormError(msg, field) {
  document.querySelectorAll('.invalid').forEach((el) => el.classList.remove('invalid'));
  const box = $('formError');
  box.hidden = !msg;
  box.textContent = msg || '';
  if (msg && field) {
    field.classList.add('invalid');
    field.focus();
    field.scrollIntoView({ behavior: 'smooth', block: 'center' });
  }
}

// Plain-language names for the executors the server reports.
const EXECUTORS = {
  http: ['HTTP / HTTPS', 'Websites and REST / GraphQL APIs.'],
  sql: ['Database (SQL)', 'Runs a query repeatedly. Read-only unless you allow writes.'],
  tcp: ['TCP socket', 'Opens raw connections to host:port.'],
  grpc: ['gRPC', 'Calls a unary gRPC method.'],
  ws: ['WebSocket', 'Opens WebSocket connections, optionally sending a message.'],
};

function applyTheme(t) {
  document.documentElement.setAttribute('data-theme', t);
  store.set('blasta.theme', t);
  if (lastSnap) render(lastSnap);
}

/* ---- Load profiles ---------------------------------------------------- */

const PROFILES = {
  gentle: { rps: 10, conc: 5, duration: 10 },
  steady: { rps: 100, conc: 20, duration: 30 },
  heavy: { rps: 500, conc: 100, duration: 30 },
};

function setProfile(name) {
  document.querySelectorAll('#profiles button').forEach((b) =>
    b.setAttribute('aria-checked', String(b.dataset.profile === name)));
}

function syncProfileFromFields() {
  const cur = {
    rps: parseInt($('rps').value, 10),
    conc: parseInt($('conc').value, 10),
    duration: parseInt($('duration').value, 10),
    ramp: parseInt($('ramp').value, 10) || 0,
  };
  const hit = Object.keys(PROFILES).find((k) =>
    cur.ramp === 0 && PROFILES[k].rps === cur.rps && PROFILES[k].conc === cur.conc && PROFILES[k].duration === cur.duration);
  setProfile(hit || 'custom');
}

/* ---- Headers ---------------------------------------------------------- */

function headerRow(k, v) {
  const row = document.createElement('div');
  row.className = 'hrow';
  row.innerHTML = '<input placeholder="Header name" aria-label="Header name" data-role="k">' +
    '<input placeholder="Value" aria-label="Header value" data-role="v">' +
    '<button class="btn ghost x" type="button" aria-label="Remove header">&times;</button>';
  row.querySelector('[data-role=k]').value = k || '';
  row.querySelector('[data-role=v]').value = v || '';
  row.querySelector('button').onclick = () => { row.remove(); countHeaders(); };
  $('headers').appendChild(row);
  countHeaders();
}

function countHeaders() {
  const n = Object.keys(readHeaders()).length;
  $('headerCount').textContent = n ? '(' + n + ')' : '';
}

function readHeaders() {
  const out = {};
  document.querySelectorAll('#headers .hrow').forEach((r) => {
    const k = r.querySelector('[data-role=k]').value.trim();
    if (k) out[k] = r.querySelector('[data-role=v]').value;
  });
  return out;
}

/* ---- Init ------------------------------------------------------------- */

// Routes: /test, /templates, /templates/<id>, /history. They use the history API, so the
// browser's back button works and every template has an address of its own.
const VIEWS = ['test', 'templates', 'history', 'running', 'login', 'admin', 'account'];
function go(hash) {
  if (routeStr() === hash) route();
  else { history.pushState(null, '', toUrl(hash)); route(); }
}

function route() {
  syncMineUI();
  const parts = routeStr().replace(/^#\/?/, '').split('/');
  let seg = parts[0].split('?')[0];
  if (seg === 'test') { history.replaceState(null, '', toUrl('#/jobs' + location.search)); seg = 'jobs'; }   // the old address
  let view = ['reset', 'confirm', 'confirm-email'].includes(seg) ? 'login' : seg === 'jobs' ? 'test' : VIEWS.includes(seg) && seg !== 'test' ? seg : 'test';   // emailed links open on the sign-in page
  // A visitor on a free trial can use the Jobs page only: templates and history ask them to sign in.
  if (guestMode && (view === 'history' || view === 'admin' || view === 'account')) {   // templates can be browsed; using them needs an account
    history.replaceState(null, '', toUrl('#/jobs'));
    route();
    openGate(view);
    return;
  }
  // Nobody else can reach a page without signing in; once signed in the sign-in page is pointless.
  if (authCfg && !me && !guestMode && view !== 'login') { go('#/login'); return; }
  // Signed in people have no use for the sign-in page, except to follow a link from an email.
  if (me && view === 'login' && !/^#\/(reset|confirm|confirm-email)\b/.test(routeStr())) { go('#/jobs'); return; }
  if (view === 'admin' && !(me && me.role === 'admin')) { go('#/jobs'); return; }
  document.body.classList.toggle('signed-out', view === 'login');
  const apply = () => {
    document.querySelectorAll('.nav-btn').forEach((x) =>
      x.classList.toggle('active', x.dataset.view === view));
    VIEWS.forEach((v) => { $('view-' + v).hidden = v !== view; });
    window.scrollTo({ top: 0 });
  };
  // Pages switch instantly: no animation, so text never rescales while changing tabs.
  apply();
  if (view === 'history') {
    if (parts[1]) showRunDetail(decodeURIComponent(parts[1])); else showRunList();
  }
  if (view === 'running') { document.title = 'Running jobs | BLASTA'; pollRunning(); }
  if (view === 'login') showLogin();
  if (view === 'account') loadAccount();
  if (view === 'admin') showAdmin(parts[1]);
  // The title follows the page, as the server's does on a direct visit.
  if (view === 'test') document.title = HOME_TITLE;
  if (view === 'templates' && !parts[1]) document.title = 'Load Testing Templates: Websites, APIs, Databases | BLASTA';
  if (view === 'templates') {
    document.querySelectorAll('.tpl-guest-note').forEach((n) => { n.hidden = !guestMode; });
    const tab = parts[1] || !me ? 'built' : /[?&](mine|favorites)\b/.test(routeStr()) ? 'favs' : 'built';
    if (tab === 'favs' && /[?&]mine\b/.test(routeStr())) history.replaceState(null, '', toUrl('#/templates?favorites'));   // the old address
    syncTplTabs(tab);
    if (parts[1]) {
      const [pid, q] = parts[1].split('?');
      const m = /(?:^|&)my=([^&]+)/.exec(q || '');
      openTemplate(decodeURIComponent(pid), m ? decodeURIComponent(m[1]) : '');
    } else if (tab === 'favs') showFavorites();
    else showTemplateList();
  }
  // Move focus to the new page's heading so keyboard and screen-reader users land in context.
  const focusHeading = () => { const h = document.querySelector('#view-' + view + ' h1, #view-' + view + ' h2.pt'); if (h && lastNav) h.focus({ preventScroll: true }); };
  focusHeading();
  lastNav = true;
}
let lastNav = false;

async function startApp() {
  if (appStarted) return;
  appStarted = true;
  try {
    const { executors } = await api('/executors');
    // Sorted so the list is stable between restarts, and "http" is preselected:
    // appending in registry order left the select on whichever protocol happened
    // to be registered first, silently running the wrong executor.
    executors.slice().sort().forEach((e) => {
      const o = document.createElement('option');
      o.value = e;
      o.textContent = EXECUTORS[e] ? EXECUTORS[e][0] : e;
      $('executor').appendChild(o);
    });
  } catch (e) {
    // Keep an option so the form still submits something sensible.
    if (!$('executor').options.length) {
      const o = document.createElement('option');
      o.value = 'http';
      o.textContent = EXECUTORS.http[0];
      $('executor').appendChild(o);
    }
  }
  $('executor').value = 'http';
  if (guestMode) applyGuestLimits();
  headerRow('User-Agent', BLASTA_UA);
  $('addHeader').onclick = () => { headerRow(); $('headers').lastChild.querySelector('input').focus(); };
  $('headers').addEventListener('input', countHeaders);

  document.querySelectorAll('.nav-btn').forEach((b) => { b.onclick = () => go('#/' + (b.dataset.view === 'test' ? 'jobs' : b.dataset.view)); });

  document.querySelectorAll('#profiles button').forEach((b) => {
    b.onclick = () => {
      const p = PROFILES[b.dataset.profile];
      if (p) {
        const L = guestMode && guestInfo ? guestInfo.limits : null;
        $('rps').value = L ? Math.min(p.rps, L.maxRps) : p.rps;
        $('conc').value = L ? Math.min(p.conc, L.maxConcurrency) : p.conc;
        $('duration').value = L ? Math.min(p.duration, L.maxDurationSec) : p.duration;
        $('ramp').value = 0;
      } else {
        $('rps').focus();
      }
      setProfile(b.dataset.profile);
      updateSafety();
    };
  });

  $('start').onclick = start;
  $('stop').onclick = stop;
  document.addEventListener('keydown', (e) => {
    if ((e.ctrlKey || e.metaKey) && e.key === 'Enter' && !$('start').disabled) start();
  });

  const url = new URLSearchParams(location.search).get('url');
  if (url) $('url').value = url;
  updateSafety();
  loadRuns();
  loadPresets();      // visitors can browse the templates; using a job needs an account
  wireHistory();
  pollHealth();
  setInterval(pollHealth, 10000);
  route();
  restoreSession();
  if (me && me.role === 'admin') refreshPending();
  if (guestMode) renderTrial();
}

const fmtBytes = (n) => (n >= 1073741824 ? (n / 1073741824).toFixed(1) + ' GB' : Math.round(n / 1048576) + ' MB');

// There is no always-visible status box: tell the user only when the server
// becomes unreachable or comes back.
// Reconnect to the test that was running (or show the last result) after a
// reload or a return visit.
// Each started job's pass/fail targets, kept by run id (never its headers or credentials), so the
// live view can judge the run against them.
const SLO_KEY = 'blasta.slos';
function rememberSLO(id, slo) {
  try {
    const m = JSON.parse(store.get(SLO_KEY) || '{}');
    m[id] = slo || null;
    const keep = Object.keys(m).slice(-12);
    store.set(SLO_KEY, JSON.stringify(Object.fromEntries(keep.map((k) => [k, m[k]]))));
  } catch (e) { /* the live view still works without the targets */ }
}
const recallSLO = (id) => { try { return (JSON.parse(store.get(SLO_KEY) || '{}'))[id] || null; } catch (e) { return null; } };

async function watchRun(id) {
  if (!(runId === id && es)) {
    saveSession({ runId: id, slo: recallSLO(id) });
    await restoreSession(id);
  }
  go('#/running');
}

async function restoreSession(only) {
  const s = only ? { runId: only, slo: recallSLO(only) } : loadSession();
  if (!s || !s.runId) return;
  let run;
  try { run = await api('/runs/' + s.runId); } catch (e) { saveSession(null); return; }   // the server forgot it
  // Live results only exist while a test is running; a finished one lives in History.
  if (run.state !== 'running') { saveSession(null); return; }
  runId = run.id;
  activeSLO = s.slo || null;
  series = []; resSeries = [];
  $('results').hidden = false;
  $('resultsEmpty').hidden = true;
  $('resultsBody').hidden = false;
  $('runmeta').textContent = runId;
  $('dl-json').href = 'api/runs/' + runId + '/report';
  $('dl-csv').href = 'api/runs/' + runId + '/report?format=csv';
  try { series = ((await api('/runs/' + runId + '/metrics')).series || []).slice(-240); } catch (e) { /* restored from history: no live series */ }
  if (run.state === 'running') {
    $('stop').disabled = false;
    markRunning(true);
    listen(runId);
    if (!only) toast('Reconnected to your running test', 'ok');
  } else {
    const m = run.summary || {};
    lastSnap = Object.assign({}, m, { running: false, progressPct: 100 });
    render(lastSnap);
    showFinalResources(runId);
  }
}

async function pollHealth() {
  let ok = true;
  try { const h = await api('/health'); if (h && h.version) appVersion = h.version; } catch (e) { ok = false; }
  paintVersion();
  if (ok !== healthy) toast(ok ? 'The BLASTA server is back online' : 'Cannot reach the BLASTA server', ok ? 'ok' : 'error');
  healthy = ok;
}

/* ---- Form summary + validation --------------------------------------- */

const isLocal = (u) => /^(\w+:\/\/)?(localhost|127\.|\[::1\]|::1|10\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.|0\.0\.0\.0)/i.test(u);

function updateSafety() {
  const c = parseInt($('conc').value, 10) || 0;
  const q = parseInt($('queue').value, 10) || 0;
  const rps = parseInt($('rps').value, 10) || 0;
  const secs = parseInt($('duration').value, 10) || 0;
  const ramp = parseInt($('ramp').value, 10) || 0;
  const proto = $('executor').value || 'http';
  const url = $('url').value.trim();
  const badUrl = url && !/^\w+:\/\//.test(url) && proto !== 'tcp' && proto !== 'grpc';

  // Only surface the fields the selected executor actually reads, so a sql job
  // is not filled in with an irrelevant URL.
  document.querySelectorAll('.proto').forEach((el) => { el.hidden = el.dataset.for !== proto; });
  $('urlRow').hidden = proto === 'sql';
  $('methodRow').hidden = proto !== 'http';
  $('bodyRow').hidden = proto !== 'http';
  $('expectRow').hidden = proto !== 'http';
  $('httpOpts').hidden = proto !== 'http';
  $('headersBox').hidden = proto === 'sql';
  $('executorHelp').textContent = (EXECUTORS[proto] || [, ''])[1];
  $('urlLabel').textContent = proto === 'tcp' || proto === 'grpc' ? 'Host and port' : 'Address (URL)';
  $('url').placeholder = { ws: 'wss://staging.example.com/socket', tcp: 'staging.example.com:6379',
    grpc: 'staging.example.com:50051' }[proto] || 'https://staging.example.com/';

  // Inline hints on the address field.
  const help = $('urlHelp');
  help.className = 'help';
  help.textContent = '';
  if (badUrl) {
    help.className = 'help err';
    help.textContent = 'Add a scheme, e.g. ' + (proto === 'ws' ? 'wss://' : 'https://') + url;
  } else if (url && isLocal(url) && $('blockprivate').checked && proto !== 'sql') {
    help.className = 'help warn';
    help.textContent = 'This looks like a local/private address. It will be blocked unless you turn off ' +
      '"Block private & loopback addresses" under Advanced limits.';
  }

  // Plain-language summary of what is about to happen.
  const what = proto === 'sql' ? 'the database' : url ? '<b>' + esc(url) + '</b>' : 'your target';
  let html;
  if (rps > 0) {
    const effSecs = ramp > 0 && ramp <= secs ? secs - ramp / 2 : secs; // a linear ramp averages half the rate
    html = 'About<span class="big">' + fmtInt(rps * effSecs) + ' requests</span>' +
      'sent to ' + what + ' at <b>' + fmtInt(rps) + '/s</b> for <b>' + secs + ' s</b>, ' +
      'using up to <b>' + c + '</b> at once' +
      (ramp > 0 ? ', ramping up over <b>' + ramp + ' s</b>' : '') + '.';
  } else {
    html = 'As many requests as possible<span class="big">' + secs + ' s</span>' +
      'against ' + what + ' with <b>' + c + '</b> connections.';
  }
  $('summary').innerHTML = html;

  const chips = [
    '<span class="chip ok">memory stays flat</span>',
    '<span class="chip">' + c + ' workers</span>',
    '<span class="chip">' + q + ' queue slots</span>',
    '<span class="chip ' + ($('blockprivate').checked ? 'ok' : 'bad') + '">SSRF guard ' +
      ($('blockprivate').checked ? 'on' : 'off') + '</span>',
  ];
  const notes = [];
  if (proto === 'sql') {
    if (!$('dsnenv').value.trim()) notes.push('Set the connection variable name. BLASTA never reads a DSN from this form.');
    if ($('allowwrite').checked) notes.push('Writes are enabled: this test can modify data.');
  }
  if (proto === 'grpc' && !$('grpcmethod').value.trim()) {
    notes.push('gRPC needs a method, e.g. /pkg.Service/Call');
  }
  $('safety').innerHTML = '<div class="chips">' + chips.join('') + '</div>' +
    notes.map((n) => '<div class="warn-line">' + esc(n) + '</div>').join('');
}
['conc', 'queue', 'rps', 'duration', 'ramp', 'url', 'dsnenv', 'grpcmethod'].forEach((id) => {
  $(id).addEventListener('input', () => {
    if (['conc', 'rps', 'duration', 'ramp'].includes(id)) syncProfileFromFields();
    if (id === 'url') $('url').classList.remove('invalid');
    updateSafety();
  });
});
['allowwrite', 'executor', 'blockprivate'].forEach((id) =>
  $(id).addEventListener('change', updateSafety));

// Returns [message, field] for the first problem, or null.
function validate() {
  const proto = $('executor').value;
  // The web UI never expands environment variables, so a ${NAME} would be sent literally.
  for (const id of ['url', 'body', 'tcpbody', 'grpcbody', 'wsbody', 'query']) {
    if (/\$\{[A-Za-z_][A-Za-z0-9_]*\}/.test($(id).value)) {
      return ['This field still contains a ${NAME} placeholder. The web UI does not read environment variables, so replace it with the real value (it is not saved), or run this job with the CLI: blasta run job.json.', $(id)];
    }
  }
  for (const r of document.querySelectorAll('#headers .hrow [data-role=v]')) {
    if (/\$\{[A-Za-z_][A-Za-z0-9_]*\}/.test(r.value)) {
      return ['A header value still contains a ${NAME} placeholder. Replace it with the real value, or run the job with the CLI.', r];
    }
  }
  const url = $('url').value.trim();
  if (proto === 'sql') {
    if (!$('dsnenv').value.trim()) return ['Enter the name of the environment variable that holds the database connection.', $('dsnenv')];
    if (!$('query').value.trim()) return ['Enter a query to run.', $('query')];
  } else if (proto === 'grpc') {
    if (!$('grpcmethod').value.trim()) return ['Enter the gRPC method, e.g. /pkg.Service/Call.', $('grpcmethod')];
    if (!$('grpctarget').value.trim() && !url) return ['Enter the host and port to call.', $('url')];
  } else if (!url) {
    return ['Enter the address you want to test.', $('url')];
  } else if (proto !== 'tcp' && !/^\w+:\/\//.test(url)) {
    return ['The address needs a scheme, e.g. ' + (proto === 'ws' ? 'wss://' : 'https://') + url, $('url')];
  }
  const rawExpect = $('expect').value.trim();
  if (proto === 'http' && rawExpect && parseExpect(rawExpect).length !== rawExpect.split(/[\s,]+/).filter(Boolean).length) {
    return ['Status codes must be numbers between 100 and 599, separated by commas.', $('expect')];
  }
  if (!(parseInt($('duration').value, 10) > 0)) return ['Duration must be at least 1 second.', $('duration')];
  if (!(parseInt($('conc').value, 10) > 0)) return ['Parallel connections must be at least 1.', $('conc')];
  if (guestMode && guestInfo) {
    const L = guestInfo.limits;
    if (!(parseInt($('rps').value, 10) > 0) || parseInt($('rps').value, 10) > L.maxRps) return ['The free trial allows 1 to ' + L.maxRps + ' requests per second. Sign in or create an account for more.', $('rps')];
    if (parseInt($('conc').value, 10) > L.maxConcurrency) return ['The free trial allows up to ' + L.maxConcurrency + ' parallel connections. Sign in or create an account for more.', $('conc')];
    if (parseInt($('duration').value, 10) > L.maxDurationSec) return ['The free trial allows tests up to ' + L.maxDurationSec + ' seconds. Sign in or create an account for longer ones.', $('duration')];
  }
  return null;
}

// Typed \\r and \\n become real control characters, because a text box cannot hold them.
const unescapeCtl = (s) => String(s || '').replace(/\\r/g, '\r').replace(/\\n/g, '\n');
const escapeCtl = (s) => String(s || '').replace(/\r/g, '\\r').replace(/\n/g, '\\n');

// The SLO block of a job: percent errors and millisecond latencies in the form,
// nanoseconds in the job file.
function readSLO() {
  const err = parseFloat($('slo-err').value);
  const p95 = parseFloat($('slo-p95').value);
  const p99 = parseFloat($('slo-p99').value);
  const slo = {};
  if (err >= 0) slo.maxErrorRate = err;
  if (p95 > 0) slo.maxP95 = Math.round(p95 * 1e6);
  if (p99 > 0) slo.maxP99 = Math.round(p99 * 1e6);
  return Object.keys(slo).length ? slo : null;
}

function parseExpect(s) {
  return String(s || '').split(/[\s,]+/).map((x) => parseInt(x, 10)).filter((n) => n >= 100 && n <= 599);
}

// A short, human name for a test typed into the form.
function manualTestHost() {
  const raw = ($('url').value || $('grpctarget').value || '').trim();
  try { return new URL(/^\w+:\/\//.test(raw) ? raw : 'http://' + raw).host || 'test'; } catch (e) { return raw.slice(0, 40) || 'test'; }
}

function buildJob() {
  const secs = parseInt($('duration').value, 10) || 10;
  const rps = parseInt($('rps').value, 10) || 0;
  const proto = $('executor').value;
  const meta = {};
  let body = '';
  let db;

  if (proto === 'grpc') {
    meta.method = $('grpcmethod').value.trim();
    meta.target = $('grpctarget').value.trim() || $('url').value.trim();
    body = $('grpcbody').value;
  } else if (proto === 'sql') {
    meta.query = $('query').value.trim();
    db = {
      dsnEnv: $('dsnenv').value.trim(),
      allowWrite: $('allowwrite').checked,
    };
    // Presets set the pool size to match concurrency; dropping it would silently
    // open the default 16 connections against the database.
    const pool = parseInt($('dbmaxopen').value, 10);
    if (pool > 0) db.maxOpen = pool;
    const driver = $('dbdriver').value;
    if (driver) db.driver = driver;
  } else if (proto === 'tcp') {
    // Typed escapes become real bytes, because a text box cannot hold a CR/LF.
    body = unescapeCtl($('tcpbody').value);
    const exp = unescapeCtl($('tcpexpect').value);
    if (exp) meta.expectPrefix = exp;
    if ($('tcphex').value.trim()) meta.bodyHex = $('tcphex').value.trim();
    if ($('tcpexphex').value.trim()) meta.expectHex = $('tcpexphex').value.trim();
    const n = parseInt($('tcpread').value, 10);
    if (n > 0) meta.readBytes = n;
  } else if (proto === 'ws') {
    body = $('wsbody').value;
  } else {
    body = $('body').value;
    if (!$('followredir').checked) meta.followRedirects = false;
    if ($('insecuretls').checked) meta.insecureTLS = true;
  }

  const job = {
    name: 'Manual test · ' + manualTestHost() + ' · ' + new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }),
    executor: proto,
    target: { url: proto === 'sql' ? '' : $('url').value.trim(), meta: meta },
    method: proto === 'http' ? $('method').value : '',
    body: body,
    headers: readHeaders(),
    concurrency: parseInt($('conc').value, 10) || 20,
    rps: rps,
    burst: Math.max(rps, 1),
    duration: secs * ns,
    ramp: (parseInt($('ramp').value, 10) || 0) * ns,
    timeout: (parseInt($('timeout').value, 10) || 10) * ns,
    queueSize: parseInt($('queue').value, 10) || 1000,
    maxWorkers: parseInt($('maxworkers').value, 10) || 200,
    onQueueFull: $('onfull').value,
    blockPrivate: $('blockprivate').checked,
  };
  if (db) job.db = db;
  const slo = readSLO();
  if (slo) job.slo = slo;
  const expect = parseExpect($('expect').value);
  if (proto === 'http' && expect.length) job.expectStatus = expect;
  return job;
}

/* ---- Running ---------------------------------------------------------- */

async function start() {
  if (guestMode && guestInfo && (guestInfo.expired || (guestInfo.active && guestInfo.runsUsed >= guestInfo.runsMax))) {
    openGate('trial');
    return;
  }
  const problem = validate();
  showFormError(problem && problem[0], problem && problem[1]);
  if (problem) return;
  if (guestMode && guestInfo && guestInfo.needsCheck && captchaWanted('Guest') && !(await guestChallenge())) return;
  if ($('executor').value === 'sql' && $('allowwrite').checked &&
      !confirm('Writes are enabled. This test can INSERT, UPDATE or DELETE data.\n\nOnly continue on a staging database. Start anyway?')) return;

  const btn = $('start');
  btn.disabled = true;
  try {
    const job = await api('/jobs', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(buildJob()),
    });
    const run = await api('/jobs/' + job.id + '/start', { method: 'POST' });
    rememberSLO(run.id, readSLO());
    loadRuns();
    toast('Started \u201c' + testName(job.name) + '\u201d. It runs under Running jobs; you can start another.', 'ok');
    await pollRunning();
    if (guestMode) await watchRun(run.id);   // a visitor has no side panel: show it
    if (guestMode) refreshGuest();
  } catch (e) {
    if (e.code === 'captcha_required') {          // the server wants the bot check first
      await refreshGuest();
      if (guestInfo) guestInfo.needsCheck = true;
      showFormError('Please confirm you are not a robot, then press Start job again.');
      return;
    }
    showFormError('Could not start the job: ' + e.message);
    if (guestMode) refreshGuest();
  } finally {
    btn.disabled = false;
  }
}

function listen(id) {
  if (es) es.close();
  es = new EventSource('api/runs/' + id + '/stream');
  es.onerror = () => { if (es && es.readyState === 2) es = null; };
  es.onmessage = (ev) => {
    const s = JSON.parse(ev.data);
    series.push(s);
    if (series.length > 240) series.shift();
    lastSnap = s;
    render(s);
    if (!s.running) {
      es.close(); es = null;
      $('stop').disabled = true;
      markRunning(false);
      loadRuns();
      pollRunning();
      showFinalResources(id);
      toast('Job finished', 'ok');
    }
  };
}

async function stop() {
  if (!runId) return;
  $('stop').disabled = true;
  try { await api('/runs/' + runId + '/stop', { method: 'POST' }); toast('Stopping job…'); }
  catch (e) { toast('Could not stop: ' + e.message, 'error'); }
}

/* ---- Results ---------------------------------------------------------- */

const tip = {
  requests: 'Total requests completed so far',
  'req/sec': 'Average throughput',
  p50: 'Half of requests were faster than this (typical)',
  p95: '95% of requests were faster than this',
  p99: '99% of requests were faster than this (worst-case-ish)',
  errors: 'Requests that failed or timed out',
  'error %': 'Share of requests that failed',
  skipped: 'Requests dropped because the queue was full (backpressure)',
};

function stat(k, v, cls, sub) {
  return '<div class="stat ' + (cls || '') + '" title="' + esc(tip[k] || '') + '"><div class="k">' + k +
    '</div><div class="v">' + v + '</div>' + (sub ? '<div class="sub">' + esc(sub) + '</div>' : '') + '</div>';
}

function verdict(s, errRate, p95) {
  let cls = 'good', title = 'Looking healthy', text = 'No errors and nothing was dropped.';
  if (errRate > 5) {
    cls = 'bad'; title = 'The target is failing';
    text = errRate.toFixed(1) + '% of requests failed. Try a gentler load, or check the target’s logs.';
  } else if (errRate > 1 || s.skipped > 0) {
    cls = 'warn'; title = 'Struggling under this load';
    text = s.skipped > 0
      ? fmtInt(s.skipped) + ' requests were skipped because the queue filled up — the target is not keeping pace.'
      : errRate.toFixed(1) + '% of requests failed.';
  } else if (s.errors > 0) {
    text = fmtInt(s.errors) + ' errors (' + errRate.toFixed(2) + '%) — within normal noise.';
  }
  if (p95 != null) text += ' Typical slow request (p95): ' + fmtLat(p95) + '.';
  return { cls: cls + (s.running ? ' live' : ''), title: (s.running ? '' : 'Finished — ') + title, text };
}

// Compare a snapshot with the targets the run started with.
function sloCheck(s, errRate, p, activeSLO) {
  if (!activeSLO) return null;
  const miss = [];
  if (activeSLO.maxErrorRate != null && s.total && errRate > activeSLO.maxErrorRate) {
    miss.push('errors ' + errRate.toFixed(2) + '% > ' + activeSLO.maxErrorRate + '%');
  }
  if (activeSLO.maxP95 && p.p95 != null && p.p95 > activeSLO.maxP95 / 1000) {
    miss.push('p95 ' + fmtLat(p.p95) + ' > ' + fmtLat(activeSLO.maxP95 / 1000));
  }
  if (activeSLO.maxP99 && p.p99 != null && p.p99 > activeSLO.maxP99 / 1000) {
    miss.push('p99 ' + fmtLat(p.p99) + ' > ' + fmtLat(activeSLO.maxP99 / 1000));
  }
  if (miss.length) return { met: false, text: (s.running ? 'SLO currently missed: ' : 'SLO missed: ') + miss.join('; ') + '.' };
  return { met: true, text: s.running ? 'Within your SLO so far.' : 'All SLO targets met.' };
}

function render(s, o) {
  o = o || {};
  const sfx = o.sfx || '';              // '' = the live results, 'H' = a saved run in History
  const el = (n) => $(n + sfx);
  if (!sfx) $('runAgain').hidden = !!s.running;        // a finished test can be run again
  const slo = 'slo' in o ? o.slo : activeSLO;
  const p = s.latency.percentiles || {};
  const errRate = s.total ? (s.errors / s.total) * 100 : 0;

  const v = verdict(s, errRate, p.p95);
  const chk = sloCheck(s, errRate, p, slo);
  if (chk) {
    v.text += ' ' + chk.text;
    if (!chk.met) { v.cls = 'bad' + (s.running ? ' live' : ''); v.title = (s.running ? '' : 'Finished \u2014 ') + 'SLO missed'; }
    else if (!s.running && v.cls.startsWith('good')) v.title = 'Finished \u2014 SLO met';
  }
  el('verdict').className = 'verdict ' + v.cls;
  el('verdict').innerHTML = '<span class="v-dot"></span><div><div class="v-title">' + esc(v.title) +
    '</div><div class="v-text">' + esc(v.text) + '</div></div>';

  el('stats').innerHTML = [
    stat('requests', fmtInt(s.total)),
    stat('req/sec', s.avgRps.toFixed(1)),
    stat('p50', fmtLat(p.p50)),
    stat('p95', fmtLat(p.p95)),
    stat('p99', fmtLat(p.p99)),
    stat('errors', fmtInt(s.errors), s.errors ? 'err' : 'ok'),
    stat('error %', errRate.toFixed(2) + '%', errRate > 1 ? 'err' : ''),
    stat('skipped', fmtInt(s.skipped), s.skipped ? 'warn' : ''),
    s.rowsTotal > 0
      ? stat('rows', fmtInt(s.rowsTotal))
      : stat('transfer', (s.bytesTotal / 1048576).toFixed(1) + ' MB'),
  ].join('');

  const order = ['p50', 'p75', 'p90', 'p95', 'p99', 'p99_9', 'p99_99', 'p99_999'];
  el('pcts').innerHTML = order.filter((k) => p[k] != null)
    .map((k) => '<div class="pct"><div class="k">' + k.replace('_', '.') +
      '</div><div class="v">' + fmtLat(p[k]) + '</div></div>').join('');
  chips('codes' + sfx, s.statusCodes, (k) => (/^[23]/.test(k) ? 'ok' : /^[45]/.test(k) ? 'bad' : ''));
  chips('errs' + sfx, s.errorKinds, () => 'bad');

  if (!sfx) {                           // progress and live server usage exist only in the live view
    const pct = Math.min(100, s.progressPct || 0);
    $('bar').style.width = pct + '%';
    $('progress').setAttribute('aria-valuenow', String(Math.round(pct)));
    $('progressText').textContent = s.running ? Math.round(pct) + '%' : 'done';
    paintResources(s);
  }
  const css = getComputedStyle(document.documentElement);
  draw('c-rps' + sfx, o.rps || series.map((x) => x.rps), css.getPropertyValue('--accent').trim(), true);
  draw('c-lat' + sfx, o.p95 || series.map((x) => (x.latency.percentiles || {}).p95 || 0), css.getPropertyValue('--info').trim(), false, true);
  if (!sfx && s.waitTime && s.waitTime.percentiles && s.waitTime.percentiles.p99 > 0) {
    $('runmeta').textContent = runId + '  \u00b7  queue wait p99 ' + fmtLat(s.waitTime.percentiles.p99);
  }
}

/* ---- What the load generator itself costs ---------------------------------- */

let resSeries = [];   // live samples of this server's CPU and RAM during the run

const fmtPct = (v) => (v < 10 ? v.toFixed(1) : Math.round(v)) + '%';
const coresText = (pct, cores) => (+(pct * cores / 100).toFixed(2)) + ' of ' + (+cores.toFixed(1)) + ' cores';
const heat = (pct) => (pct >= 90 ? 'err' : pct >= 70 ? 'warn' : '');

function drawResources(list, sfx) {
  const css = getComputedStyle(document.documentElement);
  draw('c-cpu' + (sfx || ''), list.map((x) => x.cpuPct), css.getPropertyValue('--accent').trim(), true, false, fmtPct);
  draw('c-mem' + (sfx || ''), list.map((x) => x.memUsed), css.getPropertyValue('--info').trim(), true, false, fmtBytes);
}

// One reading per stream message while the run is live.
function paintResources(s) {
  if (!s.server) return;
  const v = s.server;
  resSeries.push({ cpuPct: v.cpuPct, memUsed: v.memUsed });
  if (resSeries.length > 240) resSeries.shift();
  $('resBlock').hidden = false;
  $('resScope').textContent = v.scope === 'container' ? 'this container' : 'this machine';
  const peakCpu = Math.max(...resSeries.map((x) => x.cpuPct));
  const peakMem = Math.max(...resSeries.map((x) => x.memUsed));
  $('resStats').innerHTML = [
    stat('CPU now', fmtPct(v.cpuPct), heat(v.cpuPct), coresText(v.cpuPct, v.cores)),
    stat('CPU peak', fmtPct(peakCpu), heat(peakCpu), 'so far'),
    stat('RAM now', fmtBytes(v.memUsed), '', v.memLimit ? 'of ' + fmtBytes(v.memLimit) : ''),
    stat('RAM peak', fmtBytes(peakMem), '', 'so far'),
  ].join('');
  resNote(peakCpu, s.running, '');
  drawResources(resSeries, '');
}

// A saturated load generator makes the target look slower than it is.
function resNote(peakCpu, running, sfx) {
  const n = $('resNote' + (sfx || ''));
  n.hidden = peakCpu < 85;
  if (!n.hidden) {
    n.textContent = (running ? 'The load generator is close to its CPU limit' : 'The load generator reached ' + Math.round(peakCpu) + '% CPU') +
      ': latency may be overstated and throughput understated. Lower the rate or give BLASTA more CPU before trusting this result.';
  }
}

// What was recorded for a finished run. Averages come first: they describe what
// the test cost; the peaks say how close it came to the limit.
function paintFinalResources(r, sfx) {
  sfx = sfx || '';
  $('resBlock' + sfx).hidden = false;
  $('resScope' + sfx).textContent = (r.scope === 'container' ? 'this container' : 'this machine') +
    ' \u00b7 recorded with this result (' + r.samples + ' samples)';
  $('resStats' + sfx).innerHTML = [
    stat('CPU average', fmtPct(r.cpuAvgPct), '', coresText(r.cpuAvgPct, r.cores)),
    stat('RAM average', fmtBytes(r.memAvg), '', r.memLimit ? 'of ' + fmtBytes(r.memLimit) : ''),
    stat('CPU peak', fmtPct(r.cpuPeakPct), heat(r.cpuPeakPct), coresText(r.cpuPeakPct, r.cores)),
    stat('RAM peak', fmtBytes(r.memPeak), '', '+' + fmtBytes(Math.max(0, r.memPeak - r.memStart)) + ' during the test'),
    stat('RAM at start', fmtBytes(r.memStart)),
  ].join('');
  resNote(r.cpuPeakPct, false, sfx);
  if (r.series && r.series.length > 1) drawResources(r.series, sfx);
}

// When a live run ends, replace the live approximation with what the server recorded.
async function showFinalResources(id) {
  try {
    const run = await api('/runs/' + id);
    const r = run.summary && run.summary.resources;
    if (r) paintFinalResources(r, '');
  } catch (e) { /* the live figures stay on screen */ }
}

function chips(id, obj, cls) {
  const keys = Object.keys(obj || {}).sort();
  $(id).innerHTML = keys.length
    ? keys.map((k) => '<span class="code-chip ' + (cls ? cls(k) : '') + '">' + esc(k) + ' · ' + fmtInt(obj[k]) + '</span>').join('')
    : '<span class="muted">none</span>';
}

function draw(id, data, color, fill, latency, fmt) {
  const cv = $(id);
  if (!cv) return;
  const dpr = window.devicePixelRatio || 1;
  const w = cv.clientWidth || 400, h = 120;
  if (cv.width !== Math.round(w * dpr)) { cv.width = Math.round(w * dpr); cv.height = h * dpr; }
  const ctx = cv.getContext('2d');
  const css = getComputedStyle(document.documentElement);
  const dim = css.getPropertyValue('--dim').trim();
  const grid = css.getPropertyValue('--border').trim();
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, w, h);
  if (data.length < 2) return;
  const max = Math.max(...data, 1e-9);
  const step = w / (data.length - 1);
  const y = (v) => h - (v / max) * (h - 22) - 4;

  ctx.strokeStyle = grid;
  ctx.lineWidth = 1;
  [0.5, 1].forEach((f) => { const gy = Math.round(y(max * f)) + .5; ctx.beginPath(); ctx.moveTo(0, gy); ctx.lineTo(w, gy); ctx.stroke(); });

  if (fill) {
    ctx.beginPath();
    ctx.moveTo(0, h);
    data.forEach((v, i) => ctx.lineTo(i * step, y(v)));
    ctx.lineTo(w, h);
    ctx.closePath();
    ctx.globalAlpha = 0.16;
    ctx.fillStyle = color;
    ctx.fill();
    ctx.globalAlpha = 1;
  }
  ctx.beginPath();
  data.forEach((v, i) => (i ? ctx.lineTo(i * step, y(v)) : ctx.moveTo(0, y(v))));
  ctx.strokeStyle = color;
  ctx.lineWidth = 2;
  ctx.lineJoin = 'round';
  ctx.stroke();

  ctx.fillStyle = dim;
  ctx.font = '10px "JetBrains Mono", ui-monospace, monospace';
  const label = fmt ? fmt(max) : latency ? fmtLat(max)
    : (max > 1000 ? (max / 1000).toFixed(1) + 'k' : max.toFixed(1));
  ctx.fillText('max ' + label, 4, 11);
  const last = data[data.length - 1];
  const lastLabel = fmt ? fmt(last) : latency ? fmtLat(last) : (last > 1000 ? (last / 1000).toFixed(1) + 'k' : last.toFixed(1));
  ctx.textAlign = 'right';
  ctx.fillText('now ' + lastLabel, w - 4, 11);
  ctx.textAlign = 'left';
}
window.addEventListener('resize', () => { if (lastSnap) render(lastSnap); });

/* ---- History ---------------------------------------------------------- */

function ago(iso) {
  const t = new Date(iso);
  const s = Math.max(0, (Date.now() - t.getTime()) / 1000);
  if (s < 60) return 'just now';
  if (s < 3600) return Math.floor(s / 60) + ' min ago';
  if (s < 86400) return t.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  return t.toLocaleDateString() + ' ' + t.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
}

const fmtSecs = (sec) => {
  if (sec < 1) return Math.round(sec * 1000) + ' ms';
  if (sec < 60) return (+sec.toFixed(1)) + ' s';
  const m = Math.floor(sec / 60), r = Math.round(sec % 60);
  return m + ' min' + (r ? ' ' + r + ' s' : '');
};
const nsToSecs = (n) => (n || 0) / ns;

// "100 req/s · 20 connections · 30 s", from the plan saved with the run.
function planText(plan) {
  if (!plan) return 'not recorded';
  return [plan.rps > 0 ? fmtInt(plan.rps) + ' req/s' : 'as fast as possible',
    plan.concurrency + ' connections', fmtSecs(nsToSecs(plan.duration)) +
      (plan.ramp ? ', ramping up over ' + fmtSecs(nsToSecs(plan.ramp)) : '')].join(' \u00b7 ');
}

function sloText(slo) {
  if (!slo) return 'none set';
  const t = [];
  if (slo.maxErrorRate != null) t.push('errors \u2264 ' + slo.maxErrorRate + '%');
  const lat = (us) => fmtLat(us).replace(/\.00(?=[a-z])/, '');   // 50ms, not 50.00ms
  if (slo.maxP95) t.push('p95 \u2264 ' + lat(slo.maxP95 / 1000));
  if (slo.maxP99) t.push('p99 \u2264 ' + lat(slo.maxP99 / 1000));
  return t.join(' \u00b7 ') || 'none set';
}

// Did a saved run meet the targets it was started with? null when it had none.
function sloOutcome(run) {
  const slo = run.plan && run.plan.slo;
  const s = run.summary;
  if (!slo || !s || !s.latency) return null;
  const errRate = s.total ? (s.errors / s.total) * 100 : 0;
  return sloCheck(Object.assign({}, s, { running: false }), errRate, s.latency.percentiles || {}, slo);
}

const resultCell = (r) => {
  const o = sloOutcome(r);
  return '<span class="state ' + esc(r.state) + '">' + esc(r.state) + '</span>' +
    (o ? ' <span class="tag ' + (o.met ? 'gate' : 'miss') + '">SLO ' + (o.met ? 'met' : 'missed') + '</span>' : '');
};

async function loadRuns() {
  try {
    const { runs } = await api('/runs');
    const tb = document.querySelector('#runs tbody');
    tb.innerHTML = runs.slice().sort((a, b) => new Date(b.startedAt) - new Date(a.startedAt)).map((r) => {
      const s = r.summary || {};
      const p95 = s.latency ? s.latency.percentiles.p95 : null;
      const res = s.resources;
      return '<tr class="runrow" tabindex="0" role="link" data-id="' + esc(r.id) + '" aria-label="Open run ' + esc(r.jobName) + '">' +
        '<td title="' + esc(new Date(r.startedAt).toLocaleString()) + '">' + ago(r.startedAt) + '</td>' +
        '<td class="jobcell" title="' + esc(r.jobName) + '">' + esc(r.jobName) + (r.baseline ? ' <span class="tag gate" title="The run later runs of this test are compared with">baseline</span>' : '') + '</td>' +
        '<td class="target c-target" title="' + esc(r.target || '') + '">' + esc(r.target || '') + '</td>' +
        '<td class="num c-req">' + fmtInt(s.total) + '</td><td class="num c-rps">' + (s.avgRps || 0).toFixed(1) + '</td>' +
        '<td class="num c-p95">' + fmtLat(p95) + '</td><td class="num c-err">' + fmtInt(s.errors) + '</td>' +
        '<td class="num c-cpu">' + (res ? fmtPct(res.cpuAvgPct) : '<span class="muted">-</span>') + '</td>' +
        '<td class="num c-ram">' + (res ? fmtBytes(res.memAvg) : '<span class="muted">-</span>') + '</td>' +
        // the list shows only the state; SLO outcome and everything else is on the opened run
        '<td><span class="state ' + esc(r.state) + '">' + esc(r.state) + '</span></td>' +
        '<td class="c-del">' + (r.state === 'running' ? '' : '<button type="button" class="del-btn" data-del="' + esc(r.id) + '" aria-label="Delete this run" title="Delete this run">' + TRASH + '</button>') + '</td>' +
        '<td class="chev-col" aria-hidden="true">&rsaquo;</td></tr>';
    }).join('') || '<tr><td colspan="12" class="empty-row">No runs yet. Start a test and it will show up here.</td></tr>';
  } catch (e) {}
}

const TRASH = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 7h16M10 11v6M14 11v6M6 7l1 13h10l1-13M9 7V4h6v3"/></svg>';

// Only administrators delete history (with sign-in off, whoever runs BLASTA is the administrator).
const canDelete = () => !authCfg || !!(me && me.role === 'admin');

async function deleteRun(id) {
  if (!confirm('Delete this run from the history? This cannot be undone.')) return false;
  try { await api('/runs/' + encodeURIComponent(id), { method: 'DELETE' }); toast('Run deleted', 'ok'); return true; }
  catch (e) { toast(e.message, 'error'); return false; }
}

function wireHistory() {
  document.body.classList.toggle('can-delete', canDelete());
  $('histTools').hidden = !canDelete();
  $('histDelete').hidden = !canDelete();
  const tb = document.querySelector('#runs tbody');
  const open = (tr) => { if (tr && tr.dataset.id) go('#/history/' + encodeURIComponent(tr.dataset.id)); };
  tb.addEventListener('click', async (e) => {
    const del = e.target.closest('[data-del]');
    if (del) { e.stopPropagation(); if (await deleteRun(del.dataset.del)) loadRuns(); return; }
    open(e.target.closest('tr.runrow'));
  });
  $('histDelete').onclick = async () => { if (histRun && await deleteRun(histRun.id)) { loadRuns(); go('#/history'); } };
  $('histClear').onclick = () => { $('clearAll').checked = false; $('clearErr').hidden = true; $('clearDlg').showModal(); };
  $('clearCancel').onclick = () => $('clearDlg').close();
  $('clearForm').onsubmit = async (e) => {
    e.preventDefault();
    $('clearOk').disabled = true;
    try {
      const r = await api('/runs' + ($('clearAll').checked ? '?scope=all' : ''), { method: 'DELETE' });
      $('clearDlg').close();
      toast(r.deleted + (r.deleted === 1 ? ' run deleted' : ' runs deleted'), 'ok');
      loadRuns();
    } catch (err) { $('clearErr').textContent = err.message; $('clearErr').hidden = false; }
    $('clearOk').disabled = false;
  };
  tb.addEventListener('keydown', (e) => { if (e.target.closest('[data-del]')) return; if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); open(e.target.closest('tr.runrow')); } });
  $('histReuse').onclick = () => { if (histRun) reuseRun(histRun); };
  // The saved-run view reuses the live results markup, with every id suffixed "H",
  // so the two can never drift apart.
  const body = $('resultsBody').cloneNode(true);
  body.id = 'histResultsBody';
  body.hidden = false;
  body.querySelectorAll('[id]').forEach((n) => { n.id += 'H'; });
  const pw = body.querySelector('.progress-wrap');
  if (pw) pw.remove();                       // a finished run has no progress bar
  $('histBody').appendChild(body);
}

let histRun = null;

async function showRunList() {
  $('histListView').hidden = false;
  $('histDetailView').hidden = true;
  await loadRuns();
}

async function showRunDetail(id) {
  $('histListView').hidden = true;
  $('histDetailView').hidden = false;
  let run;
  try { run = await api('/runs/' + encodeURIComponent(id)); } catch (e) {
    toast('That run is no longer available.', 'error');
    go('#/history');
    return;
  }
  histRun = run;
  const s = run.summary || {};
  const plan = run.plan || null;
  $('histTitle').textContent = run.jobName;
  $('histState').innerHTML = resultCell(run);
  $('histJson').href = 'api/runs/' + encodeURIComponent(run.id) + '/report';
  $('histCsv').href = 'api/runs/' + encodeURIComponent(run.id) + '/report?format=csv';

  const secs = s.durationMs != null ? s.durationMs / 1000 : null;
  const rows = [
    ['Started', new Date(run.startedAt).toLocaleString()],
    ['Ran for', secs != null ? fmtSecs(secs) : '-'],
    ['Target', run.target || '(database)'],
    ['Type', run.executor + (plan && plan.method ? ' \u00b7 ' + plan.method : '')],
    ['Load', planText(plan)],
    ['SLO targets', sloText(plan && plan.slo)],
  ];
  if (run.ownerName) rows.push(['Started by', run.ownerName]);
  if (run.state === 'aborted' || run.reason) rows.push(['Ended', run.reason || 'stopped early']);
  rows.push(['Run id', run.id]);
  $('histMeta').innerHTML = rows.map(([k, v]) => '<div><dt>' + esc(k) + '</dt><dd>' + esc(v) + '</dd></div>').join('');

  const ser = s.series || [];
  const hasSeries = ser.length > 1;
  $('histNoSeries').hidden = hasSeries;
  const charts = $('c-rpsH').closest('.charts');
  if (charts) charts.hidden = !hasSeries;
  if (s.latency) {
    render(Object.assign({}, s, { running: false, progressPct: 100 }),
      { sfx: 'H', slo: plan && plan.slo, rps: ser.map((p) => p.rps), p95: ser.map((p) => p.p95) });
  }
  if (s.resources) paintFinalResources(s.resources, 'H'); else $('resBlockH').hidden = true;
  setupCompare(run);
}

// Fill the Test form with a past run's target and load settings. Headers and
// bodies are never saved with history, so those are left as they are.
function reuseRun(run) {
  const plan = run.plan || {};
  if ([...$('executor').options].some((o) => o.value === run.executor)) $('executor').value = run.executor;
  $('url').value = run.target || '';
  if (plan.method) {
    if (![...$('method').options].some((o) => o.value === plan.method)) {
      const o = document.createElement('option');
      o.value = o.textContent = plan.method;
      $('method').appendChild(o);
    }
    $('method').value = plan.method;
  }
  if (plan.rps != null) $('rps').value = plan.rps;
  if (plan.concurrency) $('conc').value = plan.concurrency;
  if (plan.duration) $('duration').value = Math.max(1, Math.round(nsToSecs(plan.duration)));
  $('ramp').value = plan.ramp ? Math.round(nsToSecs(plan.ramp)) : 0;
  if (plan.timeout) $('timeout').value = Math.max(1, Math.round(nsToSecs(plan.timeout)));
  $('expect').value = (plan.expectStatus || []).join(', ');
  const slo = plan.slo || {};
  $('slo-err').value = slo.maxErrorRate != null ? slo.maxErrorRate : '';
  $('slo-p95').value = slo.maxP95 ? Math.round(slo.maxP95 / 1e6) : '';
  $('slo-p99').value = slo.maxP99 ? Math.round(slo.maxP99 / 1e6) : '';
  if (plan.slo) $('advanced').open = true;
  syncProfileFromFields();
  updateSafety();
  loadedFrom = null;
  showBanner();
  toast('Loaded the target and load settings. Headers and bodies are not saved with history: add them again if the job needs them.', 'ok');
  go('#/jobs');
}

function esc(s) {
  return String(s == null ? '' : s).replace(/[&<>"]/g, (c) =>
    ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]));
}

/* ---- Templates page --------------------------------------------------- */

// Small stroke icons per category, so the grid can be scanned by shape as well as by name.
const ICONS = {
  bolt: '<path d="M13 2 4 14h7l-1 8 9-12h-7z"/>',
  key: '<circle cx="8" cy="15" r="4"/><path d="m11 12 9-9m-3 3 3 3m-5-1 2 2"/>',
  shield: '<path d="M12 3 4 6v6c0 5 3.5 8 8 9 4.5-1 8-4 8-9V6z"/><path d="m9 12 2 2 4-4"/>',
  db: '<ellipse cx="12" cy="6" rx="8" ry="3"/><path d="M4 6v6c0 1.7 3.6 3 8 3s8-1.3 8-3V6M4 12v6c0 1.7 3.6 3 8 3s8-1.3 8-3v-6"/>',
  layers: '<path d="m12 3 9 5-9 5-9-5z"/><path d="m3 13 9 5 9-5"/>',
  chat: '<path d="M4 5h16v11H9l-5 4z"/>',
  mail: '<rect x="3" y="5" width="18" height="14" rx="2"/><path d="m3 7 9 6 9-6"/>',
  braces: '<path d="M8 4c-2 0-3 1-3 3v2c0 1.5-1 3-3 3 2 0 3 1.5 3 3v2c0 2 1 3 3 3M16 4c2 0 3 1 3 3v2c0 1.5 1 3 3 3-2 0-3 1.5-3 3v2c0 2-1 3-3 3"/>',
  globe: '<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3c3 3 3 15 0 18M12 3c-3 3-3 15 0 18"/>',
  server: '<rect x="3" y="4" width="18" height="6" rx="1.5"/><rect x="3" y="14" width="18" height="6" rx="1.5"/><path d="M7 7h.01M7 17h.01"/>',
  users: '<circle cx="9" cy="8" r="3"/><path d="M3 20c0-3.3 2.7-6 6-6s6 2.7 6 6M16 5a3 3 0 0 1 0 6M17 14c2.5.5 4 2.5 4 6"/>',
  search: '<circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/>',
  radio: '<circle cx="12" cy="12" r="2"/><path d="M8 8a6 6 0 0 0 0 8M16 8a6 6 0 0 1 0 8M5 5a10 10 0 0 0 0 14M19 5a10 10 0 0 1 0 14"/>',
  code: '<path d="M6 3h8l4 4v14H6z"/><path d="m10 13-2 2 2 2M14 13l2 2-2 2"/>',
  file: '<path d="M6 3h8l4 4v14H6z"/><path d="M9 12h6M9 16h6"/>',
  cart: '<circle cx="9" cy="20" r="1.5"/><circle cx="18" cy="20" r="1.5"/><path d="M3 4h3l2.5 11H19l2-8H7"/>',
  cube: '<path d="m12 3 8 4.5v9L12 21l-8-4.5v-9z"/><path d="M12 12 4 7.5M12 12l8-4.5M12 12v9"/>',
  pulse: '<path d="M3 12h4l3-8 4 16 3-8h4"/>',
  check: '<circle cx="12" cy="12" r="9"/><path d="m8 12 3 3 5-6"/>',
  layout: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M3 9h18M9 9v11"/>',
  book: '<path d="M4 5c3-1 5-1 8 1 3-2 5-2 8-1v14c-3-1-5-1-8 1-3-2-5-2-8-1z"/><path d="M12 6v14"/>',
  network: '<circle cx="12" cy="5" r="2"/><circle cx="5" cy="19" r="2"/><circle cx="19" cy="19" r="2"/><path d="M12 7v5M12 12l-6 5M12 12l6 5"/>',
  chart: '<path d="M4 20V4M4 20h16"/><path d="m8 15 3-4 3 2 4-6"/>',
  app: '<rect x="4" y="3" width="16" height="18" rx="2"/><path d="M8 7h8M8 11h8M8 15h5"/>',
};
const CAT_ICON = {
  Identity: 'key', SAML: 'shield', Database: 'db', Cache: 'layers', Messaging: 'chat', Mail: 'mail',
  API: 'braces', 'Headless CMS': 'braces', JavaScript: 'braces', 'Web performance': 'globe', Static: 'file',
  Infrastructure: 'server', Directory: 'users', Search: 'search', WebSocket: 'radio', SOAP: 'code',
  'E-commerce': 'cart', Frameworks: 'cube', 'Test patterns': 'pulse', Reliability: 'check', CMS: 'layout',
  Wiki: 'book', Forum: 'chat', gRPC: 'network', TCP: 'network', Generic: 'server', Operations: 'chart',
  'Data services': 'db', 'Self-hosted apps': 'app',
};
const catIcon = (cat) => '<svg viewBox="0 0 24 24" aria-hidden="true">' + (ICONS[CAT_ICON[cat]] || ICONS.bolt) + '</svg>';


let tplList = [], tplCat = '', tplReady = null, tplLoaded = false;
let presetDef = null, renderedJobs = [], jobFilter = 'all', renderTimer = null, renderSeq = 0;
let loadedFrom = null;

async function loadPresets() {
  tplReady = (async () => {
    try {
      const { presets } = await api('/presets');
      tplList = presets.map((p) => Object.assign(p, {
        _hay: [p.id, p.title, p.category, p.summary, p.description, p.stack,
          ...(p.jobList || []).map((x) => x.name + ' ' + x.id)].join(' ').toLowerCase(),
      })).sort((a, b) => a.title.localeCompare(b.title));
    } catch (e) {
      tplList = [];
      toast('Templates could not be loaded: ' + e.message, 'error');
    }
    tplLoaded = true;
    if (tplList.length) $('tplLink').title = tplList.length + ' ready-made templates for WordPress, identity providers, databases, caches, APIs and more';
    buildCategoryChips();
    renderTemplateList();
  })();
  return tplReady;
}

function buildCategoryChips() {
  const counts = {};
  tplList.forEach((p) => { counts[p.category] = (counts[p.category] || 0) + 1; });
  const cats = Object.keys(counts).sort();
  $('tplCats').innerHTML = chip('', 'All', tplList.length) +
    cats.map((c) => chip(c, c, counts[c])).join('');
  $('tplCats').querySelectorAll('button').forEach((b) => {
    b.onclick = () => { tplCat = b.dataset.cat; buildCategoryChips(); renderTemplateList(); };
  });
}

function chip(value, label, n) {
  const on = (tplCat === value);
  return '<button type="button" class="chip' + (on ? ' on' : '') + '" data-cat="' + esc(value) +
    '" aria-pressed="' + on + '">' + esc(label) + ' <span class="n">' + n + '</span></button>';
}

const tokens = (s) => String(s || '').toLowerCase().split(/\s+/).filter(Boolean);

const jobText = (x) => (x.name + ' ' + x.id).toLowerCase();

// Templates must contain every search word somewhere, but ranking favours the
// ones where the words appear TOGETHER: the whole phrase in a job name beats the
// words scattered over different jobs.
function matchTemplates() {
  const raw = $('tplSearch').value.trim().toLowerCase();
  const toks = tokens(raw);
  return tplList
    .filter((p) => (!tplCat || p.category === tplCat) && toks.every((t) => p._hay.includes(t)))
    .map((p) => {
      let score = 0;
      if (toks.length) {
        const title = p.title.toLowerCase();
        const jobs = p.jobList || [];
        if (title.includes(raw)) score += 10;
        if (jobs.some((x) => jobText(x).includes(raw))) score += 8;
        if (jobs.some((x) => toks.every((t) => jobText(x).includes(t)))) score += 5;
        score += toks.reduce((s, t) => s + (title.includes(t) ? 3 : 0) + (p.id.includes(t) ? 2 : 0) +
          (p.category.toLowerCase().includes(t) ? 1 : 0), 0);
      }
      return { p, score };
    })
    .sort((a, b) => b.score - a.score || a.p.title.localeCompare(b.p.title))
    .map((x) => x.p);
}

function renderTemplateList() {
  if (!tplList.length && !tplLoaded) {
    $('tplGrid').innerHTML = '<div class="skel"></div>'.repeat(6);
    return;
  }
  const toks = tokens($('tplSearch').value);
  const hits = matchTemplates();
  $('tplCount').textContent = (toks.length || tplCat)
    ? hits.length + ' of ' + tplList.length + ' templates' : tplList.length + ' templates';
  $('tplEmpty').hidden = hits.length > 0;
  $('tplGrid').innerHTML = hits.map((p) => {
    // When the match came from job names, show which jobs matched.
    let matched = '';
    if (toks.length) {
      const all = (p.jobList || []).filter((x) => toks.every((t) => jobText(x).includes(t)));
      const any = (p.jobList || []).filter((x) => toks.some((t) => jobText(x).includes(t)));
      const jobs = toks.every((t) => p.title.toLowerCase().includes(t)) ? [] : (all.length ? all : any);
      if (jobs.length) {
        matched = '<div class="tmatch">Jobs: ' + jobs.slice(0, 3).map((x) => esc(x.name.startsWith(p.title + ': ') ? x.name.slice(p.title.length + 2) : x.name)).join(' &middot; ') +
          (jobs.length > 3 ? ' <span class="muted">+' + (jobs.length - 3) + ' more</span>' : '') + '</div>';
      }
    }
    return '<a class="tcard" href="templates/' + encodeURIComponent(p.id) + '">' +
      '<div class="tcard-head"><span class="ticon">' + catIcon(p.category) + '</span><h3>' + esc(p.title) + '</h3></div>' +
      '<div><span class="tag">' + esc(p.category) + '</span></div>' +
      '<p class="tsum">' + esc(p.description || (p.summary || '').replace(/ Includes an enterprise test plan.*$/, '')) + '</p>' +
      (p.stack ? '<div class="tstack">' + esc(p.stack) + '</div>' : '') + matched +
      '<div class="tfoot"><span>' + p.jobs + ' jobs</span><span class="go">Open &rarr;</span></div></a>';
  }).join('');
}

async function showTemplateList() {
  hideMine();
  $('tplListView').hidden = false;
  $('tplDetailView').hidden = true;
  renderTemplateList();   // shows loading skeletons until the catalogue arrives
  await tplReady;
  renderTemplateList();
}

function wireTemplateSearch() {
  $('tplSearch').addEventListener('input', renderTemplateList);
  $('tplSearch').addEventListener('keydown', (e) => {
    if (e.key === 'Enter') {
      const first = matchTemplates()[0];
      if (first) go('#/templates/' + encodeURIComponent(first.id));
    } else if (e.key === 'Escape') { $('tplSearch').value = ''; renderTemplateList(); }
  });
  $('tplClearSearch').onclick = () => {
    $('tplSearch').value = ''; tplCat = ''; buildCategoryChips(); renderTemplateList(); $('tplSearch').focus();
  };
  $('jobSearch').addEventListener('input', renderJobList);
  document.addEventListener('keydown', (e) => {
    const typing = /^(INPUT|TEXTAREA|SELECT)$/.test((document.activeElement || {}).tagName || '');
    if (e.key === '/' && !typing && !$('view-templates').hidden) {
      e.preventDefault();
      (!$('tplListView').hidden ? $('tplSearch') : $('jobSearch')).focus();
    }
  });
  $('tplDismiss').onclick = () => { loadedFrom = null; showBanner(); };
  $('tplEdit').onclick = editLoadedJob;
}

/* ---- Template detail -------------------------------------------------- */

async function openTemplate(id, setId) {
  hideMine();
  $('tplListView').hidden = true;
  $('tplDetailView').hidden = false;
  await tplReady;
  if (!presetDef || presetDef.id !== id) {
    $('presetJobs').innerHTML = '<p class="muted">loading&hellip;</p>';
    try {
      presetDef = await api('/presets/' + encodeURIComponent(id));
    } catch (e) {
      presetDef = null;
      toast('Template not found: ' + id, 'error');
      go('#/templates');
      return;
    }
    jobFilter = 'all';
    $('jobSearch').value = '';
    buildTemplateHeader();
    shownSet = '';
  }
  // Your own copy of a template opens with your settings in place.
  openedSet = null;
  if ((setId || '') !== shownSet) { buildTemplateHeader(); shownSet = setId || ''; }
  if (setId && me) {
    try {
      const r = await api('/my-templates/' + encodeURIComponent(setId));
      if (r.template && r.template.presetId === id) {
        openedSet = r.template;
        Object.entries(r.values || {}).forEach(([k, v]) => { const i = settingInput(k); if (i && !i.readOnly) i.value = v; });
      }
    } catch (e) { toast('That template of yours was not found', 'error'); }
  }
  if (me) await Promise.all([loadFavorites(), loadSets()]);
  showSetBar();
  await refreshJobs();
}
let openedSet = null, shownSet = '';

// The star on a template: keep it, with the settings filled in, in My favorites; again to remove it.
async function starTemplate() {
  try {
    if (openedSet) {
      await api('/my-templates/' + encodeURIComponent(openedSet.id), { method: 'DELETE' });
      toast('Removed \u201c' + openedSet.name + '\u201d from My favorites', 'ok');
      go('#/templates/' + encodeURIComponent(presetDef.id));
    } else {
      // Kept as it stands now, under the template's own name: nothing is asked.
      const t = await api('/my-templates', { method: 'POST', headers: JSON_HEADERS,
        body: JSON.stringify({ presetId: presetDef.id, name: presetDef.title, description: '', values: presetValues() }) });
      toast('Added \u201c' + t.name + '\u201d to My favorites', 'ok');
      go('#/templates/' + encodeURIComponent(presetDef.id) + '?my=' + encodeURIComponent(t.id));
    }
  } catch (e) { toast(e.message, 'error'); }
}

function showSetBar() {
  $('setBar').hidden = !openedSet;
  const star = $('tplStar');
  star.hidden = !me || guestMode || !presetDef;
  star.textContent = openedSet ? '\u2605' : '\u2606';
  star.classList.toggle('on', !!openedSet);
  star.setAttribute('aria-pressed', String(!!openedSet));
  star.title = openedSet ? 'In My favorites: click to remove' : 'Add this template, with your settings, to My favorites';
  if (openedSet) $('setBarName').textContent = openedSet.name;
}

function buildTemplateHeader() {
  $('tplTitle').textContent = presetDef.title;
  document.title = presetDef.title.replace(/ \(.*$/, '') + ' Load Testing Template | BLASTA';
  $('tplCat').textContent = presetDef.category || '';
  $('tplIcon').innerHTML = catIcon(presetDef.category);
  $('presetSummary').textContent = presetDef.description || (presetDef.summary || '').replace(/ Includes an enterprise test plan.*$/, '');
  $('tplStack').textContent = presetDef.stack ? 'Stack: ' + presetDef.stack : '';
  const box = $('presetVars');
  box.innerHTML = '';
  const vars = (presetDef.variables || []).filter((v) => !v.derived);
  $('tplSettings').hidden = vars.length === 0;
  vars.forEach((v) => {
    const label = document.createElement('label');
    label.append(document.createTextNode(v.name + ' '));
    if (v.sensitive) {
      const s = document.createElement('span');
      s.className = 'hint';
      s.textContent = '(credential \u2014 set as an environment variable)';
      label.appendChild(s);
    }
    const input = document.createElement('input');
    input.spellcheck = false;
    input.dataset.var = v.name;
    if (v.sensitive) {
      // Credentials are never taken through the form: they stay as an
      // environment-variable reference that the CLI expands when it runs.
      input.value = v.default || '';
      input.readOnly = true;
      input.tabIndex = -1;
    } else {
      input.value = v.default || '';
      if (v.placeholder) input.placeholder = 'set this before running';
      input.addEventListener('input', () => {
        clearTimeout(renderTimer);
        renderTimer = setTimeout(refreshJobs, 350);
      });
    }
    label.appendChild(input);
    if (v.description) {
      const h = document.createElement('span');
      h.className = 'help';
      h.textContent = v.description;
      label.appendChild(h);
    }
    box.appendChild(label);
  });
}

function presetValues() {
  const out = {};
  document.querySelectorAll('#presetVars [data-var]').forEach((i) => {
    const v = i.value.trim();
    if (v) out[i.dataset.var] = v;
  });
  return out;
}

// Render the template with the current settings. Sequence-numbered so a slow
// response to an older keystroke cannot overwrite a newer one.
async function refreshJobs() {
  if (!presetDef) return;
  const seq = ++renderSeq;
  let res;
  try {
    res = await api('/presets/' + encodeURIComponent(presetDef.id) + '/render', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ values: presetValues() }),
    });
  } catch (e) {
    if (seq === renderSeq) toast('Could not build the jobs: ' + e.message, 'error');
    return;
  }
  if (seq !== renderSeq) return;
  renderedJobs = res.jobs || [];
  buildJobFilters();
  renderJobList();
}

const isPlan = (j) => j.jobId.startsWith('ent-');
const JOB_FILTERS = [
  ['all', 'All', () => true],
  ['scen', 'Scenarios', (j) => !isPlan(j)],
  ['plan', 'Enterprise plan', isPlan],
  ['gate', 'Pass/fail gates', (j) => !!j.job.slo],
  ['read', 'Read-only', (j) => !j.safety || j.safety === 'read'],
];

function buildJobFilters() {
  $('jobFilters').innerHTML = JOB_FILTERS.map(([k, label, fn]) => {
    const n = renderedJobs.filter(fn).length;
    if (k !== 'all' && n === 0) return '';
    return '<button type="button" class="chip' + (jobFilter === k ? ' on' : '') + '" data-f="' + k +
      '" aria-pressed="' + (jobFilter === k) + '">' + label + ' <span class="n">' + n + '</span></button>';
  }).join('');
  $('jobFilters').querySelectorAll('button').forEach((b) => {
    b.onclick = () => { jobFilter = b.dataset.f; buildJobFilters(); renderJobList(); };
  });
}

function jobCard(j) {
  const target = (j.job.target || {});
  const shown = target.url || (target.meta && target.meta.target) ||
    (target.meta && target.meta.query) || '';
  const tag = j.safety && j.safety !== 'read'
    ? '<span class="tag ' + esc(j.safety) + '">' + esc(j.safety) + '</span>' : '';
  const gate = j.job.slo ? '<span class="tag gate" title="Has pass/fail targets (SLO)">SLO</span>' : '';
  const starred = me && !guestMode && favs.list.some((f) => f.name === j.name);
  const star = me && !guestMode ? '<button class="star' + (starred ? ' on' : '') + '" type="button" data-star="' + esc(j.jobId) + '" aria-pressed="' + !!starred +
    '" title="' + (starred ? 'In My favorites: click to remove' : 'Add this job to My favorites') + '">' + (starred ? '\u2605' : '\u2606') + '</button>' : '';
  return '<div class="pjob"><div><div class="pjob-name">' + star + esc(j.name) + ' ' + tag + gate +
    '</div><div class="pjob-id">' + esc(j.jobId) + '</div></div>' +
    '<div class="pjob-actions"><button class="btn primary-sm" type="button" data-use="' + esc(j.jobId) + '">' + (guestMode ? 'Sign in to use' : 'Use this job') + '</button>' +
    '</div>' +
    (j.notes ? '<div class="pjob-notes">' + esc(j.notes) + '</div>' : '') +
    (shown ? '<div class="pjob-target">' + esc(shown) + '</div>' : '') + '</div>';
}

function renderJobList() {
  const fn = (JOB_FILTERS.find((f) => f[0] === jobFilter) || JOB_FILTERS[0])[2];
  const toks = tokens($('jobSearch').value);
  const jobs = renderedJobs.filter(fn).filter((j) => {
    const hay = (j.name + ' ' + j.jobId + ' ' + (j.notes || '')).toLowerCase();
    return toks.every((t) => hay.includes(t));
  });
  const scen = jobs.filter((j) => !isPlan(j));
  const plan = jobs.filter(isPlan);
  let html = '';
  if (scen.length) html += '<h3 class="group-h">Scenarios <span class="count">' + scen.length + '</span></h3><div class="jobs">' + scen.map(jobCard).join('') + '</div>';
  if (plan.length) {
    html += '<h3 class="group-h">Enterprise test plan <span class="count">' + plan.length +
      '</span> <span class="hint">run 01 &rarr; 10 in order; SLO jobs are pass/fail gates</span></h3><div class="jobs">' +
      plan.map(jobCard).join('') + '</div>';
  }
  if (!jobs.length) html = '<p class="muted-p">No jobs match. Clear the filter or the search.</p>';
  $('presetJobs').innerHTML = html;
  $('presetJobs').querySelectorAll('[data-star]').forEach((b) => {
    b.onclick = async () => {
      const job = renderedJobs.find((x) => x.jobId === b.dataset.star);
      if (!job) return;
      const have = favs.list.find((f) => f.name === job.name);
      try {
        if (have) await api('/my-favorites/' + encodeURIComponent(have.id), { method: 'DELETE' });
        else {
          // Straight into My favorites with the template's own defaults: nothing is asked.
          await api('/my-favorites', { method: 'POST', headers: JSON_HEADERS,
            body: JSON.stringify({ name: job.name, description: (job.notes || '').slice(0, 500), job: job.job }) });
          toast('Added \u201c' + job.name + '\u201d to My favorites', 'ok');
        }
        await loadFavorites();
        renderJobList();
      } catch (e) { toast(e.message, 'error'); }
    };
  });
  $('presetJobs').querySelectorAll('[data-use]').forEach((b) => {
    b.onclick = () => {
      if (guestMode) { openGate('use'); return; }
      const job = renderedJobs.find((x) => x.jobId === b.dataset.use);
      if (job) useJob(job);
    };
  });
}

// The variables a job actually uses, so the user is asked only for those. A
// derived value (a SAML request) depends on other settings, so those are included.
function varsUsedBy(jobId) {
  const raw = (presetDef.jobs.find((x) => x.id === jobId) || {}).job || {};
  const byName = Object.fromEntries((presetDef.variables || []).map((v) => [v.name, v]));
  const used = new Set([...JSON.stringify(raw).matchAll(/\{\{(\w+)\}\}/g)].map((m) => m[1]));
  [...used].forEach((n) => {
    if (byName[n] && byName[n].derived) {
      ['ssoUrl', 'sloUrl', 'acsUrl', 'spEntityId', 'nameId'].forEach((d) => { if (byName[d]) used.add(d); });
    }
  });
  return [...used].map((n) => byName[n]).filter((v) => v && !v.derived);
}

const settingInput = (name) => document.querySelector('#presetVars [data-var="' + name + '"]');
const ENV_RE = /\$\{([A-Z][A-Z0-9_]*)\}/g;

// Guides are plain text: one paragraph per line, `code` in backticks.
const guideHtml = (g) => String(g || '').split('\n').map((l) =>
  '<p>' + esc(l).replace(/`([^`]+)`/g, '<code>$1</code>') + '</p>').join('');

// Everything this job needs from the user: the template values it uses, the
// page path when the job's URL has a fixed one, and every ${CREDENTIAL} it reads.
function detailsFor(j) {
  const raw = (presetDef.jobs.find((x) => x.id === j.jobId) || {}).job || {};
  const m = /^\{\{(\w+)\}\}(\/[^{}]*)$/.exec((raw.target || {}).url || '');
  const names = [...new Set([...JSON.stringify(j.job).matchAll(ENV_RE)].map((x) => x[1]))];
  return {
    vars: varsUsedBy(j.jobId).filter((v) => !v.sensitive),
    path: m ? m[2] : null,                       // e.g. "/wp-admin/"
    creds: names.map((n) => Object.assign({ name: n, label: n, guide: '' }, (presetDef.secrets || {})[n])),
  };
}

const howBlock = (guide) => guide
  ? '<details class="how"><summary>How do I get this?</summary><div class="how-body">' + guideHtml(guide) + '</div></details>' : '';

// Ask for the details this job needs before it is added. Resolves with
// { vars, path, secrets }, or null if the user cancels.
function askForDetails(j, d, opts) {
  opts = opts || {};
  return new Promise((resolve) => {
    const dlg = $('askDlg');
    const short = j.name.startsWith(presetDef.title + ': ') ? j.name.slice(presetDef.title.length + 2) : j.name;
    $('askTitle').textContent = (opts.edit ? 'Edit \u201c' : 'Set up \u201c') + short + '\u201d';
    $('askOk').textContent = opts.edit ? 'Save changes' : 'Add to job';
    const unset = d.vars.filter((v) => v.placeholder).length;
    $('askIntro').textContent = opts.edit
      ? 'Change any detail. Only the address, path, headers and body are updated: your rate, duration and other load settings stay as they are.'
      : unset || d.creds.length
        ? 'Fill in what you can. Nothing is checked here, and you can change everything on the Jobs page.'
        : 'Check the details this job will use.';

    let n = 0;
    const fields = [];
    const field = (spec) => {
      const id = 'ask' + (n++);
      fields.push(Object.assign({ id }, spec));
      const reveal = spec.secret ? '<button type="button" class="reveal" data-for="' + id + '">Show</button>' : '';
      return '<div class="ask-field"><label for="' + id + '">' + esc(spec.label) + spec.tag +
        (spec.vname ? '<span class="vname">' + esc(spec.vname) + '</span>' : '') + '</label>' +
        '<div class="input-wrap"><input id="' + id + '" spellcheck="false" autocomplete="' + (spec.secret ? 'new-password' : 'off') +
        '" type="' + (spec.secret ? 'password' : 'text') + '" value="' + esc(spec.value) + '"' +
        (spec.placeholder ? ' placeholder="' + esc(spec.placeholder) + '"' : '') + '>' + reveal + '</div>' +
        howBlock(spec.guide) + '</div>';
    };
    const req = '<span class="req">needed</span>', opt = (t) => '<span class="opt">' + t + '</span>';

    let html = '';
    if (d.vars.length) {
      html += '<h3 class="ask-h">Your system</h3>' + d.vars.map((v) => {
        const cur = settingInput(v.name);
        const val = cur ? cur.value : (v.default || '');
        const isExample = v.placeholder && val === (v.default || '');
        return field({ kind: 'var', v, label: v.description || v.name, vname: v.name, value: val, guide: v.guide,
          tag: v.placeholder ? (isExample ? req : opt('from your settings')) : opt('optional') });
      }).join('');
    }
    if (d.path) {
      html += '<h3 class="ask-h">Page</h3>' + field({ kind: 'path', label: 'Path to request', vname: 'path', value: d.pathValue || d.path,
        tag: opt('optional'),
        guide: 'The part of the address after the domain, starting with /. It is filled in with what this template tests.\nChange it to test a different page, for example `/about/` or `/checkout/`. Add a query string if you need one, for example `/search?q=shoes`.' });
    }
    if (d.creds.length) {
      html += '<h3 class="ask-h">Credentials</h3>' + d.creds.map((c) => field({
        kind: 'secret', c, secret: true, label: c.label, vname: c.name, value: c.value || '', tag: req,
        placeholder: 'paste it here', guide: c.guide })).join('');
    }
    $('askFields').innerHTML = html;
    $('askFields').querySelectorAll('.reveal').forEach((b) => {
      b.onclick = () => {
        const i = $(b.dataset.for);
        i.type = i.type === 'password' ? 'text' : 'password';
        b.textContent = i.type === 'password' ? 'Show' : 'Hide';
      };
    });
    $('askErr').hidden = true;
    $('askNote').hidden = !d.creds.length;
    $('askNote').textContent = d.creds.length
      ? 'Credentials stay in your browser and are sent only to this BLASTA server and the system you are testing. They are not saved with the template or in History.'
      : '';

    let done = false;
    const finish = (value) => { if (done) return; done = true; dlg.close(); resolve(value); };
    $('askCancel').onclick = () => finish(null);
    dlg.oncancel = (e) => { e.preventDefault(); finish(null); };   // Esc
    // No validation: whatever is entered is used, and anything left empty falls
    // back to the template's own default (an empty credential stays ${NAME}).
    $('askForm').onsubmit = (e) => {
      e.preventDefault();
      const out = { vars: {}, path: null, secrets: {} };
      for (const f of fields) {
        const val = $(f.id).value.trim();
        if (f.kind === 'var') out.vars[f.v.name] = val || f.v.default || '';
        else if (f.kind === 'path') out.path = val || d.path;
        else if (val) out.secrets[f.c.name] = val;
      }
      finish(out);
    };
    dlg.showModal();
    // Start on the first field that still needs an answer.
    const first = fields.find((f) => f.kind === 'secret' || (f.kind === 'var' && f.v.placeholder && $(f.id).value === (f.v.default || ''))) || fields[0];
    if (first) { $(first.id).focus(); $(first.id).select(); }
  });
}

// Credentials are typed once in the dialog and put straight into the form. The
// preset API never sees them, and the job file keeps ${NAME} until this point.
function applySecrets(secrets) {
  const sub = (s) => Object.entries(secrets).reduce((a, [k, v]) => a.split('${' + k + '}').join(v), s);
  ['url', 'body', 'tcpbody', 'grpcbody', 'wsbody', 'query'].forEach((id) => { const e = $(id); if (e) e.value = sub(e.value); });
  document.querySelectorAll('#headers .hrow [data-role=v]').forEach((i) => { i.value = sub(i.value); });
}

// Hand a chosen job to the Jobs page and go back there to start it. It asks for
// everything the job needs first, so it never silently uses an example address
// or an unfilled credential.
async function useJob(j) {
  const d = detailsFor(j);
  let answers = null;
  if (d.vars.length || d.path || d.creds.length) {
    answers = await askForDetails(j, d);
    if (!answers) return false;
    Object.entries(answers.vars).forEach(([k, v]) => { const i = settingInput(k); if (i) i.value = v; });
    await refreshJobs();                       // rebuild every job with the new settings
    j = renderedJobs.find((x) => x.jobId === j.jobId) || j;
  }
  usePresetJob(j);
  if (answers) {
    if (d.path && answers.path && answers.path !== d.path) {
      const cur = $('url').value;
      if (cur.endsWith(d.path)) $('url').value = cur.slice(0, cur.length - d.path.length) + answers.path;
    }
    applySecrets(answers.secrets);
    updateSafety();
  }
  loadedFrom = { id: presetDef.id, title: presetDef.title, jobId: j.jobId, job: j.name, notes: j.notes || '',
    safety: j.safety || 'read', gate: !!j.job.slo,
    // kept in memory only, so Edit can prefill the dialog
    vars: answers ? answers.vars : {}, path: answers ? answers.path : null, secrets: answers ? answers.secrets : {} };
  showBanner();
  go('#/jobs');
  return true;
}

// Update only the target-related fields from a rendered job (address, headers,
// body, query), leaving the load settings the user tuned untouched.
function applyDetails(job) {
  const target = job.target || {};
  const meta = target.meta || {};
  if (target.url) $('url').value = target.url;
  if (meta.method && $('grpcmethod')) $('grpcmethod').value = meta.method;
  if (meta.target && $('grpctarget')) $('grpctarget').value = meta.target;
  if (meta.addr && $('grpctarget')) $('grpctarget').value = meta.addr;
  if (meta.query && $('query')) $('query').value = meta.query;
  if (job.db && job.db.dsnEnv && $('dsnenv')) $('dsnenv').value = job.db.dsnEnv;
  if (job.body) {
    const f = { grpc: 'grpcbody', tcp: 'tcpbody', ws: 'wsbody' }[job.executor];
    if (f && $(f)) $(f).value = job.executor === 'tcp' ? escapeCtl(job.body) : job.body;
    else $('body').value = job.body;
  }
  // Headers: update the ones the template sets, add missing ones, keep any the user added.
  Object.entries(job.headers || {}).forEach(([k, v]) => {
    const row = [...document.querySelectorAll('#headers .hrow')]
      .find((r) => r.querySelector('[data-role=k]').value.trim().toLowerCase() === k.toLowerCase());
    const val = isAgentHeader(k) ? taggedAgent(v) : v;
    if (row) row.querySelector('[data-role=v]').value = val; else headerRow(k, val);
  });
  countHeaders();
}

// Reopen the details dialog for the loaded job, prefilled with the last answers.
async function editLoadedJob() {
  const lf = loadedFrom;
  if (!lf) return;
  try {
    if (!presetDef || presetDef.id !== lf.id) {      // the user browsed another template meanwhile
      presetDef = await api('/presets/' + encodeURIComponent(lf.id));
      jobFilter = 'all';
      $('jobSearch').value = '';
      buildTemplateHeader();
    }
    Object.entries(lf.vars || {}).forEach(([k, v]) => { const i = settingInput(k); if (i) i.value = v; });
    await refreshJobs();
  } catch (e) {
    toast('Could not reopen the template: ' + e.message, 'error');
    return;
  }
  let j = renderedJobs.find((x) => x.jobId === lf.jobId);
  if (!j) { toast('That job no longer exists in the template.', 'error'); return; }
  const d = detailsFor(j);
  d.pathValue = lf.path || d.path;
  d.creds.forEach((c) => { c.value = (lf.secrets || {})[c.name] || ''; });
  const answers = await askForDetails(j, d, { edit: true });
  if (!answers) return;
  Object.entries(answers.vars).forEach(([k, v]) => { const i = settingInput(k); if (i) i.value = v; });
  await refreshJobs();
  j = renderedJobs.find((x) => x.jobId === lf.jobId) || j;
  applyDetails(j.job);
  if (d.path && answers.path && answers.path !== d.path) {
    const cur = $('url').value;
    if (cur.endsWith(d.path)) $('url').value = cur.slice(0, cur.length - d.path.length) + answers.path;
  }
  applySecrets(answers.secrets);
  updateSafety();
  showFormError('');
  loadedFrom = Object.assign({}, lf, { vars: answers.vars, path: answers.path, secrets: answers.secrets });
  toast('Details updated \u2014 your load settings were not changed', 'ok');
}

function showBanner() {
  $('tplBanner').hidden = !loadedFrom;
  if (!loadedFrom) return;
  const jobLabel = loadedFrom.job.startsWith(loadedFrom.title + ': ') ? loadedFrom.job.slice(loadedFrom.title.length + 2) : loadedFrom.job;
  $('tplBannerTag').textContent = loadedFrom.mine ? 'My favorite' : 'From template';
  $('tplBannerName').textContent = loadedFrom.title + ' \u203a ' + jobLabel;
  $('tplBannerTags').innerHTML = (loadedFrom.safety !== 'read'
    ? '<span class="tag ' + esc(loadedFrom.safety) + '">' + esc(loadedFrom.safety) + '</span> ' : '') +
    (loadedFrom.gate ? '<span class="tag gate">SLO</span>' : '');
  $('tplBannerNotes').textContent = loadedFrom.notes;
  $('tplChange').href = loadedFrom.mine ? 'templates?favorites' : 'templates/' + encodeURIComponent(loadedFrom.id);
  $('tplEdit').hidden = !!loadedFrom.mine;       // built-in templates have details to fill in; yours are already filled
  $('tplSave').hidden = !loadedFrom.mine;
}
wireTemplateSearch();

// Fill the job form from a rendered preset job, so the user can review every
// field before starting rather than trusting the template blindly.
function usePresetJob(j) {
  const job = j.job;
  const target = job.target || {};
  const meta = target.meta || {};

  $('executor').value = job.executor || 'http';
  if (![...$('executor').options].some((o) => o.value === $('executor').value)) {
    // A preset may use an executor this build does not know; keep it visible
    // rather than silently falling back to http.
    const o = document.createElement('option');
    o.value = o.textContent = $('executor').value;
    $('executor').appendChild(o);
  }
  $('url').value = target.url || '';
  if (job.method) {
    // Presets use methods the dropdown does not list (PROPFIND, ...); add them
    // rather than letting the select silently go blank.
    if (![...$('method').options].some((o) => o.value === job.method)) {
      const o = document.createElement('option');
      o.value = o.textContent = job.method;
      $('method').appendChild(o);
    }
    $('method').value = job.method;
  }
  if (meta.method && $('grpcmethod')) $('grpcmethod').value = meta.method;
  if (meta.target && $('grpctarget')) $('grpctarget').value = meta.target;
  if (meta.addr && $('grpctarget')) $('grpctarget').value = meta.addr;
  if (meta.query && $('query')) $('query').value = meta.query;
  if (job.body) {
    const f = { grpc: 'grpcbody', tcp: 'tcpbody', ws: 'wsbody' }[job.executor];
    // Show control characters as the escapes a person can type and edit.
    const shown = job.executor === 'tcp' ? escapeCtl(job.body) : job.body;
    if (f && $(f)) $(f).value = shown;
    else $('body').value = job.body;
  }
  $('tcpread').value = meta.readBytes || 0;
  $('tcpexpect').value = escapeCtl(meta.expectPrefix);
  $('tcphex').value = meta.bodyHex || '';
  $('tcpexphex').value = meta.expectHex || '';
  $('binaryBox').open = !!(meta.bodyHex || meta.expectHex);
  $('followredir').checked = meta.followRedirects !== false;
  $('insecuretls').checked = !!meta.insecureTLS;
  $('ramp').value = job.ramp ? Math.round(job.ramp / ns) : 0;
  const slo = job.slo || {};
  $('slo-err').value = slo.maxErrorRate != null ? slo.maxErrorRate : '';
  $('slo-p95').value = slo.maxP95 ? Math.round(slo.maxP95 / 1e6) : '';
  $('slo-p99').value = slo.maxP99 ? Math.round(slo.maxP99 / 1e6) : '';
  if (job.slo) $('advanced').open = true;
  $('expect').value = (job.expectStatus || []).join(', ');
  if (job.db) {
    if (job.db.dsnEnv && $('dsnenv')) $('dsnenv').value = job.db.dsnEnv;
    if ($('dbdriver') && job.db.driver) {
      if (![...$('dbdriver').options].some((o) => o.value === job.db.driver)) {
        const o = document.createElement('option');
        o.value = o.textContent = job.db.driver;
        $('dbdriver').appendChild(o);
      }
      $('dbdriver').value = job.db.driver;
    }
    if ($('allowwrite')) $('allowwrite').checked = !!job.db.allowWrite;
    if (job.db.maxOpen && $('dbmaxopen')) $('dbmaxopen').value = job.db.maxOpen;
  }

  $('headers').innerHTML = '';
  if (!Object.keys(job.headers || {}).some(isAgentHeader)) headerRow('User-Agent', BLASTA_UA);
  Object.entries(job.headers || {}).forEach(([k, v]) => headerRow(k, isAgentHeader(k) ? taggedAgent(v) : v));
  countHeaders();

  if (job.concurrency != null) $('conc').value = job.concurrency;
  if (job.rps != null) $('rps').value = job.rps;
  if (job.duration) $('duration').value = Math.max(1, Math.round(job.duration / ns));
  if (job.timeout) $('timeout').value = Math.max(1, Math.round(job.timeout / ns));
  if (job.queueSize != null) $('queue').value = job.queueSize;
  if (job.maxWorkers != null) $('maxworkers').value = job.maxWorkers;
  if (job.onQueueFull) $('onfull').value = job.onQueueFull;
  if (job.blockPrivate != null) $('blockprivate').checked = job.blockPrivate;

  syncProfileFromFields();
  updateSafety();
  showFormError('');
  toast('Loaded \u201c' + j.name + '\u201d \u2014 review the fields, then press Start job', 'ok');
}


/* ---- Sign-in, registration, SSO and users ---------------------------------- */

// The sign-in endpoints answer with their own error codes (a wrong password is a
// 401 that must NOT be mistaken for "your session ended"), so they use this
// helper instead of api().
async function authCall(method, path, body) {
  const r = await fetch('api/auth/' + path, {
    method, headers: { 'Content-Type': 'application/json', 'X-Requested-With': 'blasta' },
    body: body ? JSON.stringify(body) : undefined,
  });
  let data = {};
  try { data = await r.json(); } catch (e) { /* empty body */ }
  return { ok: r.ok, status: r.status, data };
}

// Is sign-in on, and is someone signed in? Resolves true when the app may start.
async function bootAuth() {
  try {
    const r = await fetch('api/auth/config', { headers: { 'X-Requested-With': 'blasta' } });
    if (!r.ok) return true;                        // 404: sign-in is off
    authCfg = await r.json();
  } catch (e) { return true; }
  const m = await authCall('GET', 'me');
  if (!m.ok) {
    // Not signed in. When the administrator allows it, a visitor can try a short, limited test.
    if (authCfg.guest && !authCfg.needsSetup) {
      guestMode = true;
      document.body.classList.add('is-guest');
      $('guestActions').hidden = false;
      await refreshGuest();
      return true;
    }
    return false;
  }
  me = m.data;
  showUser();
  return true;
}

/* ---- Bot check (Cloudflare Turnstile / Google reCAPTCHA) ---- */

const SCRIPTS = { turnstile: 'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit',
  recaptcha: 'https://www.google.com/recaptcha/api.js?render=explicit' };
const captchaState = {};          // box id -> { widget, token }
let captchaScript = null;

function loadCaptchaScript(provider) {
  if (captchaScript) return captchaScript;
  captchaScript = new Promise((resolve, reject) => {
    const el = document.createElement('script');
    el.src = SCRIPTS[provider];
    el.async = true;
    el.onload = () => {
      if (provider === 'recaptcha' && window.grecaptcha && grecaptcha.ready) grecaptcha.ready(resolve); else resolve();
    };
    el.onerror = () => { captchaScript = null; reject(new Error('The bot check could not be loaded. Check your connection and reload the page.')); };
    document.head.appendChild(el);
  });
  return captchaScript;
}

const captchaWanted = (scope) => !!(authCfg && authCfg.captcha && authCfg.captcha['on' + scope]);

// Show the challenge in a box if this place is protected; hide the box if not.
async function mountCaptcha(boxId, scope, onToken) {
  const box = $(boxId);
  if (!box) return;
  const wanted = captchaWanted(scope);
  box.hidden = !wanted;
  if (!wanted) return;
  const st = captchaState[boxId] || (captchaState[boxId] = {});
  if (st.widget != null) { resetCaptcha(boxId); return; }
  try {
    await loadCaptchaScript(authCfg.captcha.provider);
    const opts = { sitekey: authCfg.captcha.siteKey, callback: (t) => { st.token = t; if (onToken) onToken(t); },
      'expired-callback': () => { st.token = ''; }, 'error-callback': () => { st.token = ''; } };
    st.widget = authCfg.captcha.provider === 'turnstile' ? turnstile.render(box, opts) : grecaptcha.render(box, opts);
  } catch (e) { showAuthError(e.message); }
}

const captchaToken = (boxId) => (captchaState[boxId] && captchaState[boxId].token) || '';

function resetCaptcha(boxId) {
  const st = captchaState[boxId];
  if (!st || st.widget == null) return;
  st.token = '';
  try { authCfg.captcha.provider === 'turnstile' ? turnstile.reset(st.widget) : grecaptcha.reset(st.widget); } catch (e) {}
}

// A visitor passes the check once, before their first free test. Resolves true when verified.
function guestChallenge() {
  return new Promise((resolve) => {
    let done = false;
    const finish = (ok) => { if (done) return; done = true; if ($('captchaDlg').open) $('captchaDlg').close(); resolve(ok); };
    $('capGuestErr').hidden = true;
    $('captchaDlg').showModal();
    $('capGuestCancel').onclick = () => finish(false);
    $('captchaDlg').onclose = () => finish(false);
    mountCaptcha('capGuest', 'Guest', async (token) => {
      const r = await authCall('POST', 'guest/captcha', { captcha: token });
      if (r.ok) { await refreshGuest(); finish(true); return; }
      $('capGuestErr').textContent = r.data.error || 'The check did not pass. Try again.';
      $('capGuestErr').hidden = false;
      resetCaptcha('capGuest');
    });
  });
}

/* ---- Free trial (a visitor without an account) ---- */

async function refreshGuest() {
  try {
    const r = await fetch('api/auth/guest', { headers: { 'X-Requested-With': 'blasta' } });
    guestInfo = await r.json();
    guestSkew = Date.parse(guestInfo.now) - Date.now();
  } catch (e) { return; }
  if (!guestInfo.enabled) { location.reload(); return; }        // the administrator turned the trial off
  renderTrial();
}

const fmtClock = (ms) => { const t = Math.max(0, Math.ceil(ms / 1000)); return Math.floor(t / 60) + ':' + String(t % 60).padStart(2, '0'); };

function renderTrial() {
  if (!guestMode || !guestInfo) return;
  const g = guestInfo, L = g.limits, bar = $('trialBar');
  bar.hidden = false;
  clearInterval(guestTimer);
  const limits = 'Up to ' + L.maxRuns + ' tests of ' + L.maxDurationSec + ' s, ' + L.maxRps + ' requests per second and ' +
    L.maxConcurrency + ' connections, on web addresses only. Templates and history need an account.';
  const tick = () => {
    if (!g.active) {
      $('trialTime').textContent = L.trialMinutes + ' minutes, starting with your first test';
      $('trialNote').textContent = limits;
      bar.classList.remove('over');
      return;
    }
    const left = Date.parse(g.endsAt) - (Date.now() + guestSkew);
    if (g.expired || left <= 0) {
      g.expired = true;
      $('trialTime').textContent = 'Your trial has ended';
      $('trialNote').textContent = 'Sign in or create an account to keep testing. It only takes a minute.';
      bar.classList.add('over');
      clearInterval(guestTimer);
      return;
    }
    $('trialTime').textContent = fmtClock(left) + ' left';
    $('trialNote').textContent = g.runsUsed + ' of ' + g.runsMax + ' tests used. ' + limits;
    bar.classList.remove('over');
  };
  tick();
  guestTimer = setInterval(tick, 1000);
}

// A guest sees the same form, with the choices that are not allowed taken away.
function applyGuestLimits() {
  const L = guestInfo ? guestInfo.limits : null;
  Array.from($('executor').options).forEach((o) => { if (o.value !== 'http') o.remove(); });
  $('executor').value = 'http';
  if (L) {
    $('rps').max = L.maxRps; $('conc').max = L.maxConcurrency; $('duration').max = L.maxDurationSec;
    $('rps').value = Math.min(+$('rps').value, L.maxRps);
    $('conc').value = Math.min(+$('conc').value, L.maxConcurrency);
    $('duration').value = Math.min(+$('duration').value, L.maxDurationSec);
  }
  $('blockprivate').checked = true;
  $('blockprivate').disabled = true;
  document.querySelectorAll('#profiles button[data-profile=heavy]').forEach((b) => { b.title = 'Scaled down to the trial limits'; });
}

// Ask a visitor to sign in or register, saying what for.
function openGate(what) {
  const t = {
    templates: ['Sign in to use templates', 'Ready-made templates for WordPress, identity providers, databases, caches and more are part of the full app. Sign in, or create an account, to use them.'],
    use: ['Sign in to use this job', 'You can browse every template and read what each job does. Using a job, adding it to a test, needs an account. Sign in, or create one.'],
    history: ['Sign in to see your history', 'Test history is saved to your account. Sign in, or create an account, to keep and compare your runs.'],
    admin: ['Sign in', 'That page needs an account.'],
    trial: ['Your free trial is used up', 'Sign in, or create an account, to keep testing with higher limits, templates and saved history.'],
    login: ['Sign in to continue', 'That needs an account. Sign in, or create one, to continue.'],
  }[what] || ['Sign in to continue', 'That needs an account.'];
  $('gateTitle').textContent = t[0];
  $('gateText').textContent = t[1];
  if (!$('gateDlg').open) $('gateDlg').showModal();
}

// Another tab signed out, or the session timed out.
function sessionEnded() {
  if (endingSession) return;
  endingSession = true;
  if (es) { es.close(); es = null; }
  me = null;
  document.body.classList.remove('is-admin');
  $('userMenu').hidden = true;
  if (authCfg && authCfg.guest) {
    store.set(SESSION_KEY, '');
    history.replaceState(null, '', toUrl('#/jobs'));
    location.reload();
    return;
  }
  toast('Your session ended. Please sign in again.', 'error');
  go('#/login');
  setTimeout(() => { endingSession = false; }, 1500);
}

const initial = (s) => (String(s || '?').trim()[0] || '?').toUpperCase();

// A round avatar: the person's picture if they have one, otherwise their initial.
function paintAvatar(el, u) {
  el.textContent = '';
  if (u.avatar) {
    const img = document.createElement('img');
    img.alt = '';
    img.src = u.avatar;
    el.appendChild(img);
  } else {
    el.textContent = initial(u.name || u.email);
  }
}

function showUser() {
  paintVersion();
  if (typeof pollRunning === 'function') pollRunning();
  $('userMenu').hidden = false;
  paintAvatar($('userAvatar'), me);
  $('userName').textContent = me.name || me.email;
  $('menuName').textContent = me.name || me.email;
  $('menuEmail').textContent = me.email;
  $('menuRole').textContent = me.role;
  $('menuAdmin').hidden = me.role !== 'admin';
  document.body.classList.toggle('is-admin', me.role === 'admin');
}

function showAuthError(msg) { $('authErr').hidden = !msg; $('authErr').textContent = msg || ''; $('authOk').hidden = true; }
function showAuthOk(msg) { $('authOk').hidden = !msg; $('authOk').textContent = msg || ''; $('authErr').hidden = true; }

function setAuthTab(tab) {
  const reg = tab === 'register';
  $('loginForm').hidden = reg;
  $('registerForm').hidden = !reg;
  $('tabSignIn').setAttribute('aria-selected', String(!reg));
  $('tabRegister').setAttribute('aria-selected', String(reg));
  showAuthError('');
  (reg ? $('regEmail') : $('loginEmail')).focus();
  mountCaptcha('capLogin', 'Login');
  mountCaptcha('capRegister', 'Register');
}

// Back to the first step of signing in with a code.
function resetOtp() {
  $('otpCodeRow').hidden = true; $('otpAgain').hidden = true;
  $('otpEmail').readOnly = false; $('otpCode').value = '';
  $('otpBtn').textContent = 'Send code';
  $('otpNote').textContent = 'Enter your email address and we will email you a six-digit code to sign in.';
}

// Emailed links: confirm a new account, or a new email address.
async function runConfirm(path, token) {
  $('loginForm').hidden = $('registerForm').hidden = $('forgotForm').hidden = $('resetForm').hidden = true;
  $('authTabs').hidden = true; $('ssoBtn').hidden = true; $('authOr').hidden = true; $('guestLink').hidden = true;
  const change = path === 'email/confirm';
  $('authTitle').textContent = change ? 'Confirming your new email' : 'Confirming your email';
  $('authSub').textContent = 'One moment…';
  showAuthError('');
  const r = await authCall('POST', path, { token });
  if (!r.ok) {
    $('authTitle').textContent = 'That link did not work';
    $('authSub').textContent = '';
    showAuthError(r.data.error || 'The link is invalid or has expired.');
    return;
  }
  history.replaceState(null, '', toUrl('#/login'));
  if (change) {
    $('authTitle').textContent = 'Email address changed';
    $('authSub').textContent = '';
    showAuthOk('Your account now uses ' + r.data.email + '.');
    return;
  }
  if (r.data.status === 'active') { afterSignIn(); return; }       // confirmed, and signed in by the server
  $('authTitle').textContent = 'Email confirmed';
  $('authSub').textContent = '';
  showAuthOk('Thank you. An administrator still has to approve your account before you can sign in. You will get an email when it is ready.');
}

// Adjust the page to what the server allows: first-run setup, closed registration, SSO.
function showLogin() {
  if (!authCfg) return;
  const setup = authCfg.needsSetup;
  $('authTitle').textContent = setup ? 'Create the administrator account' : 'Sign in';
  $('authSub').textContent = setup
    ? 'This is the first account on this BLASTA server, so it becomes the administrator.'
    : 'Welcome back. Sign in to run and review load tests.';
  $('authTabs').hidden = setup || authCfg.registration === 'closed';
  $('tabRegister').textContent = authCfg.registration === 'open' ? 'Create account' : 'Request access';
  $('ssoBtn').hidden = !authCfg.sso;
  if (authCfg.sso) $('ssoName').textContent = authCfg.ssoName || 'single sign-on';
  $('authOr').hidden = !authCfg.sso;
  $('regTokenRow').hidden = !authCfg.needsSetupToken;
  const dom = (authCfg.allowedDomains || []);
  $('regDomains').hidden = !dom.length;
  $('regDomains').textContent = dom.length ? 'Only ' + dom.map((d) => '@' + d).join(', ') + ' addresses can register.' : '';
  $('registerBtn').textContent = setup ? 'Create administrator' : authCfg.registration === 'open' ? 'Create account' : 'Request access';
  $('forgotLink').hidden = !authCfg.reset;
  $('otpLink').hidden = !authCfg.otp;
  $('linkDot').hidden = !(authCfg.reset && authCfg.otp);
  $('regConfirmHelp').hidden = !(authCfg.confirmEmail && !setup);
  $('resendRow').hidden = true;
  $('guestLink').hidden = !(authCfg.guest && !setup);
  setAuthTab(setup ? 'register' : /[?&]register\b/.test(routeStr()) ? 'register' : 'signin');
  // Forgot-password and reset-link pages share this card.
  const confirmM = /^#\/confirm\?token=([^&]+)/.exec(routeStr());
  const confirmEmailM = /^#\/confirm-email\?token=([^&]+)/.exec(routeStr());
  if (confirmM || confirmEmailM) { runConfirm(confirmM ? 'confirm' : 'email/confirm', decodeURIComponent((confirmM || confirmEmailM)[1])); return; }
  const tokenM = /^#\/reset\?token=([^&]+)/.exec(routeStr());
  const forgot = /[?&]forgot\b/.test(routeStr());
  const otp = /[?&]code\b/.test(routeStr()) && authCfg.otp;
  $('otpForm').hidden = !otp;
  if (otp) {
    $('loginForm').hidden = $('registerForm').hidden = true;
    $('authTabs').hidden = true; $('ssoBtn').hidden = true; $('authOr').hidden = true; $('guestLink').hidden = true;
    $('authTitle').textContent = 'Sign in with a code';
    $('authSub').textContent = 'No password needed. We email you a code that works once.';
    resetOtp();
    mountCaptcha('capOtp', 'Login');
    $('otpEmail').focus();
    return;
  }
  if (tokenM || forgot) {
    $('loginForm').hidden = $('registerForm').hidden = true;
    $('authTabs').hidden = true; $('ssoBtn').hidden = true; $('authOr').hidden = true; $('guestLink').hidden = true;
    $('forgotForm').hidden = !forgot || !!tokenM;
    if (forgot && !tokenM) mountCaptcha('capForgot', 'Login');
    $('resetForm').hidden = !tokenM;
    $('authTitle').textContent = tokenM ? 'Choose a new password' : 'Reset your password';
    $('authSub').textContent = tokenM ? 'Pick something at least 10 characters long.' : 'We will email you a link, if mail is set up.';
    (tokenM ? $('resetPassword') : $('forgotEmail')).focus();
  } else {
    $('forgotForm').hidden = $('resetForm').hidden = true;
  }
  // A failed SSO sign-in comes back here with the reason.
  const m = /[?&]sso_error=([^&]*)/.exec(routeStr());
  if (m) {
    showAuthError(decodeURIComponent(m[1].replace(/\+/g, ' ')));
    history.replaceState(null, '', toUrl('#/login'));
  }
}

function afterSignIn() {
  // A full reload guarantees nothing from a previous account stays in the page.
  store.set(SESSION_KEY, '');
  history.replaceState(null, '', toUrl('#/jobs'));
  location.reload();
}

const authMessage = (r) => r.data.error || (r.status === 429 ? 'Too many attempts. Wait a few minutes and try again.' : 'Something went wrong.');

function wireAuth() {
  $('tabSignIn').onclick = () => setAuthTab('signin');
  $('otpForm').onsubmit = async (e) => {
    e.preventDefault();
    showAuthError('');
    $('otpBtn').disabled = true;
    try {
      if ($('otpCodeRow').hidden) {                         // step 1: ask for the code
        if (!$('otpEmail').value.trim()) { showAuthError('Enter your email address.'); return; }
        const r = await authCall('POST', 'otp/request', { email: $('otpEmail').value, captcha: captchaToken('capOtp') });
        resetCaptcha('capOtp');
        if (!r.ok) { showAuthError(authMessage(r)); return; }
        $('otpCodeRow').hidden = false; $('capOtp').hidden = true; $('otpAgain').hidden = false;
        $('otpEmail').readOnly = true;
        $('otpBtn').textContent = 'Sign in';
        $('otpNote').textContent = 'If ' + $('otpEmail').value + ' has an account, a code is on its way. It works once and expires in 10 minutes.';
        $('otpCode').focus();
        return;
      }
      if (!$('otpCode').value.trim()) { showAuthError('Enter the six-digit code from the email.'); return; }
      const r = await authCall('POST', 'otp/verify', { email: $('otpEmail').value, code: $('otpCode').value });
      if (r.ok) { afterSignIn(); return; }
      $('otpCode').value = '';
      if (r.data.code === 'pending') showAuthOk(r.data.error); else showAuthError(authMessage(r));
    } finally { $('otpBtn').disabled = false; }
  };
  $('otpAgain').onclick = (e) => { e.preventDefault(); resetOtp(); mountCaptcha('capOtp', 'Login'); };
  $('resendLink').onclick = async (e) => {
    e.preventDefault();
    const r = await authCall('POST', 'resend', { email: resendFor || $('loginEmail').value, captcha: captchaToken('capLogin') });
    resetCaptcha('capLogin');
    if (r.ok) showAuthOk('If that address is waiting for confirmation, a new link is on its way.');
    else showAuthError(authMessage(r));
  };
  $('forgotForm').onsubmit = async (e) => {
    e.preventDefault();
    showAuthError('');
    if (!$('forgotEmail').value.trim()) { showAuthError('Enter your email address.'); return; }
    $('forgotBtn').disabled = true;
    const r = await authCall('POST', 'forgot', { email: $('forgotEmail').value, captcha: captchaToken('capForgot') });
    $('forgotBtn').disabled = false;
    resetCaptcha('capForgot');
    if (r.ok) showAuthOk('If that address has an account, a reset link is on its way. It works once and expires in an hour.');
    else showAuthError(authMessage(r));
  };
  $('resetForm').onsubmit = async (e) => {
    e.preventDefault();
    showAuthError('');
    const m = /^#\/reset\?token=([^&]+)/.exec(routeStr());
    if (!m || !$('resetPassword').value) { showAuthError('Enter a new password.'); return; }
    $('resetBtn').disabled = true;
    const r = await authCall('POST', 'reset', { token: decodeURIComponent(m[1]), password: $('resetPassword').value });
    $('resetBtn').disabled = false;
    if (!r.ok) { showAuthError(authMessage(r)); return; }
    history.replaceState(null, '', '#/login');
    $('resetPassword').value = '';
    // A reset signs the person out everywhere, this browser included.
    if (me) { me = null; document.body.classList.remove('is-admin', 'can-delete'); $('userMenu').hidden = true; }
    route();
    showAuthOk('Your password was changed. Sign in with it.');
  };
  $('gateCancel').onclick = () => $('gateDlg').close();
  ['gateSignIn', 'gateRegister'].forEach((id) => { $(id).addEventListener('click', () => $('gateDlg').close()); });
  $('tabRegister').onclick = () => setAuthTab('register');

  $('loginForm').onsubmit = async (e) => {
    e.preventDefault();
    showAuthError('');
    if (!$('loginEmail').value.trim() || !$('loginPassword').value) { showAuthError('Enter your email and password.'); return; }
    $('loginBtn').disabled = true;
    const r = await authCall('POST', 'login', { email: $('loginEmail').value, password: $('loginPassword').value, captcha: captchaToken('capLogin') });
    $('loginBtn').disabled = false;
    resetCaptcha('capLogin');
    if (r.ok) { afterSignIn(); return; }
    $('loginPassword').value = '';
    if (r.data.code === 'pending') showAuthOk(r.data.error);
    else if (r.data.code === 'unverified') { showAuthError(r.data.error); resendFor = $('loginEmail').value; $('resendRow').hidden = false; }
    else showAuthError(authMessage(r));
  };

  $('registerForm').onsubmit = async (e) => {
    e.preventDefault();
    showAuthError('');
    if (!$('regEmail').value.trim() || !$('regPassword').value) { showAuthError('Enter an email address and a password.'); return; }
    $('registerBtn').disabled = true;
    const r = await authCall('POST', 'register', { email: $('regEmail').value, name: $('regName').value,
      password: $('regPassword').value, setupToken: $('regToken').value, captcha: captchaToken('capRegister') });
    $('registerBtn').disabled = false;
    resetCaptcha('capRegister');
    if (!r.ok) { showAuthError(authMessage(r)); return; }
    if (r.data.status === 'active') { afterSignIn(); return; }       // the first admin, or no email to confirm with
    $('loginEmail').value = $('regEmail').value;
    $('regPassword').value = '';
    setAuthTab('signin');
    if (r.data.status === 'unverified') {
      resendFor = $('regEmail').value;
      showAuthOk('Check your email. We sent a link to ' + resendFor + ' to confirm your address. Open it to activate your account.');
      $('resendRow').hidden = false;
    } else {
      showAuthOk('Request received. An administrator has to approve your account before you can sign in.');
    }
  };

  // ---- user menu
  const drop = $('userDrop'), btn = $('userBtn');
  const closeMenu = () => { drop.hidden = true; btn.setAttribute('aria-expanded', 'false'); };
  btn.onclick = (e) => {
    e.stopPropagation();
    drop.hidden = !drop.hidden;
    btn.setAttribute('aria-expanded', String(!drop.hidden));
  };
  document.addEventListener('click', (e) => { if (!$('userMenu').contains(e.target)) closeMenu(); });
  document.addEventListener('keydown', (e) => { if (e.key === 'Escape') closeMenu(); });
  drop.addEventListener('click', closeMenu);
  $('menuSignOut').onclick = async () => {
    await authCall('POST', 'logout');
    store.set(SESSION_KEY, '');
    history.replaceState(null, '', toUrl(authCfg && authCfg.guest ? '#/jobs' : '#/login'));    // the homepage, when visitors are welcome
    location.reload();
  };
  wirePasswordDialog();
  wireAddUser();
  wireSsoDialog();
}

/* ---- Change / reset password ---- */

let pwTarget = null;

function openPasswordDialog(t) {
  pwTarget = t;
  $('pwTitle').textContent = t.self ? 'Change your password' : 'Reset password for ' + t.email;
  $('pwNote').textContent = t.self ? 'You will be signed out everywhere else.' : 'They will be signed out everywhere and must use the new password.';
  $('pwCurrentRow').hidden = !t.self;
  $('pwCurrent').value = ''; $('pwNew').value = '';
  $('pwErr').hidden = true;
  $('pwDlg').showModal();
  (t.self ? $('pwCurrent') : $('pwNew')).focus();
}

function wirePasswordDialog() {
  $('pwCancel').onclick = () => $('pwDlg').close();
  $('delCancel').onclick = () => $('delDlg').close();
  $('delForm').onsubmit = async (e) => {
    e.preventDefault();
    $('delErr').hidden = true;
    $('delOk').disabled = true;
    try {
      await api('/me', { method: 'DELETE', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ password: $('delPassword').value, confirm: $('delConfirm').value }) });
      store.set(SESSION_KEY, '');
      history.replaceState(null, '', toUrl('#/jobs'));
      location.reload();
    } catch (err) {
      $('delErr').textContent = err.message;
      $('delErr').hidden = false;
    } finally { $('delOk').disabled = false; }
  };
  $('pwForm').onsubmit = async (e) => {
    e.preventDefault();
    const fail = (m) => { $('pwErr').textContent = m; $('pwErr').hidden = false; };
    if (!$('pwNew').value) { fail('Enter the new password.'); return; }
    $('pwOk').disabled = true;
    let r;
    if (pwTarget.self) r = await authCall('POST', 'password', { current: $('pwCurrent').value, new: $('pwNew').value });
    else {
      const x = await fetch('api/admin/users/' + encodeURIComponent(pwTarget.id) + '/password', {
        method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Requested-With': 'blasta' },
        body: JSON.stringify({ password: $('pwNew').value }),
      });
      let data = {}; try { data = await x.json(); } catch (err) {}
      r = { ok: x.ok, status: x.status, data };
    }
    $('pwOk').disabled = false;
    if (!r.ok) { fail(authMessage(r)); return; }
    $('pwDlg').close();
    toast(pwTarget.self ? 'Password changed' : 'Password reset for ' + pwTarget.email, 'ok');
  };
}

/* ---- Administration ---- */

async function adminCall(method, path, body) {
  const r = await fetch('api/admin/' + path, {
    method, headers: { 'Content-Type': 'application/json', 'X-Requested-With': 'blasta' },
    body: body ? JSON.stringify(body) : undefined,
  });
  let data = {}; try { data = await r.json(); } catch (e) {}
  if (!r.ok) throw new Error(data.error || r.statusText);
  return data;
}

async function refreshPending() {
  try {
    const { users } = await adminCall('GET', 'users');
    const n = users.filter((u) => u.status === 'pending').length;
    $('menuPending').textContent = n ? '(' + n + ' waiting)' : '';
    if ($('sidePending')) $('sidePending').textContent = n ? n : '';
    $('userBtn').classList.toggle('has-pending', n > 0);
    return users;
  } catch (e) { return []; }
}

const fmtWhen = (iso) => (!iso || iso.startsWith('0001') ? 'never' : new Date(iso).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' }));

/* ---- My account ---- */

const THEMES = [['light', 'Light'], ['dark', 'Dark'], ['system', 'Match my device']];
const currentThemeChoice = () => { const t = store.get('blasta.theme'); return t === 'light' || t === 'dark' ? t : 'system'; };

function chooseTheme(c) {
  if (c === 'system') {
    store.set('blasta.theme', '');
    const light = window.matchMedia && matchMedia('(prefers-color-scheme: light)').matches;
    document.documentElement.setAttribute('data-theme', light ? 'light' : 'dark');
    if (lastSnap) render(lastSnap);
  } else applyTheme(c);
}

// Shrink a chosen picture to a small square JPEG in the browser, so only a few KB are stored.
function pictureFromFile(file) {
  return new Promise((resolve, reject) => {
    if (!/^image\/(png|jpeg|webp|gif)$/.test(file.type)) { reject(new Error('Choose a PNG, JPEG or WebP picture.')); return; }
    const fr = new FileReader();
    fr.onerror = () => reject(new Error('Could not read that file.'));
    fr.onload = () => {
      const img = new Image();
      img.onerror = () => reject(new Error('Could not open that picture.'));
      img.onload = () => {
        const n = 160, side = Math.min(img.width, img.height), c = document.createElement('canvas');
        c.width = c.height = n;
        const g = c.getContext('2d');
        // JPEG has no transparency: without a background, the transparent edges of a PNG turn black.
        g.fillStyle = '#e5e7eb';
        g.fillRect(0, 0, n, n);
        g.drawImage(img, (img.width - side) / 2, (img.height - side) / 2, side, side, 0, 0, n, n);
        resolve(c.toDataURL('image/jpeg', 0.85));
      };
      img.src = fr.result;
    };
    fr.readAsDataURL(file);
  });
}

async function loadAccount() {
  if (!me) return;
  const box = $('accountBody');
  const sso = !me.hasPassword;
  const theme = currentThemeChoice();
  const tile = (key, label, hint) =>
    '<button type="button" class="theme-card" role="radio" data-theme-choice="' + key + '" aria-checked="' + (key === theme) + '">' +
    '<span class="tp tp-' + key + '" aria-hidden="true"><i></i><b></b><b></b><u></u></span>' +
    '<span class="theme-card-text"><strong>' + label + '</strong><small>' + hint + '</small></span></button>';
  box.innerHTML =
    '<div class="settings-page-header"><div><span class="settings-breadcrumb">Workspace / My Account</span><h2>Your workspace, your way.</h2>' +
    '<p class="modal-subtitle">Manage your profile, your password and how BLASTA looks for you.</p></div></div>' +
    '<div class="account-grid">' +

    '<div class="settings-subcard account-card"><div class="account-hero"><div class="account-avatar-wrap"><div class="avatar-lg" id="acAvatar"></div>' +
    '<button type="button" class="avatar-edit" id="acPhotoBtn" aria-label="Change photo" title="Change photo">' +
    '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 8h3l2-3h6l2 3h3v11H4z"/><circle cx="12" cy="13" r="3.5"/></svg></button>' +
    '<input type="file" id="acPhoto" accept="image/png,image/jpeg,image/webp,image/gif" hidden></div>' +
    '<div class="account-who"><div class="account-who-name">' + esc(me.name) + '</div><div class="account-who-mail">' + esc(me.email) + '</div>' +
    '<div class="account-who-tags"><span class="role-pill ' + esc(me.role) + '">' + (me.role === 'admin' ? 'Administrator' : 'User') + '</span>' +
    (me.avatar ? '<button type="button" class="link-btn" id="acPhotoRemove">Remove photo</button>' : '') + '</div></div></div>' +

    '<form id="acProfile" class="modal-form"><h3>Personal information</h3>' +
    '<div class="account-form-row">' + fld('acName', 'Full name', me.name, { ph: 'Your name' }) +
    fld('acEmail', 'Email address', me.email, { type: 'email', help: sso ? 'Your address comes from your single sign-on provider. Set a password below to be able to change it here.' : '' }) + '</div>' +
    '<div id="acPwRow" hidden>' + fld('acEmailPw', 'Current password', '', { type: 'password', help: 'Needed to change your email address.' }) + '</div>' +
    '<p class="set-result" id="r-acProfile" role="status" hidden></p>' +
    '<div class="modal-form-actions"><button type="submit" class="btn primary-sm">Save changes</button></div></form>' +

    '<hr class="account-divider"><form id="acPassword" class="modal-form"><h3>Password</h3>' +
      (me.hasPassword ? '' : '<p class="verify-note">You sign in with single sign-on, so this account has no password yet and a password cannot be used to sign in. ' +
        'Set one here to also be able to sign in with your email address and password.</p>') +
      '<div class="account-form-row">' + (me.hasPassword ? fld('acCur', 'Current password', '', { type: 'password' }) : '') +
      fld('acNew', me.hasPassword ? 'New password' : 'Choose a password', '', { type: 'password', help: 'At least 10 characters. A short sentence works well.' }) + '</div>' +
      '<p class="set-result" id="r-acPassword" role="status" hidden></p>' +
      '<div class="modal-form-actions"><button type="submit" class="btn' + (me.hasPassword ? '' : ' primary-sm') + '">' + (me.hasPassword ? 'Change password' : 'Set password') + '</button></div></form>' +
    '</div>' +

    '<div class="settings-subcard account-card"><h3>Appearance</h3><p class="modal-subtitle">Choose how BLASTA looks for you.</p>' +
    '<div class="theme-cards" role="radiogroup" aria-label="Theme">' +
    tile('light', 'Light', 'Bright and warm') + tile('dark', 'Dark', 'Easy on the eyes') + tile('system', 'Match my device', 'Follows your system') +
    '</div></div>' +
    '<div class="settings-subcard account-card"><h3>Your data</h3><p class="modal-subtitle">See what is held about you, or remove it. <a href="privacy">Privacy and cookies</a></p>' +
    '<div class="modal-form-actions"><a class="btn small" href="api/me/export" download="blasta-my-data.json">Download my data</a>' +
    (authCfg && authCfg.selfDelete ? '<button type="button" class="btn danger inline" id="acDelete">Delete my account</button>' : '') + '</div></div>' +
    '</div>';
  paintAvatar($('acAvatar'), me);
  if ($('acDelete')) $('acDelete').onclick = () => {
    $('delPasswordRow').hidden = !me.hasPassword;
    $('delConfirmRow').hidden = me.hasPassword;
    $('delPassword').value = $('delConfirm').value = '';
    $('delErr').hidden = true;
    $('delDlg').showModal();
  };

  const say = (id) => (msg, ok) => { const o = $(id); o.hidden = false; o.className = 'set-result ' + (ok ? 'ok' : 'bad'); o.textContent = msg; };
  const emailInput = $('acEmail');
  emailInput.readOnly = sso;
  emailInput.oninput = () => { $('acPwRow').hidden = sso || emailInput.value.trim().toLowerCase() === me.email.toLowerCase(); };

  $('acProfile').onsubmit = async (e) => {
    e.preventDefault();
    const out = say('r-acProfile');
    const msgs = [];
    try {
      if ($('acName').value.trim() !== me.name) {
        const r = await authCall('POST', 'profile', { name: $('acName').value });
        if (!r.ok) throw new Error(r.data.error || 'Could not save your name.');
        me = Object.assign(me, r.data);
        showUser();
        msgs.push('Name saved.');
      }
      if (!sso && emailInput.value.trim().toLowerCase() !== me.email.toLowerCase()) {
        const r = await authCall('POST', 'email', { email: emailInput.value, currentPassword: $('acEmailPw').value });
        if (!r.ok) throw new Error(r.data.error || 'Could not change your email address.');
        if (r.data.status === 'changed') { me.email = r.data.email; showUser(); msgs.push('Email address changed.'); }
        else msgs.push('We sent a link to ' + r.data.email + '. Open it to finish changing your address.');
        $('acEmailPw').value = '';
        if (r.data.status !== 'changed') emailInput.value = me.email;
        $('acPwRow').hidden = true;
      }
      if (msgs.length) { loadAccount().then(() => say('r-acProfile')(msgs.join(' '), true)); return; }
      out('Nothing to save.', true);
    } catch (err) { out(err.message, false); }
  };
  if ($('acPassword')) $('acPassword').onsubmit = async (e) => {
    e.preventDefault();
    const out = say('r-acPassword');
    if (!$('acNew').value) { out('Enter the new password.', false); return; }
    const first = !me.hasPassword;
    const r = await authCall('POST', 'password', { current: $('acCur') ? $('acCur').value : '', new: $('acNew').value });
    if (!r.ok) { out(authMessage(r), false); return; }
    if (first) {
      me.hasPassword = true; showUser();
      loadAccount().then(() => say('r-acPassword')('Password set. You can now sign in with your email address and password as well as with single sign-on.', true));
      return;
    }
    $('acCur').value = $('acNew').value = '';
    out('Password changed. Your other devices were signed out.', true);
  };
  $('acPhotoBtn').onclick = () => $('acPhoto').click();
  $('acAvatar').onclick = () => $('acPhoto').click();
  $('acPhoto').onchange = async () => {
    const f = $('acPhoto').files[0];
    if (!f) return;
    try {
      const img = await pictureFromFile(f);
      const r = await authCall('POST', 'avatar', { image: img });
      if (!r.ok) throw new Error(r.data.error || 'Could not save the picture.');
      me.avatar = r.data.avatar; showUser(); toast('Profile photo updated', 'ok'); loadAccount();
    } catch (err) { toast(err.message, 'error'); }
  };
  if ($('acPhotoRemove')) $('acPhotoRemove').onclick = async () => {
    const r = await authCall('POST', 'avatar', { image: '' });
    if (r.ok) { me.avatar = ''; showUser(); loadAccount(); }
  };
  document.querySelectorAll('[data-theme-choice]').forEach((b) => {
    b.onclick = () => {
      chooseTheme(b.dataset.themeChoice);
      document.querySelectorAll('[data-theme-choice]').forEach((x) => x.setAttribute('aria-checked', String(x === b)));
    };
  });
}

/* ---- Administration: a sidebar of sections, one page each ---- */

const ADMIN_SECTIONS = ['general', 'users', 'trial', 'sso', 'smtp', 'bots', 'analytics', 'privacy', 'notifications'];
const num = (id) => parseInt($(id).value, 10) || 0;
const list = (a) => (a || []).join(', ');
const unlist = (v) => String(v || '').split(/[,\n]/).map((x) => x.trim()).filter(Boolean);

function showAdmin(section) {
  if (section === 'settings') section = 'general';             // the old address
  if (!ADMIN_SECTIONS.includes(section)) section = 'general';       // the Settings menu lands on General Settings
  document.querySelectorAll('.admin-side a').forEach((a) => a.classList.toggle('active', a.dataset.section === section));
  const render = { general: renderGeneral, users: renderUsers, trial: renderTrialSettings, sso: renderSso, smtp: renderSmtp, bots: renderBots, analytics: renderAnalytics, privacy: renderPrivacy, notifications: renderNotifications }[section];
  render().catch((e) => { $('adminSection').innerHTML = '<p class="form-error">' + esc(e.message) + '</p>'; });
}

const pageHead = (crumb, title, sub, actions) =>
  '<div class="settings-page-header"><div><span class="settings-breadcrumb">Admin Workspace / ' + crumb + '</span><h2>' + title +
  '</h2><p class="modal-subtitle">' + sub + '</p></div><div class="settings-page-header-actions">' + (actions || '') + '</div></div>';
const saveBtn = (form, label) => '<button type="submit" form="' + form + '" class="btn primary-sm">' + (label || 'Save Changes') + '</button>';
const pill = (on, onText, offText) => '<span class="settings-status-pill ' + (on ? 'on' : 'off') + '">' + (on ? onText : offText) + '</span>';
const subcard = (title, sub, status, body) =>
  '<div class="settings-subcard"><div class="settings-subcard-header"><div><h3>' + title + '</h3>' + (sub ? '<p class="modal-subtitle">' + sub + '</p>' : '') +
  '</div>' + (status || '') + '</div>' + body + '</div>';
const fld = (id, label, val, o = {}) =>
  '<label for="' + id + '">' + label + (o.hint ? ' <span class="hint">' + o.hint + '</span>' : '') +
  '<input id="' + id + '" type="' + (o.type || 'text') + '" value="' + esc(val == null ? '' : val) + '"' +
  (o.ph ? ' placeholder="' + esc(o.ph) + '"' : '') + (o.min != null ? ' min="' + o.min + '"' : '') +
  (o.type === 'password' ? ' autocomplete="new-password"' : ' autocomplete="off"') + ' spellcheck="false">' +
  (o.help ? '<span class="help">' + o.help + '</span>' : '') + '</label>';
const toggle = (id, label, on, hint) =>
  '<label class="toggle-row"><input id="' + id + '" type="checkbox"' + (on ? ' checked' : '') + '><span>' + label +
  (hint ? ' <span class="hint">' + hint + '</span>' : '') + '</span></label>';
const sel = (id, label, val, opts, help) =>
  '<label for="' + id + '">' + label + '<select id="' + id + '">' +
  opts.map((o) => '<option value="' + o[0] + '"' + (o[0] === val ? ' selected' : '') + '>' + esc(o[1]) + '</option>').join('') +
  '</select>' + (help ? '<span class="help">' + help + '</span>' : '') + '</label>';
const resultBox = (id) => '<p class="set-result" id="' + id + '" role="status" hidden></p>';
const sayIn = (id) => (msg, ok) => { const o = $(id); if (!o) return; o.hidden = false; o.className = 'set-result ' + (ok ? 'ok' : 'bad'); o.textContent = msg; };

const getSettings = () => adminCall('GET', 'settings');

// Wire a section's form: save, and an optional "use defaults" reset.
function wireSection(name, formId, body, again, prev) {
  $(formId).onsubmit = async (e) => {
    e.preventDefault();
    try {
      const sent = body();
      await adminCall('POST', 'settings/' + name, sent);
      announceSaved(name, prev, sent);
      await refreshPublicConfig();
      again();
    } catch (err) { toast(err.message, 'error'); }
  };
  const reset = $(formId + '-reset');
  if (reset) reset.onclick = async () => {
    if (!confirm('Forget the saved settings and use the defaults from the environment?')) return;
    try {
      await adminCall('DELETE', 'settings/' + name);
      toast(SETTINGS_TITLE[name] + ' reset to defaults', 'ok');
      await refreshPublicConfig(); again();
    }
    catch (err) { toast(err.message, 'error'); }
  };
}
const resetBtn = (saved, form) => saved ? '<button type="button" class="btn small" id="' + form + '-reset">Reset to Default</button>' : '';

async function renderGeneral() {
  const d = await getSettings(), g = d.general;
  const on = g.registration !== 'closed';
  const mail = !!(d.smtp && d.smtp.enabled && d.smtp.host);
  $('adminSection').innerHTML = pageHead('General', 'General Settings',
    'How people get an account, and the address they reach BLASTA at. Settings saved here take priority over environment variables.', saveBtn('f-general')) +
    subcard('Registration', mail
        ? 'Email is set up, so new people confirm their email address before their account is activated.'
        : 'Email is not set up, so new accounts are activated at once. Set up Email Delivery to make people confirm their address.',
      pill(on, 'Open', 'Disabled'),
      '<form id="f-general" class="modal-form">' +
      toggle('s-regOn', 'Allow people to create an account', on, 'Turn off to stop new sign-ups. SSO and accounts you create yourself still work.') +
      '<div id="s-regMore">' +
      toggle('s-regApproval', 'Administrators must approve new accounts', g.registration === 'approval', mail ? 'After they confirm their email address.' : '') +
      fld('s-domains', 'Allowed email domains', list(g.allowedDomains), { ph: 'example.com, example.org', hint: 'optional', help: 'Leave empty to allow any address. Applies to registration and to single sign-on.' }) +
      '</div>' +
      '<div class="modal-form-actions">' + resetBtn(d.saved.general, 'f-general') + '</div></form>') +
    subcard('Sign-in options', mail ? 'Ways people can sign in or recover access, using email.' : 'These need email: set up Email Delivery first.', '',
      '<div class="modal-form">' +
      toggle('s-otp', 'Let people sign in with a one-time code', !!g.otpLogin && mail, mail ? 'They enter their email address and type the six-digit code we send them. No password needed.' : 'Needs Email Delivery.') +
      toggle('s-reset', 'Let people reset a forgotten password', !g.disableReset && mail, mail ? 'The sign-in page offers \u201cForgot your password?\u201d. We email a one-time link to choose a new password.' : 'Needs Email Delivery.') +
      '</div>') +
    subcard('Address', 'Where people reach BLASTA from outside.', '',
      '<div class="modal-form">' +
      fld('s-publicUrl', 'Public URL', g.publicUrl, { ph: 'https://blasta.example.com', help: 'The address people type to reach BLASTA from outside, for example the one a reverse proxy (Pangolin, Traefik, nginx) publishes. It is used for the single sign-on redirect address, for links in emails and to decide whether cookies are HTTPS-only. Include a path if BLASTA lives under one, such as https://example.com/blasta. Leave it empty to use the address of each request.' }) +
      '</div>');
  $('s-otp').disabled = $('s-reset').disabled = !mail;
  const sync = () => { $('s-regMore').hidden = !$('s-regOn').checked; };
  $('s-regOn').onchange = sync;
  sync();
  // One save for both cards: the address field sits outside the form, so submit it with the form.
  wireSection('general', 'f-general', () => ({
    publicUrl: $('s-publicUrl').value.trim(),
    registration: !$('s-regOn').checked ? 'closed' : $('s-regApproval').checked ? 'approval' : 'open',
    allowedDomains: unlist($('s-domains').value), otpLogin: $('s-otp').checked, disableReset: mail ? !$('s-reset').checked : g.disableReset }), renderGeneral, g);
}

async function renderTrialSettings() {
  const d = await getSettings(), t = d.guest;
  $('adminSection').innerHTML = pageHead('Free Trial', 'Free Trial',
    'Someone who has not registered lands on the Jobs page and can run a few small web tests for a short while. Templates, history and everything else ask them to sign in or register.', saveBtn('f-trial')) +
    subcard('Visitor limits', 'Trial tests are never saved and can only reach public addresses.', pill(t.enabled, 'Enabled', 'Off'),
      '<form id="f-trial" class="modal-form">' +
      toggle('s-guestOn', 'Let visitors try BLASTA without an account', t.enabled) +
      '<div class="account-form-row">' +
      fld('s-gMinutes', 'Trial length', t.trialMinutes, { type: 'number', min: 1, hint: 'minutes' }) +
      fld('s-gRuns', 'Tests per trial', t.maxRuns, { type: 'number', min: 1 }) +
      fld('s-gDur', 'Longest test', t.maxDurationSec, { type: 'number', min: 1, hint: 'seconds' }) +
      fld('s-gRps', 'Requests per second', t.maxRps, { type: 'number', min: 1, hint: 'at most' }) +
      fld('s-gConc', 'Parallel connections', t.maxConcurrency, { type: 'number', min: 1, hint: 'at most' }) +
      fld('s-gIp', 'Tests per network per day', t.dailyPerIp, { type: 'number', min: 1, help: 'A backstop for visitors who clear their cookies to get a new trial.' }) +
      '</div><div class="modal-form-actions">' + resetBtn(d.saved.guest, 'f-trial') + '</div></form>');
  wireSection('guest', 'f-trial', () => ({ enabled: $('s-guestOn').checked, trialMinutes: num('s-gMinutes'), maxRuns: num('s-gRuns'),
    maxDurationSec: num('s-gDur'), maxRps: num('s-gRps'), maxConcurrency: num('s-gConc'), dailyPerIp: num('s-gIp') }), renderTrialSettings, t);
}

async function renderSso() {
  const d = await getSettings(), o = d.sso;
  const configured = !!(o.issuer || o.clientId);
  const row = configured
    ? '<div class="sso-provider-row"><div class="sso-provider-avatar">' + esc(initial(o.name || o.issuer)) + '</div>' +
      '<div class="sso-provider-info"><div class="sso-provider-name">' + esc(o.name || 'Single sign-on') + '</div>' +
      '<div class="sso-provider-meta">OpenID Connect &middot; ' + esc(o.issuer) + ' &middot; ' + (d.saved.sso ? 'saved in the database' : 'from environment variables') + '</div></div>' +
      pill(o.enabled, 'Enabled', 'Disabled') +
      '<div class="sso-provider-actions"><button type="button" class="btn small" id="sso-check">Check connection</button>' +
      '<button type="button" class="btn small" id="sso-edit">Edit</button>' +
      '<button type="button" class="row-remove-btn" id="sso-remove" title="Remove this provider" aria-label="Remove this provider">&times;</button></div></div>' +
      resultBox('r-sso-list')
    : '<p class="field-hint">No identity providers configured yet. Add one so people can sign in with your company account.</p>';
  $('adminSection').innerHTML = pageHead('Identity', 'Single Sign-On',
    'Configure a trusted OpenID Connect identity provider: Keycloak, Okta, Microsoft Entra ID, Google, Auth0 and others. Settings saved here take priority over environment variables.', '') +
    '<div class="settings-subcard"><div class="settings-subcard-header"><div><h3>Identity providers</h3><p class="modal-subtitle">People can sign in through any enabled provider.</p></div>' +
    (configured ? '' : '<button type="button" id="sso-add" class="btn primary-sm">+ Add provider</button>') + '</div>' +
    '<div class="sso-provider-list">' + row + '</div></div>';

  if ($('sso-add')) $('sso-add').onclick = () => openSsoDialog(o, false, d.redirectUrl);
  if ($('sso-edit')) $('sso-edit').onclick = () => openSsoDialog(o, true, d.redirectUrl);
  if ($('sso-check')) $('sso-check').onclick = async () => {
    const say = sayIn('r-sso-list');
    say('Contacting the provider…', true);
    try {
      const r = await adminCall('POST', 'settings/sso/test', ssoBodyFrom(o));
      say('The provider answered. Issuer: ' + r.issuer + '. Sign-in endpoint found. (The client ID and secret are only proven by a real sign-in.)', true);
    } catch (err) { say(err.message, false); }
  };
  if ($('sso-remove')) $('sso-remove').onclick = async () => {
    if (!confirm('Remove this identity provider? Nobody will be able to sign in with it, and its client secret is deleted. Existing accounts stay.')) return;
    try {
      await adminCall('POST', 'settings/sso', { enabled: false, clearSecret: true });
      toast('Single sign-on provider removed', 'ok');
      await refreshPublicConfig();
      renderSso();
    } catch (err) { toast(err.message, 'error'); }
  };
}

// The provider as the API wants it, from what is already saved (no secret: the server keeps it).
const ssoBodyFrom = (o) => ({ enabled: o.enabled, name: o.name, issuer: o.issuer, clientId: o.clientId, scopes: o.scopes, autoCreate: o.autoCreate,
  trust: o.trust, adminEmails: o.adminEmails, adminGroup: o.adminGroup, groupsClaim: o.groupsClaim, discoveryUrl: o.discoveryUrl, allowInsecure: o.allowInsecure });

let ssoBefore = null;
function openSsoDialog(o, editing, redirectUrl) {
  ssoBefore = editing ? o : null;
  $('ssoDlgTitle').textContent = editing ? 'Edit identity provider' : 'Add identity provider';
  $('ssoFields').innerHTML =
    '<div class="redirect-box"><div class="redirect-title">Redirect address</div>' +
    '<p class="help">Register BLASTA at your identity provider with exactly this address. It comes from the Public URL in General Settings.</p>' +
    '<div class="copy-row"><code id="s-redirect">' + esc(redirectUrl) + '</code><button class="btn small" type="button" id="s-copy">Copy</button></div></div>' +
    '<label for="s-ptype">Provider type<select id="s-ptype" disabled><option>OpenID Connect (OIDC)</option></select>' +
    '<span class="help">Works with any OpenID Connect provider. SAML-only providers usually offer OIDC as well.</span></label>' +
    toggle('s-ssoOn', 'Enable this provider', editing ? o.enabled : true) +
    '<div class="account-form-row">' +
    fld('s-ssoName', 'Provider name', o.name, { ph: 'Acme SSO', help: 'Shown on the sign-in button as “Sign in with …”.' }) +
    fld('s-issuer', 'Issuer URL', o.issuer, { ph: 'https://login.example.com/realms/main', help: 'The provider’s issuer, exactly as it reports it.' }) +
    fld('s-clientId', 'Client ID', o.clientId) +
    fld('s-clientSecret', 'Client secret', '', { type: 'password', ph: o.clientSecretSet ? 'Leave blank to keep the current secret' : 'Leave blank for a public client', help: 'Stored encrypted. It is never shown again.' }) +
    sel('s-trust', 'New accounts from SSO are', o.trust || 'active', [['active', 'Active immediately'], ['pending', 'Waiting for approval']]) +
    fld('s-adminGroup', 'Administrator group', o.adminGroup, { hint: 'optional', ph: 'blasta-admins' }) +
    '</div>' +
    fld('s-adminEmails', 'Administrator emails', list(o.adminEmails), { hint: 'optional', ph: 'boss@example.com', help: 'These people become administrators when they sign in (their email must be verified by the provider).' }) +
    toggle('s-autoCreate', 'Create an account the first time someone signs in', o.autoCreate !== false) +
    '<details class="adv"><summary>Advanced</summary><div class="account-form-row">' +
    fld('s-scopes', 'Scopes', o.scopes || 'openid email profile') +
    fld('s-groupsClaim', 'Groups claim', o.groupsClaim, { ph: 'groups' }) + '</div>' +
    fld('s-discovery', 'Discovery URL', o.discoveryUrl, { hint: 'optional', help: 'Only if BLASTA must reach the provider at a different address than users do (for example inside Docker).' }) +
    toggle('s-insecure', 'Allow plain http', o.allowInsecure, 'development only') + '</details>';
  $('s-copy').onclick = async () => {
    try { await navigator.clipboard.writeText($('s-redirect').textContent); toast('Copied', 'ok'); } catch (err) { toast('Select the address and copy it', 'error'); }
  };
  $('r-sso').hidden = true;
  $('ssoDlg').showModal();
  $('s-ssoName').focus();
}

function ssoBody() {
  return { enabled: $('s-ssoOn').checked, name: $('s-ssoName').value.trim(), issuer: $('s-issuer').value.trim(), clientId: $('s-clientId').value.trim(),
    clientSecret: $('s-clientSecret').value, scopes: $('s-scopes').value.trim(), autoCreate: $('s-autoCreate').checked, trust: $('s-trust').value,
    adminEmails: unlist($('s-adminEmails').value), adminGroup: $('s-adminGroup').value.trim(), groupsClaim: $('s-groupsClaim').value.trim(),
    discoveryUrl: $('s-discovery').value.trim(), allowInsecure: $('s-insecure').checked };
}

function wireSsoDialog() {
  $('ssoCancel').onclick = () => $('ssoDlg').close();
  $('s-test').onclick = async () => {
    const say = sayIn('r-sso');
    say('Contacting the provider…', true);
    try {
      const r = await adminCall('POST', 'settings/sso/test', ssoBody());
      say('The provider answered. Issuer: ' + r.issuer + '. Sign-in endpoint found. (The client ID and secret are only proven by a real sign-in.)', true);
    } catch (err) { say(err.message, false); }
  };
  $('ssoForm').onsubmit = async (e) => {
    e.preventDefault();
    $('ssoSave').disabled = true;
    try {
      const sent = ssoBody();
      await adminCall('POST', 'settings/sso', sent);
      $('ssoDlg').close();
      toast(ssoBefore && (ssoBefore.issuer || ssoBefore.clientId) ? 'Single sign-on provider updated' : 'Single sign-on provider added', 'ok');
      await refreshPublicConfig();
      renderSso();
    } catch (err) { sayIn('r-sso')(err.message, false); }
    $('ssoSave').disabled = false;
  };
}

const CAPTCHA_PROVIDERS = {
  turnstile: { label: 'Cloudflare Turnstile', ico: 'CF', desc: 'Free, privacy-friendly, usually invisible to real people.', docs: 'dash.cloudflare.com → Turnstile' },
  recaptcha: { label: 'Google reCAPTCHA', ico: 'G', desc: 'The familiar "I am not a robot" checkbox (v2).', docs: 'google.com/recaptcha/admin' },
};

async function renderBots(staged) {
  const d = await getSettings(), c = d.captcha;
  const provider = staged || c.provider || 'turnstile';
  const P = CAPTCHA_PROVIDERS[provider];
  const keep = c.provider === provider;      // the saved keys belong to the saved provider
  $('adminSection').innerHTML = pageHead('Bots', 'Bot Protection',
    'Keep automated traffic away from sign-in, registration and the free trial by asking visitors to pass a quick check.', saveBtn('f-bots')) +
    '<form id="f-bots" class="modal-form">' +
    subcard('Provider', 'Pick the service that runs the check. Your keys are tied to it.', c.enabled ? pill(true, 'Protecting', '') : pill(false, '', 'Off'),
      '<div class="captcha-provider-picker" role="radiogroup" aria-label="Provider">' +
      Object.entries(CAPTCHA_PROVIDERS).map(([k, p2]) =>
        '<label class="captcha-tile' + (k === provider ? ' active' : '') + '"><span class="captcha-ico" aria-hidden="true">' + p2.ico + '</span>' +
        '<span><strong>' + p2.label + '</strong><small>' + p2.desc + '</small></span>' +
        '<input type="radio" name="botProvider" value="' + k + '"' + (k === provider ? ' checked' : '') + '></label>').join('') + '</div>') +
    subcard(P.label + ' keys', 'Get a site key and a secret key at ' + P.docs + ', and allow this site’s address for them.', '',
      fld('b-site', 'Site key', keep ? c.siteKey : '', { help: 'Public: it is shown on the sign-in page.' }) +
      fld('b-secret', 'Secret key', '', { type: 'password', ph: keep && c.secretSet ? 'Leave blank to keep the current secret key' : '', help: 'Stored encrypted. It is never shown again.' }) +
      '<div class="modal-form-actions"><button type="button" class="btn small" id="b-verify">Test secret key</button></div>' + resultBox('r-bots')) +
    subcard('Protection', c.envOff ? 'The BLASTA_CAPTCHA_OFF environment variable is set, so the check is off whatever is saved here.' : 'Turn it on, then choose where visitors are asked.', '',
      toggle('b-on', 'Enable bot protection', c.enabled && keep, 'Only turns on if the provider accepts your secret key.') +
      '<div id="b-scopes" class="scope-list">' +
      toggle('b-login', 'On sign-in and password reset', c.enabled ? c.onLogin : true, 'Stops automated password guessing.') +
      toggle('b-register', 'On registration', c.enabled ? c.onRegister : true, 'Stops bots creating fake accounts.') +
      toggle('b-guest', 'Before a visitor’s first free test', c.enabled ? c.onGuest : true, 'Stops automated use of the free trial.') +
      '</div>') +
    '</form>';

  document.querySelectorAll('input[name=botProvider]').forEach((r) => { r.onchange = () => renderBots(r.value); });
  const sync = () => { $('b-scopes').hidden = !$('b-on').checked; };
  $('b-on').onchange = sync;
  sync();
  const body = () => ({ enabled: $('b-on').checked, provider, siteKey: $('b-site').value.trim(), secretKey: $('b-secret').value,
    onLogin: $('b-login').checked, onRegister: $('b-register').checked, onGuest: $('b-guest').checked });
  $('b-verify').onclick = async () => {
    const say = sayIn('r-bots');
    say('Asking ' + P.label + '…', true);
    try { const r = await adminCall('POST', 'settings/captcha/verify', body()); say(r.status, true); }
    catch (err) { say(err.message, false); }
  };
  $('f-bots').onsubmit = async (e) => {
    e.preventDefault();
    try {
      const sent = body();
      await adminCall('POST', 'settings/captcha', sent);
      announceSaved('captcha', keep ? c : { provider: c.provider }, sent);
      await refreshPublicConfig();
      renderBots();
    } catch (err) { sayIn('r-bots')(err.message, false); toast(err.message, 'error'); }
  };
}

async function renderAnalytics(staged) {
  const d = await getSettings(), a = d.analytics, provs = d.analyticsProviders || [];
  const provider = staged || a.provider || 'ga4';
  const P = provs.find((p) => p.id === provider) || provs[0] || {};
  const keep = a.provider === provider;        // what is saved belongs to the saved provider
  $('adminSection').innerHTML = pageHead('Analytics', 'Analytics',
    'Count visits to this site with your own analytics service. BLASTA loads it only while it is switched on, and lets through only the addresses that service needs.', saveBtn('f-analytics')) +
    '<form id="f-analytics" class="modal-form">' +
    subcard('Service', 'Pick one service. Nothing is loaded until you turn it on.', pill(a.enabled && a.provider, 'On', 'Off'),
      toggle('a-on', 'Load analytics on this site', a.enabled, 'Turn it off to stop all tracking at once. What you entered stays saved.') +
      sel('a-provider', 'Provider', provider, provs.map((p) => [p.id, p.name])) +
      (P.idLabel ? fld('a-id', P.idLabel, keep ? a.id : '') : '') +
      (P.urlHelp ? fld('a-url', P.urlHelp, keep ? a.scriptUrl : '') : '') +
      '<div class="modal-form-actions">' + resetBtn(d.saved.analytics, 'f-analytics') + '</div>') +
    subcard('Privacy', 'Who is counted.', '',
      toggle('a-dnt', 'Respect Do Not Track', a.respectDnt, 'Stays off for people whose browser sends Do Not Track or Global Privacy Control.') +
      toggle('a-signed', 'Also count people who are signed in', a.trackSignedIn, 'Off: only visitors are counted, never the people using BLASTA. Pages with a one-time token in the address (password reset, email confirmation) are never counted.')) +
    subcard('Other addresses', 'Only if your service talks to more hosts than it needs by default (for example tags loaded through Google Tag Manager).', '',
      fld('a-extra', 'Extra allowed hosts', list(a.extraHosts), { ph: 'cdn.example.com, https://*.example.com', hint: 'optional', help: 'Separate with commas. Each is allowed to load scripts and receive data from this site.' })) +
    '</form>';
  $('a-provider').onchange = () => renderAnalytics($('a-provider').value);
  const val = (id) => ($(id) ? $(id).value.trim() : '');
  const body = () => ({ enabled: $('a-on').checked, provider, id: val('a-id'), scriptUrl: val('a-url'),
    extraHosts: val('a-extra').split(',').map((x) => x.trim()).filter(Boolean), respectDnt: $('a-dnt').checked, trackSignedIn: $('a-signed').checked });
  wireSection('analytics', 'f-analytics', body, renderAnalytics, a);
}

async function renderPrivacy() {
  const d = await getSettings(), p = d.privacy, modes = d.privacyModes || [], a = d.analytics;
  const mode = modes.find((m) => m.id === p.mode) || modes[0] || {};
  const tracking = a.enabled && a.provider;
  const ta = (id, label, val, rows, help) => '<label for="' + id + '">' + label + '<textarea id="' + id + '" rows="' + rows + '" spellcheck="true">' + esc(val || '') + '</textarea>' +
    (help ? '<span class="help">' + help + '</span>' : '') + '</label>';
  $('adminSection').innerHTML = pageHead('Privacy', 'Privacy & Cookies',
    'Tell people what this site stores, and let them decide about optional cookies. BLASTA\u2019s own cookies are all essential; consent matters when analytics is on.', saveBtn('f-privacy')) +
    '<form id="f-privacy" class="modal-form">' +
    subcard('Cookie consent', 'How visitors are asked about optional cookies (analytics).', tracking ? pill(true, 'Analytics is on', '') : pill(false, '', 'No optional cookies'),
      sel('p-mode', 'When someone visits', p.mode, modes.map((m) => [m.id, m.name]), esc(mode.help || '')) +
      (tracking && (p.mode === 'off' || p.mode === 'notice') ? '<p class="set-result bad">Analytics is on, but visitors are not asked. In the EU, the UK and other places that is not enough: choose \u201cAsk first\u201d unless you are sure.</p>' : '') +
      ta('p-message', 'Banner text', p.message, 3, 'Optional. Leave empty to use BLASTA\u2019s wording, which names the analytics service.')) +
    subcard('Privacy page', 'BLASTA makes a page at /privacy from these settings and what is switched on.', '',
      fld('p-controller', 'Who runs this site', p.controller, { ph: 'Example Ltd', hint: 'optional' }) +
      fld('p-contact', 'Contact for privacy requests', p.contact, { ph: 'privacy@example.com', hint: 'optional' }) +
      fld('p-policy', 'Your own privacy policy', p.policyUrl, { ph: 'https://example.com/privacy', hint: 'optional', help: 'If set, the banner links to it instead of BLASTA\u2019s page.' }) +
      ta('p-notes', 'More for the privacy page', p.notes, 5, 'Optional. Separate paragraphs with a blank line.') +
      '<p class="help"><a href="privacy" target="_blank" rel="noopener">View the privacy page</a></p>') +
    subcard('People\u2019s rights', 'Everyone can download their data from My account.', '',
      toggle('p-selfdelete', 'Let people delete their own account and test history', !p.noSelfDelete, 'Most data protection laws expect this. Turn it off only if you must keep records; people can then still ask you.')) +
    '<div class="modal-form-actions">' + resetBtn(d.saved.privacy, 'f-privacy') + '</div></form>';
  $('p-mode').onchange = () => { const m = modes.find((x) => x.id === $('p-mode').value); document.querySelector('#p-mode ~ .help').innerHTML = esc((m || {}).help || ''); };
  const body = () => ({ mode: $('p-mode').value, message: $('p-message').value.trim(), policyUrl: $('p-policy').value.trim(),
    controller: $('p-controller').value.trim(), contact: $('p-contact').value.trim(), notes: $('p-notes').value.trim(), noSelfDelete: !$('p-selfdelete').checked });
  wireSection('privacy', 'f-privacy', body, renderPrivacy, p);
}

async function renderNotifications() {
  const d = await getSettings(), n = d.notifications;
  const cleared = {};
  const hookField = (id, label, h, ph, help) =>
    '<label for="' + id + '">' + label + '<input id="' + id + '" type="text" autocomplete="off" spellcheck="false" placeholder="' +
    esc(h.set ? 'Saved (' + h.host + '). Type a new address to replace it.' : ph) + '">' +
    '<span class="help">' + help + (h.set ? ' <button type="button" class="link-btn" data-clear="' + id + '">Remove the saved address</button>' : '') + '</span></label>';
  $('adminSection').innerHTML = pageHead('Notifications', 'Notifications',
    'Tell people when a test finishes. Set it up once here: nobody else has to do anything.', saveBtn('f-notify')) +
    '<form id="f-notify" class="modal-form">' +
    subcard('Announcements', 'Applies to every test run by anyone with an account. Visitors on the free trial are not announced.', pill(n.enabled, 'On', 'Off'),
      toggle('n-on', 'Announce finished tests', n.enabled, 'Turn it off to stop all announcements at once. What you entered stays saved.') +
      sel('n-when', 'Announce', n.on, [['problems', 'Only when something needs attention'], ['always', 'Every finished test']],
        'Something needs attention when a pass/fail target is missed, 1% or more of requests fail (when the test has no error target), or the test does not complete.')) +
    subcard('Email', 'Sent from your Email Delivery settings.', n.emailAvailable ? '' : pill(false, '', 'Email not set up'),
      toggle('n-runner', 'Email the person who ran the test', n.emailRunner, n.emailAvailable ? 'They hear about their own tests without setting anything up.' : 'Set up Email Delivery first, then this works.') +
      fld('n-to', 'Also email', (n.emailTo || []).join(', '), { ph: 'team@example.com, lead@example.com', hint: 'optional', help: 'Separate with commas (up to 10). These addresses hear about every announced test.' })) +
    subcard('Chat and webhooks', 'Posted to a shared channel. Treat each address like a password: anyone who has it can post to that channel.', '',
      hookField('n-slack', 'Slack webhook address', n.slack, 'https://hooks.slack.com/services/\u2026', 'Create an incoming webhook in Slack (it also works with Mattermost).') +
      hookField('n-teams', 'Microsoft Teams webhook address', n.teams, 'https://\u2026.webhook.office.com/\u2026', 'Use a Teams Workflows webhook (\u201cPost to a channel when a webhook request is received\u201d).') +
      hookField('n-hook', 'Any webhook address', n.webhook, 'https://example.com/hooks/blasta', 'BLASTA sends a JSON message with the headline numbers, who started the test and a link.') +
      '<p class="set-result" id="r-notify" role="status" hidden></p>' +
      '<div class="modal-form-actions"><button type="button" class="btn small" id="n-test">Send a test</button>' + resetBtn(d.saved.notifications, 'f-notify') + '</div>' +
      (n.last && n.last.text ? '<p class="help">Last announcement ' + esc(ago(n.last.at)) + ': ' + esc(n.last.text) + '</p>' : '')) +
    '</form>';
  if (!n.emailAvailable) $('n-runner').disabled = true;
  $('adminSection').querySelectorAll('[data-clear]').forEach((b) => { b.onclick = () => { cleared[b.dataset.clear] = true; $(b.dataset.clear).value = ''; $(b.dataset.clear).placeholder = 'Will be removed when you save'; b.remove(); }; });
  const val = (id) => (cleared[id] ? '' : $(id).value.trim() || undefined);
  const body = () => ({ enabled: $('n-on').checked, on: $('n-when').value, emailRunner: $('n-runner').checked,
    emailTo: $('n-to').value.split(',').map((x) => x.trim()).filter(Boolean), slack: val('n-slack'), teams: val('n-teams'), webhook: val('n-hook') });
  wireSection('notifications', 'f-notify', body, renderNotifications, n);
  $('n-test').onclick = async () => {
    const o = $('r-notify');
    o.hidden = false; o.className = 'set-result'; o.textContent = 'Sending\u2026';
    try {
      const r = await adminCall('POST', 'settings/notifications/test', {});
      const bad = r.results.filter((x) => !x.ok);
      o.className = 'set-result ' + (bad.length ? 'bad' : 'ok');
      o.textContent = r.results.map((x) => x.channel + (x.ok ? ': sent' : ': failed (' + x.error + ')')).join(' \u00b7 ');
    } catch (err) { o.className = 'set-result bad'; o.textContent = err.message; }
  };
}

async function renderSmtp() {
  const d = await getSettings(), m = d.smtp;
  $('adminSection').innerHTML = pageHead('Email', 'Email Delivery',
    'Configure SMTP delivery and verify that email works. Used for password-reset links and for approval notices. BLASTA works without it.', saveBtn('f-smtp')) +
    (d.mailProblem ? '<div class="set-result bad mail-problem" role="alert"><strong>Last delivery problem</strong> (' + esc(fmtWhen(d.mailProblem.at)) + '): the email \u201c' + esc(d.mailProblem.subject) +
      '\u201d to ' + esc(d.mailProblem.to) + ' was not delivered.<br>' + esc(d.mailProblem.error) +
      '<br><span class="mail-hint">Check the settings below with \u201cTest configuration\u201d and \u201cSend test\u201d. This note clears after the next email goes out.</span></div>' : '') +
    subcard('SMTP connection', 'Settings saved here take priority over environment variables.', pill(m.enabled && m.host, 'Enabled', 'Not configured'),
      '<form id="f-smtp" class="modal-form">' +
      toggle('s-mailOn', 'Enable', m.enabled) +
      '<div class="account-form-row">' +
      fld('s-host', 'SMTP host', m.host, { ph: 'smtp.example.com' }) +
      fld('s-port', 'Port', m.port, { type: 'number', min: 1 }) +
      sel('s-security', 'Security', m.security || 'starttls', [['starttls', 'STARTTLS (usually port 587)'], ['tls', 'TLS (usually port 465)'], ['none', 'None (only for a trusted local relay)']]) +
      fld('s-from', 'From address', m.from, { ph: 'blasta@example.com', help: 'The address the email comes from.' }) +
      fld('s-fromName', 'Sender name', m.fromName, { ph: 'BLASTA by AWHADI', help: 'The text people see as the sender in their mail app. Used on every email BLASTA sends.' }) +
      fld('s-user', 'Username', m.username, { hint: 'optional' }) +
      fld('s-pass', 'Password', '', { type: 'password', ph: m.passwordSet ? 'Leave blank to keep the current password' : 'Required if your server needs authentication', help: 'Stored encrypted. It is never shown again.' }) +
      '</div><div class="modal-form-actions"><button type="button" class="btn small" id="s-mailverify">Test configuration</button>' + resetBtn(d.saved.smtp, 'f-smtp') + '</div>' +
      resultBox('r-smtpv') + '</form>') +
    subcard('Send a test email', 'Verify the full delivery path with a real message to your own address.', '',
      '<div class="modal-form-actions"><button type="button" class="btn small primary-sm" id="s-mailtest">Send test</button></div>' + resultBox('r-smtp'));
  const body = () => ({ enabled: $('s-mailOn').checked, host: $('s-host').value.trim(), port: num('s-port'), security: $('s-security').value,
    username: $('s-user').value.trim(), password: $('s-pass').value, from: $('s-from').value.trim(), fromName: $('s-fromName').value.trim() });
  wireSection('smtp', 'f-smtp', body, renderSmtp, m);
  $('s-security').onchange = () => { const p = { starttls: 587, tls: 465 }[$('s-security').value]; if (p) $('s-port').value = p; };
  // Checks the form as it is, before saving: reach the server, secure the connection, log in. Nothing is sent or saved.
  $('s-mailverify').onclick = async () => {
    const say = sayIn('r-smtpv');
    say('Connecting…', true);
    try { const r = await adminCall('POST', 'settings/smtp/verify', body()); say(r.status, true); }
    catch (err) { say(err.message, false); }
  };
  $('s-mailtest').onclick = async () => {
    const say = sayIn('r-smtp');
    say('Sending…', true);
    try { const r = await adminCall('POST', 'settings/smtp/test', body()); say('Test email ' + r.status + '. Check the inbox.', true); }
    catch (err) { say(err.message, false); }
  };
}

async function renderUsers() {
  const users = await refreshPending();
  const admins = users.filter((u) => u.role === 'admin' && u.status === 'active').length;
  const rows = users.map((u) => {
    const self = me && u.id === me.id;
    const sole = u.role === 'admin' && u.status === 'active' && admins <= 1;
    const guard = self || sole ? ' disabled title="' + (self ? 'You cannot do this to your own account' : 'At least one administrator must remain') + '"' : '';
    const item = (act, label, cls, dis) => '<button type="button" class="' + (cls || '') + '" data-act="' + act + '" data-id="' + esc(u.id) + '"' + (dis || '') + '>' + label + '</button>';
    const menu = [
      u.status === 'pending' ? item('approve', 'Approve') : '',
      u.status === 'unverified' ? item('approve', 'Activate now') + item('resend', 'Resend confirmation email') : '',
      u.role === 'admin' ? item('role-user', 'Demote to User', '', guard) : item('role-admin', 'Promote to Admin'),
      u.hasPassword ? item('password', 'Reset password') : '',
      item('delete', u.status === 'pending' ? 'Reject' : 'Delete', 'menu-danger', guard),
    ].join('');
    const label = { active: 'Active', disabled: 'Deactivated', pending: 'Pending approval', unverified: 'Unverified email' }[u.status] || u.status;
    const status = '<div class="status-cell"><label class="switch" title="' + (guard ? (self ? 'You cannot deactivate your own account' : 'At least one administrator must remain') : (u.status === 'active' ? 'Deactivate this user' : 'Activate this user')) + '">' +
      '<input type="checkbox" data-switch="' + esc(u.id) + '"' + (u.status === 'active' ? ' checked' : '') + (guard ? ' disabled' : '') + ' aria-label="Active: ' + esc(u.email) + '"></label>' +
      '<span class="settings-status-pill ' + (u.status === 'active' ? 'on' : u.status === 'disabled' ? 'off' : 'warn') + '">' + label + '</span></div>';
    return '<tr data-search="' + esc((u.name + ' ' + u.email).toLowerCase()) + '"><td><div class="user-cell"><div class="user-cell-avatar">' + (u.avatar ? '<img alt="" src="' + esc(u.avatar) + '">' : esc(initial(u.name || u.email))) + '</div>' +
      '<div><div class="user-cell-name">' + esc(u.name) + (self ? ' <span class="you-tag">(you)</span>' : '') + (u.sso ? ' <span class="auth-tag">via SSO</span>' : '') + '</div>' +
      '<div class="user-cell-email">' + esc(u.email) + '</div></div></div></td>' +
      '<td><span class="role-pill ' + esc(u.role) + '">' + (u.role === 'admin' ? 'Administrator' : 'User') + '</span></td><td>' + status + '</td>' +
      '<td>' + esc(fmtWhen(u.lastLoginAt)) + '</td>' +
      '<td class="user-actions-cell"><div class="row-menu"><button type="button" class="row-menu-btn" aria-label="Actions for ' + esc(u.email) + '" aria-expanded="false">&#8942;</button>' +
      '<div class="row-menu-dropdown" hidden>' + menu + '</div></div></td></tr>';
  }).join('') || '<tr><td colspan="5">No users.</td></tr>';
  $('adminSection').innerHTML = pageHead('Users', 'User Management',
    'Everyone here can run tests; their history is private to them. Administrators can also manage accounts and settings. Email is used for password-reset links and approval notices.',
    '<button type="button" id="addUserBtn" class="btn primary-sm">+ Add User</button>') +
    '<div class="settings-subcard"><div class="users-toolbar"><input type="search" id="userSearch" placeholder="Search members…" aria-label="Search members"></div>' +
    '<div class="users-table-wrap"><table class="users-table"><thead><tr><th>User</th><th>Role</th><th>Status</th><th>Last sign-in</th><th></th></tr></thead><tbody>' + rows + '</tbody></table></div></div>';

  $('userSearch').oninput = () => {
    const q = $('userSearch').value.trim().toLowerCase();
    document.querySelectorAll('.users-table tbody tr[data-search]').forEach((r) => r.classList.toggle('hidden', !!q && !r.dataset.search.includes(q)));
  };
  $('addUserBtn').onclick = openAddUser;
  const closeMenus = () => document.querySelectorAll('.row-menu-dropdown').forEach((m) => { m.hidden = true; m.previousElementSibling.setAttribute('aria-expanded', 'false'); });
  $('adminSection').onchange = async (e) => {
    const sw = e.target.closest('input[data-switch]');
    if (!sw) return;
    const id = sw.dataset.switch, u = users.find((x) => x.id === id);
    if (!sw.checked && !confirm('Deactivate ' + u.email + '? They are signed out immediately and cannot sign in until you activate them again.')) { sw.checked = true; return; }
    sw.disabled = true;
    try {
      await adminCall('POST', 'users/' + id + '/status', { status: sw.checked ? 'active' : 'disabled' });
      toast(sw.checked ? 'User activated' : 'User deactivated', 'ok');
    } catch (err) { toast(err.message, 'error'); }
    renderUsers();
  };
  $('adminSection').onclick = async (e) => {
    const kebab = e.target.closest('.row-menu-btn');
    if (kebab) {
      const m = kebab.nextElementSibling, open = m.hidden;
      closeMenus();
      m.hidden = !open;
      kebab.setAttribute('aria-expanded', String(open));
      if (open) {                       // fixed position: the table's scroll box must not clip it
        const r = kebab.getBoundingClientRect(), mh = m.offsetHeight;
        m.style.top = (r.bottom + 4 + mh > innerHeight ? Math.max(8, r.top - mh - 4) : r.bottom + 4) + 'px';
        m.style.left = Math.max(8, r.right - m.offsetWidth) + 'px';
      }
      e.stopPropagation();
      return;
    }
    const b = e.target.closest('button[data-act]');
    if (!b || b.disabled) return;
    closeMenus();
    const id = b.dataset.id, u = users.find((x) => x.id === id);
    try {
      switch (b.dataset.act) {
        case 'approve': await adminCall('POST', 'users/' + id + '/status', { status: 'active' }); break;
        case 'resend': await adminCall('POST', 'users/' + id + '/resend'); toast('Confirmation email sent', 'ok'); return;
        case 'disable':
          if (!confirm('Disable ' + u.email + '? They are signed out immediately.')) return;
          await adminCall('POST', 'users/' + id + '/status', { status: 'disabled' }); break;
        case 'role-admin': await adminCall('POST', 'users/' + id + '/role', { role: 'admin' }); break;
        case 'role-user': await adminCall('POST', 'users/' + id + '/role', { role: 'user' }); break;
        case 'password': openPasswordDialog({ self: false, id, email: u.email }); return;
        case 'delete':
          if (!confirm('Delete ' + u.email + '? Their account and test history are removed. This cannot be undone.')) return;
          await adminCall('DELETE', 'users/' + id); break;
      }
      renderUsers();
    } catch (err) { toast(err.message, 'error'); }
  };
  if (!renderUsers.bound) { renderUsers.bound = true; document.addEventListener('click', closeMenus); window.addEventListener('scroll', closeMenus, { passive: true }); }
}

function openAddUser() {
  ['auName', 'auEmail', 'auPassword'].forEach((id) => { $(id).value = ''; });
  $('auRole').value = 'user';
  $('auErr').hidden = true;
  $('addUserDlg').showModal();
  $('auEmail').focus();
}

function wireAddUser() {
  $('auCancel').onclick = () => $('addUserDlg').close();
  $('addUserForm').onsubmit = async (e) => {
    e.preventDefault();
    const fail = (m) => { $('auErr').textContent = m; $('auErr').hidden = false; };
    if (!$('auEmail').value.trim() || !$('auPassword').value) { fail('Enter an email address and a password.'); return; }
    $('auOk').disabled = true;
    try {
      await adminCall('POST', 'users', { email: $('auEmail').value, name: $('auName').value, password: $('auPassword').value, role: $('auRole').value });
      $('addUserDlg').close();
      toast('User added', 'ok');
      renderUsers();
    } catch (err) { fail(err.message); }
    $('auOk').disabled = false;
  };
}

async function refreshPublicConfig() {
  try { const r = await fetch('api/auth/config', { headers: { 'X-Requested-With': 'blasta' } }); if (r.ok) authCfg = await r.json(); } catch (e) {}
  syncRegistrationLinks();
}

// With registration switched off there is nothing to offer people who have no account.
function syncRegistrationLinks() {
  const closed = !!(authCfg && authCfg.registration === 'closed' && !authCfg.needsSetup);
  document.querySelectorAll('a[href="login?register"]').forEach((a) => { a.hidden = closed; });
}

/* ---- My templates, import, notifications and baselines -------------------------------- */

const JSON_HEADERS = { 'Content-Type': 'application/json' };
// Manual runs are named with the time of day; that is not part of what the test is.
const testName = (n) => String(n || '').replace(/\s\u00b7\s\d{1,2}:\d{2}(\s?[AaPp][Mm])?$/, '');
const sameTest = (a, b) => a.executor === b.executor && a.target === b.target && testName(a.jobName) === testName(b.jobName);
const SECRET_HEADER = /authorization|cookie|token|secret|key|password|passwd|auth|session|signature|credential/i;

/* My favorites: tests people save to run again. Private to them; credentials are stored encrypted.
   My templates: their own copies of built-in templates, with their settings filled in. */

let favs = { list: [], max: 100 }, sets = { list: [], max: 100 };

async function loadFavorites() {
  if (!me) { favs = { list: [], max: 100 }; return; }
  try { const r = await api('/my-favorites'); favs = { list: r.favorites || [], max: r.max || 100 }; } catch (e) { favs.list = []; }
  paintFavCount();
}

async function loadSets() {
  if (!me) { sets = { list: [], max: 100 }; return; }
  try { const r = await api('/my-templates'); sets = { list: r.templates || [], max: r.max || 100 }; } catch (e) { sets.list = []; }
  paintFavCount();
}

function paintFavCount() { $('favCount').textContent = (favs.list.length + sets.list.length) || ''; }
function hideMine() { $('favView').hidden = true; }

function syncMineUI() {
  $('tplTabs').hidden = !me;
  $('saveTpl').hidden = !me;
  $('histSaveTpl').hidden = !me;
}

function syncTplTabs(tab) {
  syncMineUI();
  $('tabBuiltin').classList.toggle('on', tab === 'built');
  $('tabFavs').classList.toggle('on', tab === 'favs');
  if (me) { loadFavorites(); loadSets(); }
}

async function showFavorites() {
  $('tplListView').hidden = true;
  $('tplDetailView').hidden = true;
  hideMine();
  $('favView').hidden = false;
  document.title = 'My favorites | BLASTA';
  await Promise.all([loadFavorites(), loadSets()]);
  renderFavorites();
}

function favCard(t) {
  return '<div class="tcard mine" data-id="' + esc(t.id) + '" data-kind="job">' +
    '<button type="button" class="card-x" data-act="del" aria-label="Remove from My favorites" title="Remove from My favorites">&times;</button>' +
    '<div class="tcard-head"><span class="ticon">' + catIcon('Generic') + '</span><h3>' + esc(t.name) + '</h3></div>' +
    '<div><span class="tag">Job</span> <span class="tag">' + esc(t.executor || 'http') + '</span></div>' +
    (t.description ? '<p class="tsum">' + esc(t.description) + '</p>' : '') +
    (t.summary ? '<div class="tstack">' + esc(t.summary) + '</div>' : '') +
    '<div class="mine-meta muted">Saved ' + esc(ago(t.updatedAt)) + '</div>' +
    '<div class="mine-actions">' +
    '<button type="button" class="btn small primary-sm" data-act="use">Use</button>' +
    '<button type="button" class="btn small" data-act="edit">Rename</button>' +
    '<button type="button" class="btn small" data-act="dup">Duplicate</button>' +
    '</div></div>';
}

function setCard(t) {
  const href = 'templates/' + encodeURIComponent(t.presetId) + '?my=' + encodeURIComponent(t.id);
  return '<div class="tcard mine" data-id="' + esc(t.id) + '" data-kind="set">' +
    '<button type="button" class="card-x" data-act="del" aria-label="Remove from My favorites" title="Remove from My favorites">&times;</button>' +
    '<div class="tcard-head"><span class="ticon">' + catIcon(t.category) + '</span><h3>' + esc(t.name) + '</h3></div>' +
    '<div><span class="tag">Template</span> <span class="tag">' + esc(t.category || '') + '</span></div>' +
    (t.description ? '<p class="tsum">' + esc(t.description) + '</p>' : '') +
    '<div class="tstack">' + (t.missing ? 'The built-in template this came from is gone' : 'From ' + esc(t.title) + ' \u00b7 ' + t.jobs + ' jobs') + '</div>' +
    '<div class="mine-meta muted">Saved ' + esc(ago(t.updatedAt)) + '</div>' +
    '<div class="mine-actions">' +
    (t.missing ? '' : '<a class="btn small primary-sm" href="' + href + '">Open</a>') +
    '<button type="button" class="btn small" data-act="edit">Rename</button>' +
    '<button type="button" class="btn small" data-act="dup">Duplicate</button>' +
    '</div></div>';
}

// One list for both kinds, newest first.
function renderFavorites() {
  const all = favs.list.map((t) => ({ t, set: false })).concat(sets.list.map((t) => ({ t, set: true })))
    .sort((a, b) => String(b.t.updatedAt).localeCompare(String(a.t.updatedAt)));
  $('favCount2').textContent = all.length + ' saved';
  $('favEmpty').hidden = all.length > 0;
  $('favGrid').innerHTML = all.map((x) => (x.set ? setCard : favCard)(x.t)).join('');
}

async function useFavorite(id) {
  let t;
  try { t = await api('/my-favorites/' + encodeURIComponent(id)); } catch (e) { toast(e.message, 'error'); return; }
  usePresetJob({ jobId: t.id, name: t.name, notes: t.description || '', safety: 'read', job: t.job });
  loadedFrom = { id: t.id, title: 'My favorites', job: t.name, notes: t.description || '', safety: 'read', gate: !!(t.job && t.job.slo), mine: true };
  showBanner();
  go('#/jobs');
}

function wireMine() {
  $('favGrid').onclick = async (e) => {
    const b = e.target.closest('[data-act]');
    if (!b) return;
    const card = b.closest('.tcard');
    const isSet = card.dataset.kind === 'set';
    const t = (isSet ? sets : favs).list.find((x) => x.id === card.dataset.id);
    if (!t) return;
    const base = isSet ? '/my-templates/' : '/my-favorites/';
    try {
      if (b.dataset.act === 'use') await useFavorite(t.id);
      else if (b.dataset.act === 'edit') openSaveDialog({ kind: isSet ? 'set' : 'fav', edit: t });
      else if (b.dataset.act === 'dup') {
        await api(base + encodeURIComponent(t.id) + '/duplicate', { method: 'POST' });
        toast('Duplicated \u201c' + t.name + '\u201d', 'ok');
      } else if (b.dataset.act === 'del') {
        if (!confirm('Remove \u201c' + t.name + '\u201d from My favorites?' + (isSet ? ' The built-in template stays.' : ' This cannot be undone.'))) return;
        await api(base + encodeURIComponent(t.id), { method: 'DELETE' });
        if (!isSet && loadedFrom && loadedFrom.mine && loadedFrom.id === t.id) { loadedFrom = null; showBanner(); }
        toast('Removed \u201c' + t.name + '\u201d', 'ok');
      }
      await Promise.all([loadFavorites(), loadSets()]); renderFavorites();
    } catch (err) { toast(err.message, 'error'); }
  };
  $('favImport').onclick = () => openImport('');
  $('saveTpl').onclick = () => openSaveDialog({ kind: 'fav' });
  $('tplSave').onclick = updateLoadedFavorite;
  $('tplStar').onclick = starTemplate;
  $('setRename').onclick = () => openedSet && openSaveDialog({ kind: 'set', edit: openedSet, stay: true });
  $('setSave').onclick = async () => {
    if (!openedSet) return;
    try {
      await api('/my-templates/' + encodeURIComponent(openedSet.id), { method: 'PUT', headers: JSON_HEADERS, body: JSON.stringify({ values: presetValues() }) });
      toast('Saved your settings in “' + openedSet.name + '”', 'ok');
    } catch (e) { toast(e.message, 'error'); }
  };
  $('setRemove').onclick = async () => {
    if (!openedSet || !confirm('Remove “' + openedSet.name + '” from My templates? The built-in template stays.')) return;
    try {
      await api('/my-templates/' + encodeURIComponent(openedSet.id), { method: 'DELETE' });
      toast('Removed “' + openedSet.name + '”', 'ok');
      go('#/templates?favorites');
    } catch (e) { toast(e.message, 'error'); }
  };
  $('importBtn').onclick = () => openImport('');
  $('runAgain').onclick = () => { if (!$('start').disabled) start(); };

  $('stCancel').onclick = () => $('saveTplDlg').close();
  $('stForm').onsubmit = async (e) => {
    e.preventDefault();
    const err = (m) => { $('stErr').textContent = m; $('stErr').hidden = false; };
    $('stErr').hidden = true;
    const name = $('stName').value.trim();
    const description = $('stDesc').value.trim();
    const kind = stMode.kind, edit = stMode.edit;
    if (!name) { err('Give it a name.'); return; }
    $('stOk').disabled = true;
    try {
      if (kind === 'newset') {
        const t = await api('/my-templates', { method: 'POST', headers: JSON_HEADERS,
          body: JSON.stringify({ presetId: presetDef.id, name, description, values: presetValues() }) });
        $('saveTplDlg').close();
        toast('Added “' + t.name + '” to My templates', 'ok');
        go('#/templates/' + encodeURIComponent(presetDef.id) + '?my=' + encodeURIComponent(t.id));
        return;
      }
      if (kind === 'set') {
        await api('/my-templates/' + encodeURIComponent(edit.id), { method: 'PUT', headers: JSON_HEADERS, body: JSON.stringify({ name, description }) });
        if (openedSet && openedSet.id === edit.id) { openedSet.name = name; showSetBar(); }
        toast('Template updated', 'ok');
        $('saveTplDlg').close();
        await loadSets();
        if (!$('favView').hidden) renderFavorites();
        return;
      }
      if (edit) {
        await api('/my-favorites/' + encodeURIComponent(edit.id), { method: 'PUT', headers: JSON_HEADERS, body: JSON.stringify({ name, description }) });
        if (loadedFrom && loadedFrom.mine && loadedFrom.id === edit.id) { loadedFrom.job = name; loadedFrom.notes = description; showBanner(); }
        toast('Favorite updated', 'ok');
      } else {
        const problem = validate();
        if (problem) { $('saveTplDlg').close(); showFormError(problem[0], problem[1]); return; }
        const job = buildJob();
        const update = $('stUpdate').checked && loadedFrom && loadedFrom.mine;
        const t = await api(update ? '/my-favorites/' + encodeURIComponent(loadedFrom.id) : '/my-favorites', {
          method: update ? 'PUT' : 'POST', headers: JSON_HEADERS, body: JSON.stringify({ name, description, job }) });
        loadedFrom = { id: t.id, title: 'My favorites', job: t.name, notes: t.description || '', safety: 'read', gate: !!job.slo, mine: true };
        showBanner();
        toast('Saved “' + t.name + '” to My favorites', 'ok');
      }
      $('saveTplDlg').close();
      await loadFavorites();
      if (!$('favView').hidden) renderFavorites();
    } catch (ex) { err(ex.message); } finally { $('stOk').disabled = false; }
  };
}

let stMode = { kind: 'fav' };
function openSaveDialog(mode) {
  stMode = mode || { kind: 'fav' };
  const { kind, edit } = stMode;
  const fav = kind === 'fav';
  $('stTitle').textContent = kind === 'newset' ? 'Add to My templates' : kind === 'set' ? 'Rename template' : edit ? 'Edit favorite' : 'Save as favorite';
  $('stNote').textContent = kind === 'newset'
    ? 'Keeps this template with the settings you filled in, so it opens ready next time. Credentials are never kept.'
    : 'Keeps this whole setup: the target, headers, body, load settings and pass/fail targets. Credentials in it are stored encrypted and are only visible to you.';
  $('stNote').hidden = !!edit;
  // A job loaded from a built-in template is offered under its own name and notes.
  $('stName').value = edit ? edit.name : kind === 'newset' ? presetDef.title : (loadedFrom ? loadedFrom.job : 'Test of ' + manualTestHost());
  $('stDesc').value = edit ? (edit.description || '') : (fav && loadedFrom ? loadedFrom.notes || '' : '');
  const canUpdate = fav && !edit && loadedFrom && loadedFrom.mine;
  $('stUpdateRow').hidden = !canUpdate;
  $('stUpdate').checked = false;
  if (canUpdate) $('stUpdateText').textContent = 'Update “' + loadedFrom.job + '” instead of making a new one';
  $('stErr').hidden = true;
  $('stOk').textContent = edit ? 'Save' : kind === 'newset' ? 'Add' : 'Save favorite';
  $('saveTplDlg').showModal();
  $('stName').select();
}

// The banner's "Save changes": put what is in the form into the favorite it came from.
async function updateLoadedFavorite() {
  if (!loadedFrom || !loadedFrom.mine) return;
  const problem = validate();
  showFormError(problem && problem[0], problem && problem[1]);
  if (problem) return;
  try {
    await api('/my-favorites/' + encodeURIComponent(loadedFrom.id), { method: 'PUT', headers: JSON_HEADERS,
      body: JSON.stringify({ name: loadedFrom.job, description: loadedFrom.notes, job: buildJob() }) });
    toast('Saved the changes to “' + loadedFrom.job + '”', 'ok');
    loadFavorites();
  } catch (e) { toast(e.message, 'error'); }
}


/* Import: a curl command, a HAR file, a Postman collection or an OpenAPI document. */

let impResult = null;

function asJobFile(text) {
  try {
    const j = JSON.parse(text);
    if (j && typeof j === 'object' && !Array.isArray(j) && j.executor && j.target && !j.log && !j.openapi && !j.swagger && !j.info) return j;
  } catch (e) { /* not JSON */ }
  return null;
}

function openImport(text) {
  impResult = null;
  $('impText').value = text || '';
  $('impErr').hidden = true;
  $('impList').hidden = true;
  $('impUse').hidden = $('impSaveAll').hidden = true;
  $('impFormat').textContent = '';
  $('importDlg').showModal();
  if (text) readImport();
  else $('impText').focus();
}

async function readImport() {
  const text = $('impText').value.trim();
  const fail = (m) => { $('impErr').textContent = m; $('impErr').hidden = false; $('impList').hidden = true; $('impUse').hidden = $('impSaveAll').hidden = true; };
  $('impErr').hidden = true;
  if (!text) { fail('Paste something to read, or choose a file.'); return; }
  const job = asJobFile(text);
  if (job) {
    impResult = { format: 'job file', requests: [{ name: job.name || 'Imported job', method: job.method || 'GET', url: (job.target && job.target.url) || '', notes: ['A BLASTA job file: its load settings come with it.'], _job: job }] };
  } else {
    try { impResult = await api('/import', { method: 'POST', headers: JSON_HEADERS, body: JSON.stringify({ text }) }); } catch (e) { fail(e.message); return; }
  }
  const rs = impResult.requests || [];
  $('impFormat').textContent = impResult.format + ': ' + rs.length + (rs.length === 1 ? ' request' : ' requests') +
    (impResult.skipped ? ' (' + impResult.skipped + ' images, scripts and similar left out)' : '');
  $('impList').innerHTML = rs.map((r, i) =>
    '<label class="imp-item"><input type="radio" name="impPick" value="' + i + '"' + (i === 0 ? ' checked' : '') + '>' +
    '<span class="imp-text"><span class="imp-name"><b class="imp-method">' + esc(r.method) + '</b> ' + esc(r.name.replace(new RegExp('^' + r.method + '\\s+'), '')) + '</span><small class="mono">' + esc(r.url) + '</small>' +
    (r.notes || []).map((n) => '<small class="imp-note">' + esc(n) + '</small>').join('') + '</span></label>').join('') +
    (impResult.warnings || []).map((w) => '<p class="note">' + esc(w) + '</p>').join('');
  $('impList').hidden = false;
  $('impUse').hidden = false;
  $('impSaveAll').hidden = !me || rs.length < 1;
  $('impSaveAll').textContent = rs.length > 1 ? 'Save all ' + rs.length + ' as favorites' : 'Save as a favorite';
}

const importedJob = (r) => r._job || { executor: 'http', method: r.method, target: { url: r.url }, headers: r.headers || {}, body: r.body || '' };

function wireImport() {
  $('impRead').onclick = readImport;
  $('impClose').onclick = () => $('importDlg').close();
  $('impFile').onchange = () => {
    const f = $('impFile').files[0];
    if (!f) return;
    if (f.size > 4 * 1024 * 1024) { $('impErr').textContent = 'That file is too large to import.'; $('impErr').hidden = false; return; }
    const rd = new FileReader();
    rd.onload = () => { $('impText').value = String(rd.result || ''); readImport(); };
    rd.readAsText(f);
    $('impFile').value = '';
  };
  $('impUse').onclick = () => {
    const i = parseInt((document.querySelector('input[name=impPick]:checked') || {}).value, 10);
    const r = impResult && impResult.requests[i];
    if (!r) return;
    $('importDlg').close();
    usePresetJob({ jobId: 'import', name: r.name, notes: '', safety: 'read', job: importedJob(r) });
    loadedFrom = null;
    showBanner();
    go('#/jobs');
  };
  $('impSaveAll').onclick = async () => {
    if (!impResult) return;
    $('impSaveAll').disabled = true;
    let saved = 0, last = '';
    for (const r of impResult.requests) {
      try {
        await api('/my-favorites', { method: 'POST', headers: JSON_HEADERS, body: JSON.stringify({ name: r.name.slice(0, 120), description: (r.notes || []).join(' ').slice(0, 500), job: importedJob(r) }) });
        saved++;
      } catch (e) { last = e.message; if (e.status === 409) break; }
    }
    $('impSaveAll').disabled = false;
    if (!saved) { $('impErr').textContent = last || 'Nothing could be saved.'; $('impErr').hidden = false; return; }
    $('importDlg').close();
    toast('Saved ' + saved + (saved === 1 ? ' favorite' : ' favorites') + (last ? ' (some could not be saved: ' + last + ')' : ''), 'ok');
    go('#/templates?favorites');
  };
}

/* Baselines and comparing two runs of the same test. */

function fmtMetric(m, v) {
  if (m.unit === 'ms') return v >= 1000 ? (v / 1000).toFixed(2) + ' s' : v.toFixed(1) + ' ms';
  if (m.unit === 'rps') return v.toFixed(1) + '/s';
  if (m.unit === 'percent') return v.toFixed(2) + '%';
  return fmtInt(v);
}

function renderCompare(r) {
  const res = r.result;
  const head = '<table class="cmp-table"><thead><tr><th>Measure</th><th>Baseline</th><th>This run</th><th>Change</th><th></th></tr></thead><tbody>';
  const rows = res.metrics.map((m) => {
    const ch = m.base !== 0 ? (m.deltaPct > 0 ? '+' : '') + m.deltaPct.toFixed(1) + '%' : (m.delta !== 0 ? 'new' : '-');
    return '<tr><td>' + esc(m.name) + '</td><td class="num">' + fmtMetric(m, m.base) + '</td><td class="num">' + fmtMetric(m, m.now) + '</td>' +
      '<td class="num">' + ch + '</td><td><span class="cmp-chip ' + esc(m.verdict) + '">' + (m.verdict === 'same' ? 'about the same' : esc(m.verdict)) + '</span></td></tr>';
  }).join('');
  let verdict = '';
  if (res.judged) {
    verdict = res.passed
      ? '<p class="set-result ok">Within the limits you set.</p>'
      : '<div class="set-result bad"><strong>Outside the limits you set:</strong><ul>' + res.regressions.map((x) => '<li>' + esc(x) + '</li>').join('') + '</ul></div>';
  }
  const notes = (res.notes || []).map((n) => '<p class="note">' + esc(n) + '</p>').join('');
  return verdict + head + rows + '</tbody></table>' + notes;
}

async function runCompare(run) {
  const to = $('cmpWith').value;
  if (!to) return;
  const q = new URLSearchParams({ to });
  [['latency', 'cmpLat'], ['errors', 'cmpErr'], ['throughput', 'cmpThr']].forEach(([k, id]) => { if ($(id).value !== '') q.set(k, $(id).value); });
  $('cmpOut').innerHTML = '<p class="muted">Comparing…</p>';
  try { $('cmpOut').innerHTML = renderCompare(await api('/runs/' + encodeURIComponent(run.id) + '/compare?' + q)); }
  catch (e) { $('cmpOut').innerHTML = '<p class="form-error">' + esc(e.message) + '</p>'; }
}

async function setupCompare(run) {
  const done = run.state !== 'running';
  const bb = $('histBaseline');
  bb.hidden = !done || guestMode || !me && !!authCfg;
  bb.textContent = run.baseline ? 'Remove baseline' : 'Set as baseline';
  bb.onclick = async () => {
    try {
      const r = await api('/runs/' + encodeURIComponent(run.id) + '/baseline', { method: 'POST', headers: JSON_HEADERS, body: JSON.stringify({ baseline: !run.baseline }) });
      toast(r.baseline ? 'This run is now the baseline for “' + run.jobName + '”' : 'Baseline removed', 'ok');
      loadRuns();
      showRunDetail(run.id);
    } catch (e) { toast(e.message, 'error'); }
  };
  $('histSaveTpl').onclick = () => { reuseRun(run); openSaveDialog({ kind: 'fav' }); };
  const box = $('histCompare');
  box.hidden = true;
  $('cmpOut').innerHTML = '';
  if (!done) return;
  let runs = [];
  try { runs = (await api('/runs')).runs || []; } catch (e) { return; }
  const same = runs.filter((r) => r.id !== run.id && r.state !== 'running' && sameTest(r, run))
    .sort((a, b) => (b.baseline ? 1 : 0) - (a.baseline ? 1 : 0) || new Date(b.startedAt) - new Date(a.startedAt));
  if (!same.length) return;
  $('cmpWith').innerHTML = same.map((r) => {
    const s = r.summary || {}, p95 = s.latency && s.latency.percentiles ? s.latency.percentiles.p95 : null;
    return '<option value="' + esc(r.id) + '">' + (r.baseline ? '★ baseline · ' : '') + esc(new Date(r.startedAt).toLocaleString()) + ' · ' +
      (s.avgRps || 0).toFixed(1) + '/s · p95 ' + fmtLat(p95) + '</option>';
  }).join('');
  $('cmpHint').textContent = same[0].baseline && !run.baseline
    ? 'Compared with the baseline you set for this test. Add limits to turn it into a pass or fail.'
    : 'Pick an earlier run of the same test. Add limits to turn it into a pass or fail.';
  $('cmpGo').onclick = () => runCompare(run);
  box.hidden = false;
  if (same[0].baseline && !run.baseline) runCompare(run);
}

wireMine();

/* ---- Running: shown in the menu only while a test is running ------------------------------ */

let runningNow = [], runningTimer = null, runningSeen = null;

async function pollRunning() {
  if (!me && authCfg && !guestMode) { runningNow = []; runningSeen = null; paintRunning(); return; }
  try { runningNow = (await api('/runs?state=running')).runs || []; } catch (e) { return; }
  // Tell people about jobs that ended while they were looking elsewhere.
  const ids = new Set(runningNow.map((r) => r.id));
  if (runningSeen) runningSeen.forEach((r, id) => { if (!ids.has(id) && id !== runId) toast('“' + testName(r.jobName) + '” finished. The result is in History.', 'ok'); });
  runningSeen = new Map(runningNow.map((r) => [r.id, r]));
  paintRunning();
  if (!$('view-running').hidden) ensureWatching();
}

// The Running jobs page shows one job live; with nothing picked yet, the first running one.
async function ensureWatching() {
  if ((!es || !runId) && runningNow.length && $('results').hidden) await watchRun(runningNow[0].id);
}

function paintRunning() {
  const n = runningNow.length;
  $('navRunning').hidden = n === 0;
  $('runCount').textContent = n > 1 ? n : '';
  const dock = $('runDock');
  dock.hidden = n === 0 || guestMode;
  document.body.classList.toggle('has-dock', !dock.hidden);
  $('dockCount').textContent = n;
  if (n) {
    $('dockList').innerHTML = runningNow.map(dockItem).join('');
    $('dockList').querySelectorAll('.run-bar span[data-pct]').forEach((s) => { s.style.width = s.dataset.pct + '%'; });
  }
  if (!$('view-running').hidden) $('runEmpty').hidden = n > 0 || !$('results').hidden;
  const h = document.querySelector('header.bar');
  if (h) document.documentElement.style.setProperty('--hdr', h.offsetHeight + 'px');
}

function dockItem(r) {
  const secs = r.plan && r.plan.duration ? r.plan.duration / 1e9 : 0;
  const el = Math.max(0, (Date.now() - new Date(r.startedAt).getTime()) / 1000);
  const pct = secs ? Math.min(100, Math.round(el / secs * 100)) : 0;
  let host = r.target || '';
  try { host = new URL(r.target).host; } catch (e) { /* not a URL: show as is */ }
  return '<div class="dock-item' + (r.id === runId && !$('view-running').hidden ? ' on' : '') + '" data-id="' + esc(r.id) + '">' +
    '<button type="button" class="dock-open" data-act="watch" title="Watch live">' +
    '<span class="dock-name">' + esc(testName(r.jobName)) + '</span>' +
    '<span class="dock-host">' + esc(host) + '</span>' +
    '<span class="run-bar"><span data-pct="' + pct + '"></span></span>' +
    '<span class="dock-time">' + Math.round(el) + (secs ? ' of ' + Math.round(secs) : '') + ' s</span></button>' +
    '<button type="button" class="card-x" data-act="stop" aria-label="Stop this job" title="Stop this job">&times;</button></div>';
}

function wireRunning() {
  $('dockList').onclick = async (e) => {
    const b = e.target.closest('[data-act]');
    if (!b) return;
    const id = b.closest('.dock-item').dataset.id;
    try {
      if (b.dataset.act === 'watch') await watchRun(id);
      else if (!b.disabled) {
        if (!confirm('Stop this job?')) return;
        b.disabled = true;
        await api('/runs/' + encodeURIComponent(id) + '/stop', { method: 'POST' });
        toast('Stopping job…');
        setTimeout(pollRunning, 800);
      }
    } catch (err) { toast(err.message, 'error'); }
  };
  clearInterval(runningTimer);
  runningTimer = setInterval(() => { if (!document.hidden && (me || !authCfg || guestMode)) pollRunning(); }, 3000);
  window.addEventListener('resize', paintRunning);
  pollRunning();
}
wireRunning();
wireImport();

/* ---- Start ---------------------------------------------------------------- */

async function init() {
  $('themeToggle').onclick = () =>
    applyTheme(document.documentElement.getAttribute('data-theme') === 'dark' ? 'light' : 'dark');
  window.addEventListener('popstate', route);
  // Links to the app's own pages are ordinary links (so they can be opened in a new tab and read
  // by crawlers); a plain click moves within the app without reloading it.
  document.addEventListener('click', (e) => {
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    const a = e.target.closest && e.target.closest('a[href]');
    if (!a || a.target || a.hasAttribute('download')) return;
    const raw = a.getAttribute('href') || '';
    // <base> would send a link like #main to the app's start page: keep it on this page.
    if (raw.length > 1 && raw[0] === '#') {
      const el = document.getElementById(decodeURIComponent(raw.slice(1)));
      if (el) { e.preventDefault(); el.scrollIntoView(); if (!el.hasAttribute('tabindex')) el.setAttribute('tabindex', '-1'); el.focus({ preventScroll: true }); }
      return;
    }
    if (raw === '#') return;
    const u = new URL(a.href, document.baseURI);
    if (u.origin !== location.origin || !u.pathname.startsWith(BASE)) return;
    const rest = u.pathname.slice(BASE.length);
    if (rest !== '' && !ROUTE_RE.test(rest)) return;      // files and the API are not pages
    e.preventDefault();
    go('#/' + rest + u.search);
  });
  wireAuth();
  const signedIn = await bootAuth();
  syncRegistrationLinks();
  // Opening the bare sign-in address (a bookmark, or where a sign-out used to land)
  // goes to the homepage when visitors are welcome; "Sign in" links still work.
  if (guestMode && /^#\/login\/?$/.test(routeStr())) history.replaceState(null, '', toUrl('#/jobs'));
  if (signedIn) await startApp(); else route();
}
init();
