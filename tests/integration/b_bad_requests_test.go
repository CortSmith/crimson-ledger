//go:build integration

package integration

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// bad sends r and asserts the status and error code, and that no data changed (B20 applies to every row).
func bad(t *testing.T, id string, r req, status int, code string) resp {
	t.Helper()
	before := versionOf(t)
	got := do(t, r)
	if got.Status != status || (code != "" && got.ErrCode() != code) {
		t.Fatalf("%s: want %d %s, got %d %.160s req=%s", id, status, code, got.Status, got.Body, got.RequestID())
	}
	if after := versionOf(t); after != before {
		t.Fatalf("%s: a rejected request changed the data version (%s -> %s) req=%s", id, before, after, got.RequestID())
	}
	return got
}

func TestB01_BadAuthHeadersAreIdentical(t *testing.T) {
	variants := map[string]string{
		"missing": "", "empty bearer": "Bearer ", "wrong scheme": "Basic abc", "garbage": "Bearer not-a-key",
		"bearer only": "Bearer", "lowercase junk": "bearer clk_", "huge": "Bearer " + strings.Repeat("A", 4000),
	}
	var first []byte
	for name, h := range variants {
		r := req{Path: "/v1/sweep?ruleset=rp", IP: freshIP()}
		if h != "" {
			r.Header = map[string]string{"Authorization": h}
		}
		got := bad(t, "B01 "+name, r, 401, "unauthorized")
		stripped := bytes.ReplaceAll(got.Body, []byte(got.RequestID()), nil)
		if first == nil {
			first = stripped
		} else if !bytes.Equal(first, stripped) {
			t.Fatalf("B01 %s: body differs from the other variants: %s", name, got.Body)
		}
	}
}

func TestB02_UnknownIDAndWrongSecretLookAlike(t *testing.T) {
	m := keyRe.FindStringSubmatch(tgt.ViewKey)
	if m == nil {
		t.Skip("view key is not in clk_v_<id>_<secret> form")
	}
	wrongSecret := "clk_v_" + m[1] + "_" + strings.Repeat("A", 43)
	unknownID := "clk_v_" + strings.Repeat("a", len(m[1])) + "_" + strings.Repeat("A", 43)
	a := bad(t, "B02 wrong secret", req{Path: "/v1/charts", Key: wrongSecret, IP: freshIP()}, 401, "unauthorized")
	b := bad(t, "B02 unknown id", req{Path: "/v1/charts", Key: unknownID, IP: freshIP()}, 401, "unauthorized")
	norm := func(r resp) string { return strings.ReplaceAll(string(r.Body), r.RequestID(), "") }
	if norm(a) != norm(b) {
		t.Fatalf("B02: bodies differ (key-existence oracle): %s vs %s", a.Body, b.Body)
	}
}

func TestB03_RevokedKeyIsDisabled(t *testing.T) {
	if tgt.APIBin == "" {
		t.Skip("B03 needs CL_API_BIN (local); AWS uses the admin Lambda (F18-1)")
	}
	key, id := makeKey(t, "view")
	if r := do(t, req{Path: "/v1/charts", Key: key}); r.Status != 200 {
		t.Fatalf("B03: fresh key: %d", r.Status)
	}
	adminRun(t, "keys", "revoke", id)
	eventually(t, "B03 403 key_disabled", 35*time.Second, func() bool {
		r := do(t, req{Path: "/v1/charts", Key: key})
		return r.Status == 403 && r.ErrCode() == "key_disabled"
	})
}

func makeKey(t *testing.T, scope string) (key, id string) {
	t.Helper()
	out := adminRun(t, "keys", "create", "--scope", scope, "--label", "it-"+scope)
	key = strings.TrimSpace(out)
	m := keyRe.FindStringSubmatch(key)
	if m == nil {
		t.Fatalf("admin create printed no key")
	}
	return key, m[1]
}

func adminRun(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command(tgt.APIBin, args...) // #nosec G204 -- operator-supplied test binary
	cmd.Env = append(cmd.Environ(), "CRIMSON_LEDGER_HOME="+tgt.Home)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("admin %v: %v", args, err)
	}
	return string(out)
}

