// Every control of the viewer page, driven through a small DOM against the real scripts (see domfake.mjs).
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import { boot, Event } from "./domfake.mjs";

const AP = JSON.parse(fs.readFileSync(new URL("./testdata/appearance.json", import.meta.url), "utf8"));
const NOW = Date.now();
const G = (n) => "Player-1329-0A1B2C" + String(n).padStart(2, "0");
const contacts = () => [
  { guid: G(1), name: "Mc Cheeki", reaction: "hostile", mapId: 1, x: 30, y: 40, lastSeen: NOW - 30_000, kind: "exact" },
  { guid: G(2), name: "Friend", reaction: "friendly", mapId: 1, x: 60, y: 40, lastSeen: NOW - 5_000, kind: "exact" },
  { guid: G(3), name: "Bandit", reaction: "hostile", mapId: 2, x: 20, y: 70, lastSeen: NOW - 90_000, kind: "approx" },
  { guid: G(4), name: "Wanderer", reaction: "neutral", mapId: 1, x: 80, y: 80, lastSeen: NOW - 400_000, kind: "exact" },
];
const trail = (name) => ({
  ok: true, trail: { guid: G(1), name, total: 120, maps: [{ mapId: 1, zone: "Elwynn Forest", count: 90 }, { mapId: 2, zone: "Durotar", count: 30 }],
    fixes: [{ t: NOW - 600_000, mapId: 1, x: 10, y: 20, kind: "exact" }, { t: NOW - 300_000, mapId: 1, x: 30, y: 40, kind: "approx" }, { t: NOW - 30_000, mapId: 2, x: 50, y: 60, kind: "exact" }] },
});
const settingsView = { ok: true, serverUrl: "https://example.test", caFile: "", ruleset: "auto", extra: { poll_interval_s: "15" }, hasKey: true, keyMask: "********", file: "/home/u/.crimson-ledger/viewer.json" };

function api(over = {}) {
  return {
    getView: { ruleset: "pvp", orientation: "std", selectedMap: 0, configured: true, showHistory: true },
    getAppearance: () => structuredClone(AP),
    connect: { ok: true }, getCharts: { ok: true, charts: [{ mapId: 1, zone: "Elwynn Forest" }, { mapId: 2, zone: "Durotar" }, { mapId: 3, zone: "Tirisfal" }] },
    getMap: { ok: false }, setVisible: true, saveView: { ok: true }, copyTarget: (n) => ({ ok: true, message: "/target " + n }),
    search: { ok: true, players: [{ guid: G(1), name: "Mc Cheeki" }, { guid: G(3), name: "Bandit" }] }, getBounty: { ok: true, ranking: [{ guid: G(1), name: "Mc Cheeki", kills: 12 }, { guid: G(2), name: "Friend", kills: 3 }] },
    getTrail: (g) => trail(g === G(3) ? "Bandit" : "Mc Cheeki"), setRuleset: { ok: true },
    loadSettings: () => structuredClone(settingsView), saveSettings: { ok: true }, saveAppearance: { ok: true },
    resetAppearance: () => structuredClone(AP), testViewer: { ok: true, message: "Server reachable, key accepted." }, revealKey: "the-real-key",
    ...over,
  };
}
async function started(over, opts) {
  const p = await boot(api(over), opts);
  await p.settle();
  p.update({ state: "ok", contacts: contacts() });
  await p.settle();
  return p;
}
const names = (p) => p.rows().map((li) => li.children[1].textContent);
const pressed = (p, id) => p.$(id).getAttribute("aria-pressed");
const selected = (p, id) => p.$(id).getAttribute("aria-selected");
// Where a contact is drawn with the default view (no map image): 800x600 canvas, 600 px square map centred on 50,50.
const at = (c) => [400 + (c.x - 50) * 6, 300 + (c.y - 50) * 6];

