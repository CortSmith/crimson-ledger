//go:build integration

package abuse

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crimsonledger/tests/integration"
)

var (
	tgt   integration.Target
	runID string
	ipSeq atomic.Uint32
)

func TestMain(m *testing.M) {
	var err error
	if tgt, err = integration.FromEnv(os.Getenv); err != nil {
		fmt.Fprintln(os.Stderr, "abuse suite:", err)
		os.Exit(2)
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	runID = hex.EncodeToString(b)
	os.Exit(m.Run())
}

func anon() Actor   { return Anonymous(tgt.BaseURL) }
func viewer() Actor { return ViewKeyHolder(tgt.BaseURL, tgt.ViewKey) }
func writer() Actor { return IngestKeyHolder(tgt.BaseURL, tgt.IngestKey) }

// freshIP gives a client IP no other test uses; it only matters when the server trusts X-Forwarded-For.
func freshIP() string { n := ipSeq.Add(1); return fmt.Sprintf("198.51.100.%d", n%250+1) }

func ipHdr(extra map[string]string) map[string]string {
	h := map[string]string{}
	if tgt.SpoofIP {
		h["X-Forwarded-For"] = freshIP()
	}
	for k, v := range extra {
		h[k] = v
	}
	return h
}

// do sends via the actor, runs the leak detector, and fails on transport errors.
func do(t testing.TB, a Actor, method, target string, body []byte, h map[string]string) Answer {
	t.Helper()
	ans, err := a.Do(method, target, body, ipHdr(h))
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	noLeak(t, target, ans)
	return ans
}

func get(t testing.TB, a Actor, target string) Answer { return do(t, a, "GET", target, nil, nil) }

// noLeak is assertNoLeak of the T19 checklist: runs on every response.
func noLeak(t testing.TB, what string, a Answer) {
	t.Helper()
	if l := Leaks(a); len(l) > 0 {
		t.Errorf("LEAK in response to %s: %q status=%d body=%.200s", what, l, a.Status, a.Body)
	}
	if a.Status >= 500 {
		t.Errorf("5xx from our code for %s: %d %.200s", what, a.Status, a.Body)
	}
}

func raw(t testing.TB, r Raw) Answer {
	t.Helper()
	a, err := SendRaw(tgt.BaseURL, r)
	if err != nil {
		t.Fatalf("raw %s %q: %v", r.Method, r.Target, err)
	}
	noLeak(t, r.Target, a)
	return a
}

// ---- builders ----

type obj = map[string]any

func nowMS() int64 { return time.Now().UnixMilli() }

func uuid4() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	h := hex.EncodeToString(b)
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func guid() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "Player-1329-" + strings.ToUpper(hex.EncodeToString(b))
}

func pingBody(contacts, kills []obj) obj {
	if contacts == nil {
		contacts = []obj{}
	}
	if kills == nil {
		kills = []obj{}
	}
	return obj{"batchId": uuid4(), "source": "x-" + runID, "ruleset": "rp", "sentAt": nowMS(),
		"contacts": contacts, "charts": []obj{}, "kills": kills}
}

func contactOf(g, name string, t int64) obj {
	return obj{"guid": g, "name": name, "raw": name + "-RP-US", "reaction": "hostile", "lastSeen": t,
		"fixes": []obj{{"t": t, "mapId": 1411, "x": 10.0, "y": 20.0, "kind": "exact"}}}
}

func js(t testing.TB, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// version is the state fingerprint: the sweep ETag of ruleset rp.
func version(t testing.TB) string {
	t.Helper()
	et := get(t, viewer(), "/v1/sweep?ruleset=rp").Header.Get("ETag")
	// ETag looks like W/"rp.<version>.<window hash>"; the hash also moves with a time bucket, so only the version counts.
	if i := strings.LastIndex(et, "."); i > 0 {
		return et[:i]
	}
	return et
}

// unchanged runs f and asserts the stored data did not change (store version equal before/after).
func unchanged(t *testing.T, id string, f func()) {
	t.Helper()
	before := version(t)
	f()
	if after := version(t); after != before {
		t.Fatalf("%s: attack changed stored data (%s -> %s)", id, before, after)
	}
}

func contains(b []byte, s string) bool { return bytes.Contains(b, []byte(s)) }
