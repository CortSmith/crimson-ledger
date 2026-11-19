import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";

class El {
  constructor(tag) { this.tag = tag; this.children = []; this.listeners = {}; this.attrs = {}; this.dataset = {}; this.value = ""; this.type = ""; }
  append(...c) { this.children.push(...c); }
  set textContent(_) { this.children = []; }
  setAttribute(k, v) { this.attrs[k] = v; }
  addEventListener(n, f) { (this.listeners[n] ||= []).push(f); }
  removeEventListener(n, f) { this.listeners[n] = (this.listeners[n] || []).filter((x) => x !== f); }
  fire(n, ev = {}) { return Promise.all((this.listeners[n] || []).map((f) => f(ev))); }
}
function setup(hasKey = true, revealKey = "clk_v_abcd1234_" + "x".repeat(43)) {
  const win = new El("win");
  const doc = { createElement: (t) => new El(t), createElementNS: (_, t) => new El(t), defaultView: win };
  const timers = [];
  const env = { doc, setTimeout: (f, ms) => (timers.push({ f, ms }), timers.length), clearTimeout: () => {} };
  const g = { window: {}, document: doc };
  new Function("window", "document", fs.readFileSync(new URL("./keyfield.js", import.meta.url), "utf8"))(g.window, doc);
  let revealed = 0;
  const host = new El("div");
  const kf = g.window.__app.keyfield.mount(host, { hasKey, mask: "••••••••••••", reveal: async () => (revealed++, revealKey) }, env);
  const [input, btn] = host.children;
  return { kf, input, btn, win, timers, host, mount: (o2) => g.window.__app.keyfield.mount(host, o2, env), revealCount: () => revealed, key: revealKey };
}

test("mask on load, key not present", () => {
  const s = setup();
  assert.equal(s.input.type, "password");
  assert.equal(s.input.value, "••••••••••••");
  assert.equal(s.input.dataset.hasKey, "true");
  assert.equal(s.revealCount(), 0);
  assert.equal(s.btn.attrs["aria-label"], "Show API key");
});

test("reveal shows key and flips button", async () => {
  const s = setup();
  await s.btn.fire("click");
  assert.equal(s.input.type, "text");
  assert.equal(s.input.value, s.key);
  assert.equal(s.btn.attrs["aria-pressed"], "true");
  assert.equal(s.btn.attrs["aria-label"], "Hide API key");
});

test("auto re-mask after 15 s", async () => {
  const s = setup();
  await s.btn.fire("click");
  assert.equal(s.timers.at(-1).ms, 15000);
  s.timers.at(-1).f();
  assert.equal(s.input.value, "••••••••••••");
  assert.equal(s.input.type, "password");
});

test("window blur and Escape re-mask a revealed key", async () => {
  const s = setup();
  await s.btn.fire("click");
  await s.win.fire("blur");
  assert.equal(s.input.value, "••••••••••••");
  await s.btn.fire("click");
  await s.input.fire("keydown", { key: "Escape" });
  assert.equal(s.input.type, "password");
});

test("alt-tab keeps typed text (hidden) and the new key; timer too", async () => {
  const s = setup();
  await s.input.fire("beforeinput");
  s.input.value = "clk_v_zzzzzzzz_new";
  await s.input.fire("input");
  s.input.type = "text";
  await s.win.fire("blur");
  await s.input.fire("focus");
  assert.equal(s.input.value, "clk_v_zzzzzzzz_new");
  assert.equal(s.input.type, "password");
  assert.equal(s.kf.newKey(), "clk_v_zzzzzzzz_new");
  await s.btn.fire("click");
  s.input.value = "clk_v_zzzzzzzz_new2";
  await s.input.fire("input");
  s.timers.at(-1).f();
  assert.equal(s.input.value, "clk_v_zzzzzzzz_new2");
});

test("alt-tab round trip leaves an untouched field unchanged", async () => {
  const s = setup();
  await s.win.fire("blur");
  await s.input.fire("focus");
  assert.equal(s.input.value, "••••••••••••");
  assert.equal(s.kf.newKey(), null);
});

test("focus never clears; first beforeinput clears; typing yields new key; unchanged yields null", async () => {
  const s = setup();
  assert.equal(s.kf.newKey(), null);
  await s.input.fire("focus");
  assert.equal(s.input.value, "••••••••••••");
  await s.input.fire("beforeinput");
  assert.equal(s.input.value, "");
  s.input.value = "clk_v_zzzzzzzz_new";
  await s.input.fire("input");
  assert.equal(s.kf.newKey(), "clk_v_zzzzzzzz_new");
  s.kf.remask();
  assert.equal(s.kf.newKey(), null);
});

test("no key stored: empty field", () => {
  const s = setup(false);
  assert.equal(s.input.value, "");
  assert.equal(s.input.dataset.hasKey, "false");
});

test("mounting twice on one host leaves a single field and a single blur listener", () => {
  const s = setup();
  s.mount({ hasKey: true, mask: "••••••••••••", reveal: async () => "k" });
  assert.equal(s.host.children.length, 2);
  assert.equal(s.win.listeners.blur.length, 1);
});
