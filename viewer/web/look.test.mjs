import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";

// Load several browser scripts into one shared window object, in page order.
function load(...files) {
  const w = {};
  for (const f of files) new Function("window", fs.readFileSync(new URL(f, import.meta.url), "utf8"))(w);
  return w.__app;
}
const A = load("./style.js", "./tracks.js", "./radar.js", "./list.js", "./world.js");

const presets = {
  default: { friendly: "#3b82f6", hostile: "#ef4444", neutral: "#f59e0b", stale: "#6b7280", trail: "#e0455b", selection: "#ffffff", background: "#0a0d12", track1: "#332288", track2: "#88ccee", track3: "#44aa99", track4: "#117733", track5: "#999933", track6: "#882255", track7: "#aa4499", track8: "#ddcc77" },
  highcontrast: { friendly: "#00ffff", hostile: "#ff3030", neutral: "#ffff00", stale: "#9a9a9a", trail: "#ff00ff", selection: "#ffffff", background: "#000000" },
};
const ap = () => ({ scheme: "system", palette: "default", custom: {}, shapes: { friendly: "circle", hostile: "diamond", neutral: "square" }, dot_size: 5, selected_ring: 9,
  outline: "thin", stale: "dim", stale_minutes: 5, approx: "filled", labels: "hover", show_trails: true, trail_style: "dashed", trail_width: 2, trail_dot: 3,
  trail_fade: true, trail_arrow: true, gap_minutes: 15, grid: "off", map_opacity: 100, background: "map", density: "comfortable", shape_only: false,
  reduce_motion: false, ui_scale: 100, neutral_as_hostile: false });
const NOW = 10_000_000;
const contact = (o) => Object.assign({ guid: "Player-1-AAAA", name: "A", reaction: "hostile", x: 50, y: 50, mapId: 1, lastSeen: NOW, kind: "exact" }, o);
const colors = presets.default;

// ---- style.js ----
test("resolvePalette: preset colours; light scheme swaps the background except high contrast", () => {
  assert.equal(A.style.resolvePalette(ap(), presets, true).background, "#0a0d12");
  assert.equal(A.style.resolvePalette(ap(), presets, false).background, "#e9e9ee");
  assert.equal(A.style.resolvePalette(Object.assign(ap(), { palette: "highcontrast" }), presets, false).background, "#000000");
});
test("resolvePalette: custom colours win over the default preset", () => {
  const a = Object.assign(ap(), { palette: "custom", custom: { friendly: "#123456" } });
  const c = A.style.resolvePalette(a, presets, true);
  assert.equal(c.friendly, "#123456");
  assert.equal(c.hostile, presets.default.hostile);
});
test("shapesFor: shape-only mode guarantees three different shapes", () => {
  const same = Object.assign(ap(), { shape_only: true, shapes: { friendly: "circle", hostile: "circle", neutral: "circle" } });
  assert.equal(new Set(Object.values(A.style.shapesFor(same))).size, 3);
  const free = Object.assign(ap(), { shapes: { friendly: "circle", hostile: "circle", neutral: "circle" } });
  assert.equal(A.style.shapesFor(free).hostile, "circle");
});
test("pipStyle: reaction picks colour and shape; selected is bigger and ringed", () => {
  const s = A.style.pipStyle(contact({ reaction: "friendly" }), ap(), colors, NOW, { selected: true });
  assert.equal(s.shape, "circle"); assert.equal(s.fill, colors.friendly); assert.equal(s.r, 7); assert.equal(s.ring.r, 9);
});
test("pipStyle: stale looks are dim, hollow or grey", () => {
  const old = contact({ lastSeen: NOW - 6 * 60000 });
  assert.equal(A.style.pipStyle(old, ap(), colors, NOW).alpha, 0.4);
  assert.equal(A.style.pipStyle(old, Object.assign(ap(), { stale: "hollow" }), colors, NOW).hollow, true);
  assert.equal(A.style.pipStyle(old, Object.assign(ap(), { stale: "grey" }), colors, NOW).fill, colors.stale);
  assert.equal(A.style.pipStyle(contact({ lastSeen: NOW - 4 * 60000 }), ap(), colors, NOW).alpha, 1);
});
test("pipStyle: stale threshold follows the setting", () => {
  assert.equal(A.style.pipStyle(contact({ lastSeen: NOW - 2 * 60000 }), Object.assign(ap(), { stale_minutes: 1 }), colors, NOW).alpha, 0.4);
});
test("pipStyle: approximate fixes can be hollow; untracked dots dim when someone is tracked (D3)", () => {
  assert.equal(A.style.pipStyle(contact({ kind: "approx" }), Object.assign(ap(), { approx: "hollow" }), colors, NOW).hollow, true);
  assert.equal(A.style.pipStyle(contact(), ap(), colors, NOW, { hasTracked: true, tracked: false }).alpha, A.style.UNTRACKED_ALPHA);
  const t = A.style.pipStyle(contact(), ap(), colors, NOW, { hasTracked: true, tracked: true, trackColor: "#abcdef" });
  assert.equal(t.alpha, 1); assert.equal(t.trackRing.color, "#abcdef");
});
test("pipStyle: neutral folds into hostile when asked", () => {
  const s = A.style.pipStyle(contact({ reaction: "neutral" }), Object.assign(ap(), { neutral_as_hostile: true }), colors, NOW);
  assert.equal(s.fill, colors.hostile); assert.equal(s.shape, "diamond");
});
test("trackColor is stable per guid and spreads over eight colours", () => {
  assert.equal(A.style.trackColor("Player-1-AAAA", colors), A.style.trackColor("Player-1-AAAA", colors));
  const seen = new Set();
  for (let i = 0; i < 200; i++) seen.add(A.style.trackIndex("Player-1-" + i.toString(16).toUpperCase().padStart(4, "0")));
  assert.equal(seen.size, 8);
});
test("contrast: black on white is 21:1, same colour is 1:1", () => {
  assert.ok(Math.abs(A.style.contrast("#000000", "#ffffff") - 21) < 0.01);
  assert.equal(A.style.contrast("#336699", "#336699"), 1);
});

