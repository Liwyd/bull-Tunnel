/* bull-Tunnel panel — vanilla JS, live over WebSocket.

   The layout follows the type-2 design ("Precision Routing"): a left
   sidebar, a breadcrumb bar, numbered sections, square corners and a
   monospace face for every number. The network flow graph keeps the
   structure of the type-1 design (three server columns, pills on the
   wires, the legend) inside the type-2 palette.
*/
"use strict";

const $ = (s, el = document) => el.querySelector(s);
const $$ = (s, el = document) => [...el.querySelectorAll(s)];
const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

const TNAME = {
  tcpmux: "TCP Mux", wsmux: "WS Mux", tcp: "TCP", ws: "WebSocket", stealth: "Stealth",
  kcp: "KCP", quic: "QUIC", wssmux: "WSS Mux", wss: "WSS", udp: "UDP",
};
const FA = {
  up: "Up", degraded: "Unstable", down: "Down", testing: "Testing", pending: "Not built",
  online: "Online", offline: "Offline", unknown: "Unknown", ok: "Healthy", broken: "Broken",
};
const NODE_W = 232;

const VIEWS = {
  network: { title: "Network view" },
  tunnels: { title: "Tunnels & protocols" },
  domains: { title: "Domains" },
  infrastructure: { title: "Infrastructure" },
  settings: { title: "System settings" },
};

let S = null;              // last state from the server
let skew = 0;              // server clock - local clock (s)
let drawer = null;         // {type, id}
let selTransport = null;
let groupDraft = null;     // {manual, ids} while editing a domain's group
let ws = null, wsAlive = false;
let view = "network";
let netQuery = "";
let netFilter = "all";
let selectedDomain = "";   // the domain open on the domains page
let selNode = null;        // the node picked in the flow graph
let selLink = null;        // the tunnel picked in the flow graph
let signedInAs = "admin";  // the name of the account, from the session
let settingsSave = null;   // the settings being saved, so two saves do not race
let lastSubmit = Promise.resolve();  // the last form submit, for what follows it

/* ------------------------------------------------------------------ api */
async function api(method, path, body) {
  const r = await fetch(path, {
    method, credentials: "same-origin",
    headers: body ? { "Content-Type": "application/json" } : {},
    body: body ? JSON.stringify(body) : undefined,
  });
  let data = null;
  try { data = await r.json(); } catch (_) { /* empty body */ }
  if (r.status === 401 && path !== "/api/login") { showAuth(); throw new Error("login required"); }
  if (!r.ok) {
    let msg = data && data.detail;
    if (Array.isArray(msg)) msg = msg.map((d) => `${(d.loc || []).slice(-1)[0]}: ${d.msg}`).join(" — ");
    throw new Error(msg || `HTTP ${r.status}`);
  }
  return data;
}

function toast(msg, kind = "info") {
  const el = document.createElement("div");
  el.className = `toast ${kind}`;
  el.textContent = msg;
  $("#toasts").appendChild(el);
  setTimeout(() => el.remove(), kind === "error" ? 7000 : 3500);
}

async function act(fn, okMsg) {
  try { const r = await fn(); if (okMsg) toast(okMsg, "success"); return r; }
  catch (e) { toast(e.message, "error"); throw e; }
}

/* --------------------------------------------------------------- helpers */
const srv = (id) => S.servers.find((s) => s.id === id);
const linkById = (id) => S.links.find((l) => l.id === id);
const byRole = (role) => S.servers.filter((s) => s.role === role).sort((a, b) => a.priority - b.priority || a.name.localeCompare(b.name));
const nm = (id) => (srv(id) || {}).name || "—";
const isBusy = (l) => S.busy.includes(l.id);
const lstatus = (l) => (isBusy(l) ? "testing" : l.status);
const isFlapping = (l) => S.flapping.includes(l.id);
/* the tunnels that are not steady: the ones that are degraded, and the ones
   that keep flapping. A tunnel that is both is counted once. */
const handedOver = (l) => !!S.handover[l.id];
const ms = (v) => (v == null ? "—" : `${Math.round(v)}ms`);
const provider = (id) => S.providers.find((p) => p.id === id);
const nowS = () => Date.now() / 1000 + skew;
const linkName = (l) => (l ? `${nm(l.kharej_id)} ⇄ ${nm(l.iran_id)}` : "—");
const linkTag = (l) => (l && l.label ? ` (${l.label})` : "");
const exitName = (t) => (t.server_id ? nm(t.server_id) : t.addr);
const exitText = (e) => (e ? e.targets.map(exitName).join(", ") : "");
const exitBad = (e) => !!e && e.targets.some((t) => t.ok === false);
function serverExits(id) {
  const out = {};
  for (const l of S.links) if (l.kharej_id === id) for (const e of Object.values(l.exits || {})) out[`${e.port}|${e.method}`] ||= e;
  return Object.values(out).sort((a, b) => a.port - b.port);
}
function linkCell(l, extra = "") {
  const st = lstatus(l);
  const sub = [l.label, l.transport, st === "up" ? ms(l.latency_ms) : FA[st]].filter(Boolean).join(" · ");
  return `<span class="grow"><span>${esc(linkName(l))}</span><span class="sub mono">${esc(sub)}${extra}</span></span>`;
}
function ago(ts) {
  if (!ts) return "—";
  const s = Math.max(0, Math.round(nowS() - ts));
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}
function dur(s) {
  s = Math.max(0, Math.round(s));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${String(s % 60).padStart(2, "0")}s`;
  return `${Math.floor(m / 60)}h ${String(m % 60).padStart(2, "0")}m`;
}
function spark(history, w = 110, h = 22) {
  const pts = (history || []).slice(-40);
  if (pts.length < 2) return "";
  const vals = pts.map((p) => p[1]).filter((v) => v >= 0);
  const max = Math.max(10, ...vals) * 1.15;
  const step = w / (pts.length - 1);
  let d = "", fails = "";
  pts.forEach((p, i) => {
    const x = (i * step).toFixed(1);
    if (p[1] < 0) { fails += `<rect x="${x - 1}" y="0" width="2" height="${h}" fill="var(--down)" opacity=".7"/>`; return; }
    const y = (h - (p[1] / max) * (h - 3) - 1.5).toFixed(1);
    d += `${d && pts[i - 1][1] >= 0 ? "L" : "M"}${x},${y}`;
  });
  return `<svg width="${w}" height="${h}" viewBox="0 0 ${w} ${h}" style="display:block">${fails}<path d="${d}" fill="none" stroke="var(--muted)" stroke-width="1.4"/></svg>`;
}
const ICON = {
  add: `<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M12 5v14M5 12h14"/></svg>`,
  scan: `<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M4 8V5a1 1 0 0 1 1-1h3M16 4h3a1 1 0 0 1 1 1v3M20 16v3a1 1 0 0 1-1 1h-3M8 20H5a1 1 0 0 1-1-1v-3"/></svg>`,
  check: `<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M4 12.5l5 5L20 6.5"/></svg>`,
  zap: `<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M13 2L4.5 13.5H11L10 22l8.5-11.5H12z"/></svg>`,
  refresh: `<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M20 12a8 8 0 1 1-2.6-5.9M20 4v4h-4"/></svg>`,
  edit: `<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M4 20h4l10-10-4-4L4 16z"/></svg>`,
  trash: `<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13"/></svg>`,
};

/* ------------------------------------------------------------------ boot */
async function boot() {
  let sess;
  try { sess = await api("GET", "/api/session"); } catch (e) { toast(e.message, "error"); return; }
  $("#ss-ver").textContent = `v${sess.version}`;
  signedInAs = sess.username || "admin";
  if (sess.error) toast(sess.error, "error");
  if (!sess.logged_in) return showAuth(sess.configured);
  startApp(sess);
}

function showAuth(configured = true) {
  $("#app").classList.add("hidden");
  $("#auth").classList.remove("hidden");
  const setup = !configured;
  $("#auth-sub").textContent = setup ? "First run: create an administrator username and password" : "Sign in to the tunnel panel";
  $("#auth-btn").textContent = setup ? "Create the account and sign in" : "Sign in";
  $("#auth-form").onsubmit = async (e) => {
    e.preventDefault();
    const f = new FormData(e.target);
    try {
      await api("POST", setup ? "/api/setup" : "/api/login", { username: f.get("username"), password: f.get("password") });
      startApp();
    } catch (err) { $("#auth-err").textContent = err.message; }
  };
}

let firstState = true;
function startApp(sess) {
  $("#auth").classList.add("hidden");
  $("#app").classList.remove("hidden");
  if (sess) signedInAs = sess.username || signedInAs;
  $("#ss-user").textContent = signedInAs;
  api("GET", "/api/state").then(setState).catch(() => {});
  connectWS();
  route();
}

function connectWS() {
  if (ws) try { ws.close(); } catch (_) { /* ignore */ }
  const proto = location.protocol === "https:" ? "wss" : "ws";
  ws = new WebSocket(`${proto}://${location.host}/ws`);
  ws.onopen = () => { wsAlive = true; $("#live").classList.add("on"); $("#live-text").textContent = "Live"; };
  ws.onclose = (e) => {
    wsAlive = false; $("#live").classList.remove("on");
    $("#live-text").textContent = "Reconnecting…";
    if (e.code === 4401) return showAuth();
    setTimeout(connectWS, 2500);
  };
  ws.onmessage = (m) => {
    const msg = JSON.parse(m.data);
    if (msg.type === "state") setState(msg.state);
    // the feed of events is worked out by the panel now: a new event means
    // the page asks for it again, and the rows come with the answer
    else if (msg.type === "event") loadViewThenRender();
    else if (msg.type === "test" && drawer && drawer.type === "link" && drawer.id === msg.link_id) {
      const l = linkById(msg.link_id);
      if (l) { l.tests[msg.result.transport] = msg.result; renderDrawer(); }
    }
  };
}
setInterval(() => { if (wsAlive) try { ws.send("ping"); } catch (_) { /* ignore */ } }, 25000);

/* The dashboard is worked out by the panel: the parts of the five pages and
   the view of the canvas come from one reading of the state, and the page
   draws what it is given — the browser owns the pixels, the panel owns the
   truth. The panel holds only the truth about itself, and the dashboard
   holds only what the panel told it: the pages are the same truth in the
   other shape. */
let VIEW = null;   // the canvas: the cards, the columns, the wires
let PARTS = null;  // the pages: what an element holds, by its id
let viewSeq = 0;
async function loadView() {
  const mine = ++viewSeq;
  const params = new URLSearchParams({ q: netQuery, filter: netFilter, sort: $("#tun-sort").value, selected: selectedDomain });
  if ($("#show-pending-2").checked) params.set("pending", "1");
  const ev = $("#ev-filter .on");   // the level of the feed the operator picked
  if (ev) params.set("ev", ev.dataset.f);
  try {
    const answer = await api("GET", `/api/view?${params}`);
    if (mine === viewSeq) { VIEW = answer.network; PARTS = answer.parts; }
  } catch (_) { /* the page keeps the view it already has */ }
}