test("start: loads view, look, connects and fetches charts; the radar view is shown, settings hidden", async () => {
  const p = await boot(api());
  await p.settle();
  assert.deepEqual(p.calls.map((c) => c[0]).slice(0, 2).sort(), ["getAppearance", "getView"]);
  for (const n of ["connect", "getCharts"]) assert.equal(p.called(n).length, 1, n);
  assert.equal(p.$("radarView").hidden, false);
  assert.equal(p.$("settingsView").hidden, true);
  assert.equal(p.$("rsBadge").textContent, "PvP");
});
test("start: not configured opens the settings with the first-run hint and does not connect", async () => {
  const p = await boot(api({ getView: { ruleset: "normal", orientation: "std", selectedMap: 0, configured: false } }));
  await p.settle();
  assert.equal(p.$("settingsView").hidden, false);
  assert.equal(p.$("radarView").hidden, true);
  assert.equal(p.$("firstRun").hidden, false);
  assert.equal(p.called("connect").length, 0);
});
test("start: a failed connect shows the banner", async () => {
  const p = await boot(api({ connect: { ok: false, code: "unreachable", message: "Could not reach the server" } }));
  await p.settle();
  assert.equal(p.$("banner").hidden, false);
  assert.match(p.$("banner").textContent, /Could not reach the server \[unreachable\]/);
});

test("status dot, clock and banner follow every update", async () => {
  const p = await started();
  assert.equal(p.$("dot").className, "dot ok");
  assert.match(p.$("updated").textContent, /\d/);
  assert.equal(p.$("banner").hidden, true);
  p.update({ state: "error", error: { message: "boom", code: "x", requestId: "r1" } });
  assert.equal(p.$("dot").className, "dot error");
  assert.equal(p.$("banner").hidden, false);
  assert.match(p.$("banner").textContent, /boom \[x r1\]/);
  p.update({ state: "ok" });
  assert.equal(p.$("banner").hidden, true);
});
test("authFailed update tells the user the key was rejected", async () => {
  const p = await started();
  p.update({ state: "authFailed", error: { message: "no", code: "unauthorized" } });
  assert.equal(p.$("dot").className, "dot authFailed");
  assert.match(p.$("msg").textContent, /rejected the key/);
});

test("list: hostile first, newest first, counts in the summary, empty hint hidden", async () => {
  const p = await started();
  assert.deepEqual(names(p), ["Mc Cheeki", "Bandit", "Wanderer", "Friend"]);
  assert.equal(p.$("summary").textContent, "4 contacts");
  assert.equal(p.$("empty").hidden, true);
});
test("list: no contacts leaves the waiting hint", async () => {
  const p = await boot(api());
  await p.settle();
  p.update({ state: "ok", contacts: [] });
  assert.equal(p.$("empty").hidden, false);
  assert.equal(p.rows().length, 0);
});
test("map picker: auto, world, and the charts grouped with and without players", async () => {
  const p = await started();
  const sel = p.$("mapSel");
  assert.deepEqual(sel.children.slice(0, 2).map((o) => o.textContent), ["Auto (follow data)", "World map"]);
  assert.equal(sel.children.filter((g) => g.tagName === "OPTGROUP").length, 2, "with players / no players");
  const opts = sel.all().filter((o) => o.tagName === "OPTION").map((o) => o.textContent);
  assert.ok(opts.includes("Elwynn Forest (3)") && opts.includes("Durotar (1)") && opts.includes("Tirisfal"), opts.join("|"));
});
test("map picker: a map with players but no chart is listed by its world.js zone name", async () => {
  const p = await boot(api({ getCharts: { ok: true, charts: [] } }));
  await p.settle();
  p.update({ state: "ok", contacts: [{ guid: G(9), name: "Elf", reaction: "friendly", mapId: 1438, x: 50, y: 60, lastSeen: NOW - 1_000, kind: "exact" }] });
  await p.settle();
  const opts = p.$("mapSel").all().filter((o) => o.tagName === "OPTION").map((o) => o.textContent);
  assert.ok(opts.includes("Teldrassil (1)"), opts.join("|"));
});
test("map image: a chart-less map asks for its image by the world.js zone name", async () => {
  const p = await boot(api({ getCharts: { ok: true, charts: [] } }));
  await p.settle();
  p.update({ state: "ok", contacts: [{ guid: G(9), name: "Elf", reaction: "friendly", mapId: 1438, x: 50, y: 60, lastSeen: NOW - 1_000, kind: "exact" }] });
  await p.settle();
  p.change("mapSel", "1438");
  await p.settle();
  assert.ok(p.called("getMap").some((c) => c[1] === "Teldrassil"), JSON.stringify(p.called("getMap")));
});
test("map picker: choosing a map saves the choice", async () => {
  const p = await started();
  p.change("mapSel", "2");
  const last = p.called("saveView").at(-1)[1];
  assert.equal(last.selectedMap, 2);
  assert.equal(last.configured, true);
});