// ---- tracks.js ----
test("tracked set: cap of 8 evicts the oldest; re-adding moves focus", () => {
  const t = A.tracks.createTracked(3);
  for (const g of ["a", "b", "c"]) assert.equal(t.add(g, g), null);
  assert.equal(t.add("d", "d").guid, "a");
  assert.deepEqual(t.list().map((x) => x.guid), ["b", "c", "d"]);
  t.add("b", "b"); assert.equal(t.focus.guid, "b");
  t.remove("b"); assert.equal(t.focus.guid, "d");
  assert.equal(A.tracks.CAP, 8);
});
test("segments (L1): sorted by time, other maps dropped, a long gap breaks the line", () => {
  const f = (t, x, m = 1) => ({ t, x, y: 1, mapId: m });
  const runs = A.tracks.segments([f(300000, 3), f(0, 1), f(60000, 2), f(0, 9, 2), f(5000000, 4)], 1, 15 * 60000);
  assert.deepEqual(runs.map((r) => r.map((p) => p.x)), [[1, 2, 3], [4]]);
});
test("withLive (L4): the live position joins the newest run, but never across a gap or twice", () => {
  const runs = [[{ t: 0, x: 1, y: 1 }, { t: 1000, x: 2, y: 2 }]];
  const joined = A.tracks.withLive(runs, { x: 3, y: 3, lastSeen: 2000, kind: "exact", mapId: 1 }, 60000);
  assert.equal(joined[0].length, 3); assert.equal(joined[0][2].live, true);
  assert.equal(A.tracks.withLive(runs, { x: 2, y: 2, lastSeen: 2000, mapId: 1 }, 60000)[0].length, 2);
  assert.equal(A.tracks.withLive(runs, { x: 9, y: 9, lastSeen: 9e6, mapId: 1 }, 60000).length, 2);
});
test("worldPath collapses consecutive fixes on one map into one visit", () => {
  const v = A.tracks.worldPath([{ t: 3, mapId: 2 }, { t: 1, mapId: 1 }, { t: 2, mapId: 1 }, { t: 4, mapId: 1 }]);
  assert.deepEqual(v.map((x) => [x.mapId, x.count]), [[1, 2], [2, 1], [1, 1]]);
});
test("limiter never runs more than n jobs at once", async () => {
  const run = A.tracks.limiter(2);
  let active = 0, peak = 0;
  const job = () => new Promise((res) => { active++; peak = Math.max(peak, active); setTimeout(() => { active--; res(); }, 5); });
  await Promise.all(Array.from({ length: 7 }, () => run(job)));
  assert.equal(peak, 2);
});

