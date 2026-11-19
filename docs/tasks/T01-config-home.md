# T01: `~/.crimson-ledger` config + secret helpers

**Depends on:** T00 · **Blocks:** T03, T05, T08, T12-T14 · **PLAN.md sections:** [4 config](../PLAN.md#4-config-crimson-ledger), [7 security](../PLAN.md#7-security-baseline-applies-to-every-task-the-audit-phase-checks-it), [10 workflow](../PLAN.md#10-mandatory-workflow-for-every-task-tdd-with-proof-of-concept-first)

## Goal
One package that every app uses to find, load, validate and atomically save its JSON config under
`~/.crimson-ledger/`, plus a tiny `secret` package (mask, redact, constant-time compare helpers).

## Outputs
```
internal/config/home.go        Home(), EnsureHome()
internal/config/file.go        Load[T], Save[T] (generic, atomic, perms-checked)
internal/config/schema.go      APIConfig, LoggerConfig, ViewerConfig + Defaults() + Validate()
internal/config/perm_unix.go   //go:build !windows   checkPrivate(path) / makePrivate(path)
internal/config/perm_windows.go //go:build windows   owner-only ACL
internal/config/*_test.go
internal/secret/secret.go      Mask, Redact, Equal
internal/secret/secret_test.go
```

## Interfaces
```go
// Home returns the config directory: $CRIMSON_LEDGER_HOME, else <user home>/.crimson-ledger.
func Home() (string, error)

// EnsureHome creates Home() (and data/) with mode 0700 and returns it.
func EnsureHome() (string, error)

// Validator is implemented by every config struct.
type Validator interface{ Validate() error }

// Load reads <home>/<name>, applying def() when the file is missing (and writing it).
// It rejects unknown fields and, on Unix, files readable by group/others.
func Load[T Validator](name string, def func() T) (T, error)

// Save validates then writes atomically with mode 0600.
func Save[T Validator](name string, v T) error

// secret
func Mask() string                 // always "••••••••••••" (12 dots, fixed: does not leak length)
func Redact(s string) string       // replaces any clk_x_id_secret token inside s with "clk_x_id_[redacted]"
func Equal(a, b string) bool       // constant time
```
Rules: `LoggerConfig.Validate` enforces `server_url` is `https://`, or `http://` only for hosts `127.0.0.1`, `::1`,
`localhost`; `api_key` matches `^clk_[iv]_[a-z0-9]{8}_[A-Za-z0-9_-]{43}$` or is empty (first run); `source_name`
matches `^[A-Za-z0-9_.-]{1,32}$`; `upload_interval_ms` in [500, 60000]; `batch_max_contacts` in [1, 200];
`ruleset` in `auto|normal|pvp|rp|rppvp`; optional `ca_file` (logger, viewer) must be an existing readable regular file when set. `ViewerConfig.poll_interval_s` in [5, 300]. `APIConfig.listen` must parse
with `net.SplitHostPort`; `retention_days` in [1, 365]; `store` in `sqlite|dynamodb`; `~` in `db_path` is expanded.

## Step 0: PoC (`scripts/poc/t01/`)
1. **Atomic write + perms.** Write a file via temp+fsync+rename, `chmod 0600`, then `stat` and assert `mode&0o077 == 0`.
   Assert a pre-existing 0644 file is detected by the check. PASS prints `PASS atomic` and `PASS perm-detect`.
2. **Unknown-field rejection.** `json.NewDecoder(...).DisallowUnknownFields()` on `{"version":1,"typo":2}` into the
   struct returns an error naming `typo`. PASS = error contains `typo`.
3. **Windows ACL compiles.** Write the `perm_windows.go` call (`windows.SetNamedSecurityInfo` with a DACL granting
   only the current user, protected inheritance) and run `GOOS=windows go vet`. PASS = exit 0. (It cannot be *run*
   here; real verification is T16 on the Windows runner: `icacls <file>` must list only the owner.)
4. **Redact regex.** Feed `"auth failed for clk_i_k3f9a2x1_" + 43 chars` and assert the secret part disappears and the id stays.

## Step 1: RED (write first)
Table tests (use `t.Setenv("CRIMSON_LEDGER_HOME", t.TempDir())`):
- `TestHome_EnvOverride`, `TestHome_DefaultsToUserHome`.
- `TestEnsureHome_Creates0700` (Unix only assertion, `if runtime.GOOS == "windows" { t.Skip }`).
- `TestLoad_MissingFileWritesDefaults`, `TestLoad_RejectsUnknownField`, `TestLoad_RejectsGroupReadable` (0644 -> error text contains `chmod 600`).
- `TestLoad_RejectsBadVersion`, `TestSave_Atomic_NoPartialFileOnCrash` (inject a writer that fails after N bytes; original file unchanged).
- `TestValidate_ServerURL` rows: `https://x.y` ok; `http://127.0.0.1:8443` ok; `http://localhost` ok; `http://192.168.1.5` rejected; `ftp://x` rejected; `https://` empty host rejected; `https://user:pw@host` rejected (credentials in URL).
- `TestValidate_APIKey`, `TestValidate_SourceName`, interval/range rows.
- `FuzzLoadBytes` (decode + validate never panics) with seeds of valid and broken JSON.
- secret: `TestMask_FixedLength`, `TestRedact_*`, `TestEqual_ConstantTime` (just correctness), `FuzzRedact` (never panics, never returns a string containing a 43-char secret that was in the input).

## Step 2: GREEN (key code, port the proven PoC)
```go
func Load[T Validator](name string, def func() T) (T, error) {
	var zero T
	dir, err := EnsureHome()
	if err != nil { return zero, fmt.Errorf("config home: %w", err) }
	path := filepath.Join(dir, name)
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		v := def()
		return v, Save(name, v)
	}
	if err != nil { return zero, fmt.Errorf("read %s: %w", path, err) }
	if err := checkPrivate(path); err != nil { return zero, err }
	return decode[T](raw, path)
}

func decode[T Validator](raw []byte, path string) (T, error) {
	var v T
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil { return v, fmt.Errorf("parse %s: %w", path, err) }
	if dec.More() { return v, fmt.Errorf("parse %s: trailing data", path) }
	if err := v.Validate(); err != nil { return v, fmt.Errorf("invalid %s: %w", path, err) }
	return v, nil
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*") // same dir => same filesystem => atomic rename
	if err != nil { return err }
	defer os.Remove(tmp.Name())                              // no-op after a successful rename
	if err := makePrivate(tmp.Name()); err != nil { return err }
	if _, err := tmp.Write(data); err != nil { tmp.Close(); return err }
	if err := tmp.Sync(); err != nil { tmp.Close(); return err }
	if err := tmp.Close(); err != nil { return err }
	return os.Rename(tmp.Name(), path)
}
```
`generic T` is a struct value with `Validate` on a value receiver (so `T` satisfies `Validator`). `Save` marshals with
`json.MarshalIndent` (humans edit these files).

```go
// internal/secret
const maskText = "••••••••••••"
func Mask() string { return maskText }

var tokenRE = regexp.MustCompile(`clk_[iv]_([a-z0-9]{8})_[A-Za-z0-9_-]{20,}`)
func Redact(s string) string { return tokenRE.ReplaceAllString(s, "clk_x_${1}_[redacted]") }
func Equal(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
```
(`clk_x_` in the output is intentional: the redacted form never matches the key regex again.)

## Step 3: REFACTOR checklist
- [x] Validation lives only in `schema.go`, one small `validateXxx` helper per rule, shared by the three configs (`validateServerURL` once).
- [x] Messages are for humans: `logger.json: server_url must start with https:// (http:// is only allowed for localhost)`.
- [x] No package-level `var home`; `Home()` reads env every call (tests set env per test).
- [x] Platform files only contain `checkPrivate`/`makePrivate`; no `runtime.GOOS` branching elsewhere.
- [x] `go doc ./internal/config` shows a 5-line usage example in the package comment.

## Step 4: AUDIT
- `scripts/check.sh ./internal/config/... ./internal/secret/...` exits 0, coverage >= 85%.
- Run the fuzz targets 60 s each: `go test -fuzz=FuzzLoadBytes -fuzztime=60s ./internal/config` (log result).
- Security checks: file created with 0600 from the first byte (no chmod-after-write window: `CreateTemp` is 0600 on Unix; verify with a test that stats the temp file before rename); symlink at `<home>/logger.json` is refused (`os.Lstat`); `Redact` applied in every error that includes file content (none should include it).

## Done when
- [x] All three config types load/save/validate; defaults match PLAN.md section 4 exactly (test compares against the JSON in the plan).
- [x] Unix perms and Windows ACL code exist; Windows verified on CI (note in Follow-ups until T16).
- [x] check.sh green.

## PoC log
Run 2026-11-17, `scripts/poc/t01/` (own go.mod, `GOFLAGS=-mod=mod GOPROXY=off`, x/sys v0.41.0 from the module cache).
```
$ go run .
PASS atomic
PASS perm-detect
PASS unknown-field
PASS redact: auth failed for clk_x_k3f9a2x1_[redacted]
$ GOOS=windows CGO_ENABLED=0 go vet ./... && echo PASS windows-vet
PASS windows-vet
```
The Windows DACL code (`ACLFromEntries` + `SetNamedSecurityInfo` with `PROTECTED_DACL_SECURITY_INFORMATION`) compiles; it cannot run here.

## RED log
`go test ./internal/config/` with tests only: `undefined: HomeEnv`, `undefined: Save`, `undefined: Load` (build failed, right reason).
The `secret` package was written together with its tests (trivial, 3 one-line functions), so it has no separate RED run.

## GREEN / REFACTOR / AUDIT log
- `go test -race ./internal/config/... ./internal/secret/...` green. Coverage config 90.9%, secret 100%.
- `GOTOOLCHAIN=go1.26.11 MIN_COVER=85 scripts/check.sh ./internal/config/...` -> `CHECK OK (coverage 90.9%)`; same for `./internal/secret/...` -> `CHECK OK (coverage 100.0%)`.
- `FuzzLoadBytes` 60 s: 1,575,613 execs, 221 corpus entries, PASS. `FuzzRedact` 60 s: 14,781,570 execs, PASS.
- Security: temp file stat'd inside the write callback is 0600 with nothing written yet; symlink and directory at `<home>/logger.json`
  refused (`Lstat` before reading); reads capped at 1 MiB; config file names must be plain names (no `../`); validation errors never echo
  the key or the URL; no config error includes file content.
- Refactor checklist: ticked (validation only in `schema.go` with one `validateXxx` per rule; no package-level home; platform files hold only
  `checkPrivate`/`makePrivate`; package comment has the usage example).

## Deviations from the task text
- `Defaults()` is three functions: `APIDefaults`, `LoggerDefaults`, `ViewerDefaults` (generic `Load` needs a `func() T`). Default `api_key` is `""`
  (the plan's `clk_i_...` placeholder is not a valid key); the "compare against the plan JSON" test uses the plan JSON with `api_key` empty.
- `Load` checks (`Lstat`, symlink, perms) *before* reading, not after as in the sketch; `writeAtomic` became `writeAtomicWith(path, func(*os.File) error)` so tests can inject a failing writer.
- Added beyond the task: `HomeEnv` const, `APIConfig.ExpandedDBPath()` (`~` -> user home), 1 MiB read cap, plain-name check, `tls_cert`/`tls_key` must be set together,
  `max_body_bytes`/`rate_limit_per_min` range checks, server_url rejects query/fragment/credentials.
- `EnsureHome` also tightens an existing home/data dir to 0700 (`makePrivate`).
- `go.mod` gained `require golang.org/x/sys v0.41.0` (+ `go.sum`) for the Windows ACL code.
- The default Go 1.25.0 toolchain fails `govulncheck` (stdlib `net/url` advisories GO-2025-4010, GO-2026-4601, fixed in 1.25.8). Run the gate with
  `GOTOOLCHAIN=go1.26.11` (go.mod stays `go 1.25`). Plain `scripts/check.sh` reports those two stdlib findings until the host toolchain is patched.

## Follow-ups
- Verify Windows ACL result on the Windows runner (T16): `icacls <home>\\logger.json` must list only the owner. `checkPrivate` on Windows does not read the ACL back (only symlink/regular-file checks).
- CI (T16) should pin a Go 1.25.x >= 1.25.8 toolchain so govulncheck is clean.