/* What a page holds is the panel's: this puts it in the element with that
   id. The buttons carry the same marks as before, so the clicks they
   answer are the same, and the few that need a handler are bound again. */
function applyParts() {
  if (!PARTS) return;
  for (const [id, html] of Object.entries(PARTS)) {
    const el = document.getElementById(id);
    if (!el) continue;
    if (id === "empty-hint") el.classList.toggle("hidden", !html); // no hint: nothing to show
    if (el.innerHTML !== html) el.innerHTML = html;
  }
  bindActions();
}

function setState(st) {
  S = st;
  skew = st.now - Date.now() / 1000;
  showErrors(st.errors || []);
  loadView().then(renderAll);
}

/* the problems the panel has with its own files: a state file or a login
   file that could not be read. They are said once, not at every check. */
let shownErrors = "";
function showErrors(list) {
  const text = (list || []).join(" · ");
  if (!text || text === shownErrors) return;
  shownErrors = text;
  toast(text, "error");
}

function renderAll() {
  if (!S) return;
  applyParts();   // the five pages, from the panel
  renderFlow();   // the canvas, from the panel
  renderDrawer(); // what the operator has open
}

// The search, the filter and the order are worked out by the panel: the
// controls only say what the operator picked, and ask for the page again.
function loadViewThenRender() { loadView().then(renderAll); }
$("#net-q").oninput = (e) => { netQuery = e.target.value; loadViewThenRender(); };
$("#net-filter").onchange = (e) => { netFilter = e.target.value; loadViewThenRender(); };
$("#net-search").onclick = () => { netQuery = $("#net-q").value; netFilter = $("#net-filter").value; loadViewThenRender(); };
$("#tun-sort").onchange = loadViewThenRender;
// the level of the feed is picked the same way: the button says which one
// it is (the one that is on), and the rows are the panel's
$$("#ev-filter button").forEach((b) => (b.onclick = () => {
  $$("#ev-filter button").forEach((x) => x.classList.toggle("on", x === b));
  loadViewThenRender();
}));

