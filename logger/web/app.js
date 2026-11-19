"use strict";
(function () {
  const $ = (id) => document.getElementById(id);
  const POLL_MS = 1000;
  const LABELS = { idle: "Idle", connecting: "Connecting", watching: "Watching", uploading: "Uploading", paused: "Paused", error: "Problem" };
  const EXTRA = ["source_name", "logs_dir", "upload_interval_ms"];
  let key = null;
  let running = false;

  function say(text, good) {
    const m = $("msg");
    m.textContent = text || "";
    m.className = text ? (good ? "good" : "bad") : "";
  }

  function ago(ms) {
    if (!ms) return "never";
    const s = Math.max(0, Math.round((Date.now() - ms) / 1000));
    return s < 60 ? s + " s ago" : Math.round(s / 60) + " min ago";
  }

  function render(st) {
    running = st.running;
    $("state").textContent = LABELS[st.state] || st.state;
    $("dot").className = "dot " + st.state;
    $("reason").textContent = st.reason ? " — " + st.reason : "";
    $("toggle").textContent = running ? "Stop" : "Start";
    $("c-lines").textContent = st.lines;
    $("c-contacts").textContent = st.contacts;
    $("c-sent").textContent = st.pingsSent;
    $("c-queued").textContent = st.queued + (st.dropped ? " (" + st.dropped + " dropped)" : "");
    $("c-last").textContent = ago(st.lastSuccess);
    const ul = $("activity");
    ul.textContent = "";
    for (const line of st.activity.slice().reverse()) {
      const li = document.createElement("li");
      li.textContent = line;
      ul.append(li);
    }
  }

  async function poll() {
    try { render(await window.getStatus()); } catch (e) { /* the window is closing */ }
  }

  function form() {
    const extra = {};
    for (const k of EXTRA) extra[k] = $(k).value;
    return { serverUrl: $("serverUrl").value, caFile: $("caFile").value, ruleset: $("ruleset").value, extra, apiKey: key.newKey() };
  }

  async function save() {
    const r = await window.saveSettings(form());
    say(r.ok ? "Saved." : r.message, r.ok);
    return r.ok;
  }

  async function load() {
    const v = await window.loadSettings();
    $("serverUrl").value = v.serverUrl;
    $("caFile").value = v.caFile;
    $("ruleset").value = v.ruleset;
    for (const k of EXTRA) $(k).value = v.extra[k] || "";
    $("file").textContent = "Saved in " + v.file;
    key = window.__app.keyfield.mount($("keyHost"), { hasKey: v.hasKey, mask: v.keyMask, reveal: () => window.revealKey() });
    if (!$("logs_dir").value) $("logs_dir").value = await window.defaultLogsDir();
  }

  async function toggle() {
    if (running) { await window.stop(); return poll(); }
    if (!(await save())) return;
    const r = await window.start();
    if (!r.ok) say(r.message, false);
    poll();
  }

  $("toggle").addEventListener("click", toggle);
  $("save").addEventListener("click", save);
  $("test").addEventListener("click", async () => {
    if (!(await save())) return;
    say("Checking...", true);
    const r = await window.testConnection();
    say(r.ok ? "Connected." : r.message, r.ok);
  });
  $("pick").addEventListener("click", async () => {
    const p = await window.pickFolder();
    if (p) $("logs_dir").value = p;
  });
  load().then(poll);
  setInterval(poll, POLL_MS);
})();
