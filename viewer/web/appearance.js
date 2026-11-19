"use strict";
// The Appearance tab: builds its controls from one table, previews on a small canvas, and reports every change
// through onChange so the radar updates live. Saving is done by the caller (the settings Save button).
(function () {
  const A = window.__app = window.__app || {};
  const SHAPES = [["circle", "Circle"], ["diamond", "Diamond"], ["square", "Square"], ["triangle", "Triangle"], ["cross", "Cross"]];
  const PRESET_NAMES = { default: "Default", colorblind: "Colour-blind safe", highcontrast: "High contrast", mono: "Mono (shapes only)", custom: "Custom" };
  const COLOR_LABELS = { friendly: "Friendly", hostile: "Hostile", neutral: "Neutral", stale: "Stale", trail: "Trail", selection: "Selection / labels", background: "Background" };

  // [group, key, label, type, extra]
  const FIELDS = [
    ["Theme", "scheme", "Colour scheme", "select", [["system", "System"], ["light", "Light"], ["dark", "Dark"]]],
    ["Dots", "dot_size", "Dot size", "range", { min: 3, max: 12, unit: " px" }],
    ["Dots", "selected_ring", "Selection ring size", "range", { min: 5, max: 20, unit: " px" }],
    ["Dots", "outline", "Outline", "select", [["none", "None"], ["thin", "Thin"], ["thick", "Thick"]]],
    ["Dots", "stale", "Stale dots look", "select", [["dim", "Dimmed"], ["hollow", "Hollow"], ["grey", "Grey"]]],
    ["Dots", "stale_minutes", "Stale after", "range", { min: 1, max: 120, unit: " min" }],
    ["Dots", "approx", "Approximate positions", "select", [["filled", "Filled"], ["hollow", "Hollow ring"]]],
    ["Dots", "labels", "Name labels", "select", [["none", "None"], ["hover", "On hover"], ["always", "Always"]]],
    ["Dots", "neutral_as_hostile", "Show neutral as hostile", "bool"],
    ["Dots", "shape_only", "Shape-only mode (always three distinct shapes)", "bool"],
    ["Trails", "trail_style", "Trail line", "select", [["solid", "Solid"], ["dashed", "Dashed"], ["dotted", "Dotted"]]],
    ["Trails", "trail_width", "Trail width", "range", { min: 1, max: 4, unit: " px" }],
    ["Trails", "trail_dot", "Trail dot size", "range", { min: 1, max: 6, unit: " px" }],
    ["Trails", "trail_fade", "Fade older positions", "bool"],
    ["Trails", "trail_arrow", "Arrowhead on newest segment", "bool"],
    ["Trails", "gap_minutes", "Break the line after", "range", { min: 1, max: 240, unit: " min without a fix" }],
    ["Map", "background", "Background", "select", [["map", "Map image"], ["panel", "Plain panel"], ["rings", "Radar rings"]]],
    ["Map", "map_opacity", "Map opacity", "range", { min: 30, max: 100, unit: " %" }],
    ["Map", "grid", "Grid", "select", [["off", "Off"], ["on", "10 % grid"]]],
    ["List", "density", "List density", "select", [["comfortable", "Comfortable"], ["compact", "Compact"]]],
    ["Accessibility", "reduce_motion", "Reduce motion", "bool"],
    ["Accessibility", "ui_scale", "Interface scale", "range", { min: 90, max: 150, unit: " %" }],
  ];

  const el = (tag, cls, text) => { const e = document.createElement(tag); if (cls) e.className = cls; if (text !== undefined) e.textContent = text; return e; };

  function selectInput(options, value, on) {
    const s = el("select");
    for (const [v, t] of options) { const o = el("option", "", t); o.value = v; s.append(o); }
    s.value = value; s.addEventListener("change", () => on(s.value));
    return s;
  }

  /**
   * Mount the tab into host. init: { appearance, presets, colorKeys, trackKeys, defaults, isDark() }.
   * Returns { get(): appearance, set(appearance), refresh() }.
   */
  function mount(host, init, onChange) {
    let ap = JSON.parse(JSON.stringify(init.appearance));
    ap.custom = ap.custom || {};
    host.textContent = "";
    const rows = [];
    const emit = () => { onChange(ap); preview(); refreshRows(); };
    const colors = () => A.style.resolvePalette(ap, init.presets, init.isDark());

    // --- live preview ---
    const pv = el("canvas", "preview"); pv.width = 360; pv.height = 96; pv.setAttribute("aria-label", "Preview");
    function preview() {
      const ctx = pv.getContext("2d"), c = colors(), now = Date.now();
      ctx.clearRect(0, 0, pv.width, pv.height); ctx.fillStyle = c.background; ctx.fillRect(0, 0, pv.width, pv.height);
      const mk = (reaction, x, y, extra) => Object.assign({ reaction, x, y, lastSeen: now, kind: "exact" }, extra || {});
      const demo = [mk("friendly", 30, 30), mk("hostile", 80, 30), mk("neutral", 130, 30), mk("hostile", 180, 30, { lastSeen: now - 3600e3 }),
        mk("hostile", 230, 30, { kind: "approx" })];
      const trail = [[30, 74], [90, 62], [150, 78], [210, 60], [270, 70]];
      ctx.save();
      ctx.strokeStyle = c.trail; ctx.fillStyle = c.trail; ctx.lineWidth = ap.trail_width;
      ctx.setLineDash({ solid: [], dashed: [6, 4], dotted: [1.5, 4] }[ap.trail_style] || []);
      for (let i = 1; i < trail.length; i++) {
        ctx.globalAlpha = ap.trail_fade ? 0.25 + 0.75 * (i / (trail.length - 1)) : 0.9;
        ctx.beginPath(); ctx.moveTo(trail[i - 1][0], trail[i - 1][1]); ctx.lineTo(trail[i][0], trail[i][1]); ctx.stroke();
      }
      ctx.setLineDash([]); ctx.globalAlpha = 1;
      trail.forEach(([x, y], i) => { ctx.beginPath(); ctx.arc(x, y, ap.trail_dot, 0, Math.PI * 2); if (i === 2 && ap.approx === "hollow") ctx.stroke(); else ctx.fill(); });
      ctx.restore();
      demo.forEach((d, i) => A.style.drawPip(ctx, A.style.pipStyle(d, ap, c, now, { selected: i === 1 }), d.x, d.y));
      const tc = (init.trackKeys || []).map((k) => c[k]);
      tc.forEach((col, i) => { ctx.fillStyle = col; ctx.fillRect(290 + (i % 4) * 16, 12 + Math.floor(i / 4) * 16, 12, 12); });
    }
    host.append(pv);

    // --- palette ---
    const pal = el("fieldset"); pal.append(el("legend", "", "Palette"));
    const presetSel = selectInput(Object.keys(PRESET_NAMES).map((k) => [k, PRESET_NAMES[k]]), ap.palette, (v) => {
      if (v === "custom" && !Object.keys(ap.custom).length) { const cur = colors(); ap.custom = {}; for (const k of [...init.colorKeys, ...init.trackKeys]) ap.custom[k] = cur[k]; }
      ap.palette = v; if (v !== "custom") ap.custom = {};
      emit(); fillColors();
    });
    const pr = el("label", "", "Preset"); pr.append(presetSel); pal.append(pr);
    const colorHost = el("div", "colors"); pal.append(colorHost);
    function fillColors() {
      colorHost.textContent = "";
      const cur = colors();
      for (const k of [...init.colorKeys, ...init.trackKeys]) {
        const row = el("label", "color"), inp = el("input"); inp.type = "color"; inp.value = cur[k] || "#000000";
        const name = el("span", "", COLOR_LABELS[k] || "Track " + k.replace("track", "")), ratio = el("span", "muted ratio");
        inp.addEventListener("input", () => {
          if (ap.palette !== "custom") { const all = colors(); ap.palette = "custom"; ap.custom = {}; for (const kk of [...init.colorKeys, ...init.trackKeys]) ap.custom[kk] = all[kk]; presetSel.value = "custom"; }
          ap.custom[k] = inp.value; emit(); ratioFor();
        });
        const ratioFor = () => {
          const c = colors(); if (k === "background") { ratio.textContent = ""; return; }
          const r = A.style.contrast(c[k], c.background); ratio.textContent = r.toFixed(1) + ":1" + (r < 3 ? " (low contrast)" : ""); ratio.className = "ratio " + (r < 3 ? "bad" : "muted");
        };
        ratioFor(); rows.push(ratioFor);
        row.append(inp, name, ratio); colorHost.append(row);
      }
    }
    const refreshRows = () => rows.forEach((f) => f());
    host.append(pal);

    // --- shapes per reaction ---
    const sh = el("fieldset"); sh.append(el("legend", "", "Shapes"));
    for (const r of ["friendly", "hostile", "neutral"]) {
      const l = el("label", "", r[0].toUpperCase() + r.slice(1));
      l.append(selectInput(SHAPES, ap.shapes[r], (v) => { ap.shapes[r] = v; emit(); }));
      sh.append(l);
    }
    host.append(sh);

    // --- table-driven fields ---
    const groups = new Map();
    for (const f of FIELDS) {
      const [group, key, label, type, extra] = f;
      if (!groups.has(group)) { const fs = el("fieldset"); fs.append(el("legend", "", group)); groups.set(group, fs); host.append(fs); }
      const l = el("label", type === "bool" ? "check" : "", label);
      let input;
      if (type === "select") input = selectInput(extra, ap[key], (v) => { ap[key] = v; emit(); });
      else if (type === "bool") { input = el("input"); input.type = "checkbox"; input.checked = !!ap[key]; input.addEventListener("change", () => { ap[key] = input.checked; emit(); }); }
      else {
        input = el("input"); input.type = "range"; input.min = extra.min; input.max = extra.max; input.value = ap[key];
        const val = el("span", "muted", ap[key] + extra.unit);
        input.addEventListener("input", () => { ap[key] = Number(input.value); val.textContent = input.value + extra.unit; emit(); });
        l.append(input, val); groups.get(group).append(l); continue;
      }
      if (type === "bool") l.prepend(input); else l.append(input);
      groups.get(group).append(l);
    }
    fillColors(); preview();
    return {
      get() { const out = JSON.parse(JSON.stringify(ap)); if (out.palette !== "custom") delete out.custom; return out; },
      refresh() { fillColors(); preview(); },
    };
  }

  A.appearanceUI = { mount, FIELDS };
})();