func TestB04_WrongScopeBothWays(t *testing.T) {
	bad(t, "B04 view posts", postJSON(tgt.ViewKey, mustJSON(t, ping(nil, nil, nil))), 403, "forbidden")
	bad(t, "B04 ingest reads", req{Path: "/v1/charts", Key: tgt.IngestKey}, 403, "forbidden")
}

func TestB05_MalformedBodies(t *testing.T) {
	good := string(mustJSON(t, ping(nil, nil, nil)))
	for name, body := range map[string]string{
		"malformed": `{"batchId":`, "trailing data": good + ` {}`, "empty": ``, "null": `null`, "array": `[]`, "string": `"x"`, "number": `7`,
	} {
		r := bad(t, "B05 "+name, postJSON(tgt.IngestKey, []byte(body)), 400, "")
		if c := r.ErrCode(); c != "invalid_ping" && c != "invalid_request" {
			t.Fatalf("B05 %s: code %q", name, c)
		}
	}
}

func TestB06_UnknownFieldRejected(t *testing.T) {
	p := ping([]obj{contact(newGUID(), word(), fix(nowMS(), 1411, 1, 1))}, nil, nil)
	p["contacts"].([]obj)[0]["admin"] = true
	// targets_diff / F18-4: PLAN T18 wants the field path named, but the server deliberately answers every
	// JSON-structure failure with the same generic message (ingest_test.go pins that), so only the code is asserted.
	bad(t, "B06 nested", ingest(mustJSON(t, p)), 400, "invalid_ping")
	p2 := ping(nil, nil, nil)
	p2["extra"] = 1
	bad(t, "B06 top", ingest(mustJSON(t, p2)), 400, "")
}

func TestB07_LimitsExceededByOne(t *testing.T) {
	now := nowMS()
	one := func() obj { return contact(newGUID(), word(), fix(now, 1411, 1, 1)) }
	manyContacts := make([]obj, 201)
	for i := range manyContacts {
		manyContacts[i] = one()
	}
	manyFixes := make([]obj, 51)
	for i := range manyFixes {
		manyFixes[i] = fix(now-int64(i), 1411, 1, 1)
	}
	manyKills := make([]obj, 201)
	for i := range manyKills {
		manyKills[i] = obj{"t": now, "killer": newGUID(), "victim": newGUID(), "mapId": 1411}
	}
	manyCharts := make([]obj, 51)
	for i := range manyCharts {
		manyCharts[i] = obj{"mapId": i + 1, "zone": "Z", "x0": 0, "y0": 0, "x1": 1, "y1": 1}
	}
	longName := contact(newGUID(), strings.Repeat("a", 25), fix(now, 1411, 1, 1))
	longRaw := contact(newGUID(), word(), fix(now, 1411, 1, 1))
	longRaw["raw"] = strings.Repeat("r", 65)
	cases := map[string]obj{
		"201 contacts":      ping(manyContacts, nil, nil),
		"51 fixes":          ping([]obj{contact(newGUID(), word(), manyFixes...)}, nil, nil),
		"201 kills":         ping(nil, manyKills, nil),
		"51 charts":         ping(nil, nil, manyCharts),
		"name 25 runes":     ping([]obj{longName}, nil, nil),
		"raw 65 runes":      ping([]obj{longRaw}, nil, nil),
		"source 33 chars":   func() obj { p := ping(nil, nil, nil); p["source"] = strings.Repeat("s", 33); return p }(),
		"source bad chars":  func() obj { p := ping(nil, nil, nil); p["source"] = "a b"; return p }(),
		"source empty":      func() obj { p := ping(nil, nil, nil); p["source"] = ""; return p }(),
		"batchId not v4":    func() obj { p := ping(nil, nil, nil); p["batchId"] = "6f1c2e0e-8b5a-1a9e-9d34-0a7c1d2f3b44"; return p }(),
		"batchId not uuid":  func() obj { p := ping(nil, nil, nil); p["batchId"] = "x"; return p }(),
		"ruleset unknown":   func() obj { p := ping(nil, nil, nil); p["ruleset"] = "hardcore"; return p }(),
		"ruleset uppercase": func() obj { p := ping(nil, nil, nil); p["ruleset"] = "RP"; return p }(),
	}
	for name, p := range cases {
		r := bad(t, "B07 "+name, ingest(mustJSON(t, p)), 400, "")
		if c := r.ErrCode(); c != "invalid_ping" && c != "invalid_request" {
			t.Fatalf("B07 %s: code %q", name, c)
		}
	}
}

