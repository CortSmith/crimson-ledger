"use strict";
(function () {
  const $ = (id) => document.getElementById(id);
  const A = window.__app;
  const cv = $("radar"), ctx = cv.getContext("2d");
  const st = { contacts: [], charts: [], mapSel: 0, view: { z: 1, cx: 50, cy: 50 }, orientation: "std", image: null, imageZone: "",
    tracked: A.tracks.createTracked(), trails: new Map(), polls: 0, hits: [], pies: [], now: Date.now(), dirty: true, key: null, tab: "list",
    look: null, ap: null, colors: {}, filters: { friendly: true, hostile: true, neutral: true, onlyTracked: false }, tz: "local", ruleset: "normal",
    ui: null, lookDirty: false, conn: "connecting", startedAt: Date.now(), lastOkAt: 0, failures: 0, polls: 0 };
  const fetchLimit = A.tracks.limiter(4); // never more than four trail calls in flight (API rate limit)

  function say(el, text, good) { el.textContent = text || ""; el.className = text ? (good ? "good" : "bad") : ""; }
  function banner(r) {
    const b = $("banner");
    b.hidden = !r;
    b.textContent = r ? (r.message || "Error") + (r.code ? " [" + r.code + (r.requestId ? " " + r.requestId : "") + "]" : "") : "";
  }
  const WORLD = A.world.WORLD;
  function inWorld() { return st.mapSel === WORLD; }
  function orient() { return inWorld() ? "std" : st.orientation; } // the world picture is never rotated
  function viewedMap() { return st.mapSel || A.list.autoMap(st.contacts); }
  function zoneName(id) { const c = chartOf(id), z = A.world.ZONES[id]; return c ? c.zone : z ? z[0] : "Map " + id; }
  function chartOf(id) { return st.charts.find((c) => c.mapId === id); }
  const focusGuid = () => (st.tracked.focus ? st.tracked.focus.guid : "");
  const trackColorOf = (guid) => A.style.trackColor(guid, st.colors);

  // ---- appearance: one place turns the saved look into colours, CSS and redraws ----
  const dark = () => st.ap.scheme === "dark" || (st.ap.scheme === "system" && window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches);
  function applyLook(look, ap) {
    if (look) st.look = look;
    st.ap = ap || st.look.appearance;
    st.colors = A.style.resolvePalette(st.ap, st.look.presets, dark());
    const root = document.documentElement;
    if (st.ap.scheme === "system") root.removeAttribute("data-theme"); else root.dataset.theme = st.ap.scheme;
    root.style.fontSize = (13 * st.ap.ui_scale / 100) + "px";
    document.body.classList.toggle("compact", st.ap.density === "compact");
    document.body.classList.toggle("reduce", !!st.ap.reduce_motion);
    $("trailsChk").checked = !!st.ap.show_trails;
    st.dirty = true; renderList(); renderChips();
  }
  if (window.matchMedia) window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => { if (st.ap) applyLook(null, st.ap); });

  // ---- drawing (redraws only when something changed) ----
  function size() { return { w: cv.clientWidth, h: cv.clientHeight, aspect: st.image && st.image.naturalHeight ? st.image.naturalWidth / st.image.naturalHeight : 1 }; }
  function resize() {
    const dpr = window.devicePixelRatio || 1, s = size();
    cv.width = Math.max(1, Math.round(s.w * dpr)); cv.height = Math.max(1, Math.round(s.h * dpr));
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    st.dirty = true;
  }
  function frame() {
    if (st.dirty && st.ap) { st.dirty = false; st.now = Date.now(); drawFrame(); }
    requestAnimationFrame(frame);
  }
  const shown = () => A.list.applyFilters(st.contacts, st.filters, new Set(st.tracked.list().map((t) => t.guid)), st.ap && st.ap.neutral_as_hostile);

  function drawFrame() {
    const sz = size(), vis = shown();
    A.radar.clampView(st.view, sz);
    if (inWorld()) {
      A.radar.draw(ctx, { view: st.view, orientation: "std", image: st.image, contacts: [], now: st.now, colors: st.colors, ap: st.ap, tracked: new Set() }, sz);
      const proj = (x, y) => A.radar.project(st.view, x, y, sz, "std");
      st.pies = A.world.pies(vis, proj);
      A.world.draw(ctx, st.pies, st.colors);
      if (st.ap.show_trails) {
        const list = st.tracked.list().filter((t) => st.trails.has(t.guid)).map((t) => ({ guid: t.guid, name: t.name, color: trackColorOf(t.guid), fixes: st.trails.get(t.guid).fixes }));
        A.world.drawPaths(ctx, A.world.paths(list, proj), { fade: st.ap.trail_fade });
      }
      st.hits = [];
      return;
    }
    const id = viewedMap(), tr = new Set(st.tracked.list().map((t) => t.guid));
    const here = vis.filter((c) => c.mapId === id);
    st.pies = [];
    const trails = st.ap.show_trails ? st.tracked.list().filter((t) => st.trails.has(t.guid)).map((t) => ({ guid: t.guid, name: t.name, mapId: id,
      fixes: st.trails.get(t.guid).fixes, contact: st.contacts.find((c) => c.guid === t.guid), color: trackColorOf(t.guid) })) : [];
    const colors = new Map([...tr].map((g) => [g, trackColorOf(g)]));
    st.hits = A.radar.draw(ctx, { view: st.view, orientation: st.orientation, image: st.image, contacts: here, now: st.now, colors: st.colors, ap: st.ap,
      selected: focusGuid(), tracked: tr, trackColors: colors, trails }, sz);
  }

  async function loadImage() {
    const ch = chartOf(viewedMap()), zone = inWorld() ? "World" : ch ? ch.zone : (A.world.ZONES[viewedMap()] || [""])[0];
    if (zone === st.imageZone) return;
    st.imageZone = zone; st.image = null; st.dirty = true;
    if (!zone) return;
    const r = await window.getMap(zone);
    if (!r.ok || zone !== st.imageZone) return;
    const img = new Image();
    img.onload = () => { st.image = img; st.dirty = true; };
    img.src = r.uri;
  }

  // ---- list ----
  function swatch(c) {
    const cvs = document.createElement("canvas"), g = cvs.getContext("2d");
    cvs.width = cvs.height = 16; cvs.className = "swatch";
    const sty = A.style.pipStyle(c, Object.assign({}, st.ap, { dot_size: Math.min(st.ap.dot_size, 6) }), st.colors, st.now, {});
    sty.alpha = 1; A.style.drawPip(g, sty, 8, 8);
    return cvs;
  }
  function row(c) {
    const li = document.createElement("li"), nm = document.createElement("span"), meta = document.createElement("span");
    nm.className = "nm"; nm.textContent = c.name; meta.className = "muted";
    meta.textContent = (c.kind === "approx" ? "~ " : "") + A.list.age(st.now - c.lastSeen);
    const tracked = st.tracked.has(c.guid), any = st.tracked.size > 0;
    li.className = (A.style.isStale(c, st.now, st.ap) ? "stale " : "") + (c.guid === focusGuid() ? "sel " : "") + (any && !tracked ? "dim" : "");
    li.tabIndex = 0;
    li.append(swatch(c), nm, meta);
    if (tracked) {
      li.style.borderLeft = "4px solid " + trackColorOf(c.guid);
      const x = document.createElement("button"); x.type = "button"; x.className = "x"; x.textContent = "x"; x.title = "Stop tracking"; x.setAttribute("aria-label", "Stop tracking " + c.name);
      x.addEventListener("click", (e) => { e.stopPropagation(); untrack(c.guid); });
      li.append(x);
    }
    const pick = () => select(c);
    li.addEventListener("click", pick);
    li.addEventListener("keydown", (e) => { if (e.key === "Enter") pick(); });
    return li;
  }
  function renderList() {
    if (!st.ap) return;
    st.now = Date.now();
    const vis = shown(), tr = new Set(st.tracked.list().map((t) => t.guid));
    // Tracked players first, then the usual grouping.
    const rows = A.list.group(vis).sort((a, b) => Number(tr.has(b.guid)) - Number(tr.has(a.guid))).slice(0, 400);
    const ul = $("list");
    ul.textContent = "";
    for (const c of rows) ul.append(row(c));
    $("summary").textContent = vis.length === st.contacts.length ? st.contacts.length + " contacts" : vis.length + " of " + st.contacts.length + " contacts";
    $("empty").hidden = st.contacts.length > 0;
  }

  // ---- tracking ----
  function renderChips() {
    const host = $("chips");
    host.textContent = "";
    for (const t of st.tracked.list()) {
      const chip = document.createElement("span"), name = document.createElement("span"), x = document.createElement("button");
      chip.className = "tchip" + (t.guid === focusGuid() ? " focus" : ""); chip.style.borderColor = trackColorOf(t.guid);
      name.textContent = t.name; x.type = "button"; x.textContent = "x"; x.title = "Stop tracking"; x.setAttribute("aria-label", "Stop tracking " + t.name);
      chip.addEventListener("click", () => { st.tracked.focusOn(t.guid); goToTracked(t.guid); renderChips(); renderList(); st.dirty = true; });
      x.addEventListener("click", (e) => { e.stopPropagation(); untrack(t.guid); });
      chip.append(name, x); host.append(chip);
    }
    $("onlyRow").hidden = st.tracked.size === 0;
  }
  function goToMap(id) {
    st.mapSel = id; st.view = { z: 1, cx: 50, cy: 50 }; $("mapSel").value = String(id); st.dirty = true; loadImage();
  }
  function goToTracked(guid) {
    const c = st.contacts.find((x) => x.guid === guid);
    const t = st.trails.get(guid), last = t && t.fixes.length ? t.fixes[t.fixes.length - 1] : null;
    const id = c ? c.mapId : last ? last.mapId : 0;
    if (id && (inWorld() || viewedMap() !== id)) goToMap(id);
  }
  // Loads one player's earlier fixes. Queued so at most four calls run at once.
  function fetchTrail(guid, name) {
    return fetchLimit(() => window.getTrail(guid, 0)).then((r) => {
      if (!st.tracked.has(guid)) return; // untracked meanwhile
      if (!r.ok) { banner(r); return; }
      st.trails.set(guid, { fixes: r.trail.fixes, total: r.trail.total, maps: r.trail.maps, name }); st.dirty = true; renderPanel();
    });
  }
  function track(guid, name) {
    const evicted = st.tracked.add(guid, name);
    if (evicted) st.trails.delete(evicted.guid);
    fetchTrail(guid, name).then(() => { goToTracked(guid); });
    renderChips(); renderList(); st.dirty = true;
  }
  function untrack(guid) {
    st.tracked.remove(guid); st.trails.delete(guid);
    renderChips(); renderList(); st.dirty = true;
  }
  function clearTracked() { st.tracked.clear(); st.trails.clear(); renderChips(); renderList(); st.dirty = true; }
  // Called on every poll: the focused player refreshes each time, the others every fifth poll.
  function refreshTrails() {
    st.polls++;
    for (const t of st.tracked.list()) if (!st.trails.has(t.guid) || t.guid === focusGuid() || st.polls % 5 === 0) fetchTrail(t.guid, t.name);
  }
  async function select(c) {
    if (st.tracked.has(c.guid) && focusGuid() === c.guid) { untrack(c.guid); return; } // clicking the focused player again stops tracking
    if (st.tracked.has(c.guid)) st.tracked.focusOn(c.guid); else track(c.guid, c.name);
    if (c.mapId && (inWorld() || viewedMap() !== c.mapId)) goToMap(c.mapId);
    renderChips(); renderList(); st.dirty = true;
    const r = await window.copyTarget(c.name);
    $("copied").textContent = r.ok ? "Copied: " + r.message : r.code === "no_clipboard" ? "Press Ctrl+C to copy: " + r.message : "";
  }

  // ---- data ----
  function fillMaps() {
    const sel = $("mapSel"), keep = String(st.mapSel);
    sel.textContent = "";
    const auto = document.createElement("option");
    auto.value = "0"; auto.textContent = "Auto (follow data)"; sel.append(auto);
    const world = document.createElement("option");
    world.value = String(WORLD); world.textContent = "World map"; sel.append(world);
    const groups = { true: group("With players"), false: group("No players logged") };
    for (const m of A.list.mapOptions(st.charts, st.contacts, (id) => (A.world.ZONES[id] || [""])[0])) {
      const o = document.createElement("option");
      o.value = String(m.mapId);
      o.textContent = m.active ? m.zone + " (" + m.count + ")" : m.zone;
      if (!m.active) o.className = "empty";
      groups[m.active].append(o);
    }
    for (const g of [groups.true, groups.false]) if (g.children.length) sel.append(g);
    sel.value = keep;
  }
  function group(label) { const g = document.createElement("optgroup"); g.label = label; return g; }
  window.__app.onUpdate = function (u) {
    $("dot").className = "dot " + u.state;
    banner(u.error || null);
    st.conn = u.state;
    if (u.state === "error") st.failures++;
    if (u.state === "ok") { st.failures = 0; st.lastOkAt = Date.now(); }
    if (u.contacts) {
      st.contacts = u.contacts; st.polls++;
      $("updated").textContent = A.list.fmtClock(Date.now(), st.tz);
      renderList(); loadImage(); fillMaps(); refreshTrails(); st.dirty = true;
    }
    renderPanel();
    if (u.state === "authFailed") say($("msg"), "The server rejected the key.", false);
  };

  function setRulesetUI(rs) { st.ruleset = rs; $("rulesetSel").value = rs; $("rsBadge").textContent = ({ normal: "Normal", pvp: "PvP", rp: "RP", rppvp: "RP-PvP" })[rs] || rs; }
  async function start() {
    const [prefs, look] = await Promise.all([window.getView(), window.getAppearance()]);
    st.orientation = prefs.orientation; st.mapSel = prefs.selectedMap; st.tz = look.timeZone;
    st.ui = { look }; fillTimeZones(look.timeZone);
    applyLook(look); $("orientSel").value = st.orientation; setRulesetUI(prefs.ruleset);
    if (!prefs.configured) { openSettings(true); return; }
    const r = await window.connect();
    if (!r.ok) { banner(r); return; }
    const ch = await window.getCharts();
    if (ch.ok) { st.charts = ch.charts; fillMaps(); } else banner(ch);
  }

  // ---- search ----
  let debounce = null;
  $("q").addEventListener("input", () => {
    clearTimeout(debounce);
    debounce = setTimeout(async () => {
      const r = await window.search($("q").value);
      const ul = $("results");
      ul.textContent = ""; ul.hidden = !(r.players && r.players.length);
      if (!r.ok) { banner(r); return; }
      for (const p of r.players) {
        const li = document.createElement("li");
        li.textContent = p.name; li.tabIndex = 0;
        const go = () => { ul.hidden = true; track(p.guid, p.name); };
        li.addEventListener("click", go);
        li.addEventListener("keydown", (e) => { if (e.key === "Enter") go(); });
        ul.append(li);
      }
    }, 250);
  });
  $("onlyTracked").addEventListener("change", () => { st.filters.onlyTracked = $("onlyTracked").checked; renderList(); st.dirty = true; });

  // ---- group filters ----
  for (const [id, key] of [["fFriendly", "friendly"], ["fHostile", "hostile"], ["fNeutral", "neutral"]]) {
    $(id).addEventListener("click", () => {
      st.filters[key] = !st.filters[key];
      $(id).setAttribute("aria-pressed", String(st.filters[key]));
      renderList(); st.dirty = true;
    });
  }

  // ---- bounty tab ----
  const SIDE_TABS = { list: "tabList", bounty: "tabBounty", history: "tabHistory", status: "tabStatus" };
  const PANES = { list: ["list", "summary"], bounty: ["bountyList"], history: ["historyPane"], status: ["statusPane"] };
  function el(tag, text, cls) { const e = document.createElement(tag); if (text !== undefined) e.textContent = text; if (cls) e.className = cls; return e; }
  function dl(pairs) {
    const d = el("dl");
    for (const [k, v] of pairs) d.append(el("dt", k), el("dd", String(v)));
    return d;
  }
  function bars(rows, onClick) {
    const box = el("div");
    for (const m of rows) {
      const r = el("div", undefined, "row" + (onClick ? " link" : ""));
      r.append(el("span", m.zone), el("span", m.pct === undefined ? String(m.count) : m.count + " (" + m.pct + "%)", "muted"));
      if (onClick) r.addEventListener("click", () => onClick(m.mapId));
      const bar = el("div", undefined, "bar"), fill = el("span");
      fill.style.width = Math.max(2, m.pct === undefined ? 0 : m.pct) + "%";
      bar.append(fill);
      box.append(r); if (m.pct !== undefined) box.append(bar);
    }
    return box;
  }
  function renderStatus() {
    const m = A.panels.statusModel({ state: st.conn, ruleset: st.ruleset, startedAt: st.startedAt, lastOkAt: st.lastOkAt, polls: st.polls,
      failures: st.failures, contacts: st.contacts, now: Date.now(), zoneOf: zoneName });
    const host = $("statusPane");
    host.textContent = "";
    host.append(el("h3", "Connection"), dl([["State", m.connection], ["Last update", m.lastUpdate], ["Realm", m.ruleset], ["Polls this session", m.polls], ["Viewer open", m.uptime]]));
    host.append(el("h3", "Contacts"), dl([["Total", m.counts.total], ["Hostile", m.counts.hostile], ["Friendly", m.counts.friendly], ["Neutral", m.counts.neutral]]));
    if (m.maps.length) { host.append(el("h3", "Maps with players")); host.append(bars(m.maps, (id) => { goToMap(id); showTab("list"); })); }
  }
  function renderHistory() {
    const host = $("historyPane"), f = st.tracked.focus;
    host.textContent = "";
    const h = f ? A.panels.historyModel(st.trails.get(f.guid), Date.now(), zoneName, st.tz) : null;
    if (!h) { host.append(el("p", f ? "Loading " + f.name + "..." : "Track a player (search above, or click one in the list) to see their history.", "muted")); return; }
    host.append(el("h3", h.name));
    const pairs = [["Fixes recorded", h.total], ["First fix shown", h.first], ["Last fix", h.last]];
    if (h.last_pos) pairs.push(["Last position", h.last_pos.zone + " " + h.last_pos.x.toFixed(0) + ", " + h.last_pos.y.toFixed(0)]);
    if (h.approx) pairs.push(["Approximate fixes", h.approx]);
    host.append(dl(pairs));
    if (h.truncated) host.append(el("p", "Showing the newest " + h.shown + " of " + h.total + " fixes.", "muted"));
    if (h.maps.length) { host.append(el("h3", "Where they were seen")); host.append(bars(h.maps, (id) => { goToMap(id); showTab("list"); })); }
  }
  function renderPanel() { if (st.tab === "status") renderStatus(); else if (st.tab === "history") renderHistory(); }
  setInterval(() => { if (st.tab === "status" || st.tab === "history") renderPanel(); }, 5000); // ages move on even without a poll
  async function showTab(t) {
    st.tab = t;
    for (const [name, panes] of Object.entries(PANES)) {
      for (const id of panes) $(id).hidden = name !== t;
      $(SIDE_TABS[name]).setAttribute("aria-selected", String(name === t));
    }
    if (t === "status" || t === "history") { renderPanel(); return; }
    if (t !== "bounty") return;
    const r = await window.getBounty();
    if (!r.ok) { banner(r); return; }
    const ol = $("bountyList");
    ol.textContent = "";
    for (const k of r.ranking) { const li = document.createElement("li"); li.textContent = k.name + " - " + k.kills; ol.append(li); }
  }
  for (const [name, id] of Object.entries(SIDE_TABS)) $(id).addEventListener("click", () => showTab(name));

  // ---- header controls ----
  $("mapSel").addEventListener("change", () => {
    st.mapSel = Number($("mapSel").value); st.view = { z: 1, cx: 50, cy: 50 }; st.dirty = true; loadImage(); persist();
  });
  function persist() {
    window.saveView({ ruleset: st.ruleset, orientation: st.orientation, selectedMap: st.mapSel, showHistory: !!(st.ap && st.ap.show_trails), configured: true });
  }

  // ---- canvas interaction: wheel zoom, drag pan, click ----
  let drag = null;
  cv.addEventListener("wheel", (e) => { e.preventDefault(); st.view.z = Math.min(20, Math.max(1, st.view.z * (e.deltaY < 0 ? 1.15 : 1 / 1.15))); A.radar.clampView(st.view, size()); st.dirty = true; }, { passive: false });
  cv.addEventListener("pointerdown", (e) => { drag = { x: e.clientX, y: e.clientY, moved: false }; });
  window.addEventListener("pointerup", () => { drag = null; });
  const tip = $("tip");
  function hideTip() { tip.hidden = true; cv.classList.remove("pick"); }
  function showTip(lines, e) {
    tip.textContent = "";
    lines.forEach((t, i) => { const el = document.createElement(i === 0 ? "strong" : "div"); el.textContent = t; tip.append(el); });
    tip.hidden = false;
    const w = cv.getBoundingClientRect(), x = e.clientX - w.left + 14, y = e.clientY - w.top + 14;
    tip.style.left = Math.max(0, Math.min(x, w.width - tip.offsetWidth - 4)) + "px";
    tip.style.top = Math.max(0, Math.min(y, w.height - tip.offsetHeight - 4)) + "px";
  }
  function hover(e) {
    const r = cv.getBoundingClientRect(), x = e.clientX - r.left, y = e.clientY - r.top;
    if (inWorld()) {
      const p = A.world.hit(st.pies, x, y);
      if (!p) { hideTip(); return; }
      const k = p.counts;
      cv.classList.add("pick");
      showTip([p.name, k.hostile + " hostile, " + k.friendly + " friendly" + (k.neutral ? ", " + k.neutral + " neutral" : ""), "Click to open this zone"], e);
      return;
    }
    const h = A.radar.hitTarget(st.hits, x, y);
    if (!h || st.ap.labels === "none") { hideTip(); return; }
    cv.classList.add("pick");
    showTip(A.list.tipLines(h, zoneName, Date.now(), { staleMinutes: st.ap.stale_minutes, timeZone: st.tz }), e);
  }
  cv.addEventListener("pointerleave", hideTip);
  cv.addEventListener("pointermove", (e) => {
    if (!drag) { hover(e); return; }
    hideTip();
    const d = A.radar.pixelsToPercent(st.view, e.clientX - drag.x, e.clientY - drag.y, size());
    const alt = orient() === "alt";
    st.view.cx -= alt ? d.y : d.x; st.view.cy -= alt ? d.x : d.y;
    A.radar.clampView(st.view, size());
    if (Math.abs(e.clientX - drag.x) + Math.abs(e.clientY - drag.y) > 3) drag.moved = true;
    drag.x = e.clientX; drag.y = e.clientY; st.dirty = true;
  });
  cv.addEventListener("click", (e) => {
    if (drag && drag.moved) return;
    const r = cv.getBoundingClientRect();
    if (inWorld()) {
      const p = A.world.hit(st.pies, e.clientX - r.left, e.clientY - r.top);
      if (p) { hideTip(); goToMap(p.mapId); persist(); }
      return;
    }
    const c = A.radar.hitTest(st.hits, e.clientX - r.left, e.clientY - r.top);
    if (c) select(c);
  });
  document.addEventListener("visibilitychange", () => window.setVisible(!document.hidden));
  window.addEventListener("resize", resize);
  // The canvas can change size without a window resize (side panel, banner, header wrapping, views toggling). A stale pixel
  // buffer makes the drawing scale away from the coordinates the hit-tests use, so follow the element itself.
  if (window.ResizeObserver) new ResizeObserver(resize).observe(cv);
  window.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && st.tracked.size) clearTracked();
    if ((e.key === "h" || e.key === "H") && !/INPUT|SELECT|TEXTAREA/.test(e.target.tagName) && !e.ctrlKey && !e.metaKey && st.ap) {
      st.ap.show_trails = !st.ap.show_trails; $("trailsChk").checked = st.ap.show_trails; st.dirty = true; persistLook();
    }
  });

  // ---- settings ----
  const TABS = ["Connection", "Display", "Appearance", "About"];
  function showSettingsTab(name) {
    for (const t of TABS) $("pane" + t).hidden = t !== name;
    for (const b of document.querySelectorAll(".stabs button")) b.setAttribute("aria-selected", String(b.dataset.tab === name));
    $("test").hidden = name !== "Connection";
  }
  for (const b of document.querySelectorAll(".stabs button")) b.addEventListener("click", () => showSettingsTab(b.dataset.tab));
  function fillTimeZones(current) {
    const sel = $("tzSel"); sel.textContent = "";
    const zones = ["local", "UTC", "America/New_York", "America/Chicago", "America/Denver", "America/Los_Angeles", "Europe/London", "Europe/Berlin", "Europe/Moscow", "Asia/Tokyo", "Australia/Sydney"];
    if (!zones.includes(current)) zones.push(current);
    for (const z of zones) { const o = document.createElement("option"); o.value = z; o.textContent = z === "local" ? "This computer's time zone" : z; sel.append(o); }
    sel.value = current;
  }
  $("tzSel").addEventListener("change", () => { st.tz = $("tzSel").value; $("updated").textContent = A.list.fmtClock(Date.now(), st.tz); });
  $("rulesetSel").addEventListener("change", async () => {
    st.trails.clear(); const rs = $("rulesetSel").value; setRulesetUI(rs); await window.setRuleset(rs);
  });
  $("orientSel").addEventListener("change", () => { st.orientation = $("orientSel").value; st.dirty = true; persist(); });
  $("trailsChk").addEventListener("change", () => { st.ap.show_trails = $("trailsChk").checked; st.dirty = true; persistLook(); });
  async function persistLook() { return window.saveAppearance({ appearance: st.ap, timeZone: st.tz }); }

  function openSettings(first) {
    $("radarView").hidden = true; $("settingsView").hidden = false; $("firstRun").hidden = !first; showSettingsTab("Connection");
    loadSettings();
  }
  const EXTRA = ["poll_interval_s"];
  async function loadSettings() {
    const v = await window.loadSettings();
    $("serverUrl").value = v.serverUrl; $("caFile").value = v.caFile; $("ruleset").value = v.ruleset;
    for (const k of EXTRA) $(k).value = v.extra[k] || "";
    $("file").textContent = "Saved in " + v.file;
    st.key = A.keyfield.mount($("keyHost"), { hasKey: v.hasKey, mask: v.keyMask, reveal: () => window.revealKey() });
    const look = await window.getAppearance();
    st.look = look; $("tzSel").value = st.tz;
    st.lookUI = A.appearanceUI.mount($("appearanceHost"), { appearance: st.ap, presets: look.presets, colorKeys: look.colorKeys, trackKeys: look.trackKeys, defaults: look.defaults, isDark: dark },
      (ap) => { applyLook(null, ap); st.lookDirty = true; });
  }
  $("resetLook").addEventListener("click", async () => {
    const look = await window.resetAppearance();
    applyLook(look); st.lookUI = A.appearanceUI.mount($("appearanceHost"), { appearance: st.ap, presets: look.presets, colorKeys: look.colorKeys, trackKeys: look.trackKeys, defaults: look.defaults, isDark: dark },
      (ap) => { applyLook(null, ap); st.lookDirty = true; });
    say($("msg"), "Appearance reset.", true);
  });
  async function save() {
    const extra = {};
    for (const k of EXTRA) extra[k] = $(k).value;
    const r = await window.saveSettings({ serverUrl: $("serverUrl").value, caFile: $("caFile").value, ruleset: $("ruleset").value, extra, apiKey: st.key.newKey() });
    if (!r.ok) { say($("msg"), r.message, false); return false; }
    const ap = st.lookUI ? st.lookUI.get() : st.ap;
    const l = await window.saveAppearance({ appearance: ap, timeZone: st.tz });
    st.lookDirty = !l.ok;
    say($("msg"), l.ok ? "Saved." : l.message, l.ok);
    return l.ok;
  }
  $("settingsBtn").addEventListener("click", () => openSettings(false));
  $("save").addEventListener("click", save);
  $("test").addEventListener("click", async () => {
    if (!(await save())) return;
    say($("msg"), "Checking...", true);
    const r = await window.testViewer();
    say($("msg"), r.message, r.ok);
  });
  $("back").addEventListener("click", async () => {
    if (!(await save())) return;
    $("settingsView").hidden = true; $("radarView").hidden = false; resize(); start();
  });

  setInterval(() => { st.dirty = true; if (st.contacts.length) renderList(); }, 30000); // ages and dimming
  resize(); requestAnimationFrame(frame); start();
})();