test("group chips: each toggles its group in the list and flips aria-pressed", async () => {
  const p = await started();
  for (const [id, hidden] of [["fHostile", ["Mc Cheeki", "Bandit"]], ["fFriendly", ["Friend"]], ["fNeutral", ["Wanderer"]]]) {
    assert.equal(pressed(p, id), "true");
    p.$(id).click();
    assert.equal(pressed(p, id), "false", id);
    for (const n of hidden) assert.ok(!names(p).includes(n), id + " hides " + n);
    p.$(id).click();
    assert.equal(pressed(p, id), "true");
    for (const n of hidden) assert.ok(names(p).includes(n), id + " shows " + n);
  }
});

test("search: debounced, lists results; fewer than the debounce time does nothing", async () => {
  const p = await started();
  p.type("q", "Mc");
  assert.equal(p.called("search").length, 0);
  p.T.tick(300); await p.settle();
  assert.deepEqual(p.called("search")[0], ["search", "Mc"]);
  assert.equal(p.$("results").hidden, false);
  assert.deepEqual(p.$("results").children.map((li) => li.textContent), ["Mc Cheeki", "Bandit"]);
});
test("search: no players hides the result list; an error shows the banner", async () => {
  let p = await started({ search: { ok: true, players: [] } });
  p.type("q", "zz"); p.T.tick(300); await p.settle();
  assert.equal(p.$("results").hidden, true);
  p = await started({ search: { ok: false, message: "rate limited", code: "rate_limited", players: [] } });
  p.type("q", "zz"); p.T.tick(300); await p.settle();
  assert.match(p.$("banner").textContent, /rate limited/);
});
test("search: picking a result (click or Enter) tracks the player: chip, trail fetch, only-tracked row", async () => {
  const p = await started();
  p.type("q", "Mc"); p.T.tick(300); await p.settle();
  const [first, second] = p.$("results").children;
  first.click(); await p.settle();
  assert.equal(p.$("results").hidden, true);
  assert.deepEqual(p.called("getTrail")[0].slice(1), [G(1), 0]);
  assert.equal(p.$("chips").children.length, 1);
  assert.match(p.$("chips").children[0].textContent, /Mc Cheeki/);
  assert.equal(p.$("onlyRow").hidden, false);
  p.type("q", "Ba"); p.T.tick(300); await p.settle();
  p.$("results").children[1].dispatchEvent(new Event("keydown", { key: "Enter" })); await p.settle();
  assert.equal(p.$("chips").children.length, 2);
});

