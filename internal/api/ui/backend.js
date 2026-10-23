/* backend.js — transport shim shared by the web UI and the native (Wails) GUI.
 *
 * The dashboard talks to exactly one surface: Backend.request() for REST-shaped
 * calls, Backend.openEvents() for the status stream, Backend.openLogs() for a
 * per-task log tail. In a browser these are HTTP fetch + WebSockets; inside the
 * Wails desktop window they are direct Go bindings (window.go.main.App.*) +
 * Wails events. The transport is chosen lazily per call, so load order relative
 * to Wails' injected runtime never matters.
 */
(function () {
  function inWails() {
    return !!(window.go && window.go.main && window.go.main.App && window.runtime);
  }
  function app() { return window.go.main.App; }
  function toErr(e) {
    return new Error(typeof e === "string" ? e : (e && e.message) || String(e));
  }
  function ok() { return { result: "ok" }; }

  /* ---------- REST ---------- */

  async function webRequest(path, opts) {
    const res = await fetch(path, opts);
    const body = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(body.error || res.statusText);
    return body;
  }

  function parseBody(opts) {
    if (opts && opts.body) {
      try { return JSON.parse(opts.body); } catch (_) { return {}; }
    }
    return {};
  }

  // wailsRequest maps a REST (path, method) onto the bound App methods, keeping
  // the exact JSON shapes the web endpoints return.
  async function wailsRequest(path, opts) {
    const A = app();
    const method = (opts && opts.method) || "GET";
    const seg = path.split("?")[0].split("/").filter(Boolean); // ["api", ...]
    const body = parseBody(opts);
    try {
      if (path === "/api/status") return { projects: await A.Status() };
      if (path === "/api/php") return await A.PHP();

      if (seg[1] === "config" && seg[2] === "projects") {
        const id = seg[3] ? decodeURIComponent(seg[3]) : null;
        if (method === "GET") return await A.GetProject(id);
        if (method === "POST") { await A.AddProject(body); return ok(); }
        if (method === "PUT") { await A.UpdateProject(id, body); return ok(); }
        if (method === "DELETE") { await A.DeleteProject(id); return ok(); }
      }
      if (seg[1] === "config" && seg[2] === "phproots") {
        if (method === "POST") { await A.AddPHPRoot(body.path); return ok(); }
        if (method === "DELETE") { await A.DeletePHPRoot(body.path); return ok(); }
      }
      if (seg[1] === "tasks") { await A.TaskAction(seg[2], seg[3], seg[4]); return ok(); }
      if (seg[1] === "projects") { await A.ProjectAction(seg[2], seg[3]); return ok(); }

      throw new Error("unrouted request: " + method + " " + path);
    } catch (e) {
      throw toErr(e);
    }
  }

  /* ---------- status stream ---------- */

  function webEvents(h) {
    const proto = location.protocol === "https:" ? "wss" : "ws";
    function connect() {
      const ws = new WebSocket(`${proto}://${location.host}/api/events`);
      ws.onopen = () => h.onOpen && h.onOpen();
      ws.onmessage = ev => {
        const m = JSON.parse(ev.data);
        if (m.type === "status") h.onStatus(m.event);
      };
      ws.onclose = () => { h.onClose && h.onClose(); setTimeout(connect, 2000); };
    }
    connect();
  }

  function wailsEvents(h) {
    window.runtime.EventsOn("ringmaster:status", ev => h.onStatus(ev));
    if (h.onOpen) h.onOpen(); // bindings are always "connected"
  }

  /* ---------- per-task log tail ---------- */

  // Returns a close function.
  function webLogs(project, task, h) {
    const proto = location.protocol === "https:" ? "wss" : "ws";
    const ws = new WebSocket(`${proto}://${location.host}/api/tasks/${project}/${task}/logs/ws`);
    ws.onmessage = ev => { const m = JSON.parse(ev.data); h.onEntry(m.entry); };
    ws.onerror = () => h.onError && h.onError();
    return () => ws.close();
  }

  function wailsLogs(project, task, h) {
    const key = project + ":" + task;
    const off = window.runtime.EventsOn("ringmaster:log", m => {
      if (m.key === key) h.onEntry(m.entry);
    });
    app().TailLog(project, task).catch(() => h.onError && h.onError());
    return () => { off(); app().StopTail(); };
  }

  window.Backend = {
    get isWails() { return inWails(); },
    request: (path, opts) => (inWails() ? wailsRequest : webRequest)(path, opts),
    openEvents: h => (inWails() ? wailsEvents : webEvents)(h),
    openLogs: (project, task, h) => (inWails() ? wailsLogs : webLogs)(project, task, h),
  };
})();
