/* Ringmaster dashboard.
 *
 * Three independent regions, so a status update never disturbs what the
 * user is doing in another one:
 *   board    the project cards; redrawn from `state` on every change
 *   logpanel one task's live log; owns its own stream
 *   sheet    settings, add project and PHP; owns its own form
 *
 * All clicks go through one delegated handler keyed on data-act.
 */
"use strict";

let state = { projects: [], config_path: "" };
let php = { roots: [], installs: [] };
let log = null;      // { project, task, entries, find, follow, close, missing }
let sheet = null;    // { kind: "settings" | "add" | "php", draft, opener }
let picker = null;   // { target, listing, opener }: the folder picker over a sheet
let firstBoard = true;

const $ = id => document.getElementById(id);
const esc = s => String(s ?? "").replace(/[&<>"']/g,
  c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
const api = (path, opts) => Backend.request(path, opts);
const jsonBody = (method, value) =>
  ({ method, headers: { "Content-Type": "application/json" }, body: JSON.stringify(value) });

function toast(msg, kind) {
  const el = $("toast");
  el.textContent = msg;
  el.className = "toast show" + (kind === "error" ? " error" : "");
  clearTimeout(el._hide);
  el._hide = setTimeout(() => { el.className = "toast"; }, kind === "error" ? 7000 : 3500);
}
const fail = (what, e) => toast(what + ": " + e.message, "error");

/* ---------- data ---------- */

async function refresh() {
  try {
    state = await api("/api/status");
    state.projects = state.projects || [];
    render();
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
  if (sheet && sheet.kind === "php") renderSheet();
}

/* ---------- what a task is doing ---------- */

const busy = t => t.status === "starting" || t.status === "running" || t.status === "stopping";
const failed = t => !busy(t) && (t.status === "failed" || !!t.start_error);
const primaryOf = p => p.tasks.find(t => t.primary) || null;

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
// tasks up it is the logo.
function ringSVG(p, size) {
  const tasks = [...p.tasks].sort((a, b) => (b.primary ? 1 : 0) - (a.primary ? 1 : 0)).slice(0, 8);
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
  if (p.error)
    return `<div class="failbox" role="alert">
      <strong><span class="node"></span>This project cannot start</strong>
      <code>${esc(p.error)}</code>
      <div class="row"><button type="button" class="btn md accent" ${act("settings", p.id)}>Open settings</button></div>
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
  return `<div class="failbox" role="alert">
    <strong><span class="node"></span>${esc(title)}</strong>
    ${detail ? `<code>${esc(detail)}</code>` : ""}
    <div class="row">
      <button type="button" class="btn md accent" ${act("task-restart", p.id, t.id)}>Try again</button>
      <button type="button" class="btn md outline" ${act("settings", p.id)}>Settings</button>
      <button type="button" class="btn md outline" ${act("log", p.id, t.id)}>Log</button>
    </div>
  </div>`;
}

function taskHTML(p, t) {
  const st = taskState(p, t);
  const nodeClass = { up: "up", wait: "wait", fail: "fail", off: "off" }[st.kind];
  const buttons = t.external || p.error ? "" : `
    <button type="button" class="btn sm raise" ${act("log", p.id, t.id)} aria-label="Log of ${esc(t.id)}">Log</button>
    ${busy(t)
      ? `<button type="button" class="btn sm raise" ${act("task-stop", p.id, t.id, "task-toggle")} aria-label="Stop ${esc(t.id)}">Stop</button>`
      : `<button type="button" class="btn sm raise" ${act("task-start", p.id, t.id, "task-toggle")} aria-label="Start ${esc(t.id)}">Start</button>`}`;
  return `<li class="task${st.kind === "fail" ? " failed" : ""}">
    <span class="node ${nodeClass}${t.primary ? " primary" : ""}" aria-hidden="true"></span>
    <span class="name">${esc(t.id)}</span>
    <span class="state">${esc(st.text)}</span>${buttons}
  </li>`;
}

function cardHTML(p) {
  const primary = primaryOf(p);
  const managed = p.tasks.filter(t => !t.external);
  const anyBusy = managed.some(busy);
  const attention = !!p.error || p.tasks.some(failed);
  const meta = [p.php ? "PHP " + p.php : "", p.path].filter(Boolean).join(" · ");

  let door = "";
  if (p.url && primary && primary.status === "running")
    door = `<a class="open" href="${esc(p.url)}" target="_blank" rel="noopener" data-open aria-label="Open ${esc(p.label || p.id)} in the browser, port ${p.port}">:${p.port} ${ARROW}</a>`;
  else if (p.port)
    door = `<span class="port" title="Not up">:${p.port}</span>`;

  let foot = "";
  if (!p.error && managed.length)
    foot = anyBusy
      ? `<button type="button" class="btn md strong" ${act("proj-stop", p.id, "", "proj-toggle")}>Stop project</button>
         <button type="button" class="btn md raise" ${act("proj-restart", p.id)}>Restart</button>`
      : `<button type="button" class="btn md strong" ${act("proj-start", p.id, "", "proj-toggle")}>Start project</button>`;

  return `<article class="card${attention ? " attention" : ""}" aria-label="${esc(p.label || p.id)}">
    <header class="card-head">
      ${ringSVG(p, 52)}
      <div class="card-title">
        <h2>${esc(p.label || p.id)}</h2>
        <span class="meta">${esc(meta)}</span>
      </div>
      ${door}
    </header>
    ${failboxHTML(p)}
    <ul class="tasks">${p.tasks.map(t => taskHTML(p, t)).join("")}</ul>
    <footer class="card-foot">
      ${foot}<span class="spacer"></span>
      <button type="button" class="btn link" ${act("settings", p.id)}>Settings</button>
    </footer>
  </article>`;
}

function renderBoard() {
  const board = $("board");
  const focused = board.contains(document.activeElement) ? document.activeElement.dataset.fk : null;

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

  if (firstBoard) {   // the entrance plays once, not on every status change
    firstBoard = false;
    board.classList.add("intro");
    setTimeout(() => board.classList.remove("intro"), 1200);
  }
  if (focused) board.querySelector(`[data-fk="${CSS.escape(focused)}"]`)?.focus();
}

function renderSummary() {
  const tasks = state.projects.flatMap(p => p.tasks.filter(t => !t.external));
  const running = tasks.filter(t => t.status === "running").length;
  const starting = tasks.filter(t => t.status === "starting").length;
  const attention = state.projects.filter(p => p.error || p.tasks.some(failed)).length;
  const n = state.projects.length;
  const bits = [`${n} project${n === 1 ? "" : "s"}`, `${running} of ${tasks.length} tasks running`];
  if (starting) bits.push(`${starting} starting`);
  $("summary").innerHTML = n === 0 ? "Bring your own binaries"
    : esc(bits.join(" · ")) + (attention ? ` · <strong>${attention} need${attention === 1 ? "s" : ""} attention</strong>` : "");
}

function renderPHPChip() {
  const minors = [...new Set(php.installs.map(i => i.version.split(".").slice(0, 2).join(".")))]
    .sort((a, b) => compareVersions(b, a));
  $("phpchip").textContent = minors.length
    ? "PHP " + minors.slice(0, 4).join(" · ") + (minors.length > 4 ? " …" : "")
    : "PHP: none found";
}

function render() {
  renderSummary();
  renderBoard();
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
    catch (e) { errors.push(`${p.label || p.id}: ${e.message}`); }
  }
  if (errors.length) toast(errors.join(" · "), "error");
  refresh();
}

function switchTheme() {
  const root = document.documentElement;
  const now = root.dataset.theme || (matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light");
  const next = now === "dark" ? "light" : "dark";
  root.dataset.theme = next;
  try { localStorage.setItem("ringmaster-theme", next); } catch { /* not remembered, still applied */ }
}

/* ---------- log panel ---------- */

const LOG_KEEP = 2000;

function openLog(project, task) {
  closeLogStream();
  log = { project, task, entries: [], pending: [], find: log ? log.find : "", follow: true, close: null, missing: false };
  $("logpanel").hidden = false;
  renderLogShell();
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
  closeLogStream();
  log = null;
  $("logpanel").hidden = true;
  $("logpanel").innerHTML = "";
}

function renderLogShell() {
  const p = state.projects.find(x => x.id === log.project);
  $("logpanel").innerHTML = `
    <div class="log-head">
      <div class="log-title">
        <h2 id="log-title">${esc(p ? p.label || p.id : log.project)} · ${esc(log.task)}</h2>
        <button type="button" class="btn sm" data-act="close-log">Close</button>
      </div>
      <div class="log-row" id="log-tabs"></div>
      <label class="log-find">Find in this log
        <input type="search" id="log-find" value="${esc(log.find)}" autocomplete="off" spellcheck="false">
      </label>
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
  holder.innerHTML = p.tasks.filter(x => !x.external).map(x =>
    `<button type="button" class="btn sm" aria-pressed="${x.id === log.task}" ${act("log", p.id, x.id)}>${esc(x.id)}</button>`).join("") +
    (t ? `<button type="button" class="btn sm" ${act("task-restart", p.id, t.id)}>Restart ${esc(t.id)}</button>` : "");
}

function logLineHTML(entry) {
  const d = new Date(entry.ts);
  const time = [d.getHours(), d.getMinutes(), d.getSeconds()].map(n => String(n).padStart(2, "0")).join(":");
  let text = entry.line, cls = entry.stream === "err" ? "err" : "";
  // Ringmaster's own lines are events, not output; a start begins a new run.
  if (text.startsWith("ringmaster: ")) {
    text = text.slice(12);
    cls = "ev" + (text.startsWith("started") ? " run" : "");
  }
  let html = esc(text);
  if (log.find) {
    const at = text.toLowerCase().indexOf(log.find.toLowerCase());
    if (at >= 0)
      html = esc(text.slice(0, at)) + "<mark>" + esc(text.slice(at, at + log.find.length)) + "</mark>" + esc(text.slice(at + log.find.length));
  }
  return `<li class="${cls}"><time>${time}</time><span class="text">${html}</span></li>`;
}

const matchesFind = entry => !log.find || entry.line.toLowerCase().includes(log.find.toLowerCase());

function renderLogLines() {
  const list = $("log-lines");
  if (!list) return;
  const shown = log.entries.filter(matchesFind);
  if (!shown.length) {
    list.innerHTML = `<li class="ev"><span class="text">${
      log.missing ? "No output yet. Start the task to see its log."
        : log.find ? "No line contains that text." : "Waiting for output."}</span></li>`;
    return;
  }
  list.innerHTML = shown.map(logLineHTML).join("");
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
  const html = fresh.filter(matchesFind).map(logLineHTML).join("");
  if (html) {
    if (list.firstElementChild && !list.querySelector("time")) list.innerHTML = "";   // drop the placeholder
    list.insertAdjacentHTML("beforeend", html);
    if (log.follow) list.scrollTop = list.scrollHeight;
  }
}

/* ---------- sheet: settings, add project, PHP ---------- */

function openSheet(kind, draft) {
  sheet = { kind, draft: draft || null, opener: document.activeElement };
  $("sheet").hidden = false;
  $("backdrop").hidden = false;
  renderSheet();
  const first = $("sheet").querySelector("input, select, textarea, button");
  if (first) first.focus();
  renderNav();
}

function closeSheet() {
  const opener = sheet && sheet.opener;
  sheet = null;
  $("sheet").hidden = true;
  $("backdrop").hidden = true;
  $("sheet").innerHTML = "";
  if (opener && document.contains(opener)) opener.focus();
  renderNav();
}

function renderNav() {
  const where = sheet && sheet.kind === "php" ? "nav-php" : sheet && sheet.kind === "add" ? "nav-add" : "nav-home";
  for (const id of ["nav-home", "nav-php", "nav-add"])
    if (id === where) $(id).setAttribute("aria-current", "page"); else $(id).removeAttribute("aria-current");
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
  $("sheet").innerHTML = sheet.kind === "php" ? phpSheetHTML() : editorSheetHTML();
}

function sheetHead(title, hint) {
  return `<div class="sheet-head">
    <div><h2 id="sheet-title">${esc(title)}</h2>${hint ? `<span class="hint">${esc(hint)}</span>` : ""}</div>
    <button type="button" class="btn tile sm" data-act="close-sheet">Close</button>
  </div>`;
}

/* -- project editor -- */

function editorSheetHTML() {
  const d = sheet.draft, adding = sheet.kind === "add";
  return sheetHead(adding ? "Add a project" : "Settings · " + (d.label || d.id), state.config_path) + `
  <div class="sheet-body">
    ${adding ? `<div class="field"><label for="ed-id">Id (short, lower case, used on the command line)</label>
      <input class="input mono" id="ed-id" value="${esc(d.id)}" placeholder="myapp" pattern="[a-z0-9][a-z0-9_-]*" autocomplete="off" spellcheck="false"></div>` : ""}
    <div class="field"><label for="ed-label">Name</label>
      <input class="input" id="ed-label" value="${esc(d.label || "")}" placeholder="${esc(d.id || "My Laravel app")}"></div>
    <div class="field"><label for="ed-path">Folder</label>
      <div class="with-browse">
        <input class="input mono" id="ed-path" value="${esc(d.path || "")}" placeholder="C:\\code\\myapp" autocomplete="off" spellcheck="false">
        <button type="button" class="btn tile" data-act="browse" data-target="ed-path">Browse</button>
      </div></div>
    <div class="row2">
      <div class="field"><label for="ed-php">PHP version</label>
        <input class="input mono" id="ed-php" value="${esc(d.php || "")}" placeholder="8.3" autocomplete="off"></div>
      <div class="field"><label for="ed-port">Port</label>
        <input class="input mono" id="ed-port" type="number" min="0" max="65535" value="${d.port || ""}" placeholder="picked for you"></div>
    </div>
    ${adding ? `<div class="field"><label for="ed-preset">Template</label>
      <select class="input" id="ed-preset">
        <option value="laravel">Laravel: serve, Vite and queue worker</option>
        <option value="laravel-serve">Laravel: serve only</option>
        <option value="custom">One command of your own</option>
      </select></div>` : ""}
    <div class="field"><span class="label">Tasks</span>
      ${d.tasks.map(taskRowHTML).join("")}
      <button type="button" class="addline" data-act="ed-add-task">+ Add a task</button>
    </div>
    ${adding ? "" : `<div class="danger-zone">
      <button type="button" class="btn danger" ${act("remove-project", d.id)}>Remove this project</button>
      <p>Stops its tasks and takes it out of ringmaster.yaml. The folder itself is not touched.</p>
    </div>`}
  </div>
  <div class="sheet-foot">
    <button type="button" class="btn strong" data-act="ed-save">${adding ? "Add project" : "Save"}</button>
    <button type="button" class="btn tile" data-act="close-sheet">Cancel</button>
    ${adding ? "" : `<span class="note">Restart a task to apply its changes</span>`}
  </div>`;
}

function taskRowHTML(t, i) {
  const envText = envToText(t.env);
  return `<div class="taskrow">
    <div class="kv">
      <input class="input mono tr-name" value="${esc(t.id || "")}" placeholder="serve" data-edt="id" data-i="${i}" aria-label="Task name" autocomplete="off" spellcheck="false">
      <input class="input mono" value="${esc(argvToText(t.argv))}" placeholder="{php} artisan serve --host={host} --port={port}"
        data-edt="argv" data-i="${i}" aria-label="Command of ${esc(t.id || "the new task")}" autocomplete="off" spellcheck="false">
      <button type="button" class="btn raise sm" data-act="ed-rm-task" data-i="${i}" aria-label="Remove task ${esc(t.id || "")}">Remove</button>
    </div>
    <div class="opts">
      <label class="check"><input type="checkbox" data-edt="primary" data-i="${i}" ${t.primary ? "checked" : ""}> main task (owns the port)</label>
      <label class="check"><input type="checkbox" data-edt="autostart" data-i="${i}" ${t.autostart === false ? "" : "checked"}> starts with the project</label>
      <label class="check"><input type="checkbox" data-edt="external" data-i="${i}" ${t.external ? "checked" : ""}> only watch it</label>
      <select class="input" data-edt="restart" data-i="${i}" aria-label="When it exits">
        <option value="" ${!t.restart || t.restart === "no" ? "selected" : ""}>never restart</option>
        <option value="on-failure" ${t.restart === "on-failure" ? "selected" : ""}>restart after a crash</option>
        <option value="always" ${t.restart === "always" ? "selected" : ""}>always restart</option>
      </select>
    </div>
    <details class="taskmore" data-i="${i}" ${t.cwd || envText || t._more ? "open" : ""}>
      <summary>Folder and environment${t.cwd || envText ? " (set)" : ""}</summary>
      <div class="field"><label for="ed-cwd-${i}">Run in subfolder</label>
        <input class="input mono" id="ed-cwd-${i}" value="${esc(t.cwd || "")}" placeholder="frontend (empty = the project folder)" data-edt="cwd" data-i="${i}" autocomplete="off" spellcheck="false"></div>
      <div class="field"><label for="ed-env-${i}">Environment variables, one KEY=value per line</label>
        <textarea class="input mono" id="ed-env-${i}" rows="2" placeholder="APP_URL=http://{host}:{port}" data-edt="env" data-i="${i}" spellcheck="false">${esc(envText)}</textarea></div>
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

// readProjectFields copies the project inputs into the draft. Anything that
// redraws the form calls it first, or the redraw would wipe what was typed.
function readProjectFields() {
  const d = sheet.draft;
  if (!$("ed-path")) return d;
  d.label = $("ed-label").value.trim();
  d.path = $("ed-path").value.trim();
  d.php = $("ed-php").value.trim();
  d.port = $("ed-port").value ? Number($("ed-port").value) : 0;
  if ($("ed-id")) d.id = $("ed-id").value.trim();
  return d;
}

function redrawEditor(keepPreset) {
  readProjectFields();
  renderSheet();
  if (keepPreset && $("ed-preset")) $("ed-preset").value = keepPreset;
}

function editTask(i, field, el) {
  const t = sheet.draft.tasks[i];
  if (!t) return;
  const value = el.type === "checkbox" ? el.checked : el.value;
  if (field === "argv") t.argv = textToArgv(value);
  else if (field === "autostart") { if (value) delete t.autostart; else t.autostart = false; }
  else if (field === "cwd") { if (value.trim()) t.cwd = value.trim(); else delete t.cwd; }
  else if (field === "env") {
    const env = textToEnv(value);
    if (!env) { t._envError = true; toast("Environment: every line must be KEY=value", "error"); return; }
    delete t._envError;
    if (Object.keys(env).length) t.env = env; else delete t.env;
  }
  else t[field] = value;
}

async function saveEditor() {
  const d = readProjectFields(), adding = sheet.kind === "add";
  if (adding && !d.id) return toast("Give the project an id", "error");
  if (!d.path) return toast("Pick the project's folder", "error");
  if (!d.tasks.length || d.tasks.some(t => !t.id)) return toast("Every task needs a name", "error");
  if (d.tasks.some(t => t._envError)) return toast("Environment: every line must be KEY=value", "error");
  // Keys starting with "_" are editor state, not config.
  const tasks = d.tasks.map(t => Object.fromEntries(Object.entries(t).filter(([k]) => !k.startsWith("_"))));
  const body = { id: d.id, label: d.label, path: d.path, php: d.php, port: d.port, tasks };
  try {
    if (adding) await api("/api/config/projects", jsonBody("POST", body));
    else await api("/api/config/projects/" + d.id, jsonBody("PUT", body));
  } catch (e) { return fail("Not saved", e); }
  closeSheet();
  toast(adding ? `Added ${d.label || d.id}` : "Saved. Restart a task to apply its changes.");
  refresh();
}

async function removeProject(id) {
  if (!confirm(`Remove "${id}" from Ringmaster? Its running tasks are stopped. The folder itself is not deleted.`)) return;
  try { await api("/api/config/projects/" + id, { method: "DELETE" }); }
  catch (e) { return fail("Not removed", e); }
  if (log && log.project === id) closeLog();
  closeSheet();
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
  return matches.sort((a, b) => compareVersions(b.version, a.version))[0] || null;
}

function phpSheetHTML() {
  const users = new Map();   // install path -> project names
  const unresolved = [];
  for (const p of state.projects) {
    if (!p.php) continue;
    const install = resolveInstall(p.php);
    if (!install) { unresolved.push(`${p.label || p.id} wants ${p.php}`); continue; }
    users.set(install.path, [...(users.get(install.path) || []), p.label || p.id]);
  }
  return sheetHead("PHP versions", "Ringmaster uses the PHP you already have") + `
  <div class="sheet-body">
    <div class="field"><span class="label">Found on this machine</span>
      ${php.installs.length ? `<ul class="list">${php.installs.map(i => `<li>
          <span class="badge">${esc(i.version)}</span>
          <span class="grow">${esc(i.path)}${users.has(i.path) ? `<span class="sub">Used by ${esc(users.get(i.path).join(", "))}</span>` : ""}</span>
        </li>`).join("")}</ul>`
        : `<p class="quiet">No PHP found. Add the folder that holds your PHP versions below.</p>`}
      ${unresolved.length ? `<p class="quiet" role="alert">Not installed: ${esc(unresolved.join("; "))}.</p>` : ""}
    </div>
    <div class="field"><span class="label">Folders Ringmaster searches</span>
      ${php.roots.length ? `<ul class="list">${php.roots.map(r => `<li>
          <span class="grow">${esc(r)}</span>
          <button type="button" class="btn raise sm" data-act="rm-root" data-path="${esc(r)}">Remove</button>
        </li>`).join("")}</ul>`
        : `<p class="quiet">None added, so Ringmaster uses the PHP on PATH and the default Laragon, XAMPP, Herd and Scoop folders.
            Adding a folder here switches those defaults off.</p>`}
      <form class="inline-form" data-form="add-root">
        <input class="input mono" id="php-root-path" name="path" required placeholder="C:\\laragon\\bin\\php" aria-label="Folder that holds PHP versions" autocomplete="off" spellcheck="false">
        <button type="button" class="btn tile" data-act="browse" data-target="php-root-path">Browse</button>
        <button type="submit" class="btn strong">Add folder</button>
      </form>
    </div>
  </div>`;
}

async function addRoot(form) {
  const path = new FormData(form).get("path");
  try { await api("/api/config/phproots", jsonBody("POST", { path })); }
  catch (e) { return fail("Folder not added", e); }
  await loadPHP();
  refresh();
}

async function removeRoot(path) {
  if (!confirm(`Stop searching "${path}"? Nothing on disk is deleted.`)) return;
  try { await api("/api/config/phproots", jsonBody("DELETE", { path })); }
  catch (e) { return fail("Folder not removed", e); }
  await loadPHP();
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
    } catch (e) { fail("Folder dialog", e); }
    return;
  }
  picker = { target, listing: null, opener: document.activeElement };
  $("picker").hidden = false;
  $("picker-backdrop").hidden = false;
  $("picker").innerHTML = PICKER_HEAD + `<p class="quiet" style="padding: 0 24px">Reading folders…</p>`;
  // Start where the field points, or at the home folder if that is not a folder.
  if (!(await pickerGo(start, true)) && start) await pickerGo("");
}

// pickerGo lists `path` in the picker. It returns false when the folder
// could not be read; `quiet` suppresses the error message.
async function pickerGo(path, quiet) {
  let listing;
  try { listing = await api("/api/fs?path=" + encodeURIComponent(path)); }
  catch (e) { if (!quiet) fail("Cannot open that folder", e); return false; }
  if (!picker) return true;   // closed while loading
  picker.listing = listing;
  renderPicker();
  return true;
}

const PICKER_HEAD = `<div class="sheet-head">
  <div><h2 id="picker-title">Pick a folder</h2></div>
  <button type="button" class="btn tile sm" data-act="pick-cancel">Cancel</button>
</div>`;

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
  $("picker").innerHTML = PICKER_HEAD + `
    <div class="picker-bar">
      <form class="inline-form" data-form="pick-path">
        <input class="input mono" id="picker-path" value="${esc(l.path)}" aria-label="Folder path" autocomplete="off" spellcheck="false">
        <button type="submit" class="btn tile">Go</button>
      </form>
      <div class="picker-places">
        ${l.parent ? `<button type="button" class="btn tile sm" data-act="pick-go" data-path="${esc(l.parent)}">Up one level</button>` : ""}
        ${l.roots.length > 1 || !l.parent ? l.roots.map(r =>
          `<button type="button" class="btn tile sm mono" data-act="pick-go" data-path="${esc(r)}">${esc(r)}</button>`).join("") : ""}
        <button type="button" class="btn tile sm" data-act="pick-go" data-path="">Home</button>
      </div>
    </div>
    <ul class="picker-list" aria-label="Folders in ${esc(l.path)}">
      ${l.dirs.length ? l.dirs.map(d => `<li><button type="button" class="${/^[.$]/.test(d.name) ? "hidden-dir" : ""}" data-act="pick-go" data-path="${esc(d.path)}">
          <span class="folder" aria-hidden="true"></span><span class="name">${esc(d.name)}</span></button></li>`).join("")
        : `<li class="quiet">No folders in here.</li>`}
      ${l.truncated ? `<li class="quiet">More folders than can be listed. Type the path above.</li>` : ""}
    </ul>
    <div class="sheet-foot">
      <button type="button" class="btn strong" data-act="pick-use">Use this folder</button>
      ${note ? `<span class="note">${esc(note)}</span>` : ""}
    </div>`;
  $("picker").querySelector(".picker-list").scrollTop = 0;
  const field = $("picker-path");   // a long path shows its end, the part that changes
  field.scrollLeft = field.scrollWidth;
}

function closePicker() {
  const opener = picker && picker.opener;
  picker = null;
  $("picker").hidden = true;
  $("picker-backdrop").hidden = true;
  $("picker").innerHTML = "";
  if (opener && document.contains(opener)) opener.focus();
}

function usePickedFolder(target, path) {
  const input = $(target);
  if (!input) return;
  input.value = path;
  // A new project with no id yet takes one from the folder's name.
  if (target === "ed-path" && $("ed-id") && !$("ed-id").value.trim()) {
    const name = path.split(/[\\/]/).filter(Boolean).pop() || "";
    $("ed-id").value = name.toLowerCase().replace(/[^a-z0-9_-]+/g, "-").replace(/^[-_]+|-+$/g, "");
  }
  input.focus();
}

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
    case "log": return openLog(p, t);
    case "close-log": return closeLog();
    case "settings": return openSettings(p);
    case "add": return openAdd();
    case "php": loadPHP(); return openSheet("php");
    case "home": if (sheet) closeSheet(); return;
    case "close-sheet": return closeSheet();
    case "theme": return switchTheme();
    case "remove-project": return removeProject(p);
    case "rm-root": return removeRoot(el.dataset.path);
    case "ed-save": return saveEditor();
    case "browse": return browseFor(el.dataset.target);
    case "pick-go": return pickerGo(el.dataset.path);
    case "pick-cancel": return closePicker();
    case "pick-use": {
      const { target, listing } = picker;
      closePicker();
      return usePickedFolder(target, listing.path);
    }
    case "ed-add-task":
      readProjectFields();
      sheet.draft.tasks.push({ id: "", argv: [] });
      return redrawEditor($("ed-preset") && $("ed-preset").value);
    case "ed-rm-task":
      readProjectFields();
      sheet.draft.tasks.splice(Number(el.dataset.i), 1);
      return redrawEditor($("ed-preset") && $("ed-preset").value);
  }
});

document.addEventListener("change", ev => {
  const el = ev.target;
  if (!sheet || !$("sheet").contains(el)) return;
  if (el.id === "ed-preset") {
    readProjectFields();
    sheet.draft.tasks = presetTasks(el.value);
    return redrawEditor(el.value);
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

document.addEventListener("input", ev => {
  if (ev.target.id === "log-find" && log) {
    log.find = ev.target.value.trim();
    renderLogLines();
  }
});

document.addEventListener("submit", ev => {
  if (ev.target.dataset.form === "add-root") {
    ev.preventDefault();
    addRoot(ev.target);
  }
  if (ev.target.dataset.form === "pick-path") {
    ev.preventDefault();
    pickerGo($("picker-path").value.trim());
  }
});

document.addEventListener("keydown", ev => {
  if (ev.key !== "Escape") return;
  if (picker) closePicker();
  else if (sheet) closeSheet();
  else if (log) closeLog();
});

Backend.openEvents({
  onOpen: () => {
    $("conn").className = "conn live"; $("conn").title = "live";
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
  onClose: () => { $("conn").className = "conn"; $("conn").title = "reconnecting"; },
});

// Uptimes are text; keep them current without waiting for an event.
setInterval(() => { if (document.visibilityState === "visible" && !sheet) renderBoard(); }, 30000);

refresh();
loadPHP();