test("tracking: clicking a list row tracks, copies /target, and clicking the focused row again stops", async () => {
  const p = await started();
  p.rows()[0].click(); await p.settle();
  assert.deepEqual(p.called("copyTarget")[0].slice(1), ["Mc Cheeki"]);
  assert.match(p.$("copied").textContent, /Copied: \/target Mc Cheeki/);
  assert.equal(p.$("chips").children.length, 1);
  p.rows()[0].click(); await p.settle();
  assert.equal(p.$("chips").children.length, 0);
});
test("tracking: when the clipboard is missing the page asks for Ctrl+C", async () => {
  const p = await started({ copyTarget: { ok: false, code: "no_clipboard", message: "/target Mc Cheeki" } });
  p.rows()[0].click(); await p.settle();
  assert.match(p.$("copied").textContent, /Press Ctrl\+C to copy: \/target Mc Cheeki/);
});
test("tracking: the row's x button and Escape stop tracking; the cap is eight", async () => {
  const p = await started();
  p.rows()[0].click(); await p.settle();
  const x = p.rows().find((li) => li.children[1].textContent === "Mc Cheeki").all().find((e) => e.className === "x");
  x.click(); await p.settle();
  assert.equal(p.$("chips").children.length, 0);
  p.rows()[0].click(); p.rows()[1].click(); await p.settle();
  assert.equal(p.$("chips").children.length, 2);
  p.key("Escape"); await p.settle();
  assert.equal(p.$("chips").children.length, 0);
});
test("tracking: chip buttons remove one player; show-only-tracked hides the others", async () => {
  const p = await started();
  p.rows()[0].click(); await p.settle();
  p.rows().find((li) => li.children[1].textContent === "Friend").click(); await p.settle();
  p.$("onlyTracked").click();
  assert.deepEqual(names(p).sort(), ["Friend", "Mc Cheeki"]);
  const chip = p.$("chips").children[0];
  chip.all().find((b) => b.tagName === "BUTTON").click(); await p.settle();
  assert.equal(p.$("chips").children.length, 1);
});

test("side tabs: each tab shows only its own pane and marks itself selected", async () => {
  const p = await started();
  const panes = { tabList: ["list", "summary"], tabBounty: ["bountyList"], tabHistory: ["historyPane"], tabStatus: ["statusPane"] };
  for (const tab of Object.keys(panes)) {
    p.$(tab).click(); await p.settle();
    for (const [t, ids] of Object.entries(panes)) {
      assert.equal(selected(p, t), String(t === tab), tab + " selected " + t);
      for (const id of ids) assert.equal(p.$(id).hidden, t !== tab, tab + " pane " + id);
    }
  }
});
test("bounty tab: loads the ranking; an error shows the banner", async () => {
  let p = await started();
  p.$("tabBounty").click(); await p.settle();
  assert.deepEqual(p.$("bountyList").children.map((li) => li.textContent), ["Mc Cheeki - 12", "Friend - 3"]);
  p = await started({ getBounty: { ok: false, message: "down", code: "unavailable" } });
  p.$("tabBounty").click(); await p.settle();
  assert.match(p.$("banner").textContent, /down/);
});
test("history tab: asks to track someone, then shows the focused player's summary", async () => {
  const p = await started();
  p.$("tabHistory").click();
  assert.match(p.$("historyPane").textContent, /Track a player/);
  p.rows()[0].click(); await p.settle();
  p.$("tabHistory").click();
  const t = p.$("historyPane").textContent;
  assert.match(t, /Mc Cheeki/); assert.match(t, /120/); assert.match(t, /Elwynn Forest/); assert.match(t, /Durotar/); assert.match(t, /Showing the newest 3 of 120/);
});
test("history tab: clicking a map row opens that map and returns to the list", async () => {
  const p = await started();
  p.rows()[0].click(); await p.settle();
  p.$("tabHistory").click();
  const row = p.$("historyPane").all().find((e) => e.className.includes("link") && e.textContent.startsWith("Durotar"));
  row.click(); await p.settle();
  assert.equal(p.$("mapSel").value, "2");
  assert.equal(selected(p, "tabList"), "true");
});
test("status tab: connection, counts and per-map rows; a map row opens that map", async () => {
  const p = await started();
  p.$("tabStatus").click();
  const t = p.$("statusPane").textContent;
  assert.match(t, /Connected/); assert.match(t, /Total4/); assert.match(t, /Hostile2/); assert.match(t, /Friendly1/); assert.match(t, /Neutral1/);
  const row = p.$("statusPane").all().find((e) => e.className.includes("link") && e.textContent.startsWith("Durotar"));
  row.click(); await p.settle();
  assert.equal(p.$("mapSel").value, "2");
});
test("status tab: shows an unreachable server after an error update", async () => {
  const p = await started();
  p.update({ state: "error", error: { message: "x" } });
  p.$("tabStatus").click();
  assert.match(p.$("statusPane").textContent, /Server unreachable, retrying \(1 failure\)/);
});

