"use strict";
(function () {
  const ORDER = ["hostile", "neutral", "friendly"];

  /** Group by reaction (hostile first) and sort each group by most recently seen. Pure. */
  function group(contacts) {
    const out = [];
    for (const r of ORDER) {
      out.push(...contacts.filter((c) => c.reaction === r).sort((a, b) => b.lastSeen - a.lastSeen || a.name.localeCompare(b.name)));
    }
    return out;
  }

  function age(ms) {
    const s = Math.max(0, Math.round(ms / 1000));
    if (s < 60) return s + "s";
    const m = Math.floor(s / 60);
    return m < 60 ? m + "m" : Math.floor(m / 60) + "h" + (m % 60) + "m";
  }

  /** The map to show in Auto mode: the most recent hostile contact's map, else the most recent contact's. */
  function autoMap(contacts) {
    const pick = (list) => list.reduce((b, c) => (!b || c.lastSeen > b.lastSeen ? c : b), null);
    const c = pick(contacts.filter((x) => x.reaction === "hostile")) || pick(contacts);
    return c ? c.mapId : 0;
  }

  /**
   * Map picker rows: maps with logged contacts first (most contacts first), then empty maps by name. Pure.
   * @returns {{mapId:number, zone:string, count:number, active:boolean}[]}
   */
  function mapOptions(charts, contacts, zoneOf) {
    const counts = new Map();
    for (const c of contacts) counts.set(c.mapId, (counts.get(c.mapId) || 0) + 1);
    const have = new Set(charts.map((ch) => ch.mapId));
    const extra = zoneOf ? [...counts.keys()].filter((id) => !have.has(id) && zoneOf(id)).map((id) => ({ mapId: id, zone: zoneOf(id) })) : [];
    const rows = charts.concat(extra).map((ch) => ({ mapId: ch.mapId, zone: ch.zone, count: counts.get(ch.mapId) || 0, active: counts.has(ch.mapId) }));
    return rows.sort((a, b) => b.active - a.active || b.count - a.count || a.zone.localeCompare(b.zone));
  }

  window.__app = window.__app || {};
  /**
   * Tooltip lines for a live contact or a historical fix. Pure.
   * hit: {contact} or {fix,name}; zoneOf(mapId) -> zone name; now: epoch ms.
   */
  function tipLines(hit, zoneOf, now, o) {
    o = o || {};
    const staleMs = (o.staleMinutes || 5) * 60000;
    const when = (ms) => (o.timeZone ? fmtTime(ms, o.timeZone) : "");
    const kind = (k) => (k === "approx" ? "approximate position" : "exact position");
    const xy = (x, y) => x.toFixed(1) + ", " + y.toFixed(1);
    if (hit.contact) {
      const c = hit.contact;
      const stale = now - c.lastSeen > staleMs;
      return [c.name, c.reaction[0].toUpperCase() + c.reaction.slice(1) + (stale ? " (stale)" : ""), zoneOf(c.mapId) + " " + xy(c.x, c.y),
        "Seen " + age(now - c.lastSeen) + " ago" + (when(c.lastSeen) ? " (" + when(c.lastSeen) + ")" : ""), kind(c.kind)];
    }
    const f = hit.fix;
    return [hit.name || "Past position", "Earlier position", zoneOf(f.mapId) + " " + xy(f.x, f.y), age(now - f.t) + " ago" + (when(f.t) ? " (" + when(f.t) + ")" : ""), kind(f.kind)];
  }

  /**
   * Which contacts the user wants to see. f: {friendly, hostile, neutral: bool, onlyTracked: bool}, tracked: Set of guids.
   * neutralAsHostile folds neutral into the hostile switch. Pure.
   */
  function applyFilters(contacts, f, tracked, neutralAsHostile) {
    return contacts.filter((c) => {
      const r = c.reaction === "neutral" && neutralAsHostile ? "hostile" : c.reaction;
      if (f[r] === false) return false;
      return !(f.onlyTracked && tracked && tracked.size && !tracked.has(c.guid));
    });
  }

  /** Format epoch ms in "local", "UTC" or an IANA zone. Falls back to UTC for a zone the runtime does not know. */
  function fmtTime(ms, zone) {
    const opt = { year: "numeric", month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false };
    if (zone && zone !== "local") opt.timeZone = zone;
    try { return new Intl.DateTimeFormat(undefined, opt).format(new Date(ms)); }
    catch (e) { return new Intl.DateTimeFormat(undefined, Object.assign(opt, { timeZone: "UTC" })).format(new Date(ms)); }
  }
  function fmtClock(ms, zone) {
    const opt = { hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false };
    if (zone && zone !== "local") opt.timeZone = zone;
    try { return new Intl.DateTimeFormat(undefined, opt).format(new Date(ms)); }
    catch (e) { return new Intl.DateTimeFormat(undefined, Object.assign(opt, { timeZone: "UTC" })).format(new Date(ms)); }
  }

  window.__app.list = { group, age, autoMap, mapOptions, tipLines, applyFilters, fmtTime, fmtClock };
})();