func TestB08_CoordinatesAndTimes(t *testing.T) {
	now := nowMS()
	mk := func(f obj) obj { return ping([]obj{contact(newGUID(), word(), f)}, nil, nil) }
	cases := map[string][]byte{
		"x 100.01":   mustJSON(t, mk(fix(now, 1411, 100.01, 1))),
		"y -0.01":    mustJSON(t, mk(fix(now, 1411, 1, -0.01))),
		"25h old":    mustJSON(t, mk(fix(now-25*3600_000, 1411, 1, 1))),
		"6 min fut":  mustJSON(t, mk(fix(now+6*60_000, 1411, 1, 1))),
		"mapId 0":    mustJSON(t, mk(fix(now, 0, 1, 1))),
		"mapId neg":  mustJSON(t, mk(fix(now, -5, 1, 1))),
		"x string":   []byte(strings.Replace(string(mustJSON(t, mk(fix(now, 1411, 1, 1)))), `"x":1`, `"x":"1"`, 1)),
		"x NaN":      []byte(strings.Replace(string(mustJSON(t, mk(fix(now, 1411, 1, 1)))), `"x":1`, `"x":NaN`, 1)),
		"x 1e999":    []byte(strings.Replace(string(mustJSON(t, mk(fix(now, 1411, 1, 1)))), `"x":1`, `"x":1e999`, 1)),
		"t float":    []byte(strings.Replace(string(mustJSON(t, mk(fix(now, 1411, 1, 1)))), fmt.Sprintf(`"t":%d`, now), fmt.Sprintf(`"t":%d.5`, now), 1)),
		"sentAt old": func() []byte { p := ping(nil, nil, nil); p["sentAt"] = now - 25*3600_000; return mustJSON(t, p) }(),
	}
	for name, body := range cases {
		bad(t, "B08 "+name, ingest(body), 400, "")
	}
}

func TestB09_BadIdentifiersAndText(t *testing.T) {
	now := nowMS()
	c := func(mut func(obj)) obj {
		x := contact(newGUID(), word(), fix(now, 1411, 1, 1))
		mut(x)
		return ping([]obj{x}, nil, nil)
	}
	cases := map[string]obj{
		"guid shape":   c(func(x obj) { x["guid"] = "Player-1-xyz" }),
		"guid kind":    c(func(x obj) { x["guid"] = "Cat-1329-0A1B2C3D" }),
		"guid lower":   c(func(x obj) { x["guid"] = "Player-1329-0a1b2c3d" }),
		"reaction":     c(func(x obj) { x["reaction"] = "angry" }),
		"fix kind":     c(func(x obj) { x["fixes"].([]obj)[0]["kind"] = "guess" }),
		"control name": c(func(x obj) { x["name"] = "a\u0007b" }),
		"newline name": c(func(x obj) { x["name"] = "a\nb" }),
		"bidi name":    c(func(x obj) { x["name"] = "a‮b" }),
		"zero width":   c(func(x obj) { x["name"] = "a​b" }),
		"empty name":   c(func(x obj) { x["name"] = "" }),
		"control raw":  c(func(x obj) { x["raw"] = "a\x00b" }),
	}
	for name, p := range cases {
		bad(t, "B09 "+name, ingest(mustJSON(t, p)), 400, "")
	}
}

