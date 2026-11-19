import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";

function load(files) {
  const w = {};
  for (const f of files) new Function("window", fs.readFileSync(new URL(f, import.meta.url), "utf8"))(w);
  return w.__app;
}
const { panels } = load(["./list.js", "./panels.js"]);
const NOW = 1_700_000_000_000;
const zone = (id) => (id === 1 ? "Elwynn" : id === 2 ? "Durotar" : "Map " + id);

test("statusModel: counts contacts per reaction and per map, newest first", () => {
  const m = panels.statusModel({
    state: "ok", ruleset: "pvp", startedAt: NOW - 3_700_000, lastOkAt: NOW - 20_000, lastErrorAt: 0, polls: 5, failures: 0,
    contacts: [
      { reaction: "friendly", mapId: 1 }, { reaction: "hostile", mapId: 1 }, { reaction: "hostile", mapId: 2 }, { reaction: "weird", mapId: 2 }, { reaction: "hostile", mapId: 2 },
    ],
    now: NOW, zoneOf: zone,
  });
  assert.deepEqual(m.counts, { friendly: 1, hostile: 3, neutral: 1, total: 5 });
  assert.deepEqual(m.maps, [{ mapId: 2, zone: "Durotar", count: 3 }, { mapId: 1, zone: "Elwynn", count: 2 }]);
  assert.equal(m.connection, "Connected");
  assert.equal(m.lastUpdate, "20s ago");
  assert.equal(m.uptime, "1h 1m");
  assert.equal(m.ruleset, "PvP");
});
test("statusModel: states are worded for people, no update yet is said plainly", () => {
  const base = { contacts: [], now: NOW, startedAt: NOW, lastOkAt: 0, polls: 0, failures: 0, zoneOf: zone, ruleset: "normal" };
  assert.equal(panels.statusModel({ ...base, state: "connecting" }).connection, "Connecting");
  assert.equal(panels.statusModel({ ...base, state: "error", failures: 3 }).connection, "Server unreachable, retrying (3 failures)");
  assert.equal(panels.statusModel({ ...base, state: "authFailed" }).connection, "Key rejected");
  assert.equal(panels.statusModel({ ...base, state: "ok" }).lastUpdate, "none yet");
  assert.equal(panels.statusModel({ ...base, state: "ok" }).uptime, "0m");
});
test("statusModel: tolerates junk contacts and a missing list", () => {
  const m = panels.statusModel({ state: "ok", contacts: [null, {}, { reaction: "friendly" }], now: NOW, startedAt: NOW, zoneOf: zone });
  assert.equal(m.counts.total, 3);
  assert.deepEqual(panels.statusModel({ state: "ok", now: NOW, startedAt: NOW, zoneOf: zone }).counts.total, 0);
});

test("historyModel: summarises first/last fix, maps visited, and the last position", () => {
  const fixes = [
    { t: NOW - 600_000, mapId: 1, x: 10, y: 20, kind: "exact" },
    { t: NOW - 300_000, mapId: 1, x: 12, y: 22, kind: "approx" },
    { t: NOW - 60_000, mapId: 2, x: 50, y: 60, kind: "exact" },
  ];
  const h = panels.historyModel({ name: "Mc Cheeki", fixes, total: 40, maps: [{ mapId: 1, zone: "Elwynn", count: 30 }, { mapId: 2, zone: "Durotar", count: 10 }] }, NOW, zone, "UTC");
  assert.equal(h.name, "Mc Cheeki");
  assert.equal(h.total, 40);
  assert.equal(h.shown, 3);
  assert.equal(h.approx, 1);
  assert.equal(h.first, "10m ago");
  assert.equal(h.last, "1m ago");
  assert.deepEqual(h.last_pos, { mapId: 2, zone: "Durotar", x: 50, y: 60 });
  assert.deepEqual(h.maps.map((m) => [m.zone, m.count, m.pct]), [["Elwynn", 30, 75], ["Durotar", 10, 25]]);
  assert.equal(h.truncated, true); // 3 fixes shown of 40
});
test("historyModel: no fixes, unsorted fixes, and maps missing from the summary", () => {
  assert.equal(panels.historyModel(null, NOW, zone, "UTC"), null);
  const e = panels.historyModel({ name: "A", fixes: [], total: 0, maps: [] }, NOW, zone, "UTC");
  assert.equal(e.first, "none"); assert.equal(e.last_pos, null); assert.equal(e.truncated, false);
  const u = panels.historyModel({ name: "A", total: 2, fixes: [{ t: NOW - 1000, mapId: 7, x: 1, y: 2 }, { t: NOW - 90_000, mapId: 7, x: 3, y: 4 }] }, NOW, zone, "UTC");
  assert.deepEqual(u.last_pos, { mapId: 7, zone: "Map 7", x: 1, y: 2 });
  assert.deepEqual(u.maps, [{ mapId: 7, zone: "Map 7", count: 2, pct: 100 }]); // derived from fixes when the API gave none
});