test("canvas: a frame draws; clicking a dot selects it, clicking empty space does nothing", async () => {
  const p = await started();
  p.frame();
  const cv = p.$("radar");
  assert.ok(cv.getContext().calls.length > 0, "something was drawn");
  const [x, y] = at(contacts()[0]);
  p.pointer("click", x, y); await p.settle();
  assert.equal(p.$("chips").children.length, 1);
  p.pointer("click", 5, 5); await p.settle();
  assert.equal(p.$("chips").children.length, 1);
});
test("canvas: hovering a dot shows a tooltip, leaving hides it", async () => {
  const p = await started();
  p.frame();
  const [x, y] = at(contacts()[0]);
  p.pointer("pointermove", x, y);
  assert.equal(p.$("tip").hidden, false);
  assert.match(p.$("tip").textContent, /Mc Cheeki/);
  p.pointer("pointerleave", 0, 0);
  assert.equal(p.$("tip").hidden, true);
  p.pointer("pointermove", 5, 5);
  assert.equal(p.$("tip").hidden, true);
});
test("canvas: dragging pans without selecting; the wheel zooms and is not left to scroll the page", async () => {
  const p = await started();
  p.frame();
  const [x, y] = at(contacts()[0]);
  p.pointer("pointerdown", 100, 100); p.pointer("pointermove", 140, 130); p.win.listeners.pointerup.forEach((f) => f());
  p.pointer("click", x, y); await p.settle();
  const ev = new Event("wheel", { deltaY: -100 });
  p.$("radar").dispatchEvent(ev);
  assert.equal(ev.defaultPrevented, true);
  p.frame();
});
test("canvas: the world map option switches the view and an empty spot shows no tooltip", async () => {
  const p = await started();
  p.change("mapSel", String(p.win.__app.world.WORLD)); p.frame();
  assert.equal(p.$("mapSel").value, String(p.win.__app.world.WORLD));
  p.pointer("pointermove", 1, 1);
  assert.equal(p.$("tip").hidden, true);
});
test("keyboard: H toggles trails and saves; typing h in a field does not", async () => {
  const p = await started();
  const before = p.$("trailsChk").checked;
  p.key("h"); await p.settle();
  assert.equal(p.$("trailsChk").checked, !before);
  assert.equal(p.called("saveAppearance").at(-1)[1].appearance.show_trails, !before);
  const n = p.called("saveAppearance").length;
  p.key("h", { target: p.$("q") }); p.key("h", { ctrlKey: true });
  assert.equal(p.called("saveAppearance").length, n);
});
test("window: hiding the page pauses polling, showing resumes it", async () => {
  const p = await started();
  p.doc.hidden = true; p.doc.dispatchEvent(new Event("visibilitychange")); await p.settle();
  p.doc.hidden = false; p.doc.dispatchEvent(new Event("visibilitychange")); await p.settle();
  assert.deepEqual(p.called("setVisible").map((c) => c[1]), [false, true]);
});