func TestB10_ContentTypeAndEncoding(t *testing.T) {
	body := mustJSON(t, ping(nil, nil, nil))
	for _, ct := range []string{"text/plain", "application/x-www-form-urlencoded", "multipart/form-data", ""} {
		r := ingest(body)
		r.Header = map[string]string{}
		if ct != "" {
			r.Header["Content-Type"] = ct
		}
		bad(t, "B10 ct="+ct, r, 415, "unsupported_media_type")
	}
	for _, enc := range []string{"br", "deflate", "zstd", "gzip, br"} {
		r := ingest(body)
		r.Header["Content-Encoding"] = enc
		bad(t, "B10 enc="+enc, r, 415, "")
	}
	r := ingest([]byte("not gzip at all"))
	r.Header["Content-Encoding"] = "gzip"
	bad(t, "B10 fake gzip", r, 400, "")
}

func TestB11_BodyOverLimit(t *testing.T) {
	big := bytes.Repeat([]byte("a"), 300<<10)
	r := bad(t, "B11 plain", ingest(big), 413, "too_large")
	if r.Dur > time.Second {
		t.Fatalf("B11: took %s", r.Dur)
	}
	bomb := ingest(gz(t, bytes.Repeat([]byte("0"), 64<<20)))
	bomb.Header["Content-Encoding"] = "gzip"
	if r := bad(t, "B11 gzip bomb", bomb, 413, "too_large"); r.Dur > 3*time.Second {
		t.Fatalf("B11: bomb took %s", r.Dur)
	}
	if h := do(t, req{Path: "/v1/health"}); h.Status != 200 {
		t.Fatalf("B11: server unhealthy afterwards: %d", h.Status)
	}
}

func TestB12_QueryAbuse(t *testing.T) {
	for name, p := range map[string]string{
		"unknown param":    "/v1/sweep?ruleset=rp&evil=1",
		"duplicate param":  "/v1/sweep?ruleset=rp&ruleset=pvp",
		"missing ruleset":  "/v1/sweep",
		"bad ruleset":      "/v1/sweep?ruleset=nope",
		"maxAge zero":      "/v1/sweep?ruleset=rp&maxAgeSec=0",
		"maxAge negative":  "/v1/sweep?ruleset=rp&maxAgeSec=-1",
		"maxAge huge":      "/v1/sweep?ruleset=rp&maxAgeSec=999999999999",
		"maxAge NaN":       "/v1/sweep?ruleset=rp&maxAgeSec=NaN",
		"zone text":        "/v1/sweep?ruleset=rp&zone=abc",
		"guid bad":         "/v1/sweep?ruleset=rp&guid=x",
		"limit 0":          "/v1/bounty?ruleset=rp&limit=0",
		"limit -1":         "/v1/bounty?ruleset=rp&limit=-1",
		"limit 101":        "/v1/bounty?ruleset=rp&limit=101",
		"limit NaN":        "/v1/bounty?ruleset=rp&limit=NaN",
		"players limit 51": "/v1/players?ruleset=rp&q=ab&limit=51",
		"q 1 char":         "/v1/players?ruleset=rp&q=a",
		"q 1 KB":           "/v1/players?ruleset=rp&q=" + strings.Repeat("a", 1024),
		"q control":        "/v1/players?ruleset=rp&q=a%00b",
		"q missing":        "/v1/players?ruleset=rp",
		"trail limit":      "/v1/players/" + newGUID() + "/trail?ruleset=rp&limit=2001",
		"trail since":      "/v1/players/" + newGUID() + "/trail?ruleset=rp&since=abc",
		"charts param":     "/v1/charts?x=1",
	} {
		bad(t, "B12 "+name, view(p), 400, "invalid_request")
	}
}