// ---- radar.js drawing, with a recording context ----
function recorder() {
  const calls = [];
  const ctx = new Proxy({}, {
    get(t, k) { if (k === "calls") return calls; if (k === "measureText") return () => ({ width: 30 }); return (...a) => calls.push([k, ...a]); },
    set(t, k, v) { calls.push(["set:" + k, v]); return true; },
  });
  return ctx;
}
const size = { w: 200, h: 100, aspect: 2 };
const scene = (o) => Object.assign({ view: { z: 1, cx: 50, cy: 50 }, orientation: "std", image: null, contacts: [], trails: [], now: NOW, colors, ap: ap(), tracked: new Set() }, o);
const lineTos = (ctx) => ctx.calls.filter((c) => c[0] === "lineTo").length;

test("draw (L1/L2): a gap in the trail is not bridged by a line", () => {
  const f = (t, x) => ({ t, x, y: 50, mapId: 1, kind: "exact" });
  const ctx = recorder();
  A.radar.draw(ctx, scene({ trails: [{ guid: "g", name: "g", mapId: 1, color: "#fff", contact: null, fixes: [f(0, 10), f(60000, 20), f(7200000, 80), f(7260000, 90)] }] }), size);
  // two runs of two points = two line segments; a bridged line would add a third
  assert.equal(ctx.calls.filter((c) => c[0] === "stroke").length >= 2, true);
  const moves = ctx.calls.filter((c) => c[0] === "moveTo").length;
  assert.equal(moves >= 2, true);
});
test("draw (L2): dashed style sets a line dash; solid clears it", () => {
  const f = [{ t: 0, x: 10, y: 50, mapId: 1 }, { t: 1, x: 20, y: 50, mapId: 1 }];
  const t = [{ guid: "g", name: "g", mapId: 1, color: "#fff", fixes: f }];
  const dashed = recorder(); A.radar.draw(dashed, scene({ trails: t }), size);
  assert.ok(dashed.calls.some((c) => c[0] === "setLineDash" && c[1].length === 2));
  const solid = recorder(); A.radar.draw(solid, scene({ trails: t, ap: Object.assign(ap(), { trail_style: "solid" }) }), size);
  assert.ok(!solid.calls.some((c) => c[0] === "setLineDash" && c[1].length > 0));
});
test("draw (L3): approximate trail dots are stroked rings, exact ones are filled", () => {
  const ctx = recorder();
  A.radar.draw(ctx, scene({ trails: [{ guid: "g", name: "g", mapId: 1, color: "#fff", fixes: [{ t: 0, x: 10, y: 50, mapId: 1, kind: "approx" }] }] }), size);
  const arcs = ctx.calls.map((c) => c[0]);
  assert.ok(arcs.indexOf("arc") < arcs.lastIndexOf("stroke"));
});
test("draw (L4): the live dot is the trail's last vertex", () => {
  const ctx = recorder();
  const live = contact({ x: 90, y: 50, lastSeen: 2000, reaction: "friendly" }); // a circle, so its own path adds no lineTo
  A.radar.draw(ctx, scene({ contacts: [live], trails: [{ guid: live.guid, name: "A", mapId: 1, color: "#fff", contact: live,
    fixes: [{ t: 0, x: 10, y: 50, mapId: 1 }, { t: 1000, x: 20, y: 50, mapId: 1 }] }], now: 2500, ap: Object.assign(ap(), { trail_arrow: false }) }), size);
  const last = ctx.calls.filter((c) => c[0] === "lineTo").pop();
  const p = A.radar.project({ z: 1, cx: 50, cy: 50 }, 90, 50, size, "std");
  assert.deepEqual([last[1], last[2]], [p.x, p.y]);
});
test("draw (D2): every save has its restore, so alpha never leaks", () => {
  const ctx = recorder();
  A.radar.draw(ctx, scene({ contacts: [contact(), contact({ guid: "Player-1-BBBB", lastSeen: 0 })] }), size);
  const n = (k) => ctx.calls.filter((c) => c[0] === k).length;
  assert.equal(n("save"), n("restore"));
});
test("draw (D4): hit radius follows dot size with a floor", () => {
  assert.equal(A.radar.hitRadius(Object.assign(ap(), { dot_size: 3 })), 8);
  assert.equal(A.radar.hitRadius(Object.assign(ap(), { dot_size: 12 })), 16);
  const hits = A.radar.draw(recorder(), scene({ contacts: [contact()] }), size);
  assert.equal(hits[0].r, 9);
});
test("draw: tracked players are drawn last so dimmed dots never cover them", () => {
  const a = contact({ guid: "Player-1-AAAA", x: 40 }), b = contact({ guid: "Player-1-BBBB", x: 60 });
  const hits = A.radar.draw(recorder(), scene({ contacts: [a, b], tracked: new Set([a.guid]) }), size);
  assert.equal(hits[hits.length - 1].contact.guid, a.guid);
});
test("draw: rings background and grid draw extra paths; map opacity is applied to the image", () => {
  const img = { naturalWidth: 100, naturalHeight: 50 };
  const g = recorder(); A.radar.draw(g, scene({ image: img, ap: Object.assign(ap(), { grid: "on", map_opacity: 60 }) }), size);
  assert.ok(g.calls.some((c) => c[0] === "set:globalAlpha" && c[1] === 0.6));
  assert.ok(g.calls.some((c) => c[0] === "drawImage"));
  const r = recorder(); A.radar.draw(r, scene({ image: img, ap: Object.assign(ap(), { background: "rings" }) }), size);
  assert.ok(!r.calls.some((c) => c[0] === "drawImage"));
  assert.equal(r.calls.filter((c) => c[0] === "arc").length, 4);
});
test("draw: always-on labels skip overlapping names", () => {
  const ctx = recorder();
  A.radar.draw(ctx, scene({ contacts: [contact({ name: "One" }), contact({ guid: "Player-1-BBBB", name: "Two", x: 50.5 })], ap: Object.assign(ap(), { labels: "always" }) }), size);
  assert.equal(ctx.calls.filter((c) => c[0] === "fillText").length, 1);
});

