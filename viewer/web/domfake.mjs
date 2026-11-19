// A small DOM for page tests (no dependencies): enough of Element, events and a canvas to run the real page scripts.
import fs from "node:fs";
import vm from "node:vm";

const VOID = new Set(["input", "br", "img", "hr"]);
const rel = (p) => new URL(p, import.meta.url);

export class Event {
  constructor(type, init = {}) { this.type = type; this.defaultPrevented = false; this.bubbles = true; Object.assign(this, init); }
  preventDefault() { this.defaultPrevented = true; }
  stopPropagation() { this.stopped = true; }
}

export class El {
  constructor(doc, tag) {
    this.ownerDocument = doc; this.tagName = tag.toUpperCase(); this.children = []; this.parentNode = null; this.attrs = {};
    this.listeners = {}; this._text = ""; this._value = ""; this.dataset = {}; this.style = {}; this.hidden = false; this.className = "";
    this.checked = false; this.disabled = false; this.tabIndex = -1; this._sel = 0;
    const self = this;
    this.classList = {
      _l() { return self.className.split(/\s+/).filter(Boolean); },
      add(c) { if (!this._l().includes(c)) self.className = [...this._l(), c].join(" "); },
      remove(c) { self.className = this._l().filter((x) => x !== c).join(" "); },
      contains(c) { return this._l().includes(c); },
      toggle(c, force) { const on = force === undefined ? !this.contains(c) : !!force; on ? this.add(c) : this.remove(c); return on; },
    };
  }
  get id() { return this.attrs.id || ""; }
  set id(v) { this.attrs.id = v; }
  get type() { return this.attrs.type || (this.tagName === "INPUT" ? "text" : ""); }
  set type(v) { this.attrs.type = v; }
  get textContent() { return this._text + this.children.map((c) => c.textContent).join(""); }
  set textContent(v) { this.children.forEach((c) => (c.parentNode = null)); this.children = []; this._text = String(v); }
  get options() { return this.all().filter((c) => c.tagName === "OPTION"); }
  get value() {
    if (this.tagName === "SELECT") { const o = this.options[this._sel]; return o ? o.value : ""; }
    if (this.tagName === "OPTION") return this.attrs.value !== undefined ? this.attrs.value : this.textContent;
    return this._value;
  }
  set value(v) {
    if (this.tagName === "SELECT") { const i = this.options.findIndex((o) => o.value === String(v)); this._sel = i; return; }
    if (this.tagName === "OPTION") { this.attrs.value = String(v); return; }
    this._value = String(v);
  }
  get selectedIndex() { return this._sel; }
  setAttribute(k, v) { this.attrs[k] = String(v); if (k === "hidden") this.hidden = true; if (k.startsWith("data-")) this.dataset[k.slice(5)] = String(v); }
  getAttribute(k) { return this.attrs[k] === undefined ? null : this.attrs[k]; }
  removeAttribute(k) { delete this.attrs[k]; if (k === "hidden") this.hidden = false; if (k.startsWith("data-")) delete this.dataset[k.slice(5)]; }
  append(...nodes) {
    for (const n of nodes) {
      if (typeof n === "string") { this._text += n; continue; }
      if (n.parentNode) n.remove();
      n.parentNode = this; this.children.push(n);
    }
  }
  prepend(...nodes) { const old = this.children; this.children = []; this.append(...nodes); this.children.push(...old); }
  appendChild(n) { this.append(n); return n; }
  remove() { if (this.parentNode) { this.parentNode.children = this.parentNode.children.filter((c) => c !== this); this.parentNode = null; } }
  addEventListener(t, f) { (this.listeners[t] ||= []).push(f); }
  removeEventListener(t, f) { this.listeners[t] = (this.listeners[t] || []).filter((x) => x !== f); }
  dispatchEvent(e) {
    e.target = e.target || this;
    for (let n = this; n && !e.stopped; n = e.bubbles ? n.parentNode || (n === this.ownerDocument.documentElement ? this.ownerDocument : null) : null) {
      e.currentTarget = n;
      for (const f of [...((n.listeners || {})[e.type] || [])]) f.call(n, e);
    }
    return !e.defaultPrevented;
  }
  click() { if (this.disabled) return; if (this.type === "checkbox") { this.checked = !this.checked; this.dispatchEvent(new Event("change")); } this.dispatchEvent(new Event("click")); }
  focus() { this.ownerDocument.activeElement = this; this.dispatchEvent(new Event("focus", { bubbles: false })); }
  select() {}
  get offsetWidth() { return 120; }
  get offsetHeight() { return 40; }
  get clientWidth() { return 800; }
  get clientHeight() { return 600; }
  getBoundingClientRect() { return { left: 0, top: 0, width: 800, height: 600, right: 800, bottom: 600 }; }
  getContext() { return this._ctx ||= makeCtx(); }
  setPointerCapture() {}
  all() { return this.children.flatMap((c) => [c, ...c.all()]); }
  querySelectorAll(sel) {
    // Supports "tag", ".class", "#id", "[data-x]" and a descendant chain such as ".stabs button".
    const parts = sel.trim().split(/\s+/);
    const match = (e, p) => (p.startsWith(".") ? e.classList.contains(p.slice(1)) : p.startsWith("#") ? e.id === p.slice(1) : p.startsWith("[") ? e.attrs[p.slice(1, -1)] !== undefined || e.dataset[p.slice(6, -1)] !== undefined : e.tagName === p.toUpperCase());
    return this.all().filter((e) => {
      if (!match(e, parts[parts.length - 1])) return false;
      let a = e.parentNode;
      for (let i = parts.length - 2; i >= 0; i--) { while (a && !match(a, parts[i])) a = a.parentNode; if (!a) return false; a = a.parentNode; }
      return true;
    });
  }
  querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }
}

