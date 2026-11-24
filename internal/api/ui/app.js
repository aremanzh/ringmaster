/* Ringmaster dashboard.
 *
 * Three independent regions, so a status update never disturbs what the
 * user is doing in another one:
 *   board    the project cards; redrawn from `state` on every change
 *   logpanel one task's live log; owns its own stream
 *   sheet    settings, add project and PHP, in a modal <dialog>; owns its form
 * The folder picker is a second modal <dialog> that opens over the sheet.
 *
 * All clicks go through one delegated handler keyed on data-act.
 */
"use strict";

let state = { projects: [], config_path: "" };
let php = { roots: [], installs: [] };
let loaded = false;  // the first status has arrived
let log = null;      // { project, task, entries, pending, find, follow, close, missing, openerFk }
let sheet = null;    // { kind: "settings" | "add" | "php", draft, dirty, saving, error, openerFk }
let picker = null;   // { target, listing, lastError }: the folder picker over a sheet
let firstBoard = true;

const $ = id => document.getElementById(id);
const esc = s => String(s ?? "").replace(/[&<>"']/g,
  c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
// vh is text for screen readers only: it turns "Stop" into "Stop serve in Blog".
const vh = text => `<span class="visually-hidden">${esc(text)}</span>`;
const api = (path, opts) => Backend.request(path, opts);
const jsonBody = (method, value) =>
  ({ method, headers: { "Content-Type": "application/json" }, body: JSON.stringify(value) });
const modalOpen = () => $("sheet").open || $("picker").open;
const byFk = fk => fk ? document.querySelector(`[data-fk="${CSS.escape(fk)}"]`) : null;

// redraw runs fn, which replaces the content of `box`, and puts keyboard
// focus back on the control that had it. Controls are matched by id or by
// data-fk, since the elements themselves are new.
function redraw(box, fn) {
  const a = document.activeElement;
  const key = !a || !box.contains(a) ? null
    : a.id ? "#" + CSS.escape(a.id)
    : a.dataset.fk ? `[data-fk="${CSS.escape(a.dataset.fk)}"]` : null;
  fn();
  if (key) box.querySelector(key)?.focus();
}

/* ---------- announcements for screen readers ---------- */

const liveTimers = new Map();   // live region id -> timer that empties it again

// speak says a message without showing it. While a modal dialog is open the
// rest of the page is inert, so the message goes through that dialog.
function speak(text, urgent) {
  const dialog = $("picker").open ? $("picker") : $("sheet").open ? $("sheet") : null;
  const target = dialog || document;
  if (typeof target.ariaNotify === "function") {
    target.ariaNotify(text, { priority: urgent ? "high" : "normal" });
    return;
  }
  // No ariaNotify: use the live region that is already in the page.
  const region = $(dialog ? dialog.id + "-live" : "page-live");
  clearTimeout(liveTimers.get(region.id));
  region.textContent = "";
  setTimeout(() => {
    region.textContent = text;
    if (!dialog) $("page-live-label").hidden = false;
  }, 50);
  // Emptied afterwards, so it is not found later when reading the page.
  liveTimers.set(region.id, setTimeout(() => {
    region.textContent = "";
    if (!dialog) $("page-live-label").hidden = true;
  }, 10000));
}

let announceTimer = null;
function announce(text, { delay = 0, urgent = false } = {}) {
  clearTimeout(announceTimer);
  if (delay) announceTimer = setTimeout(() => speak(text, urgent), delay);
  else speak(text, urgent);
}

/* ---------- notifications ---------- */

// Notifications report on something that happened on the page. An error
// stays until it is dismissed; a confirmation leaves after a few seconds,
// because the board already shows the result. None appear over a modal
// dialog: they wait in toastQueue until it closes. Errors that belong to a
// dialog are shown inside it instead (sheetError, pickerError).
const toastBox = $("toasts");
const toastQueue = [];
const TOAST_MAX = 4;
let lastFocus = null;       // where focus was before it went into a notification
let lastModalClose = 0;

function syncToasts() {
  const show = toastBox.children.length > 0 && !modalOpen();
  if (typeof toastBox.showPopover !== "function") { toastBox.classList.toggle("open", show); return; }
  const open = toastBox.matches(":popover-open");
  if (show && !open) toastBox.showPopover();
  if (!show && open) toastBox.hidePopover();
}

function toast(msg, kind) {
  if (modalOpen()) { toastQueue.push([msg, kind]); return; }
  for (const old of [...toastBox.children]) if (old.dataset.msg === msg) old.remove();   // said again: replace
  while (toastBox.children.length >= TOAST_MAX) toastBox.firstElementChild.remove();

  const item = document.createElement("div");
  item.className = "toast" + (kind === "error" ? " error" : "");
  item.dataset.msg = msg;
  const text = document.createElement("p");
  text.textContent = msg;
  const close = document.createElement("button");
  close.type = "button";
  close.dataset.act = "toast-dismiss";
  close.innerHTML = `Dismiss${vh(" notification")}`;
  item.append(text, close);
  toastBox.append(item);
  syncToasts();

  // A dialog that has just closed moves focus back to its opener; a message
  // spoken at the same moment would be cut off, so it waits a little.
  const delay = performance.now() - lastModalClose < 500 ? 700 : 0;
  announce((kind === "error" ? "Error: " : "") + msg, { urgent: kind === "error", delay });
  if (kind !== "error") expireToast(item, 5000);
}
const fail = (what, e) => toast(what + ": " + e.message, "error");

function expireToast(item, ms) {
  setTimeout(() => {
    if (!item.isConnected) return;
    if (item.contains(document.activeElement)) return expireToast(item, 1000);   // not while someone is on its button
    item.remove();
    syncToasts();
  }, ms);
}

// dismissToast is the Dismiss button. Focus goes to the next notification,
// or back to where it was before it entered the stack.
function dismissToast(item) {
  const hadFocus = item.contains(document.activeElement);
  const next = item.nextElementSibling || item.previousElementSibling;
  item.remove();
  syncToasts();
  if (!hadFocus) return;
  if (next) next.querySelector("button").focus();
  else if (lastFocus) (lastFocus.el.isConnected ? lastFocus.el : byFk(lastFocus.fk))?.focus();
}

function flushToasts() {
  if (modalOpen()) return;
  for (const [msg, kind] of toastQueue.splice(0)) toast(msg, kind);
  syncToasts();
}

document.addEventListener("focusin", ev => {
  if (!toastBox.contains(ev.target)) lastFocus = { el: ev.target, fk: ev.target.dataset ? ev.target.dataset.fk : "" };
});

/* ---------- data ---------- */

async function refresh() {
  try {
    state = await api("/api/status");
    state.projects = state.projects || [];
    render();
    loaded = true;
  } catch (e) { fail("status", e); }
}

let refreshTimer = null;
function refreshSoon() {
  clearTimeout(refreshTimer);
  refreshTimer = setTimeout(refresh, 250);
}

async function loadPHP() {
  try { php = await api("/api/php"); }
  catch { php = { roots: [], installs: [] }; }   // daemon without PHP discovery
  php.roots = php.roots || [];
  php.installs = php.installs || [];
  renderPHPChip();
  if (sheet && sheet.kind === "php" && !picker) renderSheet();
}

/* ---------- what a task is doing ---------- */

const busy = t => t.status === "starting" || t.status === "running" || t.status === "stopping";
const failed = t => !busy(t) && (t.status === "failed" || !!t.start_error);
const primaryOf = p => p.tasks.find(t => t.primary) || null;
const nameOf = p => p.label || p.id;

// taskState reduces a task to one of four kinds, each with its own node
// shape and colour, plus the sentence shown next to it.
function taskState(p, t) {
  if (t.external)
    return t.status === "running"
      ? { kind: "up", text: "Watched · up" + (t.port ? " · :" + t.port : "") }
      : { kind: "off", text: "Watched · not answering" + (t.port ? " on :" + t.port : "") };
  if (t.status === "running") {
    const bits = ["Running", since(t.started_at)];
    if (!t.primary && t.port) bits.push(":" + t.port);
    if (t.run) bits.push("run " + t.run);
    return { kind: "up", text: bits.filter(Boolean).join(" · ") };
  }
  if (t.status === "starting") {
    const bits = ["Starting"];
    if (t.port) bits.push("waiting for :" + t.port);
    if (t.run) bits.push("attempt " + t.run);
    return { kind: "wait", text: bits.join(" · ") };
  }
  if (t.status === "stopping") return { kind: "wait", text: "Stopping" };
  if (failed(t)) {
    const bits = ["Failed"];
    if (t.exit_code != null && !t.start_error) bits.push("exit code " + t.exit_code);
    if (t.run) bits.push(t.run + " attempts");
    return { kind: "fail", text: bits.join(" · ") };
  }
  const primary = primaryOf(p);
  if (primary && primary !== t && t.autostart && failed(primary))
    return { kind: "off", text: "Waiting · starts once " + primary.id + " is up" };
  return { kind: "off", text: t.autostart ? "Stopped" : "Stopped · manual start" };
}

function since(iso) {
  if (!iso) return "";
  const s = Math.max(0, Math.round((Date.now() - new Date(iso).getTime()) / 1000));
  if (s < 60) return s + " s";
  const m = Math.floor(s / 60);
  if (m < 60) return m + " min";
  const h = Math.floor(m / 60);
  if (h < 24) return h + " h" + (m % 60 ? " " + (m % 60) + " min" : "");
  return Math.floor(h / 24) + " d" + (h % 24 ? " " + (h % 24) + " h" : "");
}

// The project's ring: one node per task, the primary task on top. With three
// tasks up it is the logo. It is decoration: the task list says the same in words.
function ringSVG(p, size) {
  const tasks = p.tasks.toSorted((a, b) => (b.primary ? 1 : 0) - (a.primary ? 1 : 0)).slice(0, 8);
  let active = false;
  const nodes = tasks.map((t, i) => {
    const kind = taskState(p, t).kind;
    if (kind !== "off") active = true;
    const a = (-90 + i * 360 / tasks.length) * Math.PI / 180;
    const cx = (32 + 20 * Math.cos(a)).toFixed(2), cy = (32 + 20 * Math.sin(a)).toFixed(2);
    const r = kind === "off" ? 5.5 : kind === "wait" ? 5 : 7;
    return `<circle class="n-${kind}${t.primary ? " primary" : ""}" cx="${cx}" cy="${cy}" r="${r}"/>`;
  }).join("");
  return `<svg class="ring${active ? "" : " idle"}" viewBox="0 0 64 64" width="${size}" height="${size}" aria-hidden="true">
    <circle class="r" cx="32" cy="32" r="20"/>${nodes}</svg>`;
}

// noteFailures tells a screen reader about a task that has just failed. The
// card shows it too, but a card is not announced when it changes. Each
// failure is spoken once; the failures already there at load are not.
const knownFailures = new Set();
function noteFailures(silent) {
  const now = new Set(), fresh = [];
  for (const p of state.projects)
    for (const t of p.tasks) {
      if (!failed(t)) continue;
      const key = p.id + ":" + t.id;
      now.add(key);
      if (!knownFailures.has(key)) fresh.push(`${t.id} in ${nameOf(p)} failed`);
    }
  knownFailures.clear();
  for (const key of now) knownFailures.add(key);
  if (!silent && fresh.length) announce(fresh.join(". ") + ".");
}

/* ---------- board ---------- */

// act builds the attributes of an actionable element. data-fk lets a redraw
// put keyboard focus back where it was; a button that changes its action
// with the state (Stop becomes Start) passes a slot name that stays the same.
const act = (name, p, t, slot) =>
  `data-act="${name}" data-p="${esc(p || "")}" data-t="${esc(t || "")}" data-fk="${slot || name}|${esc(p || "")}|${esc(t || "")}"`;

const ARROW = `<svg viewBox="0 0 16 16" width="13" height="13" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="2.2"><path d="M5 3h8v8M13 3L3 13"/></svg>`;

function cleanStartError(msg) {
  return String(msg).replace(/^supervisor: (cannot start \S+: )?/, "").replace(/^portcheck: /, "");
}

function failboxHTML(p) {
  const name = nameOf(p);
  if (p.error)
    return `<div class="failbox">
      <strong><span class="node" aria-hidden="true"></span>This project cannot start</strong>
      <code>${esc(p.error)}</code>
      <div class="row"><button type="button" class="btn md accent" ${act("settings", p.id, "", "fail-settings")}>Open settings${vh(" of " + name)}</button></div>
    </div>`;
  const t = p.tasks.find(failed);
  if (!t) return "";
  let title, detail;
  if (t.start_error) {
    title = `${t.id} could not start`;
    detail = cleanStartError(t.start_error);
  } else {
    title = `${t.id} stopped` + (t.run ? ` after ${t.run} attempts` : "") +
      (t.exit_code != null ? ` (exit code ${t.exit_code})` : "");
    detail = t.last_output || "";
  }
  return `<div class="failbox">
    <strong><span class="node" aria-hidden="true"></span>${esc(title)}</strong>
    ${detail ? `<code>${esc(detail)}</code>` : ""}
    <div class="row">
      <button type="button" class="btn md accent" ${act("task-restart", p.id, t.id, "fail-retry")}>Try again${vh(`: ${t.id} in ${name}`)}</button>
      <button type="button" class="btn md outline" ${act("settings", p.id, "", "fail-settings")}>Settings${vh(" of " + name)}</button>
      <button type="button" class="btn md outline" ${act("log", p.id, t.id, "fail-log")}>Log${vh(` of ${t.id} in ${name}`)}</button>
    </div>
  </div>`;
}

function taskHTML(p, t) {
  const st = taskState(p, t);
  const what = `${t.id} in ${nameOf(p)}`;
  const buttons = t.external || p.error ? "" : `
    <button type="button" class="btn sm raise" ${act("log", p.id, t.id)}>Log${vh(" of " + what)}</button>
    ${busy(t)
      ? `<button type="button" class="btn sm raise" ${act("task-stop", p.id, t.id, "task-toggle")}>Stop${vh(" " + what)}</button>`
      : `<button type="button" class="btn sm raise" ${act("task-start", p.id, t.id, "task-toggle")}>Start${vh(" " + what)}</button>`}`;
  return `<li class="task${st.kind === "fail" ? " failed" : ""}">
    <span class="node ${st.kind}${t.primary ? " primary" : ""}" aria-hidden="true"></span>
    <span class="name">${esc(t.id)}</span>
    <span class="state">${esc(st.text)}</span>${buttons}
  </li>`;
}

function cardHTML(p) {
  const name = nameOf(p);
  const primary = primaryOf(p);
  const managed = p.tasks.filter(t => !t.external);
  const anyBusy = managed.some(busy);
  const attention = !!p.error || p.tasks.some(failed);
  const meta = [p.php ? "PHP " + p.php : "", p.path].filter(Boolean).join(" · ");

  // The visible text of the link is the port; its full name starts with what it does.
  let door = "";
  if (p.url && primary && primary.status === "running")
    door = `<a class="open" href="${esc(p.url)}" target="_blank" rel="noopener" data-open data-fk="open|${esc(p.id)}|">${vh(`Open ${name} in the browser, port `)}:${p.port} ${ARROW}</a>`;
  else if (p.port)
    door = `<span class="port">:${p.port}${vh(" (not running)")}</span>`;

  let foot = "";
  if (!p.error && managed.length)
    foot = anyBusy
      ? `<button type="button" class="btn md strong" ${act("proj-stop", p.id, "", "proj-toggle")}>Stop project${vh(" " + name)}</button>
         <button type="button" class="btn md raise" ${act("proj-restart", p.id)}>Restart${vh(" " + name)}</button>`
      : `<button type="button" class="btn md strong" ${act("proj-start", p.id, "", "proj-toggle")}>Start project${vh(" " + name)}</button>`;

  return `<article class="card${attention ? " attention" : ""}" aria-labelledby="card-${esc(p.id)}">
    <header class="card-head">
      ${ringSVG(p, 52)}
      <div class="card-title">
        <h2 id="card-${esc(p.id)}">${esc(name)}</h2>
        <span class="meta">${esc(meta)}</span>
      </div>
      ${door}
    </header>
    ${failboxHTML(p)}
    <ul class="tasks">${p.tasks.map(t => taskHTML(p, t)).join("")}</ul>
    <footer class="card-foot">
      ${foot}<span class="spacer"></span>
      <button type="button" class="btn link" ${act("settings", p.id)}>Settings${vh(" of " + name)}</button>
    </footer>
  </article>`;
}

function renderBoard() {
  const board = $("board");
  redraw(board, () => {
    if (!state.projects.length) {
      board.innerHTML = `<div class="firstrun">
        <h2>No projects yet</h2>
        <p>Add a project by picking its folder, a PHP version and a template. Ringmaster then starts,
           stops and watches its tasks from here.</p>
        <button type="button" class="btn strong" ${act("add")}>+ Add your first project</button>
        ${state.config_path ? `<p>It is saved to <span class="path">${esc(state.config_path)}</span></p>` : ""}
      </div>`;
    } else {
      board.innerHTML = state.projects.map(cardHTML).join("") +
        `<button type="button" class="addcard" ${act("add")}>
          <span class="big">+ Add a project</span>
          <span class="small">Pick its folder, a PHP version and a template. Laravel is set up for you.</span>
        </button>`;
    }
  });

  if (firstBoard) {   // the entrance plays once, not on every status change
    firstBoard = false;
    board.classList.add("intro");
    setTimeout(() => board.classList.remove("intro"), 1200);
  }
}

let summaryHTML = null;
function renderSummary() {
  const tasks = state.projects.flatMap(p => p.tasks.filter(t => !t.external));
  const running = tasks.filter(t => t.status === "running").length;
  const starting = tasks.filter(t => t.status === "starting").length;
  const attention = state.projects.filter(p => p.error || p.tasks.some(failed)).length;
  const n = state.projects.length;
  const bits = [`${n} project${n === 1 ? "" : "s"}`, `${running} of ${tasks.length} tasks running`];
  if (starting) bits.push(`${starting} starting`);
  const html = n === 0 ? "Bring your own binaries"
    : esc(bits.join(" · ")) + (attention ? ` · <strong>${attention} need${attention === 1 ? "s" : ""} attention</strong>` : "");
  if (html === summaryHTML) return;   // written only when it changes
  summaryHTML = html;
  $("summary").innerHTML = html;
}

function renderPHPChip() {
  const minors = [...new Set(php.installs.map(i => i.version.split(".").slice(0, 2).join(".")))]
    .toSorted((a, b) => compareVersions(b, a));
  $("phpchip").textContent = minors.length
    ? "PHP " + minors.slice(0, 4).join(" · ") + (minors.length > 4 ? " …" : "")
    : "PHP: none found";
}

function render() {
  renderSummary();
  renderBoard();
  noteFailures(!loaded);
  if (log) renderLogTabs();
}

/* ---------- actions ---------- */

async function taskAction(project, task, action) {
  try { await api(`/api/tasks/${project}/${task}/${action}`, { method: "POST" }); }
  catch (e) { fail(`${task} ${action}`, e); }
  refresh();
}

async function projectAction(project, action) {
  try { await api(`/api/projects/${project}/${action}`, { method: "POST" }); }
  catch (e) { fail(`${project} ${action}`, e); }
  refresh();
}

async function allProjects(action) {
  const errors = [];
  for (const p of state.projects) {
    if (p.error || !p.tasks.some(t => !t.external)) continue;
    try { await api(`/api/projects/${p.id}/${action}`, { method: "POST" }); }
    catch (e) { errors.push(`${nameOf(p)}: ${e.message}`); }
  }
  if (errors.length) toast(errors.join(" · "), "error");
  refresh();
}

/* ---------- theme ---------- */

// The choice lives in <meta name="color-scheme">: "light dark" follows the
// system, "light" or "dark" pins one. The stylesheet reads the meta, so
// nothing here has to touch a class or a style.
const schemeMeta = document.querySelector('meta[name="color-scheme"]');

function storedScheme() {
  try {
    const v = localStorage.getItem("color-scheme");
    return v === "light" || v === "dark" ? v : null;
  } catch { return null; }   // storage blocked: follow the system
}

// switchTheme flips what is on screen now. Going back to what the system
// asks for removes the stored choice instead of pinning it, so the page
// follows the system again when that changes later.
function switchTheme() {
  const system = matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
  const shown = schemeMeta.content === "light" || schemeMeta.content === "dark" ? schemeMeta.content : system;
  const target = shown === "dark" ? "light" : "dark";
  const pin = target === system ? null : target;
  try {
    if (pin) localStorage.setItem("color-scheme", pin);
    else localStorage.removeItem("color-scheme");
  } catch { /* not remembered, still applied below */ }
  schemeMeta.content = pin || "light dark";
}

// Another tab changed the theme.
window.addEventListener("storage", ev => {
  if (ev.key === "color-scheme" || ev.key === null) schemeMeta.content = storedScheme() || "light dark";
});

/* ---------- log panel ---------- */

const LOG_KEEP = 2000;
const narrow = matchMedia("(max-width: 67.5em)");

// In a narrow window the log covers the page, so nothing under it may take
// focus or be read. In a wide one it is a column next to the board.
function syncLogCover() {
  const cover = !!log && narrow.matches;
  for (const id of ["top", "board", "bottomnav"]) $(id).inert = cover;
}
narrow.addEventListener("change", syncLogCover);

function openLog(project, task) {
  const prev = log;
  closeLogStream();
  log = { project, task, entries: [], pending: [], find: prev ? prev.find : "", follow: true, close: null, missing: false,
    openerFk: prev ? prev.openerFk : "" };
  $("logpanel").hidden = false;
  redraw($("logpanel"), renderLogShell);
  syncLogCover();
  log.close = Backend.openLogs(project, task, {
    onEntry: entry => {
      if (!log || log.project !== project || log.task !== task) return;
      log.pending.push(entry);
      if (log.pending.length === 1) requestAnimationFrame(flushLog);
    },
    onError: () => {
      if (!log || log.project !== project || log.task !== task) return;
      log.missing = true;
      renderLogLines();
    },
  });
}

function closeLogStream() {
  if (log && log.close) log.close();
  if (log) log.close = null;
}

function closeLog() {
  if (!log) return;
  const panel = $("logpanel");
  const hadFocus = panel.contains(document.activeElement);
  const opener = log.openerFk;
  closeLogStream();
  log = null;
  panel.hidden = true;
  panel.replaceChildren();
  syncLogCover();
  if (hadFocus) byFk(opener)?.focus();   // back to the Log button it was opened from
}

function renderLogShell() {
  const p = state.projects.find(x => x.id === log.project);
  $("logpanel").innerHTML = `
    <div class="log-head">
      <div class="log-title">
        <h2 id="log-title" tabindex="-1">${esc(p ? nameOf(p) : log.project)} · ${esc(log.task)}${vh(" log")}</h2>
        <button type="button" class="btn sm" data-act="close-log" id="log-close">Close${vh(" log")}</button>
      </div>
      <div class="log-row" id="log-tabs" role="group" aria-label="Task"></div>
      <search><label class="log-find">Find in this log
        <input type="search" id="log-find" value="${esc(log.find)}" autocomplete="off" spellcheck="false">
      </label></search>
    </div>
    <ol class="log-lines" id="log-lines" tabindex="0" aria-label="Log lines"></ol>
    <p class="log-foot">Shows the last ${LOG_KEEP.toLocaleString("en")} lines since Ringmaster started. Older output is kept in
      <span class="mono">${esc(log.project)}_${esc(log.task)}.log</span>.</p>`;
  renderLogTabs();
  renderLogLines();
  $("log-lines").addEventListener("scroll", ev => {
    const el = ev.currentTarget;
    if (log) log.follow = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
  });
}

function renderLogTabs() {
  const p = state.projects.find(x => x.id === log.project);
  const holder = $("log-tabs");
  if (!p || !holder) return;
  const t = p.tasks.find(x => x.id === log.task);
  redraw(holder, () => {
    holder.innerHTML = p.tasks.filter(x => !x.external).map(x =>
      `<button type="button" class="btn sm" aria-pressed="${x.id === log.task}" ${act("log", p.id, x.id, "log-tab")}>${esc(x.id)}</button>`).join("") +
      (t ? `<button type="button" class="btn sm" ${act("task-restart", p.id, t.id, "log-restart")}>Restart ${esc(t.id)}</button>` : "");
  });
}

// Log text comes from the task, so it is set as text, never as markup.
function logLineNode(entry) {
  const d = new Date(entry.ts);
  let text = entry.line, cls = entry.stream === "err" ? "err" : "";
  // Ringmaster's own lines are events, not output; a start begins a new run.
  if (text.startsWith("ringmaster: ")) {
    text = text.slice(12);
    cls = "ev" + (text.startsWith("started") ? " run" : "");
  }
  const li = document.createElement("li");
  li.className = cls;
  const time = document.createElement("time");
  time.textContent = [d.getHours(), d.getMinutes(), d.getSeconds()].map(n => String(n).padStart(2, "0")).join(":");
  const span = document.createElement("span");
  span.className = "text";
  const at = log.find ? text.toLowerCase().indexOf(log.find.toLowerCase()) : -1;
  if (at < 0) span.textContent = text;
  else {
    const mark = document.createElement("mark");
    mark.textContent = text.slice(at, at + log.find.length);
    span.append(text.slice(0, at), mark, text.slice(at + log.find.length));
  }
  li.append(time, span);
  return li;
}

const matchesFind = entry => !log.find || entry.line.toLowerCase().includes(log.find.toLowerCase());

function renderLogLines() {
  const list = $("log-lines");
  if (!list) return;
  const shown = log.entries.filter(matchesFind);
  if (!shown.length) {
    const li = document.createElement("li");
    li.className = "ev placeholder";
    const span = document.createElement("span");
    span.className = "text";
    span.textContent = log.missing ? "No output yet. Start the task to see its log."
      : log.find ? "No line contains that text." : "Waiting for output.";
    li.append(span);
    list.replaceChildren(li);
    return;
  }
  list.replaceChildren(...shown.map(logLineNode));
  if (log.follow) list.scrollTop = list.scrollHeight;
}

// flushLog adds the lines that arrived since the last frame in one go: the
// first message of a stream is the whole buffer, up to 2000 lines.
function flushLog() {
  if (!log) return;
  const list = $("log-lines");
  const fresh = log.pending.splice(0);
  const wasEmpty = log.entries.length === 0;
  log.entries.push(...fresh);
  log.missing = false;
  if (log.entries.length > LOG_KEEP) {
    log.entries.splice(0, log.entries.length - LOG_KEEP);
    renderLogLines();
    return;
  }
  if (wasEmpty || !list) { renderLogLines(); return; }
  const nodes = fresh.filter(matchesFind).map(logLineNode);
  if (nodes.length) {
    list.querySelector(".placeholder")?.remove();
    list.append(...nodes);
    if (log.follow) list.scrollTop = list.scrollHeight;
  }
}

/* ---------- dialogs ---------- */

// askToClose closes a dialog the way Esc does: its "cancel" event runs first
// and may refuse, which is how the editor protects unsaved changes.
function askToClose(dialog) {
  if (typeof dialog.requestClose === "function") dialog.requestClose();
  else if (dialog.dispatchEvent(new Event("cancel", { cancelable: true }))) dialog.close();
}

// afterModal runs when a dialog has closed. The browser has already moved
// focus back to the control that opened it. The board is redrawn while a
// dialog is open, which replaces that control, so its successor is found by
// data-fk when focus was left on nothing.
function afterModal(openerFk) {
  lastModalClose = performance.now();
  if (openerFk && (!document.activeElement || document.activeElement === document.body)) byFk(openerFk)?.focus();
  flushToasts();
}

// closedby="any" also closes a dialog on a click outside it. Where the
// attribute is not supported, do the same by hand. The press and the release
// must both be outside, so dragging a text selection out does not close it.
if (!("closedBy" in HTMLDialogElement.prototype)) {
  const outside = (e, dialog) => {
    const r = dialog.getBoundingClientRect();
    return e.clientX < r.left || e.clientX > r.right || e.clientY < r.top || e.clientY > r.bottom;
  };
  for (const dialog of [$("sheet"), $("picker")]) {
    let pressedOutside = false;
    dialog.addEventListener("pointerdown", e => { pressedOutside = e.target === dialog && outside(e, dialog); });
    dialog.addEventListener("pointerup", e => {
      if (pressedOutside && e.target === dialog && outside(e, dialog) && dialog.getAttribute("closedby") === "any") askToClose(dialog);
    });
  }
}

/* ---------- sheet: settings, add project, PHP ---------- */

function openSheet(kind, draft) {
  const dialog = $("sheet");
  const from = document.activeElement;
  sheet = { kind, draft: draft || null, dirty: false, saving: false, error: "", openerFk: from && from.dataset ? from.dataset.fk || "" : "" };
  // A form closes on Esc or its own buttons, and asks first when something
  // was changed. The PHP sheet has nothing to lose: a click outside closes it.
  dialog.setAttribute("closedby", kind === "php" ? "any" : "closerequest");
  dialog.classList.remove("guarding");
  renderSheet();
  if (!dialog.open) dialog.showModal();
  syncToasts();
}

// Esc, the Close and Cancel buttons and a click outside all arrive here.
$("sheet").addEventListener("cancel", ev => {
  if (ev.target !== $("sheet") || !sheet || !sheet.dirty) return;
  ev.preventDefault();
  $("sheet").classList.add("guarding");
  $("sheet-discard")?.focus();   // the button is described by the "unsaved changes" line
});

$("sheet").addEventListener("close", () => {
  const opener = sheet ? sheet.openerFk : "";
  sheet = null;
  $("sheet").classList.remove("guarding");
  $("sheet-frame").replaceChildren();
  afterModal(opener);
});

// sheetError shows a failure inside the open sheet, next to what caused it.
function sheetError(msg) {
  if (!sheet) { if (msg) toast(msg, "error"); return; }
  sheet.error = msg;
  const el = $("sheet-error");
  if (el) {
    el.hidden = !msg;
    el.querySelector(".text").textContent = msg;
  }
  if (msg) announce("Error: " + msg, { urgent: true });
}

async function openSettings(projectId) {
  try {
    const draft = await api("/api/config/projects/" + projectId);
    draft.tasks = draft.tasks || [];
    openSheet("settings", draft);
  } catch (e) { fail("settings", e); }
}

function openAdd() {
  openSheet("add", { id: "", label: "", path: "", php: "", port: 0, tasks: presetTasks("laravel") });
}

function renderSheet() {
  if (!sheet) return;
  const frame = $("sheet-frame");
  redraw(frame, () => {
    const typed = $("php-root-path") ? $("php-root-path").value : "";   // survives a redraw of the PHP sheet
    frame.innerHTML = sheet.kind === "php" ? phpSheetHTML() : editorSheetHTML();
    if (typed && $("php-root-path")) $("php-root-path").value = typed;
  });
  for (const el of frame.querySelectorAll("input, select, textarea")) syncAria(el);
}

function sheetHead(title, hint) {
  return `<div class="sheet-head">
    <div><h1 id="sheet-title">${esc(title)}</h1>${hint ? `<span class="hint">${esc(hint)}</span>` : ""}</div>
    <button type="button" class="btn tile sm" data-act="close-sheet" id="sheet-close">Close</button>
  </div>`;
}

const ERROR_ICON = `<span class="error-icon" role="img" aria-label="Error">!</span>`;
const REQUIRED = `<span aria-hidden="true"> *</span>`;
// errorMsg is the text under a field. CSS shows it once the field is
// :user-invalid; the field points at it through data-error.
const errorMsg = (id, text) => `<p class="error-msg" id="${id}">${ERROR_ICON}${esc(text)}</p>`;
const formError = msg => `<p class="form-error" id="sheet-error" ${msg ? "" : "hidden"}>${ERROR_ICON}<span class="text">${esc(msg)}</span></p>`;
const ID_PATTERN = "[a-z0-9][a-z0-9_\\-]*";   // the daemon's rule for project and task ids

/* -- project editor -- */

function editorSheetHTML() {
  const d = sheet.draft, adding = sheet.kind === "add";
  return `<form class="frame-in" id="editor" data-form="editor">` +
  sheetHead(adding ? "Add a project" : "Settings · " + (d.label || d.id), state.config_path) + `
  <div class="sheet-body">
    <p class="quiet">Fields marked * are required.</p>
    <div class="field"><label for="ed-path">Folder${REQUIRED}</label>
      <div class="with-browse">
        <input class="input mono" id="ed-path" value="${esc(d.path || "")}" required autofocus data-error="ed-path-error"
          autocomplete="off" spellcheck="false">
        <button type="button" class="btn tile" data-act="browse" data-target="ed-path" id="ed-path-browse">Browse${vh(" for the project folder")}</button>
      </div>
      ${errorMsg("ed-path-error", "Pick the project's folder.")}</div>
    ${adding ? `<div class="field"><label for="ed-id">Id${REQUIRED}</label>
      <input class="input mono" id="ed-id" value="${esc(d.id)}" required pattern="${ID_PATTERN}" data-error="ed-id-error" aria-describedby="ed-id-hint"
        autocomplete="off" spellcheck="false">
      <span class="hint" id="ed-id-hint">Short and lower case, for example myapp. It is what you type on the command line.</span>
      ${errorMsg("ed-id-error", "Enter an id made of lower-case letters, digits, - or _.")}</div>` : ""}
    <div class="field"><label for="ed-label">Name</label>
      <input class="input" id="ed-label" value="${esc(d.label || "")}" aria-describedby="ed-label-hint" autocomplete="off">
      <span class="hint" id="ed-label-hint">Shown on the board. Leave empty to show the id.</span></div>
    <div class="row2">
      <div class="field"><label for="ed-php">PHP version</label>
        <input class="input mono" id="ed-php" value="${esc(d.php || "")}" aria-describedby="ed-php-hint" autocomplete="off">
        <span class="hint" id="ed-php-hint">For example 8.3. Leave empty if no task uses {php}.</span></div>
      <div class="field"><label for="ed-port">Port</label>
        <input class="input mono" id="ed-port" type="number" min="1" max="65535" step="1" value="${d.port || ""}" data-error="ed-port-error" aria-describedby="ed-port-hint">
        <span class="hint" id="ed-port-hint">Leave empty to have one picked.</span>
        ${errorMsg("ed-port-error", "Enter a port from 1 to 65535.")}</div>
    </div>
    ${adding ? `<div class="field"><label for="ed-preset">Template</label>
      <select class="input" id="ed-preset">
        <option value="laravel">Laravel: serve, Vite and queue worker</option>
        <option value="laravel-serve">Laravel: serve only</option>
        <option value="custom">One command of your own</option>
      </select></div>` : ""}
    <div class="field" role="group" aria-labelledby="ed-tasks-label">
      <span class="label" id="ed-tasks-label">Tasks</span>
      ${adding ? "" : `<span class="hint">A change to a task applies the next time that task is restarted.</span>`}
      ${d.tasks.map(taskRowHTML).join("")}
      <button type="button" class="addline" data-act="ed-add-task" id="ed-add-task">+ Add a task</button>
    </div>
    ${adding ? "" : `<div class="danger-zone">
      <button type="button" class="btn danger" id="ed-remove" ${act("remove-project", d.id)}>Remove this project</button>
      <p>Stops its tasks and takes it out of ringmaster.yaml. The folder itself is not touched.</p>
    </div>`}
  </div>
  <div class="sheet-foot">
    ${formError(sheet.error)}
    <p class="guard" id="sheet-guard">You have unsaved changes.</p>
    <div class="foot-row">
      <button type="submit" class="btn strong" id="ed-save">${adding ? "Add project" : "Save"}</button>
      <button type="button" class="btn tile" data-act="close-sheet" id="ed-cancel">Cancel</button>
      <button type="button" class="btn danger discard" data-act="discard-sheet" id="sheet-discard" aria-describedby="sheet-guard">Discard${vh(" changes")}</button>
    </div>
  </div></form>`;
}

function taskRowHTML(t, i) {
  const envText = envToText(t.env);
  const label = t.id || "new task";
  return `<div class="taskrow" role="group" aria-label="Task ${i + 1}: ${esc(label)}">
    <div class="kv">
      <div class="field tr-name"><label for="ed-tn-${i}">Name${REQUIRED}</label>
        <input class="input mono" id="ed-tn-${i}" value="${esc(t.id || "")}" required pattern="${ID_PATTERN}" data-error="ed-tn-${i}-error"
          data-edt="id" data-i="${i}" autocomplete="off" spellcheck="false"></div>
      <div class="field"><label for="ed-tc-${i}">Command</label>
        <input class="input mono" id="ed-tc-${i}" value="${esc(argvToText(t.argv))}" placeholder="{php} artisan serve --host={host} --port={port}"
          data-edt="argv" data-i="${i}" autocomplete="off" spellcheck="false"></div>
      <button type="button" class="btn raise sm" data-act="ed-rm-task" data-i="${i}">Remove${vh(" task " + label)}</button>
      ${errorMsg(`ed-tn-${i}-error`, "Enter a task name made of lower-case letters, digits, - or _.")}
    </div>
    <div class="opts">
      <label class="check"><input type="checkbox" id="ed-tp-${i}" data-edt="primary" data-i="${i}" ${t.primary ? "checked" : ""}> main task (owns the port)</label>
      <label class="check"><input type="checkbox" id="ed-ta-${i}" data-edt="autostart" data-i="${i}" ${t.autostart === false ? "" : "checked"}> starts with the project</label>
      <label class="check"><input type="checkbox" id="ed-tx-${i}" data-edt="external" data-i="${i}" ${t.external ? "checked" : ""}> only watch it</label>
      <div class="when"><label for="ed-tr-${i}">When it exits</label>
        <select class="input" id="ed-tr-${i}" data-edt="restart" data-i="${i}">
          <option value="" ${!t.restart || t.restart === "no" ? "selected" : ""}>never restart</option>
          <option value="on-failure" ${t.restart === "on-failure" ? "selected" : ""}>restart after a crash</option>
          <option value="always" ${t.restart === "always" ? "selected" : ""}>always restart</option>
        </select></div>
    </div>
    <details class="taskmore" data-i="${i}" ${t.cwd || envText || t._more ? "open" : ""}>
      <summary>Folder and environment${t.cwd || envText ? " (set)" : ""}</summary>
      <div class="field"><label for="ed-cwd-${i}">Run in subfolder</label>
        <input class="input mono" id="ed-cwd-${i}" value="${esc(t.cwd || "")}" aria-describedby="ed-cwd-${i}-hint" placeholder="frontend" data-edt="cwd" data-i="${i}" autocomplete="off" spellcheck="false">
        <span class="hint" id="ed-cwd-${i}-hint">Leave empty to run in the project folder.</span></div>
      <div class="field"><label for="ed-env-${i}">Environment variables</label>
        <textarea class="input mono" id="ed-env-${i}" rows="2" aria-describedby="ed-env-${i}-hint" data-error="ed-env-${i}-error" placeholder="APP_URL=http://{host}:{port}" data-edt="env" data-i="${i}" spellcheck="false">${esc(envText)}</textarea>
        <span class="hint" id="ed-env-${i}-hint">One KEY=value per line.</span>
        ${errorMsg(`ed-env-${i}-error`, "Write every line as KEY=value.")}</div>
    </details>
  </div>`;
}

// argv <-> one editable line. Arguments containing whitespace are wrapped in
// double quotes so paths like "C:\Program Files\..." survive a round trip.
const argvToText = argv => (argv || []).map(a => /\s/.test(a) || a === "" ? `"${a}"` : a).join(" ");
const textToArgv = s => [...s.matchAll(/"([^"]*)"|(\S+)/g)].map(m => m[1] ?? m[2]);

// env map <-> "KEY=value" lines. textToEnv returns null for a line without
// "=", so a typo is reported instead of silently dropped.
const envToText = env => Object.keys(env || {}).sort().map(k => `${k}=${env[k]}`).join("\n");
function textToEnv(s) {
  const env = {};
  for (const line of s.split("\n").map(l => l.trim()).filter(Boolean)) {
    const i = line.indexOf("=");
    if (i < 1) return null;
    env[line.slice(0, i).trim()] = line.slice(i + 1);
  }
  return env;
}

function presetTasks(preset) {
  const serve = { id: "serve", primary: true,
    argv: ["{php}", "artisan", "serve", "--host={host}", "--port={port}"], ready: { type: "port" } };
  if (preset === "laravel")
    return [serve,
      { id: "vite", argv: ["npm.cmd", "run", "dev", "--", "--host={host}"],
        ready: { type: "stdout", match: "ready in" }, cleanup: ["public/hot"] },
      { id: "queue", autostart: false, restart: "always", argv: ["{php}", "artisan", "queue:work"] }];
  if (preset === "laravel-serve") return [serve];
  return [{ id: "run", primary: true, argv: [] }];
}

// readEditor copies the form into the draft. Anything that redraws the form
// calls it first, or the redraw would wipe what was typed.
function readEditor() {
  const d = sheet.draft;
  if (!$("ed-path")) return d;
  d.label = $("ed-label").value.trim();
  d.path = $("ed-path").value.trim();
  d.php = $("ed-php").value.trim();
  d.port = $("ed-port").value ? Number($("ed-port").value) : 0;
  if ($("ed-id")) d.id = $("ed-id").value.trim();
  for (const el of $("editor").querySelectorAll("[data-edt]")) editTask(Number(el.dataset.i), el.dataset.edt, el);
  return d;
}

function redrawEditor() {
  const preset = $("ed-preset") ? $("ed-preset").value : "";
  renderSheet();
  if (preset && $("ed-preset")) $("ed-preset").value = preset;
}

function editTask(i, field, el) {
  const t = sheet.draft.tasks[i];
  if (!t) return;
  const value = el.type === "checkbox" ? el.checked : el.value;
  if (field === "argv") t.argv = textToArgv(value);
  else if (field === "id") t.id = value.trim();
  else if (field === "autostart") { if (value) delete t.autostart; else t.autostart = false; }
  else if (field === "cwd") { if (value.trim()) t.cwd = value.trim(); else delete t.cwd; }
  else if (field === "env") {
    const env = textToEnv(value);
    if (!env) return;   // the field is marked invalid; the draft keeps the last good value
    if (Object.keys(env).length) t.env = env; else delete t.env;
  }
  else t[field] = value;
}

// saveEditor runs on submit, so the browser has already checked the required
// fields and patterns and has put focus on the first one that failed.
async function saveEditor() {
  if (!sheet || sheet.saving) return;
  const d = readEditor(), adding = sheet.kind === "add";
  if (!d.tasks.length) return sheetError("Add at least one task.");
  // Keys starting with "_" are editor state, not config.
  const tasks = d.tasks.map(t => Object.fromEntries(Object.entries(t).filter(([k]) => !k.startsWith("_"))));
  const body = { id: d.id, label: d.label, path: d.path, php: d.php, port: d.port, tasks };
  sheet.saving = true;
  sheetError("");
  try {
    if (adding) await api("/api/config/projects", jsonBody("POST", body));
    else await api("/api/config/projects/" + d.id, jsonBody("PUT", body));
  } catch (e) {
    if (sheet) sheet.saving = false;
    return sheetError("Not saved: " + e.message);
  }
  $("sheet").close();
  toast(adding ? `Added ${d.label || d.id}` : "Saved. Restart a task to apply its changes.");
  refresh();
}

async function removeProject(id) {
  if (!confirm(`Remove "${id}" from Ringmaster? Its running tasks are stopped. The folder itself is not deleted.`)) return;
  try { await api("/api/config/projects/" + id, { method: "DELETE" }); }
  catch (e) { return sheetError("Not removed: " + e.message); }
  if (log && log.project === id) closeLog();
  $("sheet").close();
  toast(`Removed ${id}`);
  refresh();
}

/* -- PHP -- */

function compareVersions(a, b) {
  const as = a.split("."), bs = b.split(".");
  for (let i = 0; i < Math.max(as.length, bs.length); i++) {
    const d = (parseInt(as[i], 10) || 0) - (parseInt(bs[i], 10) || 0);
    if (d) return d;
  }
  return 0;
}

// resolveInstall mirrors the daemon: the highest installed version that
// matches the project's version as a prefix on a dot boundary.
function resolveInstall(constraint) {
  const matches = php.installs.filter(i => i.version === constraint || i.version.startsWith(constraint + "."));
  return matches.toSorted((a, b) => compareVersions(b.version, a.version))[0] || null;
}

function phpSheetHTML() {
  const users = new Map();   // install path -> project names
  const unresolved = [];
  for (const p of state.projects) {
    if (!p.php) continue;
    const install = resolveInstall(p.php);
    if (!install) { unresolved.push(`${nameOf(p)} wants ${p.php}`); continue; }
    users.set(install.path, [...(users.get(install.path) || []), nameOf(p)]);
  }
  return `<div class="frame-in">` + sheetHead("PHP versions", "Ringmaster uses the PHP you already have") + `
  <div class="sheet-body">
    <div class="field"><span class="label">Found on this machine</span>
      ${php.installs.length ? `<ul class="list">${php.installs.map(i => `<li>
          <span class="badge">${esc(i.version)}</span>
          <span class="grow">${esc(i.path)}${users.has(i.path) ? `<span class="sub">Used by ${esc(users.get(i.path).join(", "))}</span>` : ""}</span>
        </li>`).join("")}</ul>`
        : `<p class="quiet">No PHP found. Add the folder that holds your PHP versions below.</p>`}
      ${unresolved.length ? `<p class="form-error">${ERROR_ICON}Not installed: ${esc(unresolved.join("; "))}.</p>` : ""}
    </div>
    <div class="field"><span class="label">Folders Ringmaster searches</span>
      ${php.roots.length ? `<ul class="list">${php.roots.map((r, n) => `<li>
          <span class="grow">${esc(r)}</span>
          <button type="button" class="btn raise sm" data-act="rm-root" data-path="${esc(r)}" id="php-root-rm-${n}">Remove${vh(" " + r)}</button>
        </li>`).join("")}</ul>`
        : `<p class="quiet">None added, so Ringmaster uses the PHP on PATH and the default Laragon, XAMPP, Herd and Scoop folders.
            Adding a folder here switches those defaults off.</p>`}
    </div>
    <div class="field"><label for="php-root-path">Add a folder that holds PHP versions</label>
      <form class="inline-form" data-form="add-root">
        <input class="input mono" id="php-root-path" name="path" required placeholder="C:\\laragon\\bin\\php" autocomplete="off" spellcheck="false">
        <button type="button" class="btn tile" data-act="browse" data-target="php-root-path" id="php-root-browse">Browse${vh(" for the folder")}</button>
        <button type="submit" class="btn strong" id="php-root-add">Add folder</button>
      </form>
      ${formError(sheet.error)}
    </div>
  </div></div>`;
}

async function addRoot(form) {
  const path = String(new FormData(form).get("path") || "").trim();
  sheetError("");
  try { await api("/api/config/phproots", jsonBody("POST", { path })); }
  catch (e) { return sheetError("Folder not added: " + e.message); }
  form.reset();
  await loadPHP();
  $("php-root-path")?.focus();
  announce(`Added ${path}. ${php.installs.length} PHP version${php.installs.length === 1 ? "" : "s"} found.`);
  refresh();
}

async function removeRoot(path) {
  if (!confirm(`Stop searching "${path}"? Nothing on disk is deleted.`)) return;
  sheetError("");
  try { await api("/api/config/phproots", jsonBody("DELETE", { path })); }
  catch (e) { return sheetError("Folder not removed: " + e.message); }
  await loadPHP();
  if (sheet && !$("sheet-frame").contains(document.activeElement)) $("php-root-path")?.focus();   // its button is gone
  announce(`Removed ${path}.`);
  refresh();
}

/* ---------- folder picker ---------- */

// browseFor fills the input `target` with a folder the user picks. The
// desktop app uses the system dialog. A browser cannot hand a page a real
// folder path, so there the daemon lists folders and the page shows them.
async function browseFor(target) {
  const start = $(target).value.trim();
  if (Backend.isWails) {
    try {
      const chosen = await Backend.pickFolder(start);
      if (chosen) usePickedFolder(target, chosen);
    } catch (e) { sheetError("Folder dialog: " + e.message); }
    return;
  }
  if (picker) return;
  picker = { target, listing: null, lastError: "" };
  // Start where the field points, or at the home folder if that is not a folder.
  if (await pickerGo(start, true) || await pickerGo("", true)) return;
  const why = picker.lastError;
  picker = null;
  sheetError("Cannot list folders: " + why);
}

// pickerGo lists `path` in the picker and opens it if it is not open yet.
// It returns false when the folder could not be read; `quiet` keeps the
// reason out of the picker.
async function pickerGo(path, quiet) {
  let listing;
  try { listing = await api("/api/fs?path=" + encodeURIComponent(path)); }
  catch (e) {
    if (picker) picker.lastError = e.message;
    if (!quiet) pickerError("Cannot open that folder: " + e.message);
    return false;
  }
  if (!picker) return true;   // closed while loading
  picker.listing = listing;
  renderPicker();
  const dialog = $("picker");
  if (!dialog.open) {
    dialog.showModal();
    syncToasts();
  } else {
    // The list is new: focus starts again at its first folder.
    dialog.querySelector("[autofocus]")?.focus();
    announce(`${listing.dirs.length} folder${listing.dirs.length === 1 ? "" : "s"} in ${listing.path}`);
  }
  return true;
}

function pickerError(msg) {
  const el = $("picker-error");
  if (!el) return;
  el.hidden = !msg;
  el.querySelector(".text").textContent = msg;
  if (msg) announce("Error: " + msg, { urgent: true });
}

const MARKER_NOTES = [
  ["artisan", "A Laravel project is in this folder."],
  ["composer.json", "A Composer project is in this folder."],
  ["package.json", "A Node project is in this folder."],
  ["php.exe", "PHP is in this folder."],
  ["php", "PHP is in this folder."],
];

function renderPicker() {
  const l = picker.listing;
  const note = (MARKER_NOTES.find(([file]) => l.markers.includes(file)) || [])[1] || "";
  $("picker-frame").innerHTML = `
    <div class="sheet-head">
      <div><h1 id="picker-title">Pick a folder</h1></div>
      <button type="button" class="btn tile sm" data-act="pick-cancel">Cancel</button>
    </div>
    <div class="picker-bar">
      <div class="field"><label for="picker-path">Folder</label>
        <form class="inline-form" data-form="pick-path">
          <input class="input mono" id="picker-path" value="${esc(l.path)}" autocomplete="off" spellcheck="false">
          <button type="submit" class="btn tile">Go${vh(" to this folder")}</button>
        </form>
        <p class="form-error" id="picker-error" hidden>${ERROR_ICON}<span class="text"></span></p>
      </div>
      <div class="picker-places">
        ${l.parent ? `<button type="button" class="btn tile sm" data-act="pick-go" data-path="${esc(l.parent)}">Up one level</button>` : ""}
        ${l.roots.length > 1 || !l.parent ? l.roots.map(r =>
          `<button type="button" class="btn tile sm mono" data-act="pick-go" data-path="${esc(r)}">${esc(r)}</button>`).join("") : ""}
        <button type="button" class="btn tile sm" data-act="pick-go" data-path="">Home</button>
      </div>
    </div>
    <ul class="picker-list" aria-label="Folders in ${esc(l.path)}">
      ${l.dirs.length ? l.dirs.map((d, n) => `<li><button type="button" class="${/^[.$]/.test(d.name) ? "hidden-dir" : ""}" data-act="pick-go" data-path="${esc(d.path)}" ${n === 0 ? "autofocus" : ""}>
          <span class="folder" aria-hidden="true"></span><span class="name">${esc(d.name)}</span></button></li>`).join("")
        : `<li class="quiet">No folders in here.</li>`}
      ${l.truncated ? `<li class="quiet">More folders than can be listed. Type the path above.</li>` : ""}
    </ul>
    <div class="sheet-foot">
      <div class="foot-row">
        <button type="button" class="btn strong" data-act="pick-use" ${l.dirs.length ? "" : "autofocus"}>Use this folder</button>
        ${note ? `<span class="note">${esc(note)}</span>` : ""}
      </div>
    </div>`;
  $("picker-frame").querySelector(".picker-list").scrollTop = 0;
  const field = $("picker-path");   // a long path shows its end, the part that changes
  field.scrollLeft = field.scrollWidth;
}

$("picker").addEventListener("close", () => {
  picker = null;
  $("picker-frame").replaceChildren();
  afterModal("");
});

function usePickedFolder(target, path) {
  const input = $(target);
  if (!input) return;
  input.value = path;
  // A new project with no id yet takes one from the folder's name.
  if (target === "ed-path" && $("ed-id") && !$("ed-id").value.trim()) {
    const name = path.split(/[\\/]/).filter(Boolean).pop() || "";
    $("ed-id").value = name.toLowerCase().replace(/[^a-z0-9_-]+/g, "-").replace(/^[-_]+|-+$/g, "");
  }
  input.dispatchEvent(new Event("input", { bubbles: true }));   // counts as an edit, and clears a "required" error
}

/* ---------- form fields and assistive technology ---------- */

// :user-invalid is what the eye sees: a field turns red once the user has
// left it wrong or tried to save. A screen reader needs the same fact as
// aria-invalid, and the error text linked to the field only while it shows.

function linkError(el, shown) {
  const errorId = el.dataset.error;
  if (!errorId) return;
  const ids = (el.getAttribute("aria-describedby") || "").split(/\s+/).filter(id => id && id !== errorId);
  if (shown) ids.push(errorId);
  if (ids.length) el.setAttribute("aria-describedby", ids.join(" "));
  else el.removeAttribute("aria-describedby");
}

// Text as a screen reader reads it: icons' text alternatives in, aria-hidden text out.
function spokenText(node) {
  const copy = node.cloneNode(true);
  copy.querySelectorAll('[aria-hidden="true"]').forEach(n => n.remove());
  copy.querySelectorAll('[role="img"][aria-label]').forEach(n => n.replaceWith(`${n.getAttribute("aria-label")}: `));
  return copy.textContent.replace(/\s+/g, " ").trim();
}

const flagged = new WeakSet();     // fields this script marked invalid
const announcedFields = new WeakSet();   // fields whose current error has been spoken
let announcedField = null;

function syncAria(el, speakIt = false) {
  if (!el.matches?.("input, select, textarea")) return;
  if (el.matches(":user-invalid")) {
    if (speakIt && !announcedFields.has(el)) {
      const error = $(el.dataset.error || "");
      if (error) {
        const label = el.labels?.[0] ? spokenText(el.labels[0]) + ": " : "";
        announcedField = el;
        // A moment later, so it does not talk over the field that takes focus next.
        announce(label + spokenText(error), { delay: 500 });
      }
      announcedFields.add(el);
    }
    el.setAttribute("aria-invalid", "true");
    linkError(el, true);
    flagged.add(el);
  } else if (flagged.has(el) || el.getAttribute("aria-invalid") !== "true") {
    el.setAttribute("aria-invalid", "false");
    linkError(el, false);
    flagged.delete(el);
    announcedFields.delete(el);
    if (announcedField === el) { clearTimeout(announceTimer); announcedField = null; }
  }
}

document.addEventListener("blur", ev => syncAria(ev.target, true), true);
// A failed save fires "invalid" on every field that is wrong, touched or not.
// Focus moves to the first one, which reads its error, so none is spoken here.
document.addEventListener("invalid", ev => {
  const closed = ev.target.closest?.("details:not([open])");
  if (closed) closed.open = true;   // the browser cannot focus a field inside closed details
  announcedFields.add(ev.target);
  syncAria(ev.target);
}, true);

/* ---------- events ---------- */

document.addEventListener("click", ev => {
  const open = ev.target.closest("a[data-open]");
  if (open && Backend.isWails) {   // the desktop window has no tabs: hand the link to the system browser
    ev.preventDefault();
    window.runtime.BrowserOpenURL(open.href);
    return;
  }
  const el = ev.target.closest("[data-act]");
  if (!el) return;
  const { act: name, p, t } = el.dataset;
  switch (name) {
    case "task-start": return taskAction(p, t, "start");
    case "task-stop": return taskAction(p, t, "stop");
    case "task-restart": return taskAction(p, t, "restart");
    case "proj-start": return projectAction(p, "start");
    case "proj-stop": return projectAction(p, "stop");
    case "proj-restart": return projectAction(p, "restart");
    case "start-all": return allProjects("start");
    case "stop-all": return allProjects("stop");
    case "log": {
      // From a tab inside the panel, focus stays on that tab. From the board
      // it moves to the panel's title, and Close brings it back.
      const inside = $("logpanel").contains(el);
      openLog(p, t);
      if (!inside) {
        log.openerFk = el.dataset.fk || "";
        $("log-title").focus();
      }
      return;
    }
    case "close-log": return closeLog();
    case "settings": return openSettings(p);
    case "add": return openAdd();
    case "php": loadPHP(); return openSheet("php");
    case "home": closeLog(); return window.scrollTo(0, 0);
    case "close-sheet": return askToClose($("sheet"));
    case "discard-sheet": return $("sheet").close();
    case "theme": return switchTheme();
    case "toast-dismiss": return dismissToast(el.closest(".toast"));
    case "remove-project": return removeProject(p);
    case "rm-root": return removeRoot(el.dataset.path);
    case "browse": return browseFor(el.dataset.target);
    case "pick-go": return pickerGo(el.dataset.path);
    case "pick-cancel": return $("picker").close();
    case "pick-use": {
      const { target, listing } = picker;
      $("picker").close();
      return usePickedFolder(target, listing.path);
    }
    case "ed-add-task":
      readEditor();
      sheet.dirty = true;
      sheet.draft.tasks.push({ id: "", argv: [] });
      redrawEditor();
      return $("ed-tn-" + (sheet.draft.tasks.length - 1)).focus();
    case "ed-rm-task":
      readEditor();
      sheet.dirty = true;
      sheet.draft.tasks.splice(Number(el.dataset.i), 1);
      redrawEditor();
      return $("ed-add-task").focus();   // the button that was pressed is gone
  }
});

document.addEventListener("change", ev => {
  const el = ev.target;
  syncAria(el, true);   // checkboxes and selects commit without losing focus
  if (!sheet || !sheet.draft || !$("sheet").contains(el)) return;
  sheet.dirty = true;
  if (el.id === "ed-preset") {
    readEditor();
    sheet.draft.tasks = presetTasks(el.value);
    return redrawEditor();
  }
  if (el.dataset.edt) editTask(Number(el.dataset.i), el.dataset.edt, el);
});

// "toggle" does not bubble, so it is caught on the way down.
document.addEventListener("toggle", ev => {
  const el = ev.target;
  if (sheet && sheet.draft && el.classList && el.classList.contains("taskmore")) {
    const t = sheet.draft.tasks[Number(el.dataset.i)];
    if (t) t._more = el.open;
  }
}, true);

let findTimer = null;
document.addEventListener("input", ev => {
  const el = ev.target;
  if (el.id === "log-find") {
    // Filtering redraws up to 2000 lines: wait until the typing pauses.
    clearTimeout(findTimer);
    findTimer = setTimeout(() => {
      if (!log) return;
      log.find = el.value.trim();
      renderLogLines();
    }, 150);
    return;
  }
  if (el.dataset && el.dataset.edt === "env")
    el.setCustomValidity(textToEnv(el.value) ? "" : "Write every line as KEY=value.");
  if (el.hasAttribute && el.hasAttribute("aria-invalid")) syncAria(el);   // clears the error as soon as it is fixed
  if (sheet && sheet.draft && $("sheet").contains(el)) sheet.dirty = true;
});

document.addEventListener("submit", ev => {
  const form = ev.target.dataset.form;
  if (!form) return;
  ev.preventDefault();
  if (form === "editor") saveEditor();
  if (form === "add-root") addRoot(ev.target);
  if (form === "pick-path") pickerGo($("picker-path").value.trim());
});

// Esc closes the log. The dialogs handle Esc themselves.
document.addEventListener("keydown", ev => {
  if (ev.key !== "Escape" || !log || modalOpen()) return;
  if (ev.target.id === "log-find" && ev.target.value) return;   // Esc first clears the search field
  closeLog();
});

/* ---------- connection ---------- */

let conn = "";   // "", "live" or "lost"
function setConn(next) {
  if (next === conn) return;
  // The first connection is not news; losing it and getting it back is.
  if (next === "lost") announce("Connection to Ringmaster lost. Reconnecting.");
  else if (conn === "lost") announce("Connected to Ringmaster again.");
  conn = next;
  $("conn").className = "conn " + next;
  $("conn-text").textContent = next === "live" ? "Connected" : "Reconnecting…";
  $("conn-text").classList.toggle("visually-hidden", next === "live");
}

Backend.openEvents({
  onOpen: () => {
    setConn("live");
    refresh();
    loadPHP();
  },
  onStatus: event => {
    // Show the new status at once; the details (exit code, last line, run
    // count) come with the refresh that follows.
    for (const p of state.projects)
      for (const t of p.tasks)
        if (t.key === event.key) {
          t.status = event.status;
          if (busy(t)) delete t.start_error;
        }
    render();
    refreshSoon();
    if (log && log.missing && event.key === log.project + ":" + log.task && (event.status === "starting" || event.status === "running"))
      openLog(log.project, log.task);   // the task has output now
  },
  onClose: () => setConn("lost"),
});

// Uptimes are text; keep them current without waiting for an event.
setInterval(() => { if (document.visibilityState === "visible" && !modalOpen()) renderBoard(); }, 30000);

refresh();
loadPHP();
