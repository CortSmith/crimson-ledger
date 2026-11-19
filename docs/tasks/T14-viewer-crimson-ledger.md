# T14: Viewer ("crimson-ledger", the main app)

**Depends on:** T01, T11, T12 · **Blocks:** T15, T18 · **PLAN.md sections:** [1 goals](../PLAN.md#1-goals-and-non-goals), [2 D5](../PLAN.md#d5-native-ui-system-webview-with-embedded-pages-no-http-server-no-fallback), [4 config](../PLAN.md#4-config-crimson-ledger), [5.3 read shapes](../PLAN.md#53-read-bodies-shapes-only), [7 security (viewer)](../PLAN.md#7-security-baseline-applies-to-every-task-the-audit-phase-checks-it), [13 perf](../PLAN.md#13-performance-budgets-measured-in-t21-every-task-logs-its-benchmarks-against-these), [10 workflow](../PLAN.md#10-mandatory-workflow-for-every-task-tdd-with-proof-of-concept-first)

## Goal
The rebuilt wow-radar: a native window on Linux, Windows and macOS showing the radar, list, zone maps, player search, trails and bounty
ranking. **Every datum comes from the API.** No local parsing, no history files, no local server.

## Inputs (port from)
`go-radar/web/{index.html,app.js,style.css}` (canvas radar, map picker combobox, search, trail drawing, bounty panel). Port the *rendering* code; replace every
`fetch('/api/...')` with a bound Go call. Delete the SSE code and the local server dependencies.

## Outputs
```
viewer/web/index.html app.css app.js        embedded; radar.js (canvas), list.js, search.js, trail.js, bounty.js, mapPicker.js, settings.js (uses T12 keyfield)
viewer/maps/*.webp + index.json             bundled zone images (converted from /maps PNG by scripts/maps-to-webp.sh, T16 checks size)
viewer/internal/data/poller.go              sweep poller (interval, ETag, backoff, pause when window hidden)
viewer/internal/app/app.go                  bindings: getSettings, saveSettings, revealKey, testConnection, getSweep(), search(q), getTrail(guid,map), getBounty(), getCharts(), getMap(id)
viewer/internal/maps/maps.go                embed.FS reader, returns data: URI (cached, LRU 8)
viewer/cmd/crimson-ledger/main.go           launcher (T12)
viewer/cmd/crimson-ledger-ui/main.go        UI process
viewer/internal/**/_test.go ; viewer/web/test/*.test.mjs (Node test image only)
```

## Behaviour
* First run with no key/URL: opens the **Settings** view (same masked-key widget as the logger) with a clear "connect to a server" message; **Test connection** calls `/v1/health` then an authenticated `GET /v1/charts` and reports "server reachable / key accepted / key rejected".
* Radar view: header (map picker with "Auto (follow data)", ruleset selector, connection status dot, last-updated time), canvas radar (wheel zoom, drag pan, follow), list panel (friendly / hostile / neutral), search box, bounty tab, history toggle (`H`).
* Polling: `poll_interval_s` (default 15, min 5). Uses `If-None-Match`; 304 -> no redraw. Pauses when the window is hidden/minimised (`visibilitychange`); resumes with an immediate sweep. Backoff on errors (x2 up to 120 s). Staleness: blips older than 5 min dim, older than the sweep max-age vanish.
* "Auto" map = the zone with the most recent contact for the current user's chosen focus (`follow` = selected contact or most recent hostile); user can pin a map.
* Search: debounce 250 ms, min 2 chars, `GET /v1/players`; picking a result loads `GET /v1/players/{guid}/trail?map=` and draws the trail (only fixes on the viewed map; counts per other map in the panel).
* Click a name or blip -> copies `/target First Last` to the clipboard through a bound Go function (OS clipboard) so the page never needs clipboard permissions.
* Errors are shown in a non-blocking banner with the API's `code` and `requestId` (helps support) and never the key.
* Accessibility: all controls keyboard reachable; colour is never the only signal (shape: friendly circle, hostile diamond, neutral square).

## Step 0: PoC (`scripts/poc/t14/`)
1. **Radar renders from API JSON only**: load the ported `radar.js` in a headless page (Node + `jsdom` + `canvas`, or the Linux Xvfb WebView from T12) with a fixture `sweep.json` of 2 000 contacts; PASS = draws without any `fetch` call (stub throws) and < 16 ms per frame on this machine (budget 60 fps).
2. **WebP maps**: convert 3 sample PNGs with `cwebp -q 80`; record size reduction and visual diff (SSIM > 0.98 via `compare`). PASS prints sizes; decide quality. Verify data-URI delivery of a 400 KB image through `Bind` in the T12 webview takes < 100 ms.
3. **Poller logic with fake clock**: 304s, errors with backoff, pause/resume, interval change. PASS prints request timeline.
4. **XSS probe**: feed contacts with names `<img src=x onerror=alert(1)>`, `</script><script>...`, `javascript:` and a 24-rune RTL string; render list and canvas labels; PASS = text shown literally, no element created (assert `document.querySelectorAll('img').length === 0`).
5. **Clipboard binding** on Linux (`xclip`/`wl-copy` absence!): decide implementation: use the WebView's own clipboard bridge if available, else an OS call (`pbcopy`, `clip.exe`/`SetClipboardData`, `wl-copy`/`xclip`). If no clipboard tool exists on Linux, the UI shows the text selected with "press Ctrl+C". PASS prints which path each OS uses and the fallback message (this is **not** a library-missing fatal error; clipboard is a convenience).
6. **Memory**: 1 h simulated polling with 2 000 contacts changing; heap stays flat (< 10 % growth). PASS prints samples.
7. **Idle CPU** between polls < 1 % (canvas loop only redraws on data change/interaction). PASS prints value.

## Step 1: RED
- Go: `TestPoller_*` (ETag, 304, backoff, pause, jitter bounds, stop on 401 with state `AuthFailed`), `TestApp_Bindings_UseClientOnly` (fake `apiclient` interface; assert no filesystem history writes, no listeners), `TestMaps_DataURI_LRU`, `TestSettings_*` (shared T12 tests re-run for viewer config), `TestGetTrail_RejectsBadGUID` (client-side validation before the call), `TestNoLocalWebsite` (grep + deps), `TestNoPersistedData` (after a full run the config dir contains only `viewer.json`).
- JS (Node test image): `radar.test.mjs` (coordinate transform golden values from go-radar tests: orientation std/alt, zoom, pan), `list.test.mjs` (grouping/sorting), `xss.test.mjs`, `mapPicker.test.mjs` (filter rules, keyboard nav), `keyfield.test.mjs` (T12), `poll-ui.test.mjs`.
- Visual regression: `scripts/screenshot.sh` renders 4 fixture states in the Xvfb WebView and compares to committed PNGs with 1 % tolerance.

## Step 2: GREEN (key code)
```go
// poller.go: one goroutine, one timer, owns the ETag via apiclient.
func (p *Poller) Run(ctx context.Context) {
	wait := p.interval()
	for {
		if p.visible.Load() {
			res, err := p.client.Sweep(ctx, p.params())
			wait = p.next(err)                               // backoff on error, base interval otherwise
			p.publish(res, err)                              // only emits when !res.NotModified or state changed
		}
		if sleepCtx(ctx, wait) != nil { return }
	}
}
```
`publish` serialises one JSON string and calls `window.Eval("window.__app.onSweep(" + json + ")")` where `json` came from `encoding/json` (safe: `json.Marshal` escapes `<`, `>`, `&`, U+2028/9). No string built from API text is ever concatenated into JS.

## Step 3: REFACTOR checklist
- [ ] JS split by feature (no 3 000-line `app.js`); each module exports 1-3 functions; state in one `store.js` with explicit update functions.
- [ ] Rendering functions pure (`(state, ctx) => void`), unit tested without DOM where possible.
- [ ] All user-visible strings in `strings.js`; colours and sizes in CSS custom properties.
- [ ] Go: `app.go` is bindings only; logic lives in `poller.go`/`maps.go` with tests.
- [ ] Remove every trace of the old app (grep `history`, `SSE`, `EventSource`, `127.0.0.1`).

## Step 4: AUDIT
- check.sh green for `./viewer/...`; JS tests green in the test image; coverage Go >= 85%.
- Performance (PLAN 13): first paint after data < 300 ms, 2 000 blips at 60 fps (record frame times via `performance.now()` in a scripted run, p95 < 16 ms), idle CPU < 1 %, binary size recorded (maps dominate: target < 60 MB total), memory flat in the 1 h soak.
- Security: CSP exact (T12), `innerHTML` banned (`grep -n innerHTML viewer/web` empty; ESLint rule `no-unsanitized`), no `eval`/`Function`, API key only through reveal (canary test), clipboard text built from validated name characters only, window refuses to navigate, no `target=_blank` links (there are none), no external fonts/images.

## Done when
- [ ] With T08 running and a recorded ping loaded, the viewer shows the same contacts as the old go-radar did for the same log (manual comparison using a shared fixture).
- [ ] Missing-library case verified for `crimson-ledger` (T12 PoC 8).
- [ ] check.sh green; performance numbers logged.

## PoC log
_(paste)_

## Implementation log (2026-11-17)
Built: `viewer/internal/{data,maps,app,clip}`, `viewer/web` (radar.js, list.js, app.js, index.html, app.css), `viewer/cmd/*`, `scripts/maps-to-webp.sh`.
Go tests green with `-race` (poller backoff/304/auth-stop/pause, maps LRU + traversal, bindings, no persisted data, clipboard name filter); Node `radar.test.mjs` 10/10.
Coverage: data 90 %, maps 81 %, app 83 %, web 100 %, clip 53 % (OS tool exec). check.sh: gofmt, vet x3, staticcheck, golangci-lint clean; govulncheck fails on Go 1.25.0 stdlib (same known toolchain issue as PLAN header).

## Follow-ups
- F14-1 (done): maps bundled. `scripts/maps-to-webp.py` (Pillow, q80) turned 91.4 MB of PNG into 10.9 MB of WebP in `viewer/maps/`; a test checks every indexed zone has an image. Binary is about 20 MB. `maps-to-webp.sh` (cwebp) remains as an alternative. SSIM visual check not done.
- F14-2: real-window run and PoCs 1-7 (frame time, memory soak, idle CPU, XSS probe in a real page) still open, as for T12/T13. Visual regression screenshots not done.
- F14-3: `auto` ruleset in viewer.json is sent as `normal` (the API requires a concrete ruleset). The header has a ruleset selector. Decide whether the viewer should detect it.
- F14-4: orientation `alt` is implemented as an axis swap; no golden values from go-radar were ported. Verify against the old app.
- F14-5: map picker is a native `<select>` (keyboard accessible) not the filtering combobox; chart rectangles from the API are not used for placement yet.
- F14-6: Windows clipboard uses `clip.exe`, which may garble non-ASCII names; coverage gate 85 % not reached for app/maps/clip; ESLint `no-unsanitized` not set up (a Go/Node grep test stands in).
- Heatmap and old-history import are deliberately out of scope (PLAN 1).