export function makeCtx() {
  const calls = [];
  const ctx = new Proxy({ calls }, {
    get(t, k) { if (k in t) return t[k]; if (k === "measureText") return () => ({ width: 40 }); return (...a) => { calls.push([k, ...a]); }; },
    set(t, k, v) { t[k] = v; return true; },
  });
  return ctx;
}

function parse(doc, html, root) {
  const stack = [root];
  for (const m of html.matchAll(/<!--[\s\S]*?-->|<(\/?)([a-zA-Z][\w-]*)([^>]*?)(\/?)>|([^<]+)/g)) {
    const top = stack[stack.length - 1];
    if (m[5] !== undefined) { const t = m[5].replace(/\s+/g, " "); if (t.trim()) top._text += t; continue; }
    if (m[2] === undefined) continue;
    const tag = m[2].toLowerCase();
    if (m[1]) { if (stack.length > 1) stack.pop(); continue; }
    const e = new El(doc, tag);
    for (const a of m[3].matchAll(/([\w:-]+)(?:="([^"]*)")?/g)) {
      const v = a[2] === undefined ? "" : a[2];
      if (a[1] === "class") e.className = v; else if (a[1] === "hidden") e.hidden = true; else e.setAttribute(a[1], v);
    }
    if (e.attrs.value !== undefined && tag === "input") e._value = e.attrs.value;
    top.append(e);
    if (!VOID.has(tag) && !m[4]) stack.push(e);
  }
}

/** Fake timers: setTimeout/setInterval only run when the test calls tick(). */
function timers() {
  let now = 0, id = 0; const q = new Map();
  return {
    setTimeout(f, ms = 0) { q.set(++id, { f, at: now + ms }); return id; },
    clearTimeout(i) { q.delete(i); },
    setInterval(f, ms) { q.set(++id, { f, at: now + ms, every: ms }); return id; },
    clearInterval(i) { q.delete(i); },
    tick(ms) {
      const end = now + ms;
      for (;;) {
        const due = [...q.entries()].filter(([, t]) => t.at <= end).sort((a, b) => a[1].at - b[1].at)[0];
        if (!due) break;
        const [i, t] = due; now = t.at;
        if (t.every) t.at += t.every; else q.delete(i);
        t.f();
      }
      now = end;
    },
  };
}

const SCRIPTS = ["../../internal/ui/page/keyfield.js", "style.js", "tracks.js", "radar.js", "list.js", "panels.js", "world.js", "appearance.js", "app.js"];