/* ---------------------------------------------------------------- router */
function route() {
  const id = (location.hash || "").replace(/^#\//, "") || "network";
  view = VIEWS[id] ? id : "network";
  for (const p of $$(".page")) p.hidden = p.id !== `view-${view}`;
  for (const a of $$("#nav a")) a.classList.toggle("on", a.dataset.view === view);
  $("#crumb-page").textContent = VIEWS[view].title;
  document.title = `bull-Tunnel · ${VIEWS[view].title}`;
  if (view === "network") requestAnimationFrame(() => { drawWires(); fit(); });
}
window.addEventListener("hashchange", route);

/* --------------------------------------------------------------- sidebar */
/* the strip of counters under the page title */
/* the buttons in the header of every page */
/* The buttons of a page come with the parts; what they do is the script's:
   the handlers are bound to the ids, and the parts are swapped under them. */
function bindActions() {
  const scan = $("#btn-scan"), check = $("#btn-check"), discover = $("#btn-discover");
  if (!scan) return;
  scan.onclick = () => act(scanNow, "Scan started");
  check.onclick = () => act(() => api("POST", "/api/check", {}), "Check started");
  discover.onclick = () => act(discoverNow, "Test and connection started");
  const scan2 = $("#btn-scan-2"), check2 = $("#btn-check-2"), discover2 = $("#btn-discover-2");
  if (scan2) scan2.onclick = scan.onclick;
  if (check2) check2.onclick = check.onclick;
  if (discover2) discover2.onclick = discover.onclick;
  const edit = $("#btn-settings");
  if (edit) edit.onclick = () => openForm("settings");
  $$("#net-actions .menu > .menu-btn, #tun-actions .menu > .menu-btn, #dom-actions .menu > .menu-btn, #inf-actions .menu > .menu-btn")
    .forEach((b) => (b.onclick = (e) => { e.stopPropagation(); toggleMenu(b); }));
}

function toggleMenu(btn) {
  const pop = btn.parentElement.querySelector(".menu-pop");
  const was = pop.classList.contains("open");
  $$(".menu-pop").forEach((p) => p.classList.remove("open"));
  if (!was) pop.classList.add("open");
}
function closeMenus() {
  $$(".menu-pop").forEach((p) => p.classList.remove("open"));
}
async function scanNow() {
  const r = await act(() => api("POST", "/api/scan"));
  toast(r.added ? `${r.added} existing tunnels were found and added` : "No new tunnel was found", "success");
}
function discoverNow() {
  const n = S.links.filter((l) => l.status === "pending" || l.status === "down").length;
  if (!n) return toast("There is no tunnel that is not built or that is down; the healthy tunnels are left alone");
  if (!confirm(`${n} tunnels that are not built or that are down will be built/rebuilt by testing every protocol. The healthy tunnels are left alone. Continue?`)) return;
  act(() => api("POST", "/api/discover", {}), "Building the tunnels has started");
}

/* ================================================================ the flow */
const stage = $("#stage"), world = $("#world"), nodesEl = $("#nodes"), wiresEl = $("#wires"), colsEl = $("#cols");
const cam = { x: 40, y: 58, z: 1 };
const pos = {};             // node id -> [x, y] in world units
const els = {};             // node id -> element
let viewRestored = false;
try {
  const saved = localStorage.getItem("bt-view");
  if (saved) {
    Object.assign(cam, JSON.parse(saved));
    // the zoom tools sit in the top left corner: keep the column titles
    // of the graph out of their way
    cam.x = Math.max(8, cam.x);
    cam.y = Math.max(58, cam.y);
    viewRestored = true;
  }
} catch (_) { /* ignore */ }

function applyView() {
  world.style.transform = `translate(${cam.x}px,${cam.y}px) scale(${cam.z})`;
  $("#zoom-val").textContent = `${Math.round(cam.z * 100)}%`;
  clearTimeout(applyView.t);
  applyView.t = setTimeout(() => { try { localStorage.setItem("bt-view", JSON.stringify({ ...cam, cw: stage.clientWidth })); } catch (_) { /* ignore */ } }, 300);
}
function zoomAt(cx, cy, f) {
  const r = stage.getBoundingClientRect();
  const px = cx - r.left, py = cy - r.top;
  const nz = Math.min(2.5, Math.max(0.2, cam.z * f));
  cam.x = px - (px - cam.x) * (nz / cam.z);
  cam.y = py - (py - cam.y) * (nz / cam.z);
  cam.z = nz;
  applyView();
}
function fit() {
  const ids = Object.keys(els);
  if (!ids.length) { Object.assign(cam, { x: 40, y: 58, z: 1 }); return applyView(); }
  let x0 = 1e9, y0 = 1e9, x1 = -1e9, y1 = -1e9;
  for (const id of ids) {
    const [x, y] = pos[id]; const el = els[id];
    x0 = Math.min(x0, x); y0 = Math.min(y0, y - 46); x1 = Math.max(x1, x + el.offsetWidth); y1 = Math.max(y1, y + el.offsetHeight);
  }
  for (const g of $$(".wlabel.hop", wiresEl)) {
    const m = /translate\(([-\d.]+),([-\d.]+)\)/.exec(g.getAttribute("transform") || "");
    if (m) { x0 = Math.min(x0, +m[1]); x1 = Math.max(x1, +m[1] + g.getBBox().width); }
  }
  const r = stage.getBoundingClientRect();
  const top = 58, bottom = 26, pad = 22;
  const z = Math.min(1.1, (r.width - pad * 2) / (x1 - x0), (r.height - top - bottom) / (y1 - y0));
  cam.z = Math.max(0.2, z);
  cam.x = (r.width - (x1 - x0) * cam.z) / 2 - x0 * cam.z;
  cam.y = top + (r.height - top - bottom - (y1 - y0) * cam.z) / 2 - y0 * cam.z;
  applyView();
}

/* The cards, their places and the wires between them are worked out by the
   panel (VIEW): the browser puts a card where the panel says, keeps the hand
   of the operator on the card he is dragging, and draws the wires between
   the cards it can measure. */
function renderFlow() {
  if (!S || !VIEW) return;
  const seen = new Set();
  for (const n of VIEW.nodes) {
    seen.add(n.id);
    let el = els[n.id];
    if (!el) {
      el = document.createElement("div");
      el.dataset.id = n.id;
      el.dataset.type = n.type;
      el.dataset.col = n.col;
      nodesEl.appendChild(el);
      els[n.id] = el;
      bindDrag(el);
    }
    if (el.dataset.html !== n.html) { el.innerHTML = n.html; el.dataset.html = n.html; }
    const keep = ["dragging", "hl", "sel"].filter((c) => el.classList.contains(c)).join(" ");
    const cls = `${n.class} ${keep}`.trim();
    if (el.className !== cls) el.className = cls;
    if (el.classList.contains("dragging")) continue; // a hand is on the card
    pos[n.id] = [n.x, n.y];
    el.style.left = `${n.x}px`;
    el.style.top = `${n.y}px`;
    el.style.height = `${n.height}px`;
  }
  for (const id of Object.keys(els)) if (!seen.has(id)) { els[id].remove(); delete els[id]; delete pos[id]; }

  renderColumns();
  $("#flow-badge").innerHTML = `<i class="dot ok"></i><span>${esc(VIEW.badge)}</span>`;
  if (firstState) {
    firstState = false;
    const sameScreen = cam.cw && Math.abs(cam.cw - stage.clientWidth) / stage.clientWidth < 0.3;
    if (viewRestored && sameScreen) applyView(); else requestAnimationFrame(() => { drawWiresNow(); fit(); });
  }
  drawWires();
}

/* The title above a column: what it is called and how many cards it holds
   comes from the panel, and where it sits follows the cards — the operator
   may have dragged one. */
function renderColumns() {
  const list = (VIEW && VIEW.columns) || [];
  let titles = $$(".ct", colsEl);
  if (!titles.length) {
    for (const col of ["kharej", "iran", "domain"]) {
      const t = document.createElement("div");
      t.className = "ct"; t.dataset.col = col;
      colsEl.appendChild(t);
    }
    titles = $$(".ct", colsEl);
  }
  for (const t of titles) {
    const col = list.find((c) => c.col === t.dataset.col);
    if (!col) { t.style.display = "none"; continue; }
    t.style.display = "";
    t.innerHTML = `<span class="n">${String(col.count).padStart(2, "0")} / ${esc(col.kicker)}</span><span class="t">${esc(col.title)}</span>`;
    let x = col.x, y = col.y;
    for (const id of Object.keys(els)) {
      if (els[id].dataset.col !== col.col || !pos[id]) continue;
      x = Math.min(x, pos[id][0]);
      y = Math.min(y, pos[id][1] - 40);
    }
    t.style.left = `${x}px`;
    t.style.top = `${y}px`;
    t.style.width = `${col.width}px`;
  }
}

function box(id) {
  const el = els[id];
  if (!el || !pos[id]) return null;
  const [x, y] = pos[id];
  return { x, y, w: el.offsetWidth, h: el.offsetHeight, cx: x + el.offsetWidth / 2, cy: y + el.offsetHeight / 2 };
}
function bezier(x1, y1, x2, y2, dir1, dir2, bulge) {
  const dx = bulge || Math.max(50, Math.abs(x2 - x1) * 0.45);
  const p = [x1, y1, x1 + dx * dir1, y1, x2 + dx * dir2, y2, x2, y2];
  return { d: `M${p[0]},${p[1]} C${p[2]},${p[3]} ${p[4]},${p[5]} ${p[6]},${p[7]}`, p };
}
function bezAt(p, t) {
  const u = 1 - t;
  return [
    u * u * u * p[0] + 3 * u * u * t * p[2] + 3 * u * t * t * p[4] + t * t * t * p[6],
    u * u * u * p[1] + 3 * u * u * t * p[3] + 3 * u * t * t * p[5] + t * t * t * p[7],
  ];
}

let wiresQueued = false;
function drawWires() {
  if (wiresQueued) return;
  wiresQueued = true;
  requestAnimationFrame(() => { wiresQueued = false; drawWiresNow(); });
}
/* The wires between the cards: which wires exist, what they are called and
   what colour they are comes from the panel (VIEW.edges). The browser draws
   the curve between the two cards, and where it meets them. */
function drawWiresNow() {
  if (!S || !VIEW) return;
  const showStandby = $("#show-standby").checked;
  const showPending = $("#show-pending").checked;
  const parts = [], labels = [];
  const edges = [];
  for (const w of VIEW.edges) {
    if (w.status === "pending" && !showPending) continue;
    if (w.standby && !showStandby) continue;
    const a = box(w.from), b = box(w.to);
    if (!a || !b) continue;
    edges.push({ w, a, b });
  }
  const sides = {};
  for (const e of edges) {
    e.loop = e.w.kind === "hop" && Math.abs(e.b.cx - e.a.cx) < NODE_W;
    e.dir1 = e.loop ? -1 : e.b.cx >= e.a.cx ? 1 : -1;
    e.dir2 = e.loop ? -1 : -e.dir1;
    (sides[`${e.w.from}|${e.dir1}`] ||= []).push([e, "a"]);
    (sides[`${e.w.to}|${e.dir2}`] ||= []).push([e, "b"]);
  }
  for (const list of Object.values(sides)) {
    list.sort((p, q) => (p[1] === "a" ? p[0].b.cy : p[0].a.cy) - (q[1] === "a" ? q[0].b.cy : q[0].a.cy));
    list.forEach(([e, end], i) => {
      const bx = e[end];
      e[`y_${end}`] = bx.y + bx.h * (0.22 + (0.56 * (i + 1)) / (list.length + 1));
    });
  }
  const placed = [];
  const placeLabel = (c, w) => {
    for (const t of [0.5, 0.38, 0.62, 0.3, 0.7, 0.44, 0.56]) {
      const [x, y] = bezAt(c.p, t);
      const r = [x - w / 2, y - 10, x + w / 2, y + 10];
      if (!placed.some((q) => r[0] < q[2] && q[0] < r[2] && r[1] < q[3] && q[1] < r[3])) { placed.push(r); return [x, y]; }
    }
    return bezAt(c.p, 0.5);
  };
  /* The label of a wire: a pill when the wire is the one that is picked,
     plain text on the canvas for the others — the type-1 design
     (new-ui/type1/graphs.svg). */
  const wireLabel = (txt, x, y, cls, attrs, pill) => {
    const w = txt.length * 6.6;
    if (!pill) {
      return `<g class="wlabel plain ${cls}" ${attrs} transform="translate(${x.toFixed(1)},${y.toFixed(1)})">`
        + `<text x="0" y="0" text-anchor="middle" dominant-baseline="middle">${esc(txt)}</text></g>`;
    }
    const pw = w + 22;
    return `<g class="wlabel ${cls}" ${attrs} transform="translate(${(x - pw / 2).toFixed(1)},${(y - 11).toFixed(1)})">`
      + `<rect width="${pw.toFixed(1)}" height="22" rx="11"/>`
      + `<text x="${(pw / 2).toFixed(1)}" y="15" text-anchor="middle">${esc(txt)}</text></g>`;
  };
  for (const e of edges) {
    const w = e.w;
    const x1 = e.dir1 > 0 ? e.a.x + e.a.w : e.a.x;
    const x2 = e.dir2 > 0 ? e.b.x + e.b.w : e.b.x;
    const c = bezier(x1, e.y_a, x2, e.y_b, e.dir1, e.dir2, e.loop ? 30 + Math.abs(e.y_b - e.y_a) * 0.1 : 0);
    if (w.kind === "hop") {
      parts.push(`<path class="xwire ${w.down ? "down" : ""}" d="${c.d}" data-hop="${w.ref}"/>`);
      const wd = w.label.length * 6.6;
      const [lx, ly] = e.loop ? ((p) => [p[0] - wd / 2 - 4, p[1]])(bezAt(c.p, 0.5)) : placeLabel(c, wd);
      labels.push(wireLabel(w.label, lx, ly, `hop ${w.down ? "down" : ""}`, `data-hop="${w.ref}"`, false));
    } else if (w.kind === "link") {
      const sel = selLink === w.ref;
      const cls = `wire ${w.moved ? "handover" : w.status}${sel ? " sel" : ""}${w.standby ? " standby" : ""}`;
      parts.push(`<path class="${cls}" d="${c.d}" data-link="${w.ref}"/>`);
      parts.push(`<path class="hit" d="${c.d}" data-link="${w.ref}"/>`);
      // the points where the wire meets the card, in the colour of the wire
      parts.push(`<circle class="anchor" cx="${x1.toFixed(1)}" cy="${e.y_a.toFixed(1)}" r="4" fill="${w.colour}"/>`);
      parts.push(`<circle class="anchor" cx="${x2.toFixed(1)}" cy="${e.y_b.toFixed(1)}" r="4" fill="${w.colour}"/>`);
      const wd = w.label.length * 6.6;
      const [lx, ly] = placeLabel(c, wd);
      labels.push(wireLabel(w.label, lx, ly, `${w.moved ? "handover" : w.status}${sel ? " sel" : ""}${w.standby ? " standby" : ""}`,
        `data-link="${w.ref}"`, sel));
    } else {
      parts.push(`<path class="dwire ${w.status}" d="${c.d}" data-domain="${w.ref}"/>`);
      // the domain card is reached from the left, and the point is marked too
      parts.push(`<circle class="anchor" cx="${x2.toFixed(1)}" cy="${e.y_b.toFixed(1)}" r="4" fill="var(--muted)"/>`);
    }
  }
  wiresEl.innerHTML = `<defs><marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="5" markerHeight="5" orient="auto-start-reverse"><path d="M0 0L10 5L0 10z" fill="var(--muted)"/></marker></defs>` + parts.join("") + labels.join("");
  applyFocus();
  renderFlowStatus();
}

function renderFlowStatus() {
  const el = $("#flow-status");
  if (!el) return;
  if (selLink) {
    const l = linkById(selLink);
    if (l) {
      el.innerHTML = `SELECTED: <b>${esc(linkName(l))}</b> · ${esc(l.transport || "—")}${l.latency_ms != null ? ` · ${ms(l.latency_ms)}` : ""}`;
      return;
    }
  }
  if (!selNode) { el.textContent = "Click a node or a wire to follow its path."; return; }
  if (els[selNode] && els[selNode].dataset.type === "domain") {
    const d = S.domains.find((x) => x.id === selNode);
    el.innerHTML = d ? `SELECTED: <b>${esc(d.fqdn)}</b>` : "";
    return;
  }
  const s = srv(selNode);
  const links = S.links.filter((l) => l.iran_id === selNode || l.kharej_id === selNode);
  el.innerHTML = s ? `SELECTED: <b>${esc(s.name)}</b> · ${links.length} tunnel${links.length === 1 ? "" : "s"}` : "";
}
/* the selection is an edge, never a different colour: a 1px brand border */
function paintSelection() {
  for (const [id, node] of Object.entries(els)) node.classList.toggle("sel", id === selNode);
  renderFlowStatus();
  drawWires();
}

/* drag nodes; pan & zoom the stage */
function bindDrag(el) {
  el.addEventListener("pointerdown", (e) => {
    if (e.button !== 0) return;
    e.stopPropagation();
    const id = el.dataset.id;
    const start = { cx: e.clientX, cy: e.clientY, x: pos[id][0], y: pos[id][1] };
    let moved = false;
    el.setPointerCapture(e.pointerId);
    const move = (ev) => {
      const dx = (ev.clientX - start.cx) / cam.z, dy = (ev.clientY - start.cy) / cam.z;
      if (!moved && Math.hypot(ev.clientX - start.cx, ev.clientY - start.cy) < 4) return;
      moved = true;
      el.classList.add("dragging");
      tip.classList.remove("show");
      pos[id] = [Math.round(start.x + dx), Math.round(start.y + dy)];
      el.style.left = `${pos[id][0]}px`; el.style.top = `${pos[id][1]}px`;
      drawWires();
    };
    const up = () => {
      el.removeEventListener("pointermove", move);
      el.removeEventListener("pointerup", up);
      el.removeEventListener("pointercancel", up);
      el.classList.remove("dragging");
      if (moved) { saveLayout({ [id]: pos[id] }); renderColumns(); }
      else { selNode = id; selLink = null; paintSelection(); openDrawer(el.dataset.type, id); }
    };
    el.addEventListener("pointermove", move);
    el.addEventListener("pointerup", up);
    el.addEventListener("pointercancel", up);
  });
}
let pendingLayout = {};
function saveLayout(p) {
  Object.assign(pendingLayout, p);
  Object.assign(S.layout, p);
  clearTimeout(saveLayout.t);
  saveLayout.t = setTimeout(() => {
    const body = { positions: pendingLayout };
    pendingLayout = {};
    api("PUT", "/api/layout", body).catch(() => {});
  }, 400);
}

const pointers = new Map();
let pan = null, pinch = null;
stage.addEventListener("pointerdown", (e) => {
  if (e.target.closest(".flow-tools,.fnode,.wlabel,.hit")) return;
  pointers.set(e.pointerId, { x: e.clientX, y: e.clientY });
  stage.setPointerCapture(e.pointerId);
  if (pointers.size === 1) { pan = { x: e.clientX, y: e.clientY, vx: cam.x, vy: cam.y }; stage.classList.add("panning"); }
  else if (pointers.size === 2) {
    const [p, q] = [...pointers.values()];
    pinch = { d: Math.hypot(p.x - q.x, p.y - q.y), z: cam.z };
    pan = null;
  }
});
stage.addEventListener("pointermove", (e) => {
  if (!pointers.has(e.pointerId)) return;
  pointers.set(e.pointerId, { x: e.clientX, y: e.clientY });
  if (pinch && pointers.size === 2) {
    const [p, q] = [...pointers.values()];
    const d = Math.hypot(p.x - q.x, p.y - q.y);
    zoomAt((p.x + q.x) / 2, (p.y + q.y) / 2, (pinch.z * d) / pinch.d / cam.z);
  } else if (pan) {
    cam.x = pan.vx + e.clientX - pan.x;
    cam.y = pan.vy + e.clientY - pan.y;
    applyView();
  }
});
const endPointer = (e) => {
  pointers.delete(e.pointerId);
  if (pointers.size < 2) pinch = null;
  if (!pointers.size) { pan = null; stage.classList.remove("panning"); }
};
stage.addEventListener("pointerup", endPointer);
stage.addEventListener("pointercancel", endPointer);
stage.addEventListener("wheel", (e) => {
  e.preventDefault();
  zoomAt(e.clientX, e.clientY, Math.exp(-e.deltaY * (e.ctrlKey ? 0.01 : 0.0015)));
}, { passive: false });
$("#tools").addEventListener("click", async (e) => {
  const b = e.target.closest("[data-zoom]");
  if (!b) return;
  const r = stage.getBoundingClientRect();
  const z = b.dataset.zoom;
  if (z === "in") zoomAt(r.left + r.width / 2, r.top + r.height / 2, 1.2);
  else if (z === "out") zoomAt(r.left + r.width / 2, r.top + r.height / 2, 1 / 1.2);
  else if (z === "fit") fit();
  else if (z === "reset") {
    if (!confirm("Reset the server layout to the default?")) return;
    await api("DELETE", "/api/layout");
    for (const id of Object.keys(pos)) delete pos[id];
    S.layout = {};
    renderFlow();
    requestAnimationFrame(fit);
  }
});
$("#show-standby").onchange = drawWires;
$("#show-pending").onchange = (e) => { $("#show-pending-2").checked = e.target.checked; drawWires(); loadViewThenRender(); };
$("#show-pending-2").onchange = (e) => { $("#show-pending").checked = e.target.checked; drawWires(); loadViewThenRender(); };

/* hover: light up a node's whole path, dim the rest */
let focus = null;
function relatedTo(type, id) {
  const nodes = new Set([id]), links = new Set(), domains = new Set();
  const addLink = (l) => { if (!l) return; links.add(l.id); nodes.add(l.iran_id); nodes.add(l.kharej_id); };
  if (type === "domain") {
    const d = S.domains.find((x) => x.id === id);
    if (d) {
      domains.add(d.id); if (d.iran_id) nodes.add(d.iran_id);
      for (const r of d.routes || []) { addLink(linkById(r.link_id)); for (const t of (r.exit || {}).targets || []) if (t.server_id) nodes.add(t.server_id); }
    }
  } else if (type === "server") {
    for (const l of S.links) if (l.iran_id === id || l.kharej_id === id) addLink(l);
    for (const k of byRole("kharej")) for (const e of serverExits(k.id)) for (const t of e.targets) {
      if (k.id === id && t.server_id) nodes.add(t.server_id);
      if (t.server_id === id) nodes.add(k.id);
    }
    for (const d of S.domains) {
      const via = (d.routes || []).map((r) => linkById(r.link_id)).filter(Boolean);
      if (d.iran_id === id || via.some((l) => l.kharej_id === id)) { domains.add(d.id); nodes.add(d.id); }
    }
  } else if (type === "link") {
    addLink(linkById(id));
    for (const d of S.domains) if ((d.routes || []).some((r) => r.link_id === id)) { domains.add(d.id); nodes.add(d.id); }
  }
  return { nodes, links, domains };
}
function applyFocus() {
  world.classList.toggle("focus", !!focus);
  for (const [id, el] of Object.entries(els)) el.classList.toggle("hl", !!focus && focus.nodes.has(id));
  $$("#wires [data-link], #wires [data-domain], #wires [data-hop]").forEach((el) => {
    if (el.classList.contains("hit")) return;
    if (!focus) { el.classList.remove("dim", "hl"); return; }
    const hop = el.dataset.hop && el.dataset.hop.split("|");
    const on = hop ? focus.nodes.has(hop[0]) && focus.nodes.has(hop[1])
      : el.dataset.link ? focus.links.has(el.dataset.link) : focus.domains.has(el.dataset.domain);
    el.classList.toggle("dim", !on);
    el.classList.toggle("hl", on && el.classList.contains("wire"));
  });
}
function linkTip(l) {
  const best = Object.values(l.tests || {}).filter((t) => t.ok).sort((a, b) => a.latency_ms - b.latency_ms).slice(0, 3)
    .map((t) => `${t.transport} ${ms(t.latency_ms)}`).join(", ");
  return `<b>${esc(linkName(l))}</b>${esc(linkTag(l))}<br>Status: ${FA[lstatus(l)] || l.status}${isFlapping(l) ? " — <span style='color:var(--degraded)'>keeps flapping</span>" : ""}<br>
    Protocol: <span class="mono">${esc(l.transport || "—")}</span>${l.pinned ? " (pinned)" : ""} · Latency: <span class="mono">${ms(l.latency_ms)}</span><br>
    Ports: <span class="mono">${esc((l.exposed || []).join(", ") || "—")}</span>
    ${l.error ? `<br><span style="color:var(--down)">${esc(l.error)}</span>` : ""}${best ? `<br><span class="muted">best: ${esc(best)}</span>` : ""}`;
}

/* --------------------------------------------------------- tunnels table */

/* --------------------------------------------------------------- events */
/* the feed of the log comes with the parts: GET /api/view holds ev-list,
   and the buttons only say which level the operator picked */

/* ================================================== tunnels & protocols */

/* ============================================================== domains */
/* ========================================================= infrastructure */
/* ============================================================== settings */
/* save the quick settings of the settings page */
async function saveQuickSettings() {
  const s = S.settings;
  const get = (k) => $(`[data-set="${k}"]`);
  const observe = !!get("observe_only").checked;
  if (s.observe_only && !observe && !confirm("Turn observe mode off? From now on the panel changes the DNS record itself when something fails"
    + (get("auto_repair").checked ? " and rebuilds the broken tunnels with another protocol" : "") + ".")) {
    get("observe_only").checked = true;  // leave the panel watching
    return;
  }
  const body = {
    observe_only: observe,
    check_interval: +get("check_interval").value,
    fail_threshold: +get("fail_threshold").value,
    failback_after: Math.round(+get("failback_after").value * 60),
    test_timeout: +get("test_timeout").value,
    flap_count: +get("flap_count").value,
    flap_window: Math.round(+get("flap_window").value * 60),
    auto_repair: !!get("auto_repair").checked,
    auto_adopt: !!get("auto_adopt").checked,
    auto_mesh: !!get("auto_mesh").checked,
    scan_interval: Math.round(+get("scan_interval").value * 60),
    engine_release: get("engine_release").value.trim() || "latest",
    engine_source: get("engine_source").value.trim(),
    install_engine: !!get("install_engine").checked,
    tunnel_port_base: +get("tunnel_port_base").value,
    probe_port_base: +get("probe_port_base").value,
    candidate_transports: $$("[data-set-tr]").filter((x) => x.checked).map((x) => x.value),
    backup_enabled: !!get("backup_enabled").checked,
    backup_time: get("backup_time").value.trim(),
    backup_tz: get("backup_tz").value.trim() || "Asia/Tehran",
    telegram: { ...s.telegram, enabled: !!get("tg_enabled").checked },
  };
  if (!body.candidate_transports.length) return toast("Pick at least one protocol", "error");
  // The saves go one after another. Two at the same time would write over
  // each other, and the one that arrived last would be the one that
  // counted — which is not necessarily the one the operator changed last.
  const save = (settingsSave || Promise.resolve()).then(() => api("PUT", "/api/settings", body));
  settingsSave = save.catch(() => {});
  try {
    await save;
    toast("Settings saved", "success");
  } catch (e) {
    toast(e.message, "error");
  }
}

/* ------------------------------------------------------------ onboarding */
/* ---------------------------------------------------------------- drawer */
function openDrawer(type, id) {
  drawer = { type, id };
  selTransport = null;
  groupDraft = null;
  $("#drawer").classList.add("open");
  $("#scrim").classList.add("open");
  renderDrawer(true);
}
function closeDrawer() {
  drawer = null;
  $("#drawer").classList.remove("open");
  $("#scrim").classList.remove("open");
}
function renderDrawer(force) {
  if (!drawer || !S) return;
  const body = $("#drawer-body");
  if (!force && (groupDraft || (body.contains(document.activeElement) && document.activeElement.matches("input,textarea,select")))) return;
  const keep = $("#drawer").scrollTop;
  const html = drawer.type === "link" ? linkDrawer() : drawer.type === "server" ? serverDrawer() : domainDrawer();
  if (!html) return closeDrawer();
  body.innerHTML = html;
  $("#drawer").scrollTop = keep;
}
const head = (title, sub, status) => `<div class="d-head"><div style="flex:1;min-width:0"><h2>${title}</h2><div class="d-sub">${sub}</div></div>
  <div class="right">${status ? `<span class="chip"><span class="dot ${status}"></span>${FA[status] || status}</span>` : ""}<button class="btn icon" data-close aria-label="Close">✕</button></div></div>`;

function linkDrawer() {
  const l = linkById(drawer.id);
  if (!l) return "";
  const st = lstatus(l);
  const doms = S.domains.filter((d) => (d.routes || []).some((r) => r.link_id === l.id));
  const cand = S.settings.candidate_transports;
  const all = [...cand, ...S.transports.filter((t) => !cand.includes(t))];
  const sel = selTransport || l.transport;
  const grid = all.map((t) => {
    const r = l.tests[t];
    const has = !l.engine_transports || !l.engine_transports.length || l.engine_transports.includes(t);
    const cls = ["topt", t === l.transport ? "cur" : "", t === sel ? "sel" : "", r ? (r.ok ? "ok" : "bad") : "", has ? "" : "off"].join(" ");
    const res = !has ? "not in the engine" : r ? (r.ok ? `${ms(r.latency_ms)} · ${r.connect_s ?? "?"}s` : "failed") : "not tested";
    return `<div class="${cls}" data-pick="${t}" title="${esc(r && r.error ? r.error : "")}"><span class="n">${esc(TNAME[t] || t)}</span><span class="r">${res}</span></div>`;
  }).join("");
  const carriedFrom = Object.entries(S.handover).filter(([, v]) => v === l.id).map(([k]) => linkById(k)).filter(Boolean);
  const minutes = Math.round(S.settings.failback_after / 60);
  const handBox = handedOver(l)
    ? `<div class="box"><h4>Port handover</h4><p class="note">The ports of this tunnel are open on <b>${esc(linkName(linkById(S.handover[l.id])))}</b> (the same Iran server) right now, because this tunnel had a problem. They come back on their own once it stays up for ${minutes} minutes without a drop.</p>
       <div class="btns"><button class="btn sm" data-release="${l.id}">Bring them back now</button></div></div>`
    : carriedFrom.length ? `<div class="box"><h4>Port handover</h4><p class="note">This tunnel also carries the ports of ${carriedFrom.map((x) => `<b>${esc(linkName(x))}</b>`).join(", ")}.</p></div>` : "";
  const allowed = l.allowed && l.allowed.length ? l.allowed : cand;
  return `${head(esc(linkName(l)), `${l.managed ? "built by the panel" : "existing tunnel on the server"} · <span class="mono">${esc(l.iran_name)}</span>${l.label && l.label !== l.iran_name ? ` · ${esc(l.label)}` : ""}`, st)}
  <div class="box"><h4>Status</h4><dl class="kv">
    <dt>Protocol</dt><dd><span class="mono">${esc(l.transport || "—")}</span> ${l.pinned ? "🔒 pinned" : "<span class='muted'>(automatic)</span>"}</dd>
    <dt>Latency</dt><dd class="mono">${ms(l.latency_ms)}</dd>
    <dt>Stability</dt><dd>${isFlapping(l) ? "<span class='chip warn'>keeps flapping</span>" : l.status === "up" && l.up_since ? `up for ${ago(l.up_since)}` : "—"}${l.last_fail ? ` <span class="muted small">· last drop ${ago(l.last_fail)}</span>` : ""}</dd>
    <dt>Tunnel port</dt><dd class="mono">${l.tunnel_port}</dd>
    <dt>Health check</dt><dd>${l.probe_port ? `full-path probe <span class="mono muted">127.0.0.1:${l.probe_port}</span>` : "from the tunnel connection (the full probe is added with the first config rewrite)"}</dd>
    <dt>Domains</dt><dd>${doms.map((d) => `<span class="chip">${esc(d.fqdn)}</span>`).join(" ") || "<span class='muted'>— standby</span>"}</dd>
    ${l.error ? `<dt>Error</dt><dd style="color:var(--down)">${esc(l.error)}</dd>` : ""}
  </dl>${spark(l.history, 430, 40)}</div>
  ${handBox}
  <div class="box"><h4>Forwarded ports — Iran server side (${esc(nm(l.iran_id))})</h4>
    <textarea id="lp-ports" placeholder="443&#10;8443=2096&#10;2053-2060">${esc((l.ports || []).join("\n"))}</textarea>
    <p class="hint">One port per line: “<span class="mono">443</span>” means port 443 of this Iran server goes to the same port on the foreign server; “<span class="mono">8443=2096</span>” means 8443 inside goes to 2096 outside; “<span class="mono">2053-2060</span>” is a range.</p>
    ${Object.keys(l.exits || {}).length ? `<div class="opt-list">${Object.entries(l.exits).map(([spec, e]) => `<div class="opt"><span class="dot ${exitBad(e) ? "down" : "up"}"></span><span class="grow"><span style="text-align:left"><span class="mono">${esc(spec)}</span> → ${esc(nm(l.kharej_id))} ↪ ${esc(exitText(e))}</span>${exitLine(e)}</span></div>`).join("")}</div>` : ""}
    <label class="check"><input type="checkbox" id="lp-udp" ${l.accept_udp ? "checked" : ""}> Forward UDP as well</label>
    <label class="check"><input type="checkbox" id="lp-all"> Set the same ports on every tunnel of ${esc(nm(l.kharej_id))}</label>
    <div class="btns"><button class="btn primary sm" data-save-ports="${l.id}">Save and apply</button></div>
  </div>
  <div class="box"><h4>Connection protocol</h4>
    <div class="tgrid">${grid}</div>
    <label class="check"><input type="checkbox" id="pin-on-apply" ${l.pinned ? "checked" : ""}> Pin this protocol (do not switch it on a failure)</label>
    <div class="btns">
      <button class="btn primary sm" data-apply-transport ${sel === l.transport && st === "up" ? "disabled" : ""}>Apply ${esc(TNAME[sel] || sel || "")}</button>
      <button class="btn sm" data-retest>Test every protocol</button>
      ${l.pinned ? `<button class="btn sm ghost" data-unpin>Release the pin</button>` : ""}
    </div>
    <label>The protocols the automatic repair of this tunnel tries</label>
    <div class="tchecks">${all.map((t) => `<label><input type="checkbox" class="lp-allowed" value="${t}" ${allowed.includes(t) ? "checked" : ""}>${t}</label>`).join("")}</div>
  </div>
  <div class="box"><h4>Settings</h4>
    <div class="grid2"><label>Display name<input id="lp-label" value="${esc(l.label)}"></label><label>Priority among the tunnels of this pair<input id="lp-prio" type="number" value="${l.priority}"></label></div>
    <div class="btns"><button class="btn sm" data-save-link="${l.id}">Save</button>
      ${l.managed ? `<button class="btn sm danger" data-del-link="${l.id}" data-purge="1">Delete the tunnel</button>`
        : `<button class="btn sm" data-del-link="${l.id}">Forget it (it stays on the server)</button><button class="btn sm danger" data-del-link="${l.id}" data-purge="1">Delete it from the servers</button>`}</div>
  </div>`;
}

function serverDrawer() {
  const s = srv(drawer.id);
  if (!s) return "";
  const links = S.links.filter((l) => l.iran_id === s.id || l.kharej_id === s.id);
  const other = (l) => nm(s.role === "iran" ? l.kharej_id : l.iran_id);
  const row = (l) => {
    const st = lstatus(l);
    const sub = [l.label, l.transport, st === "up" ? ms(l.latency_ms) : FA[st]].filter(Boolean).join(" · ");
    return `<div class="opt click" data-link="${l.id}"><span class="dot ${st}"></span>
      <span class="grow"><span>${esc(other(l))}</span><span class="sub mono">${esc(sub)}</span></span></div>`;
  };
  const built = links.filter((l) => l.status !== "pending"), offers = links.filter((l) => l.status === "pending");
  const broken = built.filter((l) => l.status !== "up");
  return `${head(esc(s.name), `<span class="mono">${esc(s.ip)}</span> · ${s.role === "iran" ? "Iran" : "Foreign"} server`, s.status)}
  <div class="box"><h4>Details</h4><dl class="kv">
    <dt>SSH</dt><dd class="mono">${esc(s.ssh_user)}@${esc(s.ip)}:${s.ssh_port}</dd>
    <dt>Host key</dt><dd class="mono">${esc(s.host_key_seen || s.host_key || "—")}</dd>
    <dt>SSH latency</dt><dd class="mono">${ms(s.ssh_latency_ms)}</dd>
    <dt>Last seen</dt><dd>${ago(s.last_seen)}</dd>
    <dt>Tunnel engine</dt><dd class="mono">${esc(s.engine_version || "—")}</dd>
    <dt>Last tunnel scan</dt><dd>${ago(s.last_scan)}</dd>
    <dt>Priority</dt><dd>${s.priority} ${s.enabled ? "" : "<span class='chip warn'>disabled</span>"}</dd>
    ${s.error ? `<dt>Error</dt><dd style="color:var(--down)">${esc(s.error)}</dd>` : ""}
  </dl>
  <div class="btns"><button class="btn sm" data-edit-server="${s.id}">${ICON.edit} Edit</button><button class="btn sm" data-test-server="${s.id}">Test SSH</button>
  <button class="btn sm" data-scan>${ICON.scan} Find the tunnels</button><button class="btn sm" data-install="${s.id}">Install the tunnel engine</button><button class="btn sm danger" data-del-server="${s.id}">${ICON.trash} Delete</button></div></div>
  <div class="box"><h4>Connection test ⚡</h4>
    <p class="hint">The path between this server and one on the other side is measured on every protocol: the time to connect, the handshake time and the real round trip. The best protocol can be applied right here.</p>
    ${probePeers(s).length ? `<div class="grid2"><label>${s.role === "iran" ? "Foreign server" : "Iran server"}<select data-probe-peer>
      ${probePeers(s).map((o) => `<option value="${esc(o.id)}">${esc(o.name)} — ${esc(o.ip)}</option>`).join("")}
    </select></label>
    <div class="btns"><button class="btn sm primary" data-probe="${s.id}">⚡ Measure the connection</button></div></div>
    <div id="probe-out"></div>` : `<p class="note">Add a server on the other side first so that the path can be measured.</p>`}
  </div>
  ${s.role === "kharej" ? `<div class="box"><h4>Command channel (DNS)</h4>
    <p class="hint">When not even TCP is possible, the panel writes the command into a TXT record and the agent on the server reads and runs it.</p>
    <div class="btns"><button class="btn sm" data-install-agent="${s.id}">Install the command channel</button>
      <button class="btn sm" data-send-command="${s.id}">Send the “status” command</button>
      <button class="btn sm" data-read-command="${s.id}">Read the answer</button></div>
    <div id="cmd-out" class="sub mono"></div></div>` : ""}
  <div class="box"><h4>Tunnels (${built.length})</h4><div class="opt-list">${built.map(row).join("") || "<span class='muted small'>—</span>"}</div>
    <div class="btns"><button class="btn sm" data-new-link="${s.id}">${ICON.add} New tunnel</button>
    ${broken.length ? `<button class="btn sm" data-discover-links="${broken.map((l) => l.id).join(",")}">Rebuild the broken tunnels</button>` : ""}</div></div>
  ${s.role === "kharej" && serverExits(s.id).length ? `<div class="box"><h4>Ports that finally leave through another server</h4>
    <div class="opt-list">${serverExits(s.id).map((e) => `<div class="opt"><span class="dot ${exitBad(e) ? "down" : "up"}"></span><span class="grow"><span style="text-align:left"><span class="mono">${e.port}</span> ↪ ${esc(exitText(e))}</span>${exitLine(e)}</span></div>`).join("")}</div>
    <p class="hint">The panel only reads these from the server and shows them (iptables/nft, socat, gost, realm and the connections of the process listening on the port); it changes nothing at this stage.</p></div>` : ""}
  ${offers.length ? `<div class="box"><h4>Pairs without a tunnel</h4><div class="opt-list">${offers.map(row).join("")}</div>
    <div class="btns"><button class="btn sm" data-discover-links="${offers.map((l) => l.id).join(",")}">Build with the best protocol</button></div></div>` : ""}`;
}

function probePeers(s) {
  return byRole(s.role === "iran" ? "kharej" : "iran");
}

function probeTable(r) {
  if (!r || !r.ok) {
    return `<p class="note" style="color:var(--down)">${esc((r && r.error) || "no result")}</p>`;
  }
  const rows = (r.results || []).map((x, i) => {
    const ok = !!x.ok;
    const best = ok && i === 0;
    const cells = ok
      ? `<td class="mono">${ms(x.connect_ms)}</td><td class="mono">${ms(x.handshake_ms)}</td><td class="mono">${ms(x.rtt_ms)}</td>`
      : `<td colspan="3" class="muted small">${esc(x.error || "did not work")}</td>`;
    return `<tr><td class="mono">${esc(x.transport)}${best ? " ★" : ""}</td>${cells}<td>${ok ? (best ? '<b style="color:var(--ok)">best</b>' : "✓") : "✗"}</td></tr>`;
  }).join("");
  const apply = r.best && r.link_id
    ? `<div class="btns"><button class="btn sm primary" data-apply-probe="${esc(r.link_id)}" data-transport="${esc(r.best)}">Apply ${esc(r.best)} on this tunnel</button></div>`
    : "";
  return `<table class="tbl"><thead><tr><th>Protocol</th><th>Connect</th><th>Session</th><th>Round trip</th><th></th></tr></thead><tbody>${rows}</tbody></table>${apply}`;
}

function failbackText(d) {
  if (!d || !d.failback) return "";
  const f = d.failback, name = nm(f.iran_id);
  if (f.wait < 0) return `the primary server ${name} does not have all the ports of this domain healthy yet`;
  const left = f.wait - (nowS() - S.now);
  return left > 0 ? `back to the primary server ${name} in ${dur(left)} of stability` : `back to the primary server ${name} on the next check`;
}
function exitLine(e) {
  if (!e) return "";
  const st = e.targets.map((t) => `${esc(exitName(t))}${t.server_id ? ` <span class="mono muted">${esc(t.addr)}</span>` : ""} ${t.ok === false ? "<b style='color:var(--down)'>does not answer</b>" : t.ok ? "<span style='color:var(--ok)'>✓</span>" : ""}`).join(", ");
  return `<span class="sub">final exit: ${st} <span class="muted">(${esc(e.method)}${e.inferred ? ", inferred from the connections" : ""})</span></span>`;
}

function domainDrawer() {
  const d = S.domains.find((x) => x.id === drawer.id);
  if (!d) return "";
  const p = provider(d.provider_id);
  const iran = srv(d.iran_id);
  const routeRows = (d.routes || []).map((r) => {
    const l = linkById(r.link_id);
    const st = l ? lstatus(l) : "down";
    const sub = l ? [l.label, l.transport, st === "up" ? ms(l.latency_ms) : FA[st]].filter(Boolean).join(" · ") : "no tunnel opens this port on this server";
    return `<div class="opt"><span class="dot ${r.healthy ? "up" : st}"></span>
      <span class="grow"><span style="text-align:left"><span class="mono">${esc(r.spec)}</span> → ${esc(l ? nm(l.kharej_id) : "?")}${r.exit ? ` ↪ ${esc(exitText(r.exit))}` : ""}</span><span class="sub mono">${esc(sub)}</span>${exitLine(r.exit)}</span>
      ${l ? `<button class="btn sm" data-link="${l.id}">Tunnel</button>` : ""}</div>`;
  }).join("");
  const offered = [];
  for (const l of S.links.filter((x) => x.iran_id === d.iran_id && x.status !== "pending")) {
    for (const spec of l.ports || []) if (!offered.some((o) => o.spec === spec)) offered.push({ spec, l });
  }
  for (const spec of d.specs || []) if (!offered.some((o) => o.spec === spec)) offered.push({ spec, l: null });
  const picker = offered.map((o) => `<label><input type="checkbox" class="dp-port" value="${esc(o.spec)}" ${(d.specs || []).includes(o.spec) ? "checked" : ""}><span>${esc(o.spec)} <span class="muted small">→ ${esc(o.l ? nm(o.l.kharej_id) : "?")}${o.l && (o.l.exits || {})[o.spec] ? ` ↪ ${esc(exitText(o.l.exits[o.spec]))}` : ""}</span></span></label>`).join("");
  const cov = (d.coverage || []).map((c) => {
    const s = srv(c.iran_id);
    if (!s) return "";
    const tags = [];
    if (c.iran_id === d.primary_iran) tags.push(`<span class="chip">primary</span>`);
    if (c.iran_id === d.iran_id) tags.push(`<span class="chip ok">current</span>`);
    const sub = c.ok ? "all the ports are healthy" : (c.problems[0] || "");
    return `<div class="opt ${c.ok ? "" : "off"}"><span class="dot ${c.ok ? "up" : "down"}"></span>
      <span class="grow"><span>${esc(s.name)} <span class="mono muted small">${esc(s.ip)}</span></span><span class="sub">${esc(sub)}${c.problems.length > 1 ? ` (+${c.problems.length - 1})` : ""}</span></span>
      ${tags.join("")}${c.iran_id !== d.primary_iran ? `<button class="btn sm ghost" data-primary="${c.iran_id}" title="Once the failure is over, the traffic comes back to this server">Make it primary</button>` : ""}${c.ok && c.iran_id !== d.iran_id ? `<button class="btn sm" data-switch="${c.iran_id}">Switch</button>` : ""}</div>`;
  }).join("");
  const manual = groupDraft ? groupDraft.manual : d.group.length > 0;
  const notPending = (id) => (linkById(id) || {}).status !== "pending";
  const order = groupDraft ? (groupDraft.manual ? groupDraft.ids : []) : d.effective_group.filter(manual ? () => true : notPending);
  const listed = manual ? [...order, ...S.links.filter((l) => l.status !== "pending").map((l) => l.id).filter((id) => !order.includes(id))] : order;
  const groupRows = listed.map(linkById).filter(Boolean).map((l, i) => {
    const on = order.includes(l.id);
    return `<div class="opt ${on ? "" : "off"}">
      ${manual ? `<input type="checkbox" data-g-toggle="${l.id}" ${on ? "checked" : ""}>` : `<span class="muted small mono">${i + 1}</span>`}
      <span class="dot ${lstatus(l)}"></span>${linkCell(l)}
      ${manual && on ? `<span class="ord"><button type="button" data-g-up="${l.id}" title="Higher">▲</button><button type="button" data-g-down="${l.id}" title="Lower">▼</button></span>` : ""}</div>`;
  }).join("");
  const fb = failbackText(d);
  return `${head(`<span class="mono">${esc(d.fqdn)}</span>`, `${p ? `<span class="prov ${p.kind}">${p.kind === "cloudflare" ? "Cloudflare" : "ArvanCloud"}</span> ${esc(p.name)}` : "no DNS account"}`, d.status)}
  <div class="box"><h4>Traffic path</h4>
    <div class="pathline"><span class="chip">User</span><span class="arrow">→</span><span class="chip mono">${esc(d.fqdn)}</span><span class="arrow">→</span><span class="chip">${esc(iran ? `${iran.name} ${iran.ip}` : "—")}</span></div>
    ${iran ? "" : (d.error || "").startsWith("DNS read") && !d.current_ip
      ? `<p class="note" style="color:var(--degraded)">The record of this domain could not be read from DNS (see the error below). Until that is fixed, the panel tries again every 5 minutes and does not change the record.</p>`
      : `<p class="note" style="color:var(--degraded)">The record of this domain points at none of the registered Iran servers. Pick the right one in the “Iran servers” list below.</p>`}
    ${d.pending_ip ? `<p class="note" style="color:var(--degraded)">The record was changed to <span class="mono">${esc(d.pending_ip)}</span>; waiting for the change to reach the public DNS servers (${ago(d.pending_since)})…</p>` : ""}
    ${fb ? `<p class="note"><span id="failback-timer">${esc(fb)}</span></p>` : ""}
    <dl class="kv"><dt>Record IP</dt><dd class="mono">${esc(d.current_ip || "—")}</dd><dt>TTL</dt><dd class="mono">${d.ttl}s</dd>
    <dt>Last switch</dt><dd>${ago(d.last_switch)}</dd><dt>Automatic failover</dt><dd>${d.auto_failover ? "on" : "off"}</dd>
    ${d.error ? `<dt>Error</dt><dd style="color:var(--down)">${esc(d.error)}</dd>` : ""}</dl>
    <div class="btns"><button class="btn sm" data-edit-domain="${d.id}">${ICON.edit} Edit</button><button class="btn sm" data-sync="${d.id}">${ICON.refresh} Read the record from DNS</button><button class="btn sm danger" data-del-domain="${d.id}">${ICON.trash} Delete</button></div></div>
  ${iran ? `<div class="box"><h4>Ports and routes on ${esc(iran.name)}</h4>
    <div class="opt-list">${routeRows || "<span class='muted small'>No port has been selected for this domain yet</span>"}</div>
    <label>Which ports does this domain use?</label>
    <div class="tchecks">${picker || "<span class='muted small'>There is no tunnel with forwarded ports on this server</span>"}</div>
    <p class="hint">A domain only counts as healthy when all of these ports work, and it only moves to another Iran server that has all of them healthy.</p>
    <div class="btns"><button class="btn primary sm" data-save-dports="${d.id}">Save the ports</button></div></div>` : ""}
  <div class="box"><h4>Iran servers</h4>
    <p class="note">On a failure, the record moves to the first server in this list that has <b>all the ports of the domain</b> healthy. The “primary” comes back behind the domain again after ${Math.round(S.settings.failback_after / 60)} minutes of complete stability.</p>
    <div class="opt-list">${cov || "<span class='muted small'>—</span>"}</div></div>
  <div class="box"><h4>Advanced: allowed tunnels and order</h4>
    <div class="seg"><button type="button" data-g-mode="auto" class="${manual ? "" : "on"}">Automatic</button><button type="button" data-g-mode="manual" class="${manual ? "on" : ""}">Manual selection</button></div>
    <p class="hint">${manual ? "Only the ticked tunnels are used for this domain; their order also decides the order of the Iran servers." : "Automatic: every tunnel that has the ports of this domain, in the priority order of the Iran servers; and if a foreign server fails, the other foreign tunnels on the same Iran server."}</p>
    <div class="opt-list">${groupRows || (groupDraft ? "<span class='muted small'>It is computed automatically after the save</span>" : "")}</div>
    ${groupDraft ? `<div class="btns"><button class="btn primary sm" data-g-save="${d.id}">Save</button><button class="btn sm ghost" data-g-cancel>Cancel</button></div>` : ""}
  </div>`;
}

/* ----------------------------------------------------------------- modals */
/* The forms are the panel's: the markup comes from /api/form, every field
   carries the key the API names it by, and the form says where it is sent
   and what it says when it is done. This is the whole of what the script
   does with a form: it collects what he wrote, and sends it. */
function openModal(html) {
  const f = $("#modal-form");
  f.innerHTML = html;
  $("#modal").classList.add("open");
  const first = f.querySelector("input:not([type=checkbox]),select,textarea");
  if (first) setTimeout(() => first.focus(), 50);
}
function closeModal() { $("#modal").classList.remove("open"); }
const lines = (v) => String(v || "").split(/[\n,]/).map((x) => x.trim()).filter(Boolean);

// openForm asks the panel for one of its forms.
async function openForm(id, params = {}) {
  const q = new URLSearchParams({ id });
  for (const [key, value] of Object.entries(params)) if (value) q.set(key, value);
  try {
    const answer = await api("GET", `/api/form?${q}`);
    openModal(answer.html);
    const f = $("#modal-form");
    f.dataset.method = answer.method;
    f.dataset.endpoint = answer.endpoint;
    f.dataset.done = answer.done || "";
    f.dataset.after = answer.after || "";
    f.dataset.reload = answer.reload ? "1" : "";
  } catch (err) { toast(err.message, "error"); }
}

// put writes a field into the body of the request. The fields of the
// Telegram settings are one level down: telegram.bot_token.
function put(body, key, value) {
  const parts = key.split(".");
  let at = body;
  for (const part of parts.slice(0, -1)) at = at[part] = at[part] || {};
  at = at[parts[parts.length - 1]] = value;
}

// formBody is what he wrote, as the API wants it: every field says which
// key it holds, whether it is a number, several lines or a group of
// choices — and the form says where it goes.
async function formBody(f) {
  const body = {};
  for (const el of f.querySelectorAll("[data-field],[data-file]")) {
    if (el.dataset.file) {  // a file he picked, as the API takes it
      if (el.files.length) put(body, el.dataset.file, await toBase64(el.files[0]));
      continue;
    }
    const key = el.dataset.field;
    if (el.type === "radio") { if (el.checked) put(body, key, el.value); continue; }
    if (el.dataset.checks) { if (el.checked) (body[key] = body[key] || []).push(el.value); continue; }
    let value;
    if (el.type === "checkbox") value = el.checked;
    else if (el.dataset.num) value = (el.value === "" ? +(el.dataset.def || 0) : +el.value) * (+(el.dataset.scale || 1));
    else if (el.dataset.lines) value = lines(el.value);
    else value = el.value.trim() || el.dataset.else || "";
    put(body, key, value);
  }
  return body;
}

// toBase64 reads a file the way the API takes it: as text of base64.
async function toBase64(file) {
  const buf = new Uint8Array(await file.arrayBuffer());
  let bin = "";
  for (let i = 0; i < buf.length; i += 32768) bin += String.fromCharCode.apply(null, buf.subarray(i, i + 32768));
  return btoa(bin);
}

// dig reads one field of an answer, by its path ("summary").
const dig = (at, path) => path.split(".").reduce((v, k) => (v == null ? v : v[k]), at);

// a switch he turns off may ask him first, and the question is the form's.
function formConfirm(f) {
  for (const el of f.querySelectorAll("[data-confirm]")) {
    if (el.type !== "checkbox" || !el.defaultChecked || el.checked) continue;
    let question = el.dataset.confirm;
    const other = el.dataset.confirm2When && f.querySelector(`[data-field="${el.dataset.confirm2When}"]`);
    if (other && other.checked && el.dataset.confirm2) question = el.dataset.confirm2;
    if (!confirm(question)) return false;
  }
  return true;
}

// The submit: what he wrote, to the endpoint the form names. The promise
// is kept, so whatever does it after — the test message, for one — waits
// for it instead of racing with it.
$("#modal-form").onsubmit = async (e) => {
  e.preventDefault();
  const f = e.target;
  if (!f.dataset.endpoint || !formConfirm(f)) return;
  const btn = f.querySelector("[type=submit]");
  if (btn) btn.disabled = true;
  lastSubmit = (async () => {
    try {
      const body = await formBody(f);
      const missing = f.querySelector("[data-require]");
      if (missing && !body.data && !body.bk_name) throw new Error(missing.dataset.require);
      const answer = await api(f.dataset.method, f.dataset.endpoint, body);
      if (f.dataset.done) toast(f.dataset.done.replace(/\{(\w+(?:\.\w+)*)\}/g, (_, path) => dig(answer, path) ?? ""), "success");
      closeModal();
      if (f.dataset.after === "domain" && answer.domain) {
        // the domain he just added is the one he is looking at
        selectedDomain = answer.domain.id;
        setTimeout(() => openDrawer("domain", answer.domain.id), 300);
      }
      if (f.dataset.reload) setTimeout(() => location.reload(), 1200);
      loadViewThenRender();
    } catch (err) { toast(err.message, "error"); }
    finally { if (btn) btn.disabled = false; }
  })();
  await lastSubmit;
};

/* A button that does one thing. The panel says which call it is, what it
   says when it is done, and whether the form it sits in is sent first. */
document.addEventListener("click", async (e) => {
  const b = e.target.closest("[data-act],[data-act-form]");
  if (!b) return;
  if (b.dataset.actForm) return openForm(b.dataset.actForm);
  const [method, path] = b.dataset.act.split(" ");
  try {
    if (b.hasAttribute("data-act-first")) {
      const form = b.closest("form");
      if (form) form.requestSubmit();
      await lastSubmit.catch(() => {});  // the settings go out first
    }
    const answer = await api(method, path, {});
    const field = b.dataset.actError;
    if (field && answer && answer[field]) {
      throw new Error(`${b.dataset.actErrorText || "The panel could not do it:"} ${answer[field]}`);
    }
    if (b.dataset.done) toast(b.dataset.done, "success");
  } catch (err) { toast(err.message, "error"); }
});

function saveDomain(dom, patch) {
  return api("PUT", `/api/domains/${dom.id}`, {
    fqdn: dom.fqdn, provider_id: dom.provider_id, kharej_id: dom.kharej_id, ports: dom.ports || [],
    ttl: dom.ttl, auto_failover: dom.auto_failover, group: dom.group || [], ...patch,
  });
}

const CLICKABLE = "[data-add],[data-close],[data-close-modal],[data-link],[data-dom],[data-pick],[data-apply-transport],[data-retest],[data-unpin],[data-edit-server],[data-open-server],[data-test-server],[data-install],[data-del-server],[data-scan],[data-new-link],[data-release],[data-save-ports],[data-save-link],[data-del-link],[data-discover-links],[data-switch],[data-primary],[data-save-dports],[data-sync],[data-edit-domain],[data-del-domain],[data-verify-provider],[data-edit-provider],[data-del-provider],[data-g-mode],[data-g-toggle],[data-g-up],[data-g-down],[data-g-save],[data-g-cancel],[data-install-agent],[data-send-command],[data-read-command],[data-probe],[data-apply-probe],[data-dom-ports],[data-open-settings],[data-goto]";
document.addEventListener("click", async (e) => {
  const t = e.target.closest(CLICKABLE);
  if (!t) {
    if (!e.target.closest(".menu")) closeMenus();
    return;
  }
  if (t.closest("#wires")) return; // wire clicks are handled below (pointer events)
  const d = t.dataset;
  closeMenus();
  if (d.add) { closeModal(); return openForm(d.add, { role: d.role }); }
  if ("close" in d) return closeDrawer();
  if ("closeModal" in d) return closeModal();
  if (d.goto) {
    const target = document.getElementById(d.goto);
    if (target) target.scrollIntoView({ behavior: "smooth", block: "start" });
    for (const b of $$("#set-index .index button")) b.classList.toggle("on", b === t);
    return;
  }
  if (d.dom) { selectedDomain = d.dom; return loadViewThenRender(); }
  if (d.domPorts) { return openDrawer("domain", d.domPorts); }
  if (d.openSettings) return openForm("settings");  if (d.pick) { selTransport = d.pick; return renderDrawer(true); }
  if ("applyTransport" in d) {
    const l = linkById(drawer.id);
    const tr = selTransport || l.transport || S.settings.candidate_transports[0];
    await act(() => api("POST", `/api/links/${l.id}/transport`, { transport: tr, pin: $("#pin-on-apply").checked }), `Switching to ${tr} has started`);
    selTransport = null; return;
  }
  if ("retest" in d) return act(() => api("POST", `/api/links/${drawer.id}/retest`), "Testing every protocol has started");
  if ("unpin" in d) return act(() => api("PUT", `/api/links/${drawer.id}`, { pinned: false }), "The pin was released");
  if (d.savePorts) {
    return act(() => api("PUT", `/api/links/${d.savePorts}`, { ports: lines($("#lp-ports").value), accept_udp: $("#lp-udp").checked, apply_to_kharej: $("#lp-all").checked }), "The ports are saved and are being applied on the Iran server");
  }
  if (d.saveLink) {
    const allowed = $$(".lp-allowed").filter((x) => x.checked).map((x) => x.value);
    return act(() => api("PUT", `/api/links/${d.saveLink}`, { label: $("#lp-label").value.trim(), priority: +$("#lp-prio").value || 100, allowed }), "Saved");
  }
  if (d.delLink) {
    const l = linkById(d.delLink);
    const purge = !!d.purge;
    if (!confirm(purge ? `Delete the tunnel ${linkName(l)} from the panel and from the servers?` : `Remove the tunnel ${linkName(l)} from the panel? (the servers are left alone)`)) return;
    await act(() => api("DELETE", `/api/links/${d.delLink}?purge=${purge}`), "Deleted");
    return closeDrawer();
  }
  if (d.release) return act(() => api("POST", `/api/links/${d.release}/release`), "The ports came back");
  if (d.editServer) return openForm("server", { edit: d.editServer });
  if (d.openServer) return openDrawer("server", d.openServer);
  if (d.testServer) {
    const r = await act(() => api("POST", `/api/servers/${d.testServer}/test`));
    return toast(r.status === "online" ? `SSH is fine — ${ms(r.latency_ms)}` : `SSH failed: ${r.error}`, r.status === "online" ? "success" : "error");
  }
  if ("scan" in d) { const r = await act(() => api("POST", "/api/scan")); return toast(r.added ? `${r.added} existing tunnels were found and added` : "No new tunnel was found", "success"); }
  if (d.newLink) return openForm("link", { preset: d.newLink });
  if (d.install) { toast("Installing the tunnel engine…"); const r = await act(() => api("POST", `/api/servers/${d.install}/install`)); return toast(`The tunnel engine ${r.version} is ready`, "success"); }
  if (d.probe) {
    const s = srv(d.probe);
    const sel = document.querySelector("[data-probe-peer]");
    const peer = sel ? srv(sel.value) : null;
    if (!peer) { toast("No server on the other side is selected", "error"); return; }
    const out = document.getElementById("probe-out");
    if (out) out.innerHTML = `<p class="sub muted">Measuring the path ${esc(s.name)} ⇄ ${esc(peer.name)}…</p>`;
    const body = s.role === "iran" ? { iran_id: s.id, kharej_id: peer.id } : { iran_id: peer.id, kharej_id: s.id };
    let r;
    try {
      r = await api("POST", "/api/probe", body);
    } catch (e) {
      if (out) out.innerHTML = `<p class="note" style="color:var(--down)">${esc(e.message)}</p>`;
      return toast(e.message, "error");
    }
    if (out) out.innerHTML = probeTable(r);
    return toast(r.ok ? (r.best ? `The best protocol: ${r.best}` : "No protocol worked") : "The measurement failed", r.ok ? "success" : "error");
  }
  if (d.applyProbe) {
    const l = linkById(d.applyProbe);
    if (!l) return;
    return act(() => api("POST", `/api/links/${l.id}/transport`, { transport: d.transport, pin: true }),
      `The protocol ${d.transport} was applied`);
  }
  if (d.installAgent) {
    toast("Installing the command channel…");
    const r = await act(() => api("POST", `/api/servers/${d.installAgent}/agent`));
    return toast(`The command channel is ready (${r.command_record})`, "success");
  }
  if (d.sendCommand) {
    const r = await act(() => api("POST", `/api/servers/${d.sendCommand}/command`, { action: "status" }));
    return toast(`The command was sent (${r.id}); read the answer with “Read the answer”`, "success");
  }
  if (d.readCommand) {
    const r = await act(() => api("GET", `/api/servers/${d.readCommand}/command`));
    const out = document.getElementById("cmd-out");
    if (out) out.textContent = r.answer ? JSON.stringify(r.answer) : "No answer yet";
    return toast(r.answer ? "The answer came" : "No answer yet", r.answer ? "success" : "error");
  }
  if (d.delServer) {
    if (!confirm("Remove the server from the panel? The tunnels the panel built are deleted from the servers; your own tunnels are left alone.")) return;
    await act(() => api("DELETE", `/api/servers/${d.delServer}`), "Deleted"); return closeDrawer();
  }
  if (d.discoverLinks) return act(() => api("POST", "/api/discover", { link_ids: d.discoverLinks.split(",") }), "Building the tunnels has started");
  if (d.switch) {
    const dom = S.domains.find((x) => x.id === (d.domOf || (drawer && drawer.id)));
    if (!dom) return;
    const s = srv(d.switch);
    const note = s.id !== dom.primary_iran && dom.auto_failover && !S.settings.observe_only ? `\n\nThe primary server of this domain is ${nm(dom.primary_iran)}; if this change should be permanent, make ${s.name} the “primary”, otherwise the traffic comes back to the primary once it is stable.` : "";
    if (!confirm(`Change the record of ${dom.fqdn} in DNS to ${s.name} (${s.ip})?${note}`)) return;
    return act(() => api("POST", `/api/domains/${dom.id}/switch`, { iran_id: d.switch }), "The record was changed");
  }
  if (d.primary) {
    const dom = S.domains.find((x) => x.id === (d.domOf || (drawer && drawer.id)));
    if (!dom) return;
    return act(() => api("POST", `/api/domains/${dom.id}/primary`, { iran_id: d.primary }), `${nm(d.primary)} is now the primary server of ${dom.fqdn}`);
  }
  if (d.saveDports) {
    const dom = S.domains.find((x) => x.id === d.saveDports);
    const ports = $$(".dp-port").filter((x) => x.checked).map((x) => x.value);
    if (!ports.length) return toast("Pick at least one port", "error");
    return act(() => saveDomain(dom, { ports }), "The ports of the domain are saved");
  }
  if (d.gMode) {
    const dom = S.domains.find((x) => x.id === drawer.id);
    const built = dom.effective_group.filter((id) => (linkById(id) || {}).status !== "pending");
    groupDraft = d.gMode === "manual" ? { manual: true, ids: dom.group.length ? [...dom.group] : built } : { manual: false, ids: [] };
    if (d.gMode === "auto" && !dom.group.length) groupDraft = null;
    return renderDrawer(true);
  }
  if (d.gToggle) {
    const ids = groupDraft.ids, i = ids.indexOf(d.gToggle);
    if (i >= 0) ids.splice(i, 1); else ids.push(d.gToggle);
    return renderDrawer(true);
  }
  if (d.gUp || d.gDown) {
    const id = d.gUp || d.gDown, ids = groupDraft.ids, i = ids.indexOf(id), j = d.gUp ? i - 1 : i + 1;
    if (i >= 0 && j >= 0 && j < ids.length) [ids[i], ids[j]] = [ids[j], ids[i]];
    return renderDrawer(true);
  }
  if ("gCancel" in d) { groupDraft = null; return renderDrawer(true); }
  if (d.gSave) {
    const dom = S.domains.find((x) => x.id === d.gSave);
    const group = groupDraft.manual ? groupDraft.ids : [];
    if (groupDraft.manual && !group.length) return toast("Pick at least one tunnel", "error");
    await act(() => saveDomain(dom, { group }), "Saved");
    groupDraft = null; return renderDrawer(true);
  }
  if (d.sync) return act(() => api("POST", `/api/domains/${d.sync}/sync`), "The record was read");
  if (d.editDomain) return openForm("domain", { edit: d.editDomain });
  if (d.delDomain) { if (!confirm("Remove the domain from the panel? (the DNS record is left alone)")) return; await act(() => api("DELETE", `/api/domains/${d.delDomain}`), "Deleted"); return closeDrawer(); }
  if (d.verifyProvider) { const r = await act(() => api("POST", `/api/providers/${d.verifyProvider}/verify`)); return toast(`The connection is fine: ${r.detail}`, "success"); }
  if (d.editProvider) return openForm("provider", { edit: d.editProvider });
  if (d.delProvider) { if (!confirm("Delete the DNS account?")) return; await act(() => api("DELETE", `/api/providers/${d.delProvider}`), "Deleted"); return openForm("settings"); }
  if (d.link) return openDrawer("link", d.link);
});
// wires & labels live inside the panned stage: open on a clean click only
wiresEl.addEventListener("pointerdown", (e) => {
  const w = e.target.closest(".hit,.wlabel");
  if (!w) return;
  e.stopPropagation();
  const start = [e.clientX, e.clientY];
  const up = (ev) => {
    window.removeEventListener("pointerup", up);
    if (Math.hypot(ev.clientX - start[0], ev.clientY - start[1]) < 5) {
      if (w.dataset.hop) {
        // a forwarded port: it leaves from one server and arrives at
        // another. The drawer of the server it leaves from says which
        // ports, and where they go.
        const [from] = w.dataset.hop.split("|");
        selNode = from;
        selLink = null;
        paintSelection();
        openDrawer("server", from);
        return;
      }
      selLink = w.dataset.link || null;
      selNode = null;
      paintSelection();
      openDrawer("link", w.dataset.link);
    }
  };
  window.addEventListener("pointerup", up);
});

/* hover: focus + tooltip */
const tip = $("#tip");
document.addEventListener("mouseover", (e) => {
  if (pan || $(".fnode.dragging")) return;
  const n = e.target.closest("#nodes .fnode");
  const w = e.target.closest(".hit,.wlabel");
  if (!n && !w) {
    if (focus) { focus = null; applyFocus(); tip.classList.remove("show"); }
    return;
  }
  if (n) { focus = relatedTo(n.dataset.type, n.dataset.id); applyFocus(); }
  else if (w) {
    if (w.dataset.hop) {
      // a forwarded port: it leaves from one server and arrives at another.
      // The path is the two servers, and the tip says what the wire is.
      const [from, to] = w.dataset.hop.split("|");
      focus = { nodes: new Set([from, to]), links: new Set(), domains: new Set() };
      applyFocus();
      tip.innerHTML = `<b>${esc(nm(from))} → ${esc(nm(to))}</b><br>these ports are forwarded from one server to the other`;
      tip.classList.add("show");
    } else {
      focus = relatedTo("link", w.dataset.link);
      applyFocus();
      const l = linkById(w.dataset.link);
      if (l) { tip.innerHTML = linkTip(l); tip.classList.add("show"); }
    }
  }
});
document.addEventListener("mousemove", (e) => {
  if (!tip.classList.contains("show")) return;
  const x = Math.min(e.clientX + 14, window.innerWidth - tip.offsetWidth - 8);
  tip.style.left = `${x}px`; tip.style.top = `${e.clientY + 14}px`;
});
document.addEventListener("mouseout", (e) => {
  const from = e.target.closest("#nodes .fnode,.hit,.wlabel");
  const to = e.relatedTarget && e.relatedTarget.closest && e.relatedTarget.closest("#nodes .fnode,.hit,.wlabel");
  if (from && from !== to) { focus = null; applyFocus(); tip.classList.remove("show"); }
});

/* the buttons of the page headers are re-created on every render */
document.addEventListener("click", (e) => {
  if (e.target.closest("#observe-pill")) openForm("settings");
});

$("#btn-logout").onclick = async () => { await api("POST", "/api/logout"); location.reload(); };
$("#btn-theme").onclick = () => {
  const cur = document.documentElement.dataset.theme === "light" ? "dark" : "light";
  document.documentElement.dataset.theme = cur;
  try { localStorage.setItem("bt-theme", cur); } catch (_) { /* ignore */ }
  paintThemeButton();
};
function paintThemeButton() {
  const light = document.documentElement.dataset.theme === "light";
  // the button offers the other theme, the way the design does
  $("#theme-label").textContent = light ? "Dark" : "Light";
  $("#theme-icon").innerHTML = light
    ? `<path d="M20.5 14.3A8.5 8.5 0 0 1 9.7 3.5a8.5 8.5 0 1 0 10.8 10.8z"/>`
    : `<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.2 4.2l1.4 1.4M18.4 18.4l1.4 1.4M2 12h2M20 12h2M4.2 19.8l1.4-1.4M18.4 5.6l1.4-1.4"/>`;
}
$("#btn-menu").onclick = () => $("#side").classList.toggle("open");
$("#scrim").onclick = closeDrawer;
$("#modal").addEventListener("mousedown", (e) => { if (e.target.id === "modal") closeModal(); });
document.addEventListener("keydown", (e) => { if (e.key === "Escape") { closeModal(); closeDrawer(); $("#side").classList.remove("open"); } });

let lastStageW = 0;
window.addEventListener("resize", () => {
  const w = stage.clientWidth;
  if (lastStageW && Math.abs(w - lastStageW) / lastStageW > 0.3) fit();
  lastStageW = w;
  drawWires();
});
requestAnimationFrame(() => { lastStageW = stage.clientWidth; });

/* the clock in the top bar, and the failback countdown */
setInterval(() => {
  const d = new Date();
  $("#clock").textContent = [d.getHours(), d.getMinutes(), d.getSeconds()]
    .map((n) => String(n).padStart(2, "0")).join(":");
  if (!S) return;
  if (drawer && drawer.type === "domain") {
    const el = $("#failback-timer");
    if (el) el.textContent = failbackText(S.domains.find((x) => x.id === drawer.id)) || "";
  }
}, 1000);

/* the quick settings of the settings page */
$("#set-body").addEventListener("change", (e) => {
  if (e.target.matches("[data-set],[data-set-tr]")) saveQuickSettings();
});

paintThemeButton();
boot();