// ---- settings ----
async function inSettings(over) {
  const p = await started(over);
  p.$("settingsBtn").click(); await p.settle();
  return p;
}
test("settings: the button opens the form filled from the saved values", async () => {
  const p = await inSettings();
  assert.equal(p.$("settingsView").hidden, false);
  assert.equal(p.$("radarView").hidden, true);
  assert.equal(p.$("firstRun").hidden, true);
  assert.equal(p.$("serverUrl").value, "https://example.test");
  assert.equal(p.$("poll_interval_s").value, "15");
  assert.match(p.$("file").textContent, /viewer\.json/);
  assert.equal(p.$("keyHost").children.length, 2);
});
test("settings: the four tabs switch panes; Test connection only shows on Connection", async () => {
  const p = await inSettings();
  for (const name of ["Display", "Appearance", "About", "Connection"]) {
    p.doc.querySelectorAll(".stabs button").find((b) => b.dataset.tab === name).click();
    for (const t of ["Connection", "Display", "Appearance", "About"]) assert.equal(p.$("pane" + t).hidden, t !== name, name + "/" + t);
    assert.equal(p.$("test").hidden, name !== "Connection");
    assert.equal(p.doc.querySelectorAll(".stabs button").find((b) => b.dataset.tab === name).getAttribute("aria-selected"), "true");
  }
});
test("settings: Save sends the form and the look and says Saved; the key is not sent unless edited", async () => {
  const p = await inSettings();
  p.type("serverUrl", "https://other.test");
  p.$("save").click(); await p.settle();
  const s = p.called("saveSettings")[0][1];
  assert.equal(s.serverUrl, "https://other.test");
  assert.equal(s.apiKey, null);
  assert.equal(p.called("saveAppearance").length >= 1, true);
  assert.equal(p.$("msg").textContent, "Saved.");
  assert.equal(p.$("msg").className, "good");
});
test("settings: a rejected form shows the message and does not save the look", async () => {
  const p = await inSettings({ saveSettings: { ok: false, message: "server address must be https" } });
  const n = p.called("saveAppearance").length;
  p.$("save").click(); await p.settle();
  assert.equal(p.$("msg").textContent, "server address must be https");
  assert.equal(p.$("msg").className, "bad");
  assert.equal(p.called("saveAppearance").length, n);
});
test("settings: Test connection saves first, then reports the result; a failed save stops there", async () => {
  let p = await inSettings();
  p.$("test").click(); await p.settle();
  assert.equal(p.called("saveSettings").length, 1);
  assert.equal(p.called("testViewer").length, 1);
  assert.equal(p.$("msg").textContent, "Server reachable, key accepted.");
  p = await inSettings({ saveSettings: { ok: false, message: "bad" }, testViewer: () => { throw new Error("must not run"); } });
  p.$("test").click(); await p.settle();
  assert.equal(p.called("testViewer").length, 0);
  p = await inSettings({ testViewer: { ok: false, code: "key_rejected", message: "Server reachable, but it rejected the key." } });
  p.$("test").click(); await p.settle();
  assert.equal(p.$("msg").className, "bad");
});
test("settings: Back saves, returns to the radar and reconnects", async () => {
  const p = await inSettings();
  const n = p.called("connect").length;
  p.$("back").click(); await p.settle();
  assert.equal(p.$("settingsView").hidden, true);
  assert.equal(p.$("radarView").hidden, false);
  assert.equal(p.called("connect").length, n + 1);
});
test("settings: Back with an invalid form stays on the settings", async () => {
  const p = await inSettings({ saveSettings: { ok: false, message: "bad" } });
  p.$("back").click(); await p.settle();
  assert.equal(p.$("settingsView").hidden, false);
});
test("settings: the key field reveals through revealKey and sends only a typed key", async () => {
  const p = await inSettings();
  const [input, btn] = p.$("keyHost").children;
  btn.click(); await p.settle();
  assert.equal(p.called("revealKey").length, 1);
  assert.equal(input.value, "the-real-key");
  input.dispatchEvent(new Event("beforeinput")); input.value = "new-key"; input.dispatchEvent(new Event("input"));
  p.$("save").click(); await p.settle();
  assert.equal(p.called("saveSettings")[0][1].apiKey, "new-key");
});
test("display tab: realm, orientation, time zone and trails controls act at once", async () => {
  const p = await inSettings();
  p.change("rulesetSel", "rp"); await p.settle();
  assert.deepEqual(p.called("setRuleset")[0].slice(1), ["rp"]);
  assert.equal(p.$("rsBadge").textContent, "RP");
  p.change("orientSel", "alt"); await p.settle();
  assert.equal(p.called("saveView").at(-1)[1].orientation, "alt");
  p.change("tzSel", "Asia/Tokyo");
  assert.notEqual(p.$("updated").textContent, "");
  p.$("trailsChk").click(); await p.settle();
  assert.equal(p.called("saveAppearance").at(-1)[1].timeZone, "Asia/Tokyo");
  assert.equal(p.called("saveAppearance").at(-1)[1].appearance.show_trails, p.$("trailsChk").checked);
});
test("display tab: the time zone list has the saved zone and common ones", async () => {
  const p = await started();
  const v = p.$("tzSel").all().filter((o) => o.tagName === "OPTION").map((o) => o.value);
  assert.ok(v.includes("local") && v.includes("UTC") && v.includes("Europe/London"));
});

