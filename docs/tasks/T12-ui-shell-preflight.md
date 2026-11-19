# T12: UI shell: window, CSP page, masked-key widget, preflight, launcher

**Depends on:** T01 · **Blocks:** T13, T14 · **PLAN.md sections:** [2 D5](../PLAN.md#d5-native-ui-system-webview-with-embedded-pages-no-http-server-no-fallback), [4 config](../PLAN.md#4-config-crimson-ledger), [7 security (viewer + logger)](../PLAN.md#7-security-baseline-applies-to-every-task-the-audit-phase-checks-it), [9 required libraries](../PLAN.md#9-required-system-libraries-end-users-and-builders), [10 workflow](../PLAN.md#10-mandatory-workflow-for-every-task-tdd-with-proof-of-concept-first)

## Goal
Everything both apps share for showing a native window: (1) preflight that detects missing system libraries and exits with a
clear message, (2) launcher that starts the UI binary only when preflight passes, (3) a `Window` abstraction with real
WebView implementations per OS and a fake for tests, (4) a page builder that inlines assets with a strict CSP and **never** starts a
server, (5) the reusable **settings form with masked API key + eye button**.

## Outputs
```
internal/ui/preflight/preflight.go          Check(app string) []Missing ; Report(w, missing)  (pure Go, CGO off)
internal/ui/preflight/linux.go              dlopen via purego
internal/ui/preflight/windows.go            WebView2 registry check + MessageBoxW
internal/ui/preflight/darwin.go             macOS version check + osascript dialog
internal/ui/launcher/launcher.go            Run(app string, uiBinary string) int
internal/ui/window/window.go                Window interface, Options
internal/ui/window/fake.go                  FakeWindow for tests
internal/ui/window/webview_unix.go          //go:build webview && !windows   (webview_go)
internal/ui/window/webview_windows.go       //go:build webview && windows    (go-webview2)
internal/ui/window/stub.go                  //go:build !webview   returns ErrBuiltWithoutWebview
internal/ui/page/page.go                    Build(Assets{HTML,CSS,JS}) (html string, nonce string)
internal/ui/page/keyfield.js, keyfield.css  shared masked-key component (embedded)
internal/ui/settings/settings.go            Bindings shared by both apps: LoadSettings, SaveSettings, RevealKey, TestConnection
```

## Interfaces
```go
type Missing struct{ Name, Why, Install string } // Install = per-distro/OS instruction

type Window interface {
	SetTitle(string); SetSize(w, h int)
	Bind(name string, fn any) error          // exposes a Go func to the page (promise-returning)
	SetHTML(html string) error               // no URL navigation, ever
	Eval(js string)                          // used only to push pre-serialised JSON
	Run(); Destroy()
}

// Launcher: exit codes
const ( ExitOK = 0; ExitMissingLibs = 78; ExitBuiltWithoutWebview = 70; ExitStartFailed = 1 )
```
Linux checks (any of the alternatives satisfies): `libwebkit2gtk-4.1.so.0` (or `libwebkit2gtk-4.0.so.37` if the webview_go PoC proves 4.0 is what it links), `libgtk-3.so.0`.
Windows: WebView2 runtime version `pv` under `HKLM\SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}` or `HKCU\...\{same}`; non-empty and not `0.0.0.0`.
macOS: `sw_vers -productVersion` >= 11.0.

Message format (stderr + dialog), exact text so tests can assert it:
```
Crimson Ledger cannot start: missing required libraries.

  - libwebkit2gtk-4.1.so.0  (web view engine)
      Debian/Ubuntu: sudo apt install libwebkit2gtk-4.1-0
      Fedora:        sudo dnf install webkit2gtk4.1
      Arch:          sudo pacman -S webkit2gtk-4.1
  - libgtk-3.so.0  (window toolkit)
      ...
Install the libraries above and start Crimson Ledger again. (It does not fall back to a browser or a local web page.)
```
Distro detection reads `/etc/os-release` `ID`/`ID_LIKE` to show the matching line first, still listing the others.

## The masked key field (shared component)
Behaviour (tests in Step 1 are written from this list):
* Initial state: input is `type="password"`, value is the **fixed placeholder** `••••••••••••` (from `secret.Mask()`), `data-has-key="true|false"`. The real key is **not** in the DOM or JS heap at this point.
* Eye button (`<button aria-pressed>` with an inline SVG eye, `aria-label="Show API key"`): click calls bound `revealKey()` (Go), which returns the key; the field switches to `type="text"` showing it; icon changes to eye-off; button label "Hide API key".
* Auto re-mask after **15 s**, on blur of the window, on Save, and on Escape (timer/blur only hide, never discard, text the user has typed; see docs/plans/keyfield-alt-tab.md): JS overwrites the value with the placeholder, sets `type="password"` and drops its reference.
* Editing: if the user types/pastes a new key (field focused while masked clears the placeholder first), Save sends it to Go via `saveSettings({apiKey})`; if unchanged placeholder, Go keeps the stored key. Go validates the key format (`secret`/config) and returns field errors.
* Copy: no copy button (avoids clipboard exposure); text selection works only while revealed.
* Other fields shown **always visible** as plain inputs and saved to the config file: server URL, source name, ruleset, interval, CA file, logs dir (logger). A line under the form shows the config file path (`~/.crimson-ledger/logger.json`).
* `autocomplete="off"`, `spellcheck="false"`, `name`-less password input (stops password-manager prompts).

## Step 0: PoC (`scripts/poc/t12/`) — this task owns the biggest unknowns, so PoCs are mandatory and ordered
1. **webview_go on Linux** (inside the build image from T15 or a machine with `libwebkit2gtk-4.1-dev libgtk-3-dev`): build a window with `SetHtml("<h1>ok</h1>")` and a bound `add(a,b)`; call it from JS; exit after 2 s via `Terminate`. Record which `pkg-config` name the library requires (4.0 vs 4.1) and any build tag/env needed. PASS = prints `3` from JS and exits 0. **If it requires 4.0 and Ubuntu 24.04+ ships only 4.1, the finding changes the library table in PLAN 9.**
2. **Page size**: `SetHtml` with a 300 KB inline script + CSS loads and runs on Linux (and on Windows in PoC 3). PASS prints load time. If a limit is hit, fall back to `Bind` + chunked `Eval` (record which).
3. **Windows (go-webview2)**: same program with `//go:build windows`; run on a Windows VM or the Windows CI runner. PASS = same outputs. Cross-compile check on Linux: `GOOS=windows CGO_ENABLED=0 go vet -tags webview ./internal/ui/...`. **Cannot be fully proven on this Linux box; mark the result "verified on <runner/date>" in the log.**
4. **Reveal protocol**: with the fake window, prove the DOM/JS never receives the key before `revealKey()` (inspect page-side state through a test harness `Eval("JSON.stringify(window.__debugState())")` in a debug build), and that after re-mask the value equals the placeholder. PASS prints both.
5. **CSP enforcement**: page with `default-src 'none'; script-src 'nonce-X'; style-src 'nonce-X'; img-src data:; connect-src 'none'; base-uri 'none'; form-action 'none'` blocks (a) an inline script without nonce, (b) `fetch('https://example.com')`, (c) an `<img src=http://...>`, (d) navigation by `location=`. PASS = each reported blocked via `securitypolicyviolation` events sent through `Bind`.
6. **Navigation lock**: confirm the library lets us deny navigation (WebKitGTK `decide-policy`, WebView2 `NavigationStarting` cancel). If `webview_go` lacks a hook, the CSP `navigate-to`/`base-uri` plus `window.onbeforeunload` is the fallback; record what actually blocks `location='https://example.com'`.
7. **Preflight on Linux**: with `CGO_ENABLED=0`, `Check` reports nothing missing here (libs installed) and reports exactly `libdefinitely-missing.so.9` when injected via the test seam. (Already proven in the planning PoC; re-run inside the final package.)
8. **Missing-lib reality test**: in a minimal container with **no** WebKit (`docker run --rm -v $PWD/dist:/d debian:stable-slim /d/crimson-scout`), the launcher prints the exact message above and exits 78 **and the UI binary is never exec'd**; running the UI binary directly shows the raw loader error (documenting why the launcher exists). PASS = both observed.
9. **No listener**: while the app runs, `ss -ltnp | grep <pid>` is empty (no socket opened by either binary).

## Step 1: RED
- Preflight: `TestCheck_ReportsMissing` (injected `dlopen` seam), `_AlternativesSatisfy`, `TestReport_ExactText` (golden file per distro), `TestDistroDetection` (os-release fixtures: ubuntu, debian, fedora, arch, opensuse, unknown), `TestWindowsRegistry_Table`, `TestMacVersion_Table` (`10.15` fail, `11.0` ok, `14.5` ok).
- Launcher: `TestRun_MissingLibs_Exit78_DoesNotExecUI`, `TestRun_OK_ExecsUIWithArgs`, `TestRun_UIBinaryMissing_Message`, `TestRun_PassesThroughExitCode`, `TestLauncher_NoNetworkImports` (via `go list -deps` the launcher has no `net/http`).
- Page: `TestBuild_InjectsNonceEverywhere`, `TestBuild_CSPExact` (golden string), `TestBuild_NoExternalURLs` (regex over output for `http://`, `https://`, `//cdn`), `TestBuild_NonceDiffersPerBuild`.
- Settings bindings (with fake window + temp config home): `TestSaveSettings_KeepsKeyWhenPlaceholder`, `_ReplacesKey`, `_RejectsBadKey`, `_RejectsHTTPRemote`, `TestRevealKey_ReturnsStored`, `TestLoadSettings_NeverReturnsKey` (JSON has `hasKey:true` and `keyMask`, never the key), `TestTestConnection_UsesApiclient` (httptest).
- Key widget behaviour via a headless DOM test: run `keyfield.js` under Node + `jsdom` in the test image **or**, if avoiding Node, a Go table test that drives the real page in a WebView in CI (T16). Cases: mask on load; reveal; auto re-mask by fake timer; blur re-mask; typing replaces; Save sends placeholder-vs-new correctly. (Node is acceptable in the *test* image only; the product never needs it.)

## Step 2: GREEN (key code)
```go
// preflight/linux.go
var libs = []lib{
	{Name: "libwebkit2gtk-4.1.so.0", Alt: []string{"libwebkit2gtk-4.0.so.37"}, Why: "web view engine"},
	{Name: "libgtk-3.so.0", Why: "window toolkit"},
}
func check(open func(string) bool) []Missing {
	var out []Missing
	for _, l := range libs {
		if !open(l.Name) && !anyOpen(open, l.Alt) { out = append(out, l.missing()) }
	}
	return out
}
func dlopenOK(name string) bool {
	h, err := purego.Dlopen(name, purego.RTLD_LAZY|purego.RTLD_LOCAL)
	if err != nil { return false }
	_ = purego.Dlclose(h)
	return true
}
```
```go
// page/page.go: one inline document, one nonce, no network.
func Build(a Assets) (html, nonce string) {
	nonce = randomNonce()                                  // 16 bytes crypto/rand, base64
	csp := "default-src 'none'; script-src 'nonce-" + nonce + "'; style-src 'nonce-" + nonce +
		"'; img-src data:; connect-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
	return fmt.Sprintf(pageTmpl, csp, nonce, a.CSS, nonce, a.JS, a.Body), nonce
}
```
All data crosses via `Bind`ed functions returning JSON; the page inserts text with `textContent`. `RevealKey` is the only function that returns the key and logs
"api key revealed" (no value) to the in-app activity line.
Launcher:
```go
func Run(app, ui string, args []string, d deps) int {
	if missing := d.check(app); len(missing) > 0 {
		preflight.Report(d.stderr, missing); d.dialog(app, missing)
		return ExitMissingLibs
	}
	return d.exec(ui, args)          // exec.Command, stdio inherited; Unix: syscall.Exec to avoid a lingering parent
}
```
The UI binary lives at `<dir>/lib/<name>-ui[.exe]`; the launcher resolves it relative to its own executable (`os.Executable` + `EvalSymlinks`) and refuses a UI binary outside that directory.

## Step 3: REFACTOR checklist
- [ ] `Window` has the smallest method set the two apps use; no method is added "just in case".
- [ ] Platform code isolated behind build tags with identical exported names; no `runtime.GOOS` ifs outside `preflight`.
- [ ] JS: no globals except `window.__app`; ES2020, no dependencies, `"use strict"`, each function <= 30 lines, JSDoc on the public ones; lint with `eslint --no-eslintrc` rule set committed (test image only).
- [ ] Install-instruction table is data (`[]distroHint`), not nested ifs; golden tests cover it.
- [ ] Error text reviewed by a non-author for clarity (checklist item: a person with no Go knowledge can follow it).

## Step 4: AUDIT
- check.sh green on `./internal/ui/...` (no webview tag) **and** `go vet -tags webview` inside the build image (Linux) and via cross-vet for Windows.
- Security checklist (each linked to a test): CSP exact; no `unsafe-inline`/`unsafe-eval`; navigation denied; no listener; key not in DOM until revealed; key re-masked on blur/timeout/save; key never in `Eval` payloads except the reveal return value; no key in window title/log/status text; `devtools` disabled in release (`Debug=false`); UI binary path pinned.
- Performance: launcher start-to-exec < 50 ms (it is a tiny pure-Go binary; measure `hyperfine`-style with 50 runs), `Build` < 1 ms, memory of launcher < 15 MB RSS.

## Done when
- [ ] On a machine without WebKit, both apps' launchers print the exact message and exit 78 (PoC 8 reproduced as an automated container test).
- [ ] A fake-window test suite covers every binding; real-window smoke passes on Linux (Docker + Xvfb) and Windows (runner).
- [ ] Open question from PLAN 14 (pkg-config name, SetHtml limits, navigation lock) answered in the PoC log and PLAN 9/14 updated.

## PoC log
Date: 2026-11-17. Machine: Linux box with GTK3 runtime but **no WebKitGTK dev headers**, so nothing below that needs a real window ran here.

| PoC | Result |
|---|---|
| 1 webview_go on Linux | **NOT RUN** (no `libwebkit2gtk-*-dev`). Source finding: `webview_go` (v0.0.0-20240831) hard-codes `#cgo pkg-config: gtk+-3.0 webkit2gtk-4.0` at **build** time, while its bundled `webview.h` dlopens `libwebkit2gtk-4.1.so` first, then `4.0`, at **run** time. A build image on Ubuntu 24.04 (4.1 only) therefore needs a `webkit2gtk-4.0.pc` shim pointing at 4.1, or a webview_go revision with 4.1 support. T15 must settle this; the runtime check in preflight already accepts either. |
| 2 page size | NOT RUN (needs a window). |
| 3 Windows | Cross-vet only: `GOOS=windows CGO_ENABLED=0 go vet -tags webview ./internal/ui/...` passes. Not run on Windows. |
| 4 reveal protocol | Done without a real window: `keyfield.test.mjs` (Node, no deps) proves the field holds only the placeholder until `reveal()` resolves, then re-masks on 15 s timer, window blur, Escape, and on click; `TestLoadSettings_NeverReturnsKey` proves Go never sends the key except through `RevealKey`. |
| 5 CSP enforcement | NOT RUN (needs a real engine). `TestBuild_CSPExact` only proves the policy string. |
| 6 navigation lock | NOT RUN. |
| 7 preflight on Linux | Done: `TestCheck_RealHostIsConsistentWithReport` and the injected-`open` tests (`CGO_ENABLED` irrelevant, purego). |
| 8 missing-lib container test | NOT RUN (no container run). Launcher logic is covered by `TestRun_MissingLibs_Exit78_DoesNotExecUI`. |
| 9 no listener | NOT RUN; `check.sh` grep guard passes for `internal/ui`. |

Deviations from the task text: the launcher uses `exec.Command` with inherited stdio on every OS (no `syscall.Exec`); keyfield tests use hand-written DOM stubs under Node instead of jsdom; `Report` takes the app name and family as arguments.

AUDIT: gofmt, vet (host/windows/darwin), staticcheck, golangci-lint clean; tests -race pass, coverage 88.7%; govulncheck fails only on Go 1.25.0 standard-library advisories (needs the newer patch toolchain, already noted in PLAN status).

## Follow-ups
- Run the open PoCs (1, 2, 3, 5, 6, 8, 9) in the T15 build image / Windows runner, then tick the "Done when" boxes and update PLAN 9/14 (pkg-config name above).
- Wire `webview_unix.go` / `webview_windows.go` into T13/T14 and add an `eslint` config in the test image.
- macOS real-window verification happens on the CI macOS runner (T16).
