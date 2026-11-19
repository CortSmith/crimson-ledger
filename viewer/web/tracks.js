"use strict";
// Tracked players, trail preparation and the world-map path. All pure (no DOM, no canvas).
(function () {
  const CAP = 8;

  /** An ordered set of tracked players, oldest first, capped. add() returns the evicted entry or null. */
  function createTracked(cap) {
    const max = cap || CAP, m = new Map();
    return {
      add(guid, name) {
        if (m.has(guid)) m.delete(guid); // re-adding moves it to the focus position
        m.set(guid, { guid, name });
        if (m.size > max) { const oldest = m.keys().next().value; const e = m.get(oldest); m.delete(oldest); return e; }
        return null;
      },
      remove(guid) { return m.delete(guid); },
      clear() { m.clear(); },
      has(guid) { return m.has(guid); },
      get size() { return m.size; },
      list() { return [...m.values()]; },
      /** The most recently added or clicked player. */
      get focus() { const l = [...m.values()]; return l.length ? l[l.length - 1] : null; },
      focusOn(guid) { const e = m.get(guid); if (e) { m.delete(guid); m.set(guid, e); } },
    };
  }

  /**
   * Fixes of one map, oldest first, cut into runs wherever consecutive fixes are more than gapMs apart.
   * A single run is one continuous line; the radar never joins two runs (defect L1).
   */
  function segments(fixes, mapId, gapMs) {
    const f = fixes.filter((x) => x.mapId === mapId && Number.isFinite(x.x) && Number.isFinite(x.y)).sort((a, b) => a.t - b.t);
    const runs = [];
    let cur = null, prev = null;
    for (const p of f) {
      if (!cur || p.t - prev.t > gapMs) { cur = []; runs.push(cur); }
      cur.push(p); prev = p;
    }
    return runs;
  }

  /** Append the live position to the newest run when the player is on this map and newer than the last fix (defect L4). */
  function withLive(runs, c, gapMs) {
    if (!c) return runs;
    const live = { x: c.x, y: c.y, t: c.lastSeen, kind: c.kind, mapId: c.mapId, live: true };
    const last = runs.length ? runs[runs.length - 1] : null, tail = last && last[last.length - 1];
    if (!tail || live.t - tail.t > gapMs) return runs.concat([[live]]);
    if (live.t < tail.t || (live.x === tail.x && live.y === tail.y)) return runs;
    return runs.slice(0, -1).concat([last.concat([live])]);
  }

  /** Per-zone visits in time order: consecutive fixes on one map collapse to one visit. Used for the world-map path. */
  function worldPath(fixes) {
    const out = [];
    for (const f of fixes.slice().sort((a, b) => a.t - b.t)) {
      const last = out[out.length - 1];
      if (last && last.mapId === f.mapId) { last.last = f.t; last.count++; }
      else out.push({ mapId: f.mapId, first: f.t, last: f.t, count: 1 });
    }
    return out;
  }

  /** Runs async jobs with at most n in flight. Returns a function: schedule(job) -> Promise of the job's result. */
  function limiter(n) {
    let active = 0;
    const q = [];
    const next = () => {
      if (active >= n || !q.length) return;
      active++;
      const { job, res, rej } = q.shift();
      Promise.resolve().then(job).then(res, rej).finally(() => { active--; next(); });
    };
    return (job) => new Promise((res, rej) => { q.push({ job, res, rej }); next(); });
  }

  window.__app = window.__app || {};
  window.__app.tracks = { CAP, createTracked, segments, withLive, worldPath, limiter };
})();
