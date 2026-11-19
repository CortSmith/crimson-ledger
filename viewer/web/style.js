"use strict";
// Pure look-and-feel helpers shared by the radar canvas and the list swatches, so one setting changes both.
// Nothing here touches the DOM; drawShape only needs a 2D context.
(function () {
  const REACTIONS = ["friendly", "hostile", "neutral"];
  const DISTINCT = { friendly: "circle", hostile: "diamond", neutral: "square" };
  const LIGHT_BG = "#e9e9ee";
  const UNTRACKED_ALPHA = 0.35;

  /** Colours for the current appearance. presets comes from the Go side (config.Presets), never from this file. */
  function resolvePalette(ap, presets, dark) {
    const base = presets[ap.palette] || presets["default"] || {};
    const out = Object.assign({}, base);
    if (ap.palette !== "custom" && !dark && ap.palette !== "highcontrast") out.background = LIGHT_BG;
    if (ap.palette === "custom") Object.assign(out, presets["default"], ap.custom || {});
    else if (ap.custom && ap.custom.background) out.background = ap.custom.background;
    return out;
  }

  /** The shape each reaction gets. Shape-only mode (or a mono palette) guarantees three different shapes. */
  function shapesFor(ap) {
    const s = Object.assign({}, DISTINCT, ap.shapes || {});
    const forced = ap.shape_only || ap.palette === "mono";
    if (forced && new Set(REACTIONS.map((r) => s[r])).size < 3) return Object.assign({}, DISTINCT);
    return s;
  }

  /** The reaction used for colour and shape: neutral folds into hostile when the user asks for it. */
  function reactionOf(c, ap) { return c.reaction === "neutral" && ap.neutral_as_hostile ? "hostile" : c.reaction; }

  function isStale(c, now, ap) { return now - c.lastSeen > ap.stale_minutes * 60000; }

  /**
   * Everything needed to paint one live dot. Pure.
   * o: { selected, tracked, hasTracked, trackColor }
   */
  function pipStyle(c, ap, colors, now, o) {
    o = o || {};
    const reaction = reactionOf(c, ap);
    const stale = isStale(c, now, ap);
    let fill = colors[reaction] || colors.hostile, alpha = 1, hollow = false;
    if (stale) {
      if (ap.stale === "dim") alpha = 0.4;
      else if (ap.stale === "hollow") hollow = true;
      else fill = colors.stale;
    }
    if (c.kind === "approx" && ap.approx === "hollow") hollow = true;
    if (o.hasTracked && !o.tracked) alpha = Math.min(alpha, UNTRACKED_ALPHA);
    const r = ap.dot_size + (o.selected ? 2 : 0);
    const out = { shape: shapesFor(ap)[reaction] || "diamond", r, fill, hollow, alpha, reaction, stale,
      outline: ap.outline === "none" ? 0 : ap.outline === "thick" ? 2 : 1, outlineColor: colors.background,
      ring: o.selected ? { r: ap.selected_ring, color: colors.selection } : null,
      trackRing: o.hasTracked && o.tracked && o.trackColor ? { r: ap.dot_size + 4, color: o.trackColor } : null };
    return out;
  }

  // Area-matched sizes: a circle of radius r, a square of half-side .886r and a diamond of half-diagonal 1.25r cover
  // about the same area, so no reaction looks bigger than another at the same dot size.
  function path(ctx, shape, x, y, r) {
    ctx.beginPath();
    if (shape === "circle") ctx.arc(x, y, r, 0, Math.PI * 2);
    else if (shape === "square") { const h = r * 0.886; ctx.rect(x - h, y - h, h * 2, h * 2); }
    else if (shape === "triangle") { const h = r * 1.35; ctx.moveTo(x, y - h); ctx.lineTo(x + h * 0.95, y + h * 0.7); ctx.lineTo(x - h * 0.95, y + h * 0.7); ctx.closePath(); }
    else if (shape === "cross") { const a = r * 1.1, b = r * 0.38; ctx.moveTo(x - b, y - a); ctx.lineTo(x + b, y - a); ctx.lineTo(x + b, y - b); ctx.lineTo(x + a, y - b); ctx.lineTo(x + a, y + b); ctx.lineTo(x + b, y + b); ctx.lineTo(x + b, y + a); ctx.lineTo(x - b, y + a); ctx.lineTo(x - b, y + b); ctx.lineTo(x - a, y + b); ctx.lineTo(x - a, y - b); ctx.lineTo(x - b, y - b); ctx.closePath(); }
    else { const h = r * 1.25; ctx.moveTo(x, y - h); ctx.lineTo(x + h, y); ctx.lineTo(x, y + h); ctx.lineTo(x - h, y); ctx.closePath(); } // diamond
  }

  /** Paint one dot from a pipStyle result. Leaves the context as it found it. */
  function drawPip(ctx, p, x, y) {
    ctx.save();
    ctx.globalAlpha = p.alpha;
    path(ctx, p.shape, x, y, p.r);
    if (p.hollow) { ctx.strokeStyle = p.fill; ctx.lineWidth = 2; ctx.stroke(); }
    else {
      ctx.fillStyle = p.fill; ctx.fill();
      if (p.outline) { ctx.strokeStyle = p.outlineColor; ctx.lineWidth = p.outline; ctx.stroke(); }
    }
    for (const ring of [p.trackRing, p.ring]) {
      if (!ring) continue;
      ctx.globalAlpha = 1; ctx.strokeStyle = ring.color; ctx.lineWidth = 2;
      ctx.beginPath(); ctx.arc(x, y, ring.r, 0, Math.PI * 2); ctx.stroke();
    }
    ctx.restore();
  }

  /** Stable colour key per player: FNV-1a over the GUID, mod 8 (same hash radar-live uses, so colours match). */
  function trackIndex(guid) {
    let h = 0x811c9dc5;
    for (let i = 0; i < guid.length; i++) { h ^= guid.charCodeAt(i); h = Math.imul(h, 0x01000193) >>> 0; }
    return h % 8;
  }
  function trackColor(guid, colors) { return colors["track" + (trackIndex(guid) + 1)] || colors.trail; }

  function lum(hex) {
    const v = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255).map((c) => (c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4)));
    return 0.2126 * v[0] + 0.7152 * v[1] + 0.0722 * v[2];
  }
  /** WCAG contrast ratio of two #rrggbb colours (1..21). */
  function contrast(a, b) {
    const x = lum(a), y = lum(b);
    return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05);
  }

  window.__app = window.__app || {};
  window.__app.style = { REACTIONS, UNTRACKED_ALPHA, resolvePalette, shapesFor, reactionOf, isStale, pipStyle, drawPip, drawShapePath: path,
    trackIndex, trackColor, contrast };
})();
