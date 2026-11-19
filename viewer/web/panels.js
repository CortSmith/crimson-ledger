"use strict";
// Models for the Status and History panels. Pure (no DOM): app.js renders what these return.
(function () {
  const RULESETS = { normal: "Normal", pvp: "PvP", rp: "RP", rppvp: "RP-PvP" };

  function ago(ms, now) { return window.__app.list.age(now - ms) + " ago"; }

  function span(ms) {
    const m = Math.max(0, Math.floor(ms / 60000));
    return m < 60 ? m + "m" : Math.floor(m / 60) + "h " + (m % 60) + "m";
  }

  function connection(state, failures) {
    switch (state) {
      case "ok": return "Connected";
      case "error": return "Server unreachable, retrying (" + failures + (failures === 1 ? " failure)" : " failures)");
      case "authFailed": return "Key rejected";
      default: return "Connecting";
    }
  }

  /**
   * What the Status tab shows, from poller state the page already holds (the API has no status endpoint).
   * @param {{state:string, ruleset:string, startedAt:number, lastOkAt:number, polls:number, failures:number,
   *          contacts:object[], now:number, zoneOf:(id:number)=>string}} s
   */
  function statusModel(s) {
    const counts = { friendly: 0, hostile: 0, neutral: 0, total: 0 };
    const byMap = new Map();
    for (const c of s.contacts || []) {
      counts.total++;
      counts[c && (c.reaction === "friendly" || c.reaction === "hostile") ? c.reaction : "neutral"]++;
      const id = c && Number.isFinite(c.mapId) ? c.mapId : 0;
      byMap.set(id, (byMap.get(id) || 0) + 1);
    }
    const maps = [...byMap].map(([mapId, count]) => ({ mapId, zone: s.zoneOf(mapId), count }))
      .sort((a, b) => b.count - a.count || a.zone.localeCompare(b.zone));
    const failures = s.failures || 0;
    return {
      connection: connection(s.state, failures),
      lastUpdate: s.lastOkAt ? ago(s.lastOkAt, s.now) : "none yet",
      uptime: span(s.now - s.startedAt),
      ruleset: RULESETS[s.ruleset] || s.ruleset || "",
      polls: s.polls || 0,
      counts, maps,
    };
  }

  /**
   * What the History tab shows for one trail ({name, fixes, total, maps}). Null when there is no trail yet.
   * Fixes are only the newest page the API returns, so `truncated` says when `total` is larger than what is shown.
   */
  function historyModel(trail, now, zoneOf, timeZone) {
    if (!trail) return null;
    const fixes = (trail.fixes || []).filter((f) => Number.isFinite(f.t)).sort((a, b) => a.t - b.t);
    const total = Math.max(trail.total || 0, fixes.length);
    let maps = (trail.maps || []).map((m) => ({ mapId: m.mapId, zone: m.zone || zoneOf(m.mapId), count: m.count }));
    if (!maps.length) {
      const by = new Map();
      for (const f of fixes) by.set(f.mapId, (by.get(f.mapId) || 0) + 1);
      maps = [...by].map(([mapId, count]) => ({ mapId, zone: zoneOf(mapId), count }));
    }
    const sum = maps.reduce((a, m) => a + m.count, 0) || 1;
    maps = maps.map((m) => ({ ...m, pct: Math.round((100 * m.count) / sum) })).sort((a, b) => b.count - a.count);
    const first = fixes[0], last = fixes[fixes.length - 1];
    return {
      name: trail.name || "",
      total, shown: fixes.length, truncated: fixes.length < total,
      approx: fixes.filter((f) => f.kind === "approx").length,
      first: first ? ago(first.t, now) : "none",
      last: last ? ago(last.t, now) : "none",
      firstAt: first ? window.__app.list.fmtTime(first.t, timeZone) : "",
      lastAt: last ? window.__app.list.fmtTime(last.t, timeZone) : "",
      last_pos: last ? { mapId: last.mapId, zone: zoneOf(last.mapId), x: last.x, y: last.y } : null,
      maps,
    };
  }

  window.__app = window.__app || {};
  window.__app.panels = { statusModel, historyModel };
})();
