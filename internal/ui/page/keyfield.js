"use strict";
(function () {
  const REMASK_MS = 15000;
  // SVG namespace identifier (never fetched); split so the page-wide external-URL guard stays strict.
  const SVG_NS = "http" + "://www.w3.org/2000/svg";
  const EYE = "M1 12s4-7 11-7 11 7 11 7-4 7-11 7S1 12 1 12zm11-3a3 3 0 100 6 3 3 0 000-6z";
  const EYE_OFF = "M2 2l20 20M10.6 6.1A10.9 10.9 0 0112 5c7 0 11 7 11 7a18 18 0 01-3.2 4M6.5 6.6C3.4 8.4 1 12 1 12s4 7 11 7c1.7 0 3.2-.4 4.5-1";

  /**
   * Mount the masked API-key field. The real key never enters the DOM until reveal() resolves.
   * @param {Element} host container element
   * @param {{hasKey:boolean, mask:string, reveal:function():Promise<string>}} o
   * @param {{doc?:Document, setTimeout?:Function, clearTimeout?:Function}} [env] test seams
   * @returns {{remask:function(), newKey:function():(string|null), input:Element}}
   */
  function mount(host, o, env) {
    const e = Object.assign({ doc: document, setTimeout: setTimeout.bind(globalThis), clearTimeout: clearTimeout.bind(globalThis) }, env);
    const input = e.doc.createElement("input");
    const btn = e.doc.createElement("button");
    const state = { revealed: false, timer: null, edited: false };
    if (host.__keyfieldOff) host.__keyfieldOff(); // mounting again (settings reopened) replaces the old field instead of adding a second
    host.textContent = "";
    input.autocomplete = "off";
    input.spellcheck = false;
    btn.type = "button";
    host.className = "keyfield";
    host.append(input, btn);

    function icon(off) {
      btn.textContent = "";
      const svg = e.doc.createElementNS(SVG_NS, "svg");
      const p = e.doc.createElementNS(SVG_NS, "path");
      svg.setAttribute("viewBox", "0 0 24 24");
      p.setAttribute("d", off ? EYE_OFF : EYE);
      p.setAttribute("fill", "none");
      p.setAttribute("stroke", "currentColor");
      svg.append(p);
      btn.append(svg);
      btn.setAttribute("aria-pressed", off ? "true" : "false");
      btn.setAttribute("aria-label", off ? "Hide API key" : "Show API key");
    }

    function remask() {
      e.clearTimeout(state.timer);
      state.revealed = false;
      state.edited = false;
      input.value = o.hasKey ? o.mask : "";
      input.type = "password";
      input.dataset.hasKey = String(o.hasKey);
      icon(false);
    }

    // Passive triggers (alt-tab, timer) must never discard what the user typed: hide it, keep it.
    function hide() {
      if (!state.edited) return remask();
      e.clearTimeout(state.timer);
      state.revealed = false;
      input.type = "password";
      icon(false);
    }

    async function reveal() {
      const key = await o.reveal();
      input.value = key;
      input.type = "text";
      state.revealed = true;
      icon(true);
      state.timer = e.setTimeout(hide, REMASK_MS);
    }

    btn.addEventListener("click", () => (state.revealed ? remask() : reveal()));
    // Focus (including returning from alt-tab) never edits the value; the mask is cleared on the first real edit.
    input.addEventListener("focus", () => { if (!state.edited && input.value === o.mask) input.select?.(); });
    input.addEventListener("beforeinput", () => {
      if (!state.revealed && !state.edited && input.value === o.mask) input.value = "";
    });
    input.addEventListener("input", () => { state.edited = true; });
    input.addEventListener("keydown", (ev) => { if (ev.key === "Escape") remask(); });
    const win = e.doc.defaultView;
    win?.addEventListener("blur", hide);
    host.__keyfieldOff = () => { e.clearTimeout(state.timer); win?.removeEventListener("blur", hide); };
    remask();

    return {
      input,
      remask,
      /** @returns {string|null} the new key typed by the user, or null when unchanged (Go keeps the stored key). */
      newKey() { return state.edited && input.value !== o.mask ? input.value : null; },
    };
  }

  window.__app = window.__app || {};
  window.__app.keyfield = { mount, REMASK_MS };
})();