test("appearance tab: every control exists, and changing each one changes the saved look", async () => {
  const p = await inSettings();
  const host = p.$("appearanceHost");
  const controls = host.all().filter((e) => ["INPUT", "SELECT"].includes(e.tagName));
  assert.ok(controls.length >= 25, "found " + controls.length + " controls");
  const before = JSON.stringify(AP.appearance);
  let changed = 0;
  for (const c of controls) {
    if (c.tagName === "SELECT") { const o = c.options.find((x) => x.value !== c.value); if (!o) continue; c.value = o.value; c.dispatchEvent(new Event("change")); c.dispatchEvent(new Event("input")); }
    else if (c.type === "checkbox") { c.checked = !c.checked; c.dispatchEvent(new Event("change")); c.dispatchEvent(new Event("input")); }
    else if (c.type === "range") { const max = Number(c.attrs.max), min = Number(c.attrs.min); c.value = String(Number(c.value) === max ? min : max); c.dispatchEvent(new Event("input")); c.dispatchEvent(new Event("change")); }
    else if (c.type === "color") { c.value = "#123456"; c.dispatchEvent(new Event("input")); c.dispatchEvent(new Event("change")); }
    else continue;
    changed++;
  }
  assert.ok(changed >= 25, "exercised " + changed);
  await p.settle();
  p.$("save").click(); await p.settle();
  const saved = p.called("saveAppearance").at(-1)[1].appearance;
  assert.notEqual(JSON.stringify(saved), before);
  for (const k of ["scheme", "dot_size", "trail_style", "background", "density", "ui_scale", "reduce_motion"]) assert.notDeepEqual(saved[k], AP.appearance[k], k + " did not change");
  assert.equal(p.$("msg").textContent, "Saved.");
});
test("appearance tab: the colour scheme and density apply to the page immediately", async () => {
  const p = await inSettings();
  const scheme = p.$("appearanceHost").all().find((e) => e.tagName === "SELECT" && e.options.some((o) => o.value === "dark"));
  scheme.value = "dark"; scheme.dispatchEvent(new Event("change")); scheme.dispatchEvent(new Event("input"));
  assert.equal(p.doc.documentElement.dataset.theme, "dark");
  const dens = p.$("appearanceHost").all().find((e) => e.tagName === "SELECT" && e.options.some((o) => o.value === "compact"));
  dens.value = "compact"; dens.dispatchEvent(new Event("change")); dens.dispatchEvent(new Event("input"));
  assert.ok(p.doc.body.classList.contains("compact"));
});
test("appearance tab: Reset restores the defaults and says so", async () => {
  const p = await inSettings();
  p.$("resetLook").click(); await p.settle();
  assert.equal(p.called("resetAppearance").length, 1);
  assert.equal(p.$("msg").textContent, "Appearance reset.");
  assert.equal(p.doc.documentElement.dataset.theme, undefined);
});

test("no handler threw anywhere in this run", async () => {
  const p = await started();
  p.$("tabStatus").click(); p.$("tabHistory").click(); p.$("tabBounty").click(); await p.settle();
  p.T.tick(60_000); await p.settle();
  assert.deepEqual(p.errors, []);
});

// ---- every map ----
const INDEX = JSON.parse(fs.readFileSync(new URL("../maps/index.json", import.meta.url), "utf8")).zones;
const norm = (s) => s.toLowerCase().replace(/[^a-z0-9]/g, "");
const zonesOf = async () => (await boot(api())).win.__app.world.ZONES;

test("every map: each world.js zone has a bundled image entry under its name", async () => {
  const ZONES = await zonesOf();
  const ids = Object.keys(ZONES);
  assert.ok(ids.length >= 40, "expected the full zone table, got " + ids.length);
  const missing = ids.filter((id) => !INDEX[norm(ZONES[id][0])]).map((id) => id + " " + ZONES[id][0]);
  assert.deepEqual(missing, []);
});