/**
 * Boots the real viewer page. `api` maps each bound function name to a handler (sync or async); every call is recorded
 * in `calls` as [name, ...args]. Returns the page handles tests drive.
 */
export async function boot(api = {}, opts = {}) {
  const doc = new El(null, "#document");
  doc.ownerDocument = doc; doc.documentElement = new El(doc, "html"); doc.body = new El(doc, "body");
  doc.documentElement.append(doc.body);
  parse(doc, fs.readFileSync(rel("index.html"), "utf8"), doc.body);
  doc.hidden = false;
  doc.getElementById = (id) => doc.body.all().find((e) => e.id === id) || null;
  doc.querySelectorAll = (s) => doc.body.querySelectorAll(s);
  doc.querySelector = (s) => doc.body.querySelector(s);
  doc.createElement = (t) => new El(doc, t);
  doc.createElementNS = (_ns, t) => new El(doc, t);
  doc.createTextNode = (t) => ({ textContent: t });
  const T = timers(), calls = [], errors = [], frames = [];
  const win = {
    document: doc, console, Date, Math, JSON, Promise, Map, Set, Intl, Number, String, Array, Object, Error, RegExp, isFinite, parseInt, parseFloat,
    setTimeout: T.setTimeout, clearTimeout: T.clearTimeout, setInterval: T.setInterval, clearInterval: T.clearInterval,
    requestAnimationFrame: (f) => { frames.push(f); return frames.length; },
    matchMedia: () => ({ matches: !!opts.dark, addEventListener() {} }), devicePixelRatio: 1,
    Image: class { set src(v) { this._src = v; } },
    listeners: {},
    addEventListener(t, f) { (win.listeners[t] ||= []).push(f); },
  };
  win.window = win; doc.defaultView = win;
  doc.addEventListener = El.prototype.addEventListener.bind(doc); doc.listeners = {};
  doc.dispatchEvent = El.prototype.dispatchEvent.bind(doc);
  doc.hidden = false;
  doc.parentNode = null;
  const ctx = vm.createContext(win);
  const names = ["loadSettings", "saveSettings", "revealKey", "connect", "setVisible", "setRuleset", "getCharts", "getMap", "search", "getTrail",
    "getBounty", "copyTarget", "getView", "saveView", "testViewer", "getAppearance", "saveAppearance", "resetAppearance"];
  for (const n of names) {
    win[n] = async (...a) => {
      calls.push([n, ...a]);
      if (!(n in api)) throw new Error("test did not provide " + n);
      return typeof api[n] === "function" ? api[n](...a) : api[n];
    };
  }
  // Errors in async handlers must fail the test, not vanish.
  process.on("unhandledRejection", (e) => errors.push(e));
  for (const s of SCRIPTS) vm.runInContext(fs.readFileSync(rel(s), "utf8"), ctx, { filename: s });
  const page = {
    doc, win, calls, errors, T,
    $: (id) => { const e = doc.getElementById(id); if (!e) throw new Error("no #" + id); return e; },
    /** Let every pending promise settle (the page chains several awaits). */
    async settle() { for (let i = 0; i < 20; i++) await new Promise((r) => setImmediate(r)); if (errors.length) throw errors[0]; },
    frame() { const f = frames.splice(0); f.forEach((g) => g(Date.now())); },
    called: (n) => calls.filter((c) => c[0] === n),
    key(k, o = {}) { const e = new Event("keydown", { key: k, target: doc.body, ctrlKey: false, metaKey: false, ...o }); (win.listeners.keydown || []).forEach((f) => f(e)); return e; },
    update(u) { win.__app.onUpdate(u); },
    type(id, text) { const e = page.$(id); e.value = text; e.dispatchEvent(new Event("input")); },
    change(id, v) { const e = page.$(id); if (v !== undefined) e.value = v; e.dispatchEvent(new Event("change")); },
    pointer(type, x, y, extra = {}) { page.$("radar").dispatchEvent(new Event(type, { clientX: x, clientY: y, ...extra })); },
    rows: () => page.$("list").children,
  };
  return page;
}
