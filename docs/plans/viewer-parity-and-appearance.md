# Plan: viewer parity with radar-live, line/dot fixes, Appearance settings

Status: P0-P4, P6 and most of P5 built in code and unit-tested (2026-11-18); NOT yet seen in a real window (the Chrome
extension was not connected, and the WebView PoCs from T12/T14 are still open). P6 and P7 are not done. See section 7.
Reference implementation to port from: `radar-live/site/js/` (design in `radar-live/site/ARCHITECTURE.md`,
stages 11 and 14 in `radar-live/docs/plans/`).

PLAN.md non-goals still hold: no web viewer, no heatmap, no ads, no accounts. Armory is a separate decision (Q3).

## 0. Open questions (answer before P3 and P6)

| # | Question | Recommendation |
|---|---|---|
| Q1 | "Fix the lines and dots": the defects in section 1 are what the code shows. Is there a visible symptom beyond them (wrong positions, missing dots, jitter)? | Fix section 1 first, then re-check against a real window. |
| Q2 | Does `crimson-api` have what the roster sweep needs for the world map (counts across all zones, not just the viewed one)? | Check `internal/apiserver` read endpoints and `apiclient.SweepParams` in P0. If not, add a `scope=all` read (T07 follow-up) instead of counting client-side. |
| Q3 | Armory (radar-live has `/api/v1/armory/*`; `crimson-api` has none). | Out of scope here. Separate plan, only if wanted. |
| Q4 | Which appearance options ship in v1? | The list in section 4; the first five rows are the minimum. |

## 1. Defects found in the current lines and dots (`viewer/web/radar.js`, `app.js`)

Found by reading the code, not by running it. Each gets a failing test first (`radar.test.mjs`).

| ID | Where | Problem | Fix |
|---|---|---|---|
| L1 | `drawTrail` | One polyline through every fix of the selected map. A player who left and came back, or whose fixes have gaps, gets a straight line across the map. Fixes are not sorted by time before drawing. | Sort by time; break the line when the gap exceeds a threshold (setting) or the map changes. |
| L2 | `drawTrail` | No direction, no age. Solid 2 px line in the accent colour, which is also the button colour. | Dashed line, fading older to newer, arrowhead on the newest segment. Own `trail` colour. |
| L3 | `draw` (trail dots) | Historical dots are all 3 px, one colour, one alpha. `approx` fixes (`kind:"approx"`) look the same as exact ones. | Exact = filled, approx = hollow ring (as radar-live `paintTrail`). |
| L4 | `draw` | The line is never joined to the live dot, so the path ends short of where the player is. | Append the live position as the last vertex. |
| L5 | `app.js` `$("history")` | The "Trail history" checkbox is only saved by `persist()`. Nothing in the drawing code reads it. It is a dead control. | Wire it to show or hide the trail line and dots, or remove it once P2 replaces it with the Appearance setting. |
| D1 | `shape` | The diamond is drawn at radius `r+1`, so it looks larger than the circle and square. Size is a magic 5 or 7. Stale dots only drop to 0.4 alpha. | Equal-area shapes; sizes from settings; stale styling from settings. |
| D2 | `draw` | `globalAlpha` is set per dot and reset only at the end. An early return or exception leaves it set. | `ctx.save()/restore()` per layer. |
| D3 | `draw` | Selected dot gets only a bigger size. With a selection, every other contact is hidden instead of dimmed. | Selection ring plus dimmed (not hidden) others, as radar-live does (`UNTRACKED_ALPHA`). |
| D4 | `hitTest` / `hitTarget` | Hit radius is fixed (9 px, trail 6 px) whatever the dot size or DPR. | Derive from the configured dot size, with a minimum of 8 px. |
| D5 | `app.css` | `.shape.*` list swatches are CSS and the canvas shapes are JS, so changing a style in one leaves the other behind. | One `style.js` that returns shape, size and colour for a contact. Canvas and list both use it. |
| D6 | `readColors` | Reads CSS variables once at start. A palette or theme change needs a reload. | Re-read on settings change and on `prefers-color-scheme` change. |

