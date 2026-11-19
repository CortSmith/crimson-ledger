"use strict";
(function () {
  // Zone anchor points on the 1002x668 world image: mapId -> [name, px, py]. Generated from maps/world/index.json.
  const W = 1002, H = 668, WORLD = -1;
  const ZONES = {"1411":["Durotar",292,322],"1412":["Mulgore",205,360],"1413":["The Barrens",250,345],"1416":["Alterac Mountains",790,205],"1417":["Arathi Highlands",812,248],"1418":["Badlands",838,395],"1419":["Blasted Lands",775,525],"1420":["Tirisfal Glades",700,185],"1421":["Silverpine Forest",697,245],"1422":["Western Plaguelands",752,172],"1423":["Eastern Plaguelands",842,125],"1424":["Hillsbrad Foothills",772,238],"1425":["The Hinterlands",845,185],"1426":["Dun Morogh",738,345],"1427":["Searing Gorge",805,375],"1428":["Burning Steppes",795,412],"1429":["Elwynn Forest",748,438],"1430":["Deadwind Pass",782,470],"1431":["Duskwood",748,472],"1432":["Loch Modan",825,348],"1433":["Redridge Mountains",795,440],"1434":["Stranglethorn Vale",735,525],"1435":["Swamp of Sorrows",815,480],"1436":["Westfall",712,482],"1437":["Wetlands",795,312],"1438":["Teldrassil",165,100],"1439":["Darkshore",212,185],"1440":["Ashenvale",240,245],"1441":["Thousand Needles",240,430],"1442":["Stonetalon Mountains",228,290],"1443":["Desolace",160,350],"1444":["Feralas",172,415],"1445":["Dustwallow Marsh",280,395],"1446":["Tanaris",262,492],"1447":["Azshara",325,280],"1448":["Felwood",275,200],"1449":["Un'Goro Crater",215,500],"1450":["Moonglade",335,232],"1451":["Silithus",165,505],"1452":["Winterspring",335,200],"1453":["Stormwind City",735,418],"1454":["Orgrimmar",277,308],"1455":["Ironforge",756,338],"1456":["Thunder Bluff",198,342],"1457":["Darnassus",150,112],"1458":["Undercity",716,205]};
  const ORDER = ["hostile", "neutral", "friendly"];

  /** Per-zone contact counts: Map<mapId,{hostile,neutral,friendly,total}>. Pure. */
  function counts(contacts) {
    const m = new Map();
    for (const c of contacts) {
      const e = m.get(c.mapId) || { hostile: 0, neutral: 0, friendly: 0, total: 0 };
      if (e[c.reaction] !== undefined) e[c.reaction]++;
      e.total++;
      m.set(c.mapId, e);
    }
    return m;
  }

  function radius(total) { return Math.min(22, 7 + 3 * Math.sqrt(total)); }

  /** Pie targets for every known zone that has contacts, in canvas pixels. project maps map-percent to pixels. Pure. */
  function pies(contacts, project) {
    const out = [];
    for (const [mapId, e] of counts(contacts)) {
      const z = ZONES[mapId];
      if (!z) continue;
      const p = project(z[1] / W * 100, z[2] / H * 100);
      out.push({ mapId, name: z[0], x: p.x, y: p.y, r: radius(e.total), counts: e });
    }
    return out;
  }

  /** Paint the pies; each slice is one reaction. */
  function draw(ctx, list, colors) {
    for (const p of list) {
      let a = -Math.PI / 2;
      for (const k of ORDER) {
        const n = p.counts[k];
        if (!n) continue;
        const end = a + (n / p.counts.total) * Math.PI * 2;
        ctx.beginPath();
        if (n < p.counts.total) ctx.moveTo(p.x, p.y);
        ctx.arc(p.x, p.y, p.r, a, end);
        ctx.closePath();
        ctx.fillStyle = colors[k]; ctx.fill();
        ctx.strokeStyle = "rgba(0,0,0,.45)"; ctx.lineWidth = 1; ctx.stroke();
        a = end;
      }
    }
  }

  /** The pie under (x,y), or null. */
  function hit(list, x, y) {
    let best = null, bd = Infinity;
    for (const p of list) {
      const d = Math.hypot(p.x - x, p.y - y);
      if (d <= p.r + 2 && d < bd) { best = p; bd = d; }
    }
    return best;
  }

  /**
   * One path per tracked player across zones: the player's visits (time order) placed at each zone's anchor point.
   * tracked: [{guid,name,color,fixes}]. project maps map-percent to pixels. Zones without an anchor are skipped. Pure.
   */
  function paths(tracked, project) {
    const out = [];
    for (const t of tracked) {
      const pts = [];
      for (const v of window.__app.tracks.worldPath(t.fixes)) {
        const z = ZONES[v.mapId];
        if (!z) continue;
        const p = project(z[1] / W * 100, z[2] / H * 100);
        if (pts.length && pts[pts.length - 1].mapId === v.mapId) continue;
        pts.push({ x: p.x, y: p.y, mapId: v.mapId, zone: z[0], count: v.count });
      }
      if (pts.length) out.push({ guid: t.guid, name: t.name, color: t.color, points: pts });
    }
    return out;
  }

  /** Paint the paths: a line per player that thickens toward the newest visit, an arrowhead at the end, a dot per visit. */
  function drawPaths(ctx, list, o) {
    for (const p of list) {
      ctx.save();
      ctx.strokeStyle = p.color; ctx.fillStyle = p.color; ctx.lineCap = "round";
      const n = p.points.length;
      for (let i = 1; i < n; i++) {
        ctx.lineWidth = 1.5 + 2.5 * (i / (n - 1));
        ctx.globalAlpha = o && o.fade ? 0.35 + 0.65 * (i / (n - 1)) : 0.9;
        ctx.beginPath(); ctx.moveTo(p.points[i - 1].x, p.points[i - 1].y); ctx.lineTo(p.points[i].x, p.points[i].y); ctx.stroke();
      }
      ctx.globalAlpha = 1;
      if (n > 1) {
        const a = p.points[n - 2], b = p.points[n - 1], ang = Math.atan2(b.y - a.y, b.x - a.x);
        ctx.beginPath(); ctx.moveTo(b.x, b.y);
        ctx.lineTo(b.x - 10 * Math.cos(ang - 0.45), b.y - 10 * Math.sin(ang - 0.45));
        ctx.lineTo(b.x - 10 * Math.cos(ang + 0.45), b.y - 10 * Math.sin(ang + 0.45));
        ctx.closePath(); ctx.fill();
      }
      for (const q of p.points) { ctx.beginPath(); ctx.arc(q.x, q.y, 3.5, 0, Math.PI * 2); ctx.fill(); }
      ctx.restore();
    }
  }

  window.__app = window.__app || {};
  window.__app.world = { WORLD, ZONES, counts, pies, draw, hit, paths, drawPaths };
})();