func TestB13_WrongMethodsAndNoCORS(t *testing.T) {
	routes := map[string]string{
		"/v1/health": "GET", "/v1/pings": "POST", "/v1/sweep?ruleset=rp": "GET", "/v1/players?ruleset=rp&q=ab": "GET",
		"/v1/bounty?ruleset=rp": "GET", "/v1/charts": "GET", "/v1/players/" + newGUID() + "/trail?ruleset=rp": "GET",
	}
	for path, allowed := range routes {
		for _, m := range []string{"PUT", "DELETE", "PATCH", "TRACE", "OPTIONS", "GET", "POST"} {
			if m == allowed || (allowed == "GET" && m == "HEAD") {
				continue
			}
			r := bad(t, "B13 "+m+" "+path, req{Method: m, Path: path, Key: tgt.ViewKey, IP: freshIP()}, 405, "")
			if r.Header.Get("Allow") == "" {
				t.Fatalf("B13 %s %s: no Allow header", m, path)
			}
			if r.Header.Get("Access-Control-Allow-Origin") != "" {
				t.Fatalf("B13 %s %s: CORS header present", m, path)
			}
		}
	}
	pre := do(t, req{Method: "OPTIONS", Path: "/v1/sweep", Header: map[string]string{"Origin": "https://evil.example", "Access-Control-Request-Method": "GET"}})
	if pre.Header.Get("Access-Control-Allow-Origin") != "" || pre.Header.Get("Access-Control-Allow-Methods") != "" {
		t.Fatalf("B13: preflight granted CORS: %v", pre.Header)
	}
}

func TestB14_UnknownRoutes(t *testing.T) {
	for _, p := range []string{"/", "/v1", "/v1/", "/v1/nope", "/v1/health/", "//v1/health", "/v1/../v1/health", "/v1/sweep/", "/admin", "/.env", "/v1/keys", "/v1/players//trail"} {
		r := do(t, req{Path: p, Key: tgt.ViewKey, IP: freshIP()})
		if r.Status != 404 && r.Status != 200 && r.Status != 301 && r.Status != 308 && r.Status != 400 && r.Status != 405 {
			t.Fatalf("B14 %q: unexpected %d %s", p, r.Status, r.Body)
		}
		if r.Status >= 500 || strings.Contains(string(r.Body), "goroutine") || strings.Contains(string(r.Body), ".go:") {
			t.Fatalf("B14 %q: leaks internals: %d %s", p, r.Status, r.Body)
		}
		if r.Status == 404 && r.ErrCode() != "not_found" {
			t.Fatalf("B14 %q: 404 without the standard shape: %s", p, r.Body)
		}
	}
}

func TestB15_UnknownTrailIsUniform(t *testing.T) {
	a := bad(t, "B15 a", view("/v1/players/"+newGUID()+"/trail?ruleset=rp"), 404, "not_found")
	b := bad(t, "B15 b", view("/v1/players/"+newGUID()+"/trail?ruleset=rp"), 404, "not_found")
	norm := func(r resp) string { return strings.ReplaceAll(string(r.Body), r.RequestID(), "") }
	if norm(a) != norm(b) {
		t.Fatalf("B15: 404 bodies differ: %s vs %s", a.Body, b.Body)
	}
}

func TestB16_BadKeyLockout(t *testing.T) {
	if !tgt.SpoofIP {
		t.Skip("B16 needs a target that trusts X-Forwarded-For so the test owns a private client IP")
	}
	ip := freshIP()
	var saw429 int
	for i := 0; i < 15; i++ {
		r := do(t, req{Path: "/v1/charts", Key: "clk_v_bad_bad", IP: ip})
		if r.Status == 429 {
			saw429++
			if r.Header.Get("Retry-After") == "" {
				t.Fatal("B16: 429 without Retry-After")
			}
		} else if r.Status != 401 {
			t.Fatalf("B16: unexpected %d", r.Status)
		}
	}
	if saw429 == 0 {
		t.Fatal("B16: no lockout after 15 bad keys from one IP")
	}
	if r := do(t, req{Path: "/v1/charts", Key: tgt.ViewKey, IP: ip}); r.Status != 429 {
		t.Fatalf("B16: a valid key from the locked IP must be blocked too, got %d", r.Status)
	}
	if r := do(t, req{Path: "/v1/charts", Key: tgt.ViewKey, IP: freshIP()}); r.Status != 200 {
		t.Fatalf("B16: another client must be unaffected, got %d", r.Status)
	}
}

