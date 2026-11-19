//go:build integration

package abuse

import (
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// keyCacheWindow is the server's DefaultKeyCacheTTL (30 s) plus a margin: a revoked key must be refused by then.
const keyCacheWindow = 40 * time.Second

// adminKeys runs `crimson-api keys <args>` against the main instance's home (local target only).
func adminKeys(t *testing.T, args ...string) string {
	t.Helper()
	if tgt.Kind != "local" || tgt.APIBin == "" || tgt.Home == "" {
		t.Skip("local target with CL_API_BIN and CL_HOME only")
	}
	cmd := exec.Command(tgt.APIBin, append([]string{"keys"}, args...)...)
	cmd.Env = append(os.Environ(), "CRIMSON_LEDGER_HOME="+tgt.Home)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("crimson-api keys %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// X107: a revoked key is refused within the key-cache window; the answer is 403 key_disabled.
func TestX107_RevokedKeyIsRefusedAfterCacheWindow(t *testing.T) {
	key := adminKeys(t, "create", "--scope", "view", "--label", "x107-"+runID)
	m := keyIDRe.FindStringSubmatch(key)
	if m == nil {
		t.Fatalf("unexpected key output shape")
	}
	a := viewer().WithKey("x107", key)
	if got := get(t, a, "/v1/charts").Status; got != 200 {
		t.Fatalf("fresh key: want 200, got %d", got)
	}
	adminKeys(t, "revoke", m[2])
	deadline := time.Now().Add(keyCacheWindow)
	for {
		ans := get(t, a, "/v1/charts")
		if ans.Status == 403 && contains(ans.Body, "key_disabled") {
			return
		}
		if ans.Status != 200 {
			t.Fatalf("X107: revoked key got %d, want 403 key_disabled", ans.Status)
		}
		if time.Now().After(deadline) {
			t.Fatalf("X107: revoked key still accepted after %s", keyCacheWindow)
		}
		time.Sleep(time.Second)
	}
}

// X106: for a key that does not authenticate, an unknown id and a wrong secret on a real id must not be
// told apart by timing. Requests are interleaved so drift hits both classes equally.
func TestX106_UnknownIDAndWrongSecretTimingMatch(t *testing.T) {
	m := keyIDRe.FindStringSubmatch(tgt.ViewKey)
	if m == nil {
		t.Fatal("view key shape")
	}
	wrongSecret := "clk_v_" + m[2] + "_" + strings.Repeat("A", len(m[3]))
	unknownID := "clk_v_zzzzzzzz_" + strings.Repeat("A", len(m[3]))
	const rounds = 400
	var known, unknown []float64
	for i := 0; i < rounds; i++ {
		known = append(known, timeOne(t, wrongSecret))
		unknown = append(unknown, timeOne(t, unknownID))
	}
	mk, mu := median(known), median(unknown)
	// Finding F-X106 (Low, accepted): a real id is served from the key cache, an unknown id costs one store
	// lookup, so the classes differ by the lookup time (about 1.5x on SQLite). Misses are deliberately not cached
	// (an attacker's random ids would only fill the cache) and the per-IP lockout (10 failures / 5 min) stops
	// anyone collecting enough samples. The test pins the gap so a regression to a larger one is caught.
	if ratio := mk / mu; ratio < 1.0/3 || ratio > 3 {
		t.Errorf("X106: median wrong-secret %.0fus vs unknown-id %.0fus (ratio %.2f): gap exceeds the accepted bound", mk, mu, ratio)
	}
	t.Logf("X106 medians: wrong-secret %.0fus unknown-id %.0fus", mk, mu)
}

func timeOne(t *testing.T, key string) float64 {
	t.Helper()
	a := viewer().WithKey("x106", key)
	start := time.Now()
	ans, err := a.Do("GET", "/v1/charts", nil, ipHdr(nil))
	d := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if ans.Status != 401 {
		t.Fatalf("X106: want 401, got %d", ans.Status)
	}
	return float64(d.Microseconds())
}

func median(v []float64) float64 {
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	return s[len(s)/2]
}

// X803: the running API's command line and environment contain no key material.
func TestX803_ProcessArgsAndEnvHoldNoSecrets(t *testing.T) {
	pids := os.Getenv("CL_API_PIDS")
	if tgt.Kind != "local" || pids == "" {
		t.Skip("local target with CL_API_PIDS only")
	}
	for _, p := range strings.Fields(pids) {
		if _, err := strconv.Atoi(p); err != nil {
			t.Fatalf("bad pid %q", p)
		}
		for _, f := range []string{"cmdline", "environ"} {
			b, err := os.ReadFile("/proc/" + p + "/" + f)
			if err != nil {
				t.Skipf("no /proc access: %v", err)
			}
			if strings.Contains(string(b), "clk_") || strings.Contains(strings.ToLower(string(b)), "pepper") {
				t.Errorf("X803: pid %s %s holds key or pepper material", p, f)
			}
		}
	}
}
