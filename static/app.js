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
let drawer = null;         // {type, id} of what is open in the drawer
let ws = null, wsAlive = false;
let view = "network";
let netQuery = "";
let netFilter = "all";
let selectedDomain = "";   // the domain open on the domains page
let selNode = null;        // the node picked in the flow graph
let selLink = null;        // the tunnel picked in the flow graph
let signedInAs = "admin";  // the name of the account, from the session
let settingsTimer = null;   // the pending save of the settings page, so a burst of changes sends one request
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
const ms = (v) => (v == null ? "—" : `${Math.round(v)}ms`);
const provider = (id) => S.providers.find((p) => p.id === id);
const nowS = () => Date.now() / 1000 + skew;
const linkName = (l) => (l ? `${nm(l.kharej_id)} ⇄ ${nm(l.iran_id)}` : "—");
const linkTag = (l) => (l && l.label ? ` (${l.label})` : "");
function serverExits(id) {
  const out = {};
  for (const l of S.links) if (l.kharej_id === id) for (const e of Object.values(l.exits || {})) out[`${e.port}|${e.method}`] ||= e;
  return Object.values(out).sort((a, b) => a.port - b.port);
}
/* how long is left, in the shape the drawer's failback span wants */
function dur(s) {
  s = Math.max(0, Math.round(s));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${String(s % 60).padStart(2, "0")}s`;
  return `${Math.floor(m / 60)}h ${String(m % 60).padStart(2, "0")}m`;
}

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
    // a carrier was tested on a tunnel: the tests are in the state the
    // page holds, so the page is asked for again — the drawer of that
    // tunnel comes back with the new result on its tiles
    else if (msg.type === "test") loadViewThenRender();
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
  // the drawer is part of the page now: the panel works out what it holds
  // for the thing he has open, and the answer comes with the rest
  if (drawer) params.set("open", `${drawer.type}:${drawer.id}`);
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
  // the thing he had open in the drawer is gone (it was deleted): the
  // panel says so by holding nothing for it, and the drawer closes
  if (drawer && PARTS["drawer-body"] === "") closeDrawer();
  for (const [id, html] of Object.entries(PARTS)) {
    const el = document.getElementById(id);
    if (!el) continue;
    if (id === "empty-hint") el.classList.toggle("hidden", !html); // no hint: nothing to show
    // A part is not swapped under the operator's fingers: while what he
    // is writing stands inside the part, the part keeps what it has —
    // this goes for every part, the drawer included, and only for the
    // fields he can write into (a focused button must not freeze a part:
    // the pages hold buttons that open other parts).
    const at = document.activeElement;
    if (at && el.contains(at) && at.matches("input,textarea,select")) continue;
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
  applyParts();   // the five pages and the drawer, from the panel
  renderFlow();   // the canvas, from the panel
}

/* The drawer is a part like the others: the panel works out what it holds
   for the thing he has open, and this only says which thing that is — and
   opens the panel that slides in. */
function openDrawer(type, id) {
  drawer = { type, id };
  $("#drawer").classList.add("open");
  $("#scrim").classList.add("open");
  loadViewThenRender();
}
function closeDrawer() {
  const was = drawer;
  drawer = null;
  $("#drawer").classList.remove("open");
  $("#scrim").classList.remove("open");
  const body = $("#drawer-body");
  if (body) body.innerHTML = "";
  if (was) loadViewThenRender(); // the panel holds nothing for it any more
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
/* The settings of the page are saved the way the settings form in the
   modal is: every field carries the key the API names it by, and one
   collector reads them — this body holds the keys of this page, and the
   API writes the ones it is given (the rest it keeps). One request goes
   out per burst of changes, so the fields he ticks together are saved
   together. */
async function saveSetBody() {
  const f = $("#set-body");
  const body = await collectBody(f);
  // The Telegram settings are saved as one block, and the API replaces the
  // block it is given: the page holds only the switch, so the rest of what
  // the panel knows about the bot travels with it.
  body.telegram = { ...(S.settings.telegram || {}), ...body.telegram };
  if (!body.candidate_transports || !body.candidate_transports.length) return toast("Pick at least one protocol", "error");
  try {
    await api("PUT", "/api/settings", body);
    toast("Settings saved", "success");
  } catch (e) {
    toast(e.message, "error");
  }
}

/* ------------------------------------------------------------ onboarding */
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

// collectBody is what he wrote, as the API wants it: every field says which
// key it holds, whether it is a number, several lines or a group of
// choices. Any container of these fields collects the same way — the modal
// forms and the settings of the page hold fields of the same shape, so one
// collector reads them all.
async function collectBody(f) {
  const body = {};
  for (const el of f.querySelectorAll("[data-field],[data-file]")) {
    if ("file" in el.dataset) {  // a file he picked, as the API takes it
      if (el.files.length) put(body, el.dataset.file, await toBase64(el.files[0]));
      continue;
    }
    const key = el.dataset.field;
    if (el.type === "radio") { if (el.checked) put(body, key, el.value); continue; }
    // A mark comes without a value ("data-num"), and an attribute without a
    // value reads back as an empty string — so a mark read as truthy is not
    // there at all: the number would go out as the text on the page (minutes,
    // not seconds), a group of choices as the last switch of it, a field of
    // several lines as one string the API refuses, and the file input (which
    // has no key of its own) would be sent under a key that does not exist.
    // The mark of these four is looked for, not read.
    if ("checks" in el.dataset) { if (el.checked) (body[key] = body[key] || []).push(el.value); continue; }
    let value;
    if (el.type === "checkbox") value = el.checked;
    else if ("num" in el.dataset) value = (el.value === "" ? +(el.dataset.def || 0) : +el.value) * (+(el.dataset.scale || 1));
    else if ("lines" in el.dataset) value = lines(el.value);
    else value = el.value.trim() || el.dataset.else || "";
    put(body, key, value);
  }
  return body;
}

// formBody is the fields of a modal form, read the same way as any other.
async function formBody(f) {
  return collectBody(f);
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
   says when it is done, and whether the form it sits in is sent first.

   What the button adds to the generic call, in all:
   - the body is the form it sits in (its fields are collected the same way
     as a modal form's), so a button that needs a value sits in a tiny form
     holding it; a button outside a form sends nothing.
   - data-done is the message when it worked; {path} in it is read from the
     answer, and from the body when the answer does not hold it.
   - data-act-error names a field of a 200 answer that still means failure
     (a measurement that could not be taken comes back as an answer with an
     error in it), with data-act-error-text in front of it.
   - data-confirm asks him first, with the question the panel wrote (the
     panel knows the names; the script does not).
   - data-act-into writes what came back into an element, as text: the
     first of the keys of data-act-key that is there, or data-act-else when
     none is. This is the one place an answer is written into the page as
     text — never as markup. */
document.addEventListener("click", async (e) => {
  const b = e.target.closest("[data-act],[data-act-form]");
  if (!b) return;
  if (b.dataset.actForm) return openForm(b.dataset.actForm, b.dataset.actEdit ? { edit: b.dataset.actEdit } : {});
  const [method, path] = b.dataset.act.split(" ");
  if (b.dataset.confirm && !confirm(b.dataset.confirm)) return;
  const form = b.closest("form");
  const was = b.disabled;
  b.disabled = true; // what he is waiting for is the button itself
  try {
    if (b.hasAttribute("data-act-first")) {
      if (form) form.requestSubmit();
      await lastSubmit.catch(() => {});  // the settings go out first
    }
    const body = form ? await collectBody(form) : {};
    // the browser refuses a body on a GET (the answer of the command
    // channel is the only GET down here): the fields are still what the
    // finished message may name
    const send = method === "GET" || method === "HEAD" ? undefined : body;
    const answer = await api(method, path, send);
    const field = b.dataset.actError;
    if (field && answer && answer[field]) {
      throw new Error(`${b.dataset.actErrorText || "The panel could not do it:"} ${answer[field]}`);
    }
    const into = b.dataset.actInto && document.querySelector(b.dataset.actInto);
    if (into) {
      const keys = (b.dataset.actKey || "").split(",").filter(Boolean);
      let text = keys.map((k) => dig(answer, k)).find((v) => v != null);
      if (text == null) text = b.dataset.actElse || "";
      into.textContent = typeof text === "string" ? text : JSON.stringify(text);
    }
    if (b.dataset.done) {
      toast(b.dataset.done.replace(/\{(\w+(?:\.\w+)*)\}/g, (_, p) => dig(answer, p) ?? dig(body, p) ?? ""), "success");
    }
  } catch (err) { toast(err.message, "error"); }
  finally { b.disabled = was; }
});

/* Everything the drawer used to say in JavaScript is now written by Go into
   the drawer part (web/drawer.go); what is left here is only the handful of
   attributes that decide *where* a click goes. */
const CLICKABLE = "[data-add],[data-close],[data-close-modal],[data-link],[data-dom],[data-edit-server],[data-open-server],[data-test-server],[data-new-link],[data-discover-links],[data-sync],[data-edit-domain],[data-del-domain],[data-verify-provider],[data-edit-provider],[data-del-provider],[data-dom-ports],[data-open-settings],[data-goto]";
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
  if (d.openSettings) return openForm("settings");
  if (d.editServer) return openForm("server", { edit: d.editServer });
  if (d.openServer) return openDrawer("server", d.openServer);
  if (d.testServer) {
    const r = await act(() => api("POST", `/api/servers/${d.testServer}/test`));
    return toast(r.status === "online" ? `SSH is fine — ${ms(r.latency_ms)}` : `SSH failed: ${r.error}`, r.status === "online" ? "success" : "error");
  }
  if (d.newLink) return openForm("link", { preset: d.newLink });
  if (d.discoverLinks) return act(() => api("POST", "/api/discover", { link_ids: d.discoverLinks.split(",") }), "Building the tunnels has started");
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

/* The clock in the top bar, and the failback countdown. The countdown itself
   is written by Go into the drawer (a span that carries the moment it runs
   out, data-at, and the server it runs to, data-name); all that is left for
   the script is to count it down once a second. */
setInterval(() => {
  const d = new Date();
  $("#clock").textContent = [d.getHours(), d.getMinutes(), d.getSeconds()]
    .map((n) => String(n).padStart(2, "0")).join(":");
  const el = document.getElementById("failback-timer");
  if (!el || !el.dataset.at) return; // no moment to count to (or already due)
  const left = Number(el.dataset.at) - nowS();
  const name = el.dataset.name || "";
  el.textContent = left <= 0
    ? `back to the primary server ${name} on the next check`
    : `back to the primary server ${name} in ${dur(left)} of stability`;
}, 1000);

/* The quick settings of the settings page: the fields carry their keys and
   are collected like any other form. A burst of changes goes out as one
   save — ticking a few protocols must not send a request per click. */
$("#set-body").addEventListener("change", (e) => {
  if (!e.target.matches("[data-field]")) return;
  const f = $("#set-body");
  if (!formConfirm(f)) {
    // the question he said no to: the switches go back where they were
    for (const el of f.querySelectorAll("[data-confirm]")) el.checked = el.defaultChecked;
    return;
  }
  clearTimeout(settingsTimer);
  settingsTimer = setTimeout(saveSetBody, 400);
});

paintThemeButton();
boot();