func TestB17_RateLimitWithValidKey(t *testing.T) {
	if tgt.RateURL == "" {
		t.Skip("B17 needs CL_RATE_URL: a second instance with the default 120/min limit")
	}
	limited := 0
	for i := 0; i < 140; i++ {
		r, err := doOn(tgt.RateURL, req{Path: "/v1/health"})
		if err != nil {
			t.Fatal(err)
		}
		_ = r
	}
	for i := 0; i < 140; i++ {
		r, err := doOn(tgt.RateURL, req{Method: "POST", Path: "/v1/pings", Key: tgt.RateIngestKey, Body: mustJSON(t, ping(nil, nil, nil)), Header: map[string]string{"Content-Type": "application/json"}})
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case r.Status >= 500:
			t.Fatalf("B17: 5xx under rate limiting: %d", r.Status)
		case r.Status == 429:
			limited++
			if r.ErrCode() != "rate_limited" || r.Header.Get("Retry-After") == "" {
				t.Fatalf("B17: 429 shape wrong: %s %v", r.Body, r.Header)
			}
		}
	}
	if limited == 0 {
		t.Fatal("B17: 140 requests in a minute were never rate limited")
	}
}

func TestB18_Slowloris(t *testing.T) {
	if tgt.Kind != KindLocal {
		t.Skip("B18 is local-only")
	}
	u := strings.TrimPrefix(tgt.BaseURL, "http://")
	conn, err := net.Dial("tcp", u)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_, _ = conn.Write([]byte("POST /v1/pings HTTP/1.1\r\nHost: x\r\nContent-Length: 100\r\n"))
	start := time.Now()
	_ = conn.SetReadDeadline(time.Now().Add(20 * time.Second))
	buf := make([]byte, 512)
	for time.Since(start) < 20*time.Second {
		if _, err := conn.Write([]byte("X-A: b\r\n")); err != nil {
			break
		}
		if _, err := conn.Read(buf); err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				time.Sleep(time.Second)
				continue
			}
			break
		}
		break
	}
	if d := time.Since(start); d > 16*time.Second {
		t.Fatalf("B18: trickled headers kept the connection %s (limit 15 s)", d)
	}
	if h := do(t, req{Path: "/v1/health"}); h.Status != 200 {
		t.Fatalf("B18: server unhealthy afterwards: %d", h.Status)
	}
}

// Go adds 4 KiB slack to MaxHeaderBytes (16 KiB), so 20 KiB is still accepted; 32 KiB must not be.
func TestB19_OversizedHeaders(t *testing.T) {
	r := do(t, req{Path: "/v1/health", Header: map[string]string{"X-Big": strings.Repeat("a", 32<<10)}})
	if r.Status != 431 && r.Status != 400 && r.Status != 413 {
		t.Fatalf("B19: 32 KB header: want rejection, got %d", r.Status)
	}
	u := strings.TrimPrefix(tgt.BaseURL, "http://")
	if tgt.Kind == KindLocal {
		conn, err := net.Dial("tcp", u)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()
		var b strings.Builder
		b.WriteString("GET /v1/health HTTP/1.1\r\nHost: x\r\n")
		for i := 0; i < 5000; i++ {
			fmt.Fprintf(&b, "X-%d: v\r\n", i)
		}
		b.WriteString("\r\n")
		_, _ = conn.Write([]byte(b.String()))
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		line := make([]byte, 64)
		n, _ := conn.Read(line)
		if !bytes.Contains(line[:n], []byte("431")) && !bytes.Contains(line[:n], []byte("400")) && n != 0 {
			t.Fatalf("B19: 5000 header lines: %q", line[:n])
		}
	}
	if h := do(t, req{Path: "/v1/health"}); h.Status != 200 {
		t.Fatalf("B19: server unhealthy afterwards: %d", h.Status)
	}
}

func TestB21_FloodAWS(t *testing.T) {
	if tgt.Kind != KindAWS {
		t.Skip("B21 is aws-only")
	}
	t.Skip("B21 needs the AWS test stack (F18-1)")
}

var _ = http.MethodGet