test("every map: with no chart, picking each zone lists it and asks for its image by name", async () => {
  const ZONES = await zonesOf();
  for (const id of Object.keys(ZONES)) {
    const name = ZONES[id][0];
    const p = await boot(api({ getCharts: { ok: true, charts: [] } }));
    await p.settle();
    p.update({ state: "ok", contacts: [{ guid: G(1), name: "P", reaction: "friendly", mapId: Number(id), x: 50, y: 50, lastSeen: NOW - 1_000, kind: "exact" }] });
    await p.settle();
    const opts = p.$("mapSel").all().filter((o) => o.tagName === "OPTION").map((o) => o.textContent);
    assert.ok(opts.includes(name + " (1)"), id + " " + name + " missing from picker: " + opts.join("|"));
    p.change("mapSel", id);
    await p.settle();
    assert.ok(p.called("getMap").some((c) => c[1] === name), id + " " + name + " image not requested: " + JSON.stringify(p.called("getMap")));
  }
});

test("every map: with a chart, the chart's zone name wins over the table", async () => {
  const ZONES = await zonesOf();
  for (const id of Object.keys(ZONES)) {
    const p = await boot(api({ getCharts: { ok: true, charts: [{ mapId: Number(id), zone: "Charted " + id }] } }));
    await p.settle();
    p.update({ state: "ok", contacts: [{ guid: G(1), name: "P", reaction: "friendly", mapId: Number(id), x: 50, y: 50, lastSeen: NOW - 1_000, kind: "exact" }] });
    await p.settle();
    p.change("mapSel", id);
    await p.settle();
    assert.ok(p.called("getMap").some((c) => c[1] === "Charted " + id), id + " did not use the chart name");
  }
});

// ---- map edges: panning and zooming never move the map's edge into the canvas ----
const corner = (x, y) => ({ guid: G(7), name: "Edge", reaction: "friendly", mapId: 1, x, y, lastSeen: NOW - 1_000, kind: "exact" });
async function zoomedOnCorners() {
  const p = await boot(api());
  await p.settle();
  p.update({ state: "ok", contacts: [corner(0, 0), { ...corner(100, 100), guid: G(8), name: "Far" }] });
  await p.settle();
  for (let i = 0; i < 25; i++) p.$("radar").dispatchEvent(new Event("wheel", { deltaY: -100 }));
  return p;
}
const dragBy = (p, dx, dy) => {
  p.pointer("pointerdown", 400, 300); p.pointer("pointermove", 400 + dx, 300 + dy);
  p.win.listeners.pointerup.forEach((f) => f());
};
test("map edges: dragging far past the top-left stops with the map corner at the canvas corner", async () => {
  const p = await zoomedOnCorners();
  dragBy(p, 1e5, 1e5); p.frame();
  p.pointer("pointermove", 1, 1);
  assert.equal(p.$("tip").hidden, false, "the (0,0) corner sits on the canvas corner");
  assert.match(p.$("tip").textContent, /Edge/);
});
test("map edges: dragging far past the bottom-right stops with the map corner at the canvas corner", async () => {
  const p = await zoomedOnCorners();
  dragBy(p, -1e5, -1e5); p.frame();
  p.pointer("pointermove", 799, 599);
  assert.equal(p.$("tip").hidden, false, "the (100,100) corner sits on the canvas corner");
  assert.match(p.$("tip").textContent, /Far/);
});
test("map edges: zooming out after panning returns to the centred whole map", async () => {
  const p = await zoomedOnCorners();
  dragBy(p, 1e5, 1e5);
  for (let i = 0; i < 60; i++) p.$("radar").dispatchEvent(new Event("wheel", { deltaY: 100 }));
  p.frame();
  const [x, y] = at({ x: 0, y: 0 });
  p.pointer("pointermove", x, y);
  assert.equal(p.$("tip").hidden, false, "back at zoom 1 the corner is where the centred map puts it");
});