## 2. Phases

Every phase: tests first (Go `_test.go` and `*.test.mjs`), `make check` green, no new external requests, strict CSP kept.
Each phase is one commit and can ship alone.

### P0. Groundwork (no visible change)
- Split `app.js` (319 lines) into modules the way radar-live does: a small `core` (state, bus, mounts) and one file per feature. Keep the plain `window.__app` script style (no bundler, per PLAN D5).
- Add `viewer/web/style.js`: pure `pipStyle(contact, settings, now)` returning `{shape, r, fill, stroke, alpha, ring}`. Both canvas and list swatches use it (fixes D5).
- Confirm Q2.
- Tests: `pipStyle` table test, one row per reaction and stale/approx/selected.

### P1. Line and dot fixes (section 1)
- L1-L5, D1-D4 in `radar.js`, driven by a `settings.appearance` object with built-in defaults, so this phase needs no new UI.
- Tests: trail gap breaking, sorting, live-dot joining, hollow approx dots, hit radius, selected/dimmed.
- Acceptance: a recorded trail fixture renders the same in a headless canvas snapshot test (hash of draw calls via a recording ctx, not pixels).

### P2. Appearance settings (section 4)
- Settings page becomes tabbed: **Connection** (what exists now), **Display**, **Appearance**, **About**.
- Go: add `Appearance` to `config.ViewerConfig` (`json:"appearance"`), validate every field (enum, range, colour regex `#rrggbb`), unknown keys rejected, `Version` bumped with a migration that fills defaults. File stays 0600.
- Bound functions: `loadAppearance`, `saveAppearance`, `resetAppearance`. Applied live (no restart) through a bus event; canvas redraws.
- Move out of the header into Display: Realm (ruleset), Trail history, orientation (never in the header today, only saved). The header keeps Map, a ruleset indicator and Settings.
- Tests: Go config round-trip, validation rejects bad input, migration from v1 files; JS: preview swatches, live apply, reset.

### P3. Multi-player tracking (radar-live stage 11)
- State: `tracked` ordered map, cap 8, oldest evicted. `selected` becomes the focused tracked player (back-compat getter).
- Search box gets chips, one per tracked player, coloured by `trackColor(guid)` (FNV-1a mod 8, colour-blind-safe palette; same hash as radar-live so colours match between the two UIs).
- List: tracked rows first with a remove button, others dimmed, "Show only tracked" checkbox. Replaces the single "Show all" button.
- Data: two streams per poll, as in radar-live: the roster sweep (unchanged) and one `getTrail` per tracked guid. Go side: `Trail` bound function takes a list of guids, runs them with bounded concurrency (max 4), and shares the ETag cache. Respect the API rate limit; stagger by poll.
- Hover tooltips on live dots and trail points (port `tooltip.js` `tooltipModel`, `hitTestScene`).
- Tests: eviction at 9, colour stability, trail fetch count per poll, hit-test ties.

### P4. Group filter and time zone
- Friendly / Hostile (and Neutral, which radar-live merges into hostile; make this an Appearance option) toggles in the Display tab and as header chips.
- Time zone selector (browser local, UTC, or an IANA name) for the "last seen" times in lists and tooltips. Stored in `ViewerConfig.TimeZone`.
- Tests: filter combinations, DST edge for the formatter.

### P5. World map (radar-live stage 14)
- Replace `world.js` (hard-coded anchor table, pies) with continent maps and zone rectangles: bundle `maps/world/<Continent>.webp` and `index.json` in the viewer binary via `viewer/internal/maps` (same `data:` URI path as zone maps).
- One canvas per continent with its own pan/zoom, friendly/hostile chips sized by sqrt(count), hover tooltip, click a chip to open that zone with the filter applied, and each tracked player's cross-zone path (port `worldpath.js`, `worldcounts.js`).
- Needs Q2 answered (all-zone roster).
- Tests: port radar-live's `worldpath` and `worldcounts` tests as-is; map bundle test checks every zone in `index.json` has a file.