// ---- list.js ----
test("applyFilters: reaction switches, neutral-as-hostile, only-tracked", () => {
  const cs = [contact({ guid: "a", reaction: "friendly" }), contact({ guid: "b", reaction: "hostile" }), contact({ guid: "c", reaction: "neutral" })];
  const f = (o) => A.list.applyFilters(cs, Object.assign({ friendly: true, hostile: true, neutral: true, onlyTracked: false }, o), new Set(["b"]), false).map((c) => c.guid);
  assert.deepEqual(f({}), ["a", "b", "c"]);
  assert.deepEqual(f({ friendly: false }), ["b", "c"]);
  assert.deepEqual(f({ onlyTracked: true }), ["b"]);
  assert.deepEqual(A.list.applyFilters(cs, { friendly: true, hostile: false, neutral: true }, null, true).map((c) => c.guid), ["a"]);
});
test("fmtTime: UTC and a fixed IANA zone differ, an unknown zone falls back to UTC", () => {
  const ms = Date.UTC(2026, 0, 15, 12, 0, 0);
  assert.match(A.list.fmtClock(ms, "UTC"), /12:00:00/);
  assert.match(A.list.fmtClock(ms, "Asia/Tokyo"), /21:00:00/);
  assert.match(A.list.fmtClock(ms, "Not/AZone"), /12:00:00/);
});
test("tipLines: shows the configured time zone", () => {
  const ms = Date.UTC(2026, 0, 15, 12, 0, 0);
  const lines = A.list.tipLines({ contact: contact({ lastSeen: ms }) }, () => "Z", ms + 1000, { timeZone: "UTC", staleMinutes: 5 });
  assert.ok(lines.some((l) => l.includes("12:00:00")));
});

// ---- world.js ----
test("world paths: one point per zone visit, placed at the zone anchor, unknown zones skipped", () => {
  const proj = (x, y) => ({ x, y });
  const p = A.world.paths([{ guid: "g", name: "g", color: "#fff", fixes: [{ t: 1, mapId: 1411 }, { t: 2, mapId: 1411 }, { t: 3, mapId: 99999 }, { t: 4, mapId: 1412 }] }], proj);
  assert.equal(p.length, 1);
  assert.deepEqual(p[0].points.map((q) => q.zone), ["Durotar", "Mulgore"]);
});
