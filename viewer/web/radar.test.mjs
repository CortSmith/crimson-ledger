import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";

function load(file) {
  const w = {};
  new Function("window", fs.readFileSync(new URL(file, import.meta.url), "utf8"))(w);
  return w.__app;
}
const { radar } = load("./radar.js");
const { list } = load("./list.js");
const size = { w: 200, h: 100 };

test("project: centre of the map is the centre of the canvas", () => {
  const p = radar.project({ z: 1, cx: 50, cy: 50 }, 50, 50, size, "std");
  assert.deepEqual(p, { x: 100, y: 50 });
});
test("project: std puts x right and y down; alt swaps the axes", () => {
  const v = { z: 1, cx: 50, cy: 50 };
  assert.deepEqual(radar.project(v, 60, 50, size, "std"), { x: 110, y: 50 });
  assert.deepEqual(radar.project(v, 60, 50, size, "alt"), { x: 100, y: 60 });
});
test("project: zoom scales distance from the centre", () => {
  assert.equal(radar.project({ z: 2, cx: 50, cy: 50 }, 60, 50, size, "std").x, 120);
});
test("pan: pixelsToPercent inverts project's scale", () => {
  assert.deepEqual(radar.pixelsToPercent({ z: 2 }, 20, 10, size), { x: 10, y: 5 });
});
test("hitTest: nearest blip inside its radius, none outside", () => {
  const hits = [{ x: 10, y: 10, r: 9, contact: "a" }, { x: 14, y: 10, r: 9, contact: "b" }];
  assert.equal(radar.hitTest(hits, 13, 10), "b");
  assert.equal(radar.hitTest(hits, 100, 100), null);
});
test("stale after five minutes", () => {
  assert.equal(radar.isStale({ lastSeen: 0 }, radar.STALE_MS + 1), true);
  assert.equal(radar.isStale({ lastSeen: 0 }, radar.STALE_MS), false);
});
test("list.group: hostile first, newest first within a group", () => {
  const g = list.group([{ name: "a", reaction: "friendly", lastSeen: 9 }, { name: "b", reaction: "hostile", lastSeen: 1 }, { name: "c", reaction: "hostile", lastSeen: 5 }]);
  assert.deepEqual(g.map((c) => c.name), ["c", "b", "a"]);
});
test("list.autoMap: most recent hostile wins, else most recent contact, else 0", () => {
  assert.equal(list.autoMap([{ reaction: "friendly", lastSeen: 9, mapId: 1 }, { reaction: "hostile", lastSeen: 2, mapId: 2 }]), 2);
  assert.equal(list.autoMap([{ reaction: "friendly", lastSeen: 9, mapId: 1 }]), 1);
  assert.equal(list.autoMap([]), 0);
});
test("list.age formats seconds, minutes, hours", () => {
  assert.deepEqual([list.age(5000), list.age(120000), list.age(3720000)], ["5s", "2m", "1h2m"]);
});
test("app.js builds the DOM with textContent only", () => {
  assert.equal(/innerHTML|insertAdjacentHTML|document\.write/.test(fs.readFileSync(new URL("./app.js", import.meta.url), "utf8")), false);
});
test("list.mapOptions: maps with players first by count, empty maps greyed and sorted by name after", () => {
  const charts = [{ mapId: 3, zone: "Zed" }, { mapId: 1, zone: "Alpha" }, { mapId: 2, zone: "Beta" }, { mapId: 4, zone: "Gamma" }];
  const contacts = [{ mapId: 2 }, { mapId: 4 }, { mapId: 4 }];
  const rows = list.mapOptions(charts, contacts);
  assert.deepEqual(rows.map((r) => r.zone), ["Gamma", "Beta", "Alpha", "Zed"]);
  assert.deepEqual(rows.map((r) => r.active), [true, true, false, false]);
  assert.equal(rows[0].count, 2);
});

const { world } = load("./world.js");
test("tipLines: live contact shows name, reaction, zone, position, age", () => {
  const c = { name: "Bob", reaction: "hostile", mapId: 1, x: 12.34, y: 56.78, lastSeen: 0, kind: "exact" };
  assert.deepEqual(list.tipLines({ contact: c }, () => "Durotar", 30000),
    ["Bob", "Hostile", "Durotar 12.3, 56.8", "Seen 30s ago", "exact position"]);
});
test("tipLines: a historical fix says it is an earlier position", () => {
  const l = list.tipLines({ fix: { mapId: 1, x: 1, y: 2, t: 0, kind: "approx" }, name: "Bob" }, () => "Z", 120000);
  assert.deepEqual(l, ["Bob", "Earlier position", "Z 1.0, 2.0", "2m ago", "approximate position"]);
});
test("hitTarget: returns trail hits and prefers a live dot on a tie", () => {
  const hits = [{ x: 10, y: 10, r: 6, fix: {}, name: "a" }, { x: 10, y: 10, r: 9, contact: "c" }];
  assert.equal(radar.hitTarget(hits, 10, 10).contact, "c");
  assert.equal(radar.hitTarget(hits, 40, 40), null);
});
test("world: counts per zone, pies anchored on the zone point, hit-test", () => {
  const cs = [{ mapId: 1411, reaction: "hostile" }, { mapId: 1411, reaction: "friendly" }, { mapId: 99999, reaction: "hostile" }];
  assert.equal(world.counts(cs).get(1411).total, 2);
  const pies = world.pies(cs, (x, y) => ({ x, y }));
  assert.equal(pies.length, 1); // unknown zone is skipped
  assert.equal(world.hit(pies, pies[0].x, pies[0].y).mapId, 1411);
  assert.equal(world.hit(pies, 0, 0), null);
});

test("clampView: at zoom 1 the map is centred whatever the centre was", () => {
  const v = radar.clampView({ z: 1, cx: 5, cy: 99 }, { w: 200, h: 200 });
  assert.deepEqual([v.cx, v.cy], [50, 50]);
});
test("clampView: zoomed in, the centre stops where the map edge meets the canvas edge", () => {
  const sz = { w: 200, h: 200 }; // 2x zoom shows half of each axis
  assert.deepEqual(Object.values(radar.clampView({ z: 2, cx: -40, cy: 500 }, sz)).slice(1), [25, 75]);
  assert.deepEqual(Object.values(radar.clampView({ z: 2, cx: 140, cy: 30 }, sz)).slice(1), [75, 30]);
});
test("clampView: the edge lands exactly on the canvas edge (project of 0 and 100)", () => {
  const sz = { w: 300, h: 150, aspect: 2 };
  for (const z of [1.5, 3, 20]) for (const [cx, cy] of [[-1e3, -1e3], [1e3, 1e3]]) {
    const v = radar.clampView({ z, cx, cy }, sz);
    const a = radar.project(v, 0, 0, sz, "std"), b = radar.project(v, 100, 100, sz, "std");
    assert.ok(a.x <= 1e-9 && a.y <= 1e-9 && b.x >= sz.w - 1e-9 && b.y >= sz.h - 1e-9, JSON.stringify({ z, a, b }));
  }
});
test("clampView: a canvas wider than the map keeps it centred on that axis and clamps the other", () => {
  const sz = { w: 400, h: 100, aspect: 1 }; // map is 100px square inside a 400px wide canvas
  const v = radar.clampView({ z: 2, cx: 0, cy: 0 }, sz);
  assert.equal(v.cx, 50);
  assert.equal(v.cy, 25);
});