### P6. Remaining radar-live panels
- Status panel (version, per-ruleset counts, uptime, last poll age) from `GET /v1/status` if `crimson-api` has it, else from poller state only.
- History panel (what radar-live's `history-panel.js` shows for the focused player).
- Bounty tab gets per-player drill-down (`/bounty/player`) if the API has it.
- Anything in radar-live not listed here is deliberately skipped; record that in section 5 when decided.

### P7. Docs, packaging, real-window check
- `INSTALL.md`/`OPERATIONS.md`: new settings tabs and config fields. PLAN.md: status line and the viewer section of the layout.
- Real-window runs on Linux and Windows (the open T12/T14 PoCs): screenshots for every palette in both light and dark themes.

## 3. Feature parity table (target)

| Feature | radar-live | crimson-ledger today | Phase |
|---|---|---|---|
| Multi-player tracking, 8 colours | yes | no (one) | P3 |
| Trail tooltips | yes | partial | P3 |
| Friendly/Hostile filter | yes | no | P4 |
| Time zone | yes | no | P4 |
| Continent world map, chips, paths | yes | simple pies | P5 |
| Status, history panels | yes | no | P6 |
| Armory | yes | no | Q3 (out) |
| Appearance settings | no | no | P2 (new, beyond radar-live) |

## 4. Appearance tab: options

Stored in `ViewerConfig.Appearance`. Every option has a default equal to today's look, a validator and a live preview.

| Group | Option | Values |
|---|---|---|
| Theme | Colour scheme | System / Light / Dark |
| Palette | Preset | Default (today's), Colour-blind safe (Okabe-Ito based), High contrast, Mono (shape-only), Custom |
| Palette | Custom colours | Friendly, Hostile, Neutral, Stale, Trail, Selection, Background: `#rrggbb` pickers, each with a contrast ratio readout against the map background (warn under 3:1) |
| Palette | Track colours (1-8) | Preset or custom; reset button |
| Dots | Shape per reaction | circle / diamond / square / triangle / cross for friendly, hostile, neutral |
| Dots | Size | 3-12 px; selected-ring size |
| Dots | Stale look | dim / hollow / grey; stale after N minutes (default 5) |
| Dots | Approximate fixes | filled / hollow / dashed ring |
| Dots | Outline | none / thin / thick, outline colour |
| Dots | Labels | none / name on hover / name always (collision-avoided) |
| Lines | Trail style | solid / dashed / dotted; width 1-4 px; fade with age; arrowhead on/off |
| Lines | Trail dot size | 1-6 px |
| Lines | Gap break | break the line after N minutes without a fix (default 15) |
| Map | Grid | off / 10% grid; grid opacity |
| Map | Background | map image only / dark panel / radar rings (radar-live's no-map mode) |
| Map | Map opacity | 30-100% (to make dots stand out) |
| Map | Orientation | standard / axes swapped (moved from `ViewerConfig.Orientation`; keep the old field as the source until migrated) |
| List | Density | compact / comfortable; show shape swatch on/off |
| Accessibility | Shape-only mode | forces distinct shapes whatever the palette |
| Accessibility | Reduce motion | no fades or animation |
| Accessibility | UI scale | 90-150% |

Presets and validators live in one Go file (`internal/config/appearance.go`) and one JS file (`viewer/web/style.js`);
a test asserts their preset tables are identical so they cannot drift.

## 5. Risks

- **API load.** Up to 8 trail calls per poll. Mitigation in P3: concurrency 4, ETag cache, only the focused player refreshes every poll, others every 5th.
- **Config compatibility.** Old `viewer.json` must still load. Migration test per version.
- **Scope.** P3 to P6 are each about the size of a radar-live stage. Ship P0 to P2 first; they answer the line/dot and settings requests on their own.
- **Real-window verification** is still open for T12/T14, so visual claims are unverified until P7.

## 6. Order

P0, then P1, then P2 (the requested fixes and settings), then P3 to P6 in order, each releasable. P7 closes out.

## 7. Execution log (2026-11-18)

| Phase | State | Notes / deviations from the plan |
|---|---|---|
| Q2 | Resolved | The sweep already returns every zone (no `Zone` set), so world counts need no API change. |
| P0 | Done, partial | New `style.js` (shared dot style, palette, track colour, contrast) and `tracks.js` (tracked set, trail runs, world path, limiter). `app.js` is rewritten but is still one file, not one file per feature. |
| P1 | Done | L1-L5, D1-D6 fixed in `radar.js`/`style.js` with tests (`look.test.mjs`). L5: the dead "Trail history" header checkbox is removed; "Show trails" (`appearance.show_trails`, key H) replaces it. `show_history` stays in viewer.json, now mirroring it. |
| P2 | Done | Settings tabs Connection / Display / Appearance / About. `config.Appearance` (all fields validated, zero value = defaults, so no version bump and old files load), `getAppearance` / `saveAppearance` / `resetAppearance` bindings. Presets exist only in Go (`config.Presets`) and reach the page through `getAppearance`; a test forbids copies in JS (better than the planned drift test). Realm and orientation moved to Display; the header keeps a realm badge. |
| P3 | Done | Up to 8 tracked players, chips, per-guid trail fetch (max 4 in flight, focused player every poll, others every 5th), dimmed (not hidden) untracked dots, "Show only tracked", `x` to remove, Escape clears. |
| P4 | Done | Friendly/Hostile/Neutral header toggles, time zone selector (`time_zone` in viewer.json) used for the clock and tooltips. |
| P5 | Partial | Only one world image exists locally (`maps/world/World.png`, Azeroth); no continent set to bundle. Done: filters apply to the world pies, tracked players' cross-zone paths with arrowheads. Not done: zone outline rectangles and per-continent canvases (need `extract_maps.py --world` on a game install). |
| P6 | Done (2026-11-18) | `panels.js` (pure `statusModel`, `historyModel`, 5 tests) and two new side tabs, History and Status. `crimson-api` has no `/status` and no `/bounty/player` (checked `api/openapi.yaml`), so Status uses the poller state the page already holds (connection, last update, polls, contacts per group and map) and History uses the focused player's trail (`total`, `maps`, fixes). Bounty drill-down is skipped until the API has the route. |
| P7 | Partial | INSTALL.md has a "Using the viewer" section. Still open: real-window runs and palette screenshots (headless Firefox hung here and no WebKitGTK window was available), and the PLAN.md status line. |

Verified: `go vet` (linux, windows, darwin), `go test -race` for `viewer/...` and `internal/config`, `node --test` (43 viewer tests, 9 page tests).
Not verified: the page in a real window or browser; `scripts/check.sh` (staticcheck, golangci-lint, govulncheck are not installed here).

### UI tests (2026-11-18)
`viewer/web/ui.test.mjs` boots the real page scripts and `index.html` in a dependency-free fake DOM (`domfake.mjs`) and drives every control: connect/first-run, status dot and banner, list, map picker, the three group chips, search and tracking (chips, x, Escape, only-tracked), the four side tabs with their panels, canvas click/hover/drag/wheel, H and Escape keys, visibility, and all settings tabs, Save, Test connection, Back, key reveal, Display controls, every Appearance control and Reset (43 tests). `getAppearance` comes from `testdata/appearance.json`, which a Go test keeps equal to the real answer. `TestJSSuites` runs all `*.test.mjs` from `go test`; they were not run by anything before. Found by writing them: the duplicate `const TABS` that broke every button. Still not covered: pixels (the fake canvas only records calls) and the real WebView.
