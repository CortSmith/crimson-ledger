"use strict";
(function () {
  const A = window.__app = window.__app || {};
  const STALE_MS = 5 * 60 * 1000;

  /** Map percent coordinates (0-100) to canvas pixels. Pure. orientation "std": x right, y down; "alt": axes swapped. */
  function project(view, x, y, size, orientation) {
    const [px, py] = orientation === "alt" ? [y, x] : [x, y];
    const k = scales(view, size);
    return { x: size.w / 2 + (px - view.cx) * k.x, y: size.h / 2 + (py - view.cy) * k.y };
  }

  /** Pixels per map percent on each axis. size.aspect (width/height of the map image, default 1) keeps the map's own ratio. */
  function scales(view, size) {
    const aspect = size.aspect > 0 ? size.aspect : 1;
    const fw = Math.min(size.w, size.h * aspect);
    return { x: fw / 100 * view.z, y: fw / aspect / 100 * view.z };
  }

  /**
   * Keeps the visible window inside the 0-100 map square so the map's edge is never dragged past the canvas edge.
   * Per axis: if the whole map fits, it is centred; otherwise the centre is limited to [half, 100-half]. Mutates and returns view.
   */
  function clampView(view, size) {
    const k = scales(view, size);
    const fit = (c, px, perPct) => {
      const half = px / perPct / 2;
      return half >= 50 ? 50 : Math.min(100 - half, Math.max(half, c));
    };
    view.cx = fit(view.cx, size.w, k.x);
    view.cy = fit(view.cy, size.h, k.y);
    return view;
  }

  /** Inverse of project for drag panning: pixel delta to percent delta. */
  function pixelsToPercent(view, dx, dy, size) {
    const k = scales(view, size);
    return { x: dx / k.x, y: dy / k.y };
  }

  function isStale(c, now) { return now - c.lastSeen > STALE_MS; }

  const HIT_MIN = 8;
  /** Click radius for a dot: follows the configured size, never below HIT_MIN, so big and small dots are equally easy to hit. */
  function hitRadius(ap) { return Math.max(HIT_MIN, ap.dot_size + 4); }

  const DASH = { solid: [], dashed: [6, 4], dotted: [1.5, 4] };

  /** Name labels for "always": greedy, skips a label that would overlap one already placed. Returns what was drawn. */
  function drawLabels(ctx, items, colors) {
    ctx.save();
    ctx.font = "11px system-ui, sans-serif"; ctx.textBaseline = "middle";
    const placed = [];
    for (const it of items) {
      const w = ctx.measureText(it.name).width, box = { x: it.x + it.r + 4, y: it.y - 7, w, h: 14 };
      if (placed.some((b) => box.x < b.x + b.w && b.x < box.x + box.w && box.y < b.y + b.h && b.y < box.y + box.h)) continue;
      placed.push(box);
      ctx.lineWidth = 3; ctx.strokeStyle = colors.background; ctx.strokeText(it.name, box.x, it.y);
      ctx.fillStyle = colors.selection; ctx.fillText(it.name, box.x, it.y);
    }
    ctx.restore();
    return placed.length;
  }

  function drawGrid(ctx, s, size) {
    const a = project(s.view, 0, 0, size, s.orientation), b = project(s.view, 100, 100, size, s.orientation);
    const x0 = Math.min(a.x, b.x), x1 = Math.max(a.x, b.x), y0 = Math.min(a.y, b.y), y1 = Math.max(a.y, b.y);
    ctx.save(); ctx.strokeStyle = s.colors.selection; ctx.globalAlpha = 0.18; ctx.lineWidth = 1; ctx.beginPath();
    for (let i = 0; i <= 10; i++) {
      const x = x0 + (x1 - x0) * i / 10, y = y0 + (y1 - y0) * i / 10;
      ctx.moveTo(x, y0); ctx.lineTo(x, y1); ctx.moveTo(x0, y); ctx.lineTo(x1, y);
    }
    ctx.stroke(); ctx.restore();
  }

  function drawRings(ctx, colors, size) {
    ctx.save(); ctx.strokeStyle = colors.stale; ctx.globalAlpha = 0.5; ctx.lineWidth = 1;
    for (const k of [0.25, 0.5, 0.75, 1]) { ctx.beginPath(); ctx.arc(size.w / 2, size.h / 2, Math.min(size.w, size.h) / 2 * k, 0, Math.PI * 2); ctx.stroke(); }
    ctx.restore();
  }

  function arrowhead(ctx, from, to, len) {
    const ang = Math.atan2(to.y - from.y, to.x - from.x);
    ctx.beginPath();
    ctx.moveTo(to.x, to.y);
    ctx.lineTo(to.x - len * Math.cos(ang - 0.45), to.y - len * Math.sin(ang - 0.45));
    ctx.lineTo(to.x - len * Math.cos(ang + 0.45), to.y - len * Math.sin(ang + 0.45));
    ctx.closePath(); ctx.fill();
  }

  /**
   * One trail: for each run a dashed/dotted/solid line that fades from old to new, an arrowhead on the newest segment,
   * and a dot per fix (filled when exact, hollow when approximate). Returns hit targets for the fix dots.
   */
  function drawTrail(ctx, t, s, size, ap, hits) {
    const o = s.orientation, gap = ap.gap_minutes * 60000;
    const runs = A.tracks.withLive(A.tracks.segments(t.fixes, t.mapId, gap), t.contact && t.contact.mapId === t.mapId ? t.contact : null, gap);
    const color = t.color;
    for (const run of runs) {
      const pts = run.map((f) => Object.assign(project(s.view, f.x, f.y, size, o), { f }));
      ctx.save();
      ctx.strokeStyle = color; ctx.fillStyle = color; ctx.lineWidth = ap.trail_width; ctx.lineCap = "round"; ctx.setLineDash(DASH[ap.trail_style] || []);
      for (let i = 1; i < pts.length; i++) {
        ctx.globalAlpha = ap.trail_fade ? 0.25 + 0.75 * (i / (pts.length - 1)) : 0.9;
        ctx.beginPath(); ctx.moveTo(pts[i - 1].x, pts[i - 1].y); ctx.lineTo(pts[i].x, pts[i].y); ctx.stroke();
      }
      ctx.setLineDash([]); ctx.globalAlpha = 1;
      if (ap.trail_arrow && pts.length > 1) {
        const a = pts[pts.length - 2], b = pts[pts.length - 1];
        if (Math.hypot(b.x - a.x, b.y - a.y) > 8) arrowhead(ctx, a, b, 6 + ap.trail_width);
      }
      pts.forEach((p, i) => {
        if (p.f.live) return; // the live position is drawn as the player's own dot
        ctx.globalAlpha = ap.trail_fade && pts.length > 1 ? 0.35 + 0.65 * (i / (pts.length - 1)) : 0.85;
        ctx.beginPath(); ctx.arc(p.x, p.y, ap.trail_dot, 0, Math.PI * 2);
        if (p.f.kind === "approx") { ctx.lineWidth = 1.5; ctx.stroke(); } else ctx.fill();
        hits.push({ x: p.x, y: p.y, r: Math.max(6, ap.trail_dot + 3), fix: p.f, name: t.name, guid: t.guid });
      });
      ctx.restore();
    }
  }

  /**
   * Draw one frame and return hit targets.
   * s: { view, orientation, image, contacts, trails:[{guid,name,mapId,fixes,contact,color}], now, colors, ap,
   *      selected, tracked:Set, trackColors:Map }
   */
  function draw(ctx, s, size) {
    const ap = s.ap, hits = [];
    ctx.save();
    ctx.clearRect(0, 0, size.w, size.h);
    ctx.fillStyle = s.colors.background; ctx.fillRect(0, 0, size.w, size.h);
    if (ap.background === "rings") drawRings(ctx, s.colors, size);
    else if (s.image && ap.background === "map") {
      const a = project(s.view, 0, 0, size, s.orientation), b = project(s.view, 100, 100, size, s.orientation);
      ctx.save(); ctx.globalAlpha = ap.map_opacity / 100;
      ctx.drawImage(s.image, Math.min(a.x, b.x), Math.min(a.y, b.y), Math.abs(b.x - a.x), Math.abs(b.y - a.y));
      ctx.restore();
    }
    if (ap.grid === "on" && ap.background !== "rings") drawGrid(ctx, s, size);
    for (const t of s.trails || []) drawTrail(ctx, t, s, size, ap, hits);
    const hasTracked = s.tracked && s.tracked.size > 0, labels = [], R = hitRadius(ap);
    // Dimmed dots first so tracked and selected ones are never hidden beneath them.
    const order = s.contacts.slice().sort((a, b) => (hasTracked ? Number(s.tracked.has(a.guid)) - Number(s.tracked.has(b.guid)) : 0));
    for (const c of order) {
      const p = project(s.view, c.x, c.y, size, s.orientation);
      const st = A.style.pipStyle(c, ap, s.colors, s.now, { selected: c.guid === s.selected, tracked: hasTracked && s.tracked.has(c.guid), hasTracked,
        trackColor: s.trackColors && s.trackColors.get(c.guid) });
      A.style.drawPip(ctx, st, p.x, p.y);
      hits.push({ x: p.x, y: p.y, r: R, contact: c });
      if (ap.labels === "always" && st.alpha > 0.5) labels.push({ name: c.name, x: p.x, y: p.y, r: st.r });
    }
    if (labels.length) drawLabels(ctx, labels, s.colors);
    ctx.restore();
    return hits;
  }

  /** Pick the clicked blip (nearest within its radius). */
  function hitTest(hits, x, y) {
    let best = null, bd = Infinity;
    for (const h of hits) {
      const d = Math.hypot(h.x - x, h.y - y);
      if (d <= h.r && d < bd) { best = h; bd = d; }
    }
    return best && best.contact;
  }

  /** Like hitTest but returns the whole hit ({contact} for a live dot, {fix,name} for a historical one). Live dots win ties. */
  function hitTarget(hits, x, y) {
    let best = null, bd = Infinity;
    for (const h of hits) {
      const d = Math.hypot(h.x - x, h.y - y) - (h.contact ? 2 : 0);
      if (d <= h.r && d < bd) { best = h; bd = d; }
    }
    return best;
  }

  window.__app.radar = { project, clampView, pixelsToPercent, draw, hitTest, hitTarget, hitRadius, isStale, STALE_MS };
})();
