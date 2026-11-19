//go:build integration

package abuse

import (
	"encoding/json"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestX303_TrailIsBoundedPerCall(t *testing.T) {
	g := guid()
	post(t, writer(), js(t, pingBody([]obj{contactOf(g, "Scrapee", nowMS())}, nil)))
	var seen int
	since := "0"
	for page := 0; page < 5; page++ {
		a := get(t, viewer(), "/v1/players/"+g+"/trail?ruleset=rp&since="+since)
		if a.Status != 200 {
			break
		}
		var r struct {
			Fixes []struct {
				T int64 `json:"t"`
			} `json:"fixes"`
		}
		if err := json.Unmarshal(a.Body, &r); err != nil {
			t.Fatalf("X303: %v", err)
		}
		if len(r.Fixes) > 2000 {
			t.Fatalf("X303: %d fixes in one call (cap 2000)", len(r.Fixes))
		}
		seen += len(r.Fixes)
		if len(r.Fixes) == 0 {
			break
		}
		since = Itoa(int(r.Fixes[len(r.Fixes)-1].T) + 1)
	}
	if seen == 0 {
		t.Error("X303: known guid returned no fixes at all")
	}
}

func TestX401_ChartRewriteIsIgnored(t *testing.T) {
	id := 80000 + int(nowMS()%9000)
	chart := func(x1 float64) obj {
		return obj{"mapId": id, "zone": "X" + runID, "x0": 0, "y0": 0, "x1": x1, "y1": 1}
	}
	p := pingBody(nil, nil)
	p["charts"] = []obj{chart(1)}
	post(t, writer(), js(t, p))
	q := pingBody(nil, nil)
	q["charts"] = []obj{chart(500)}
	post(t, writer(), js(t, q))
	a := get(t, viewer(), "/v1/charts")
	var c struct {
		Charts []struct {
			MapID int     `json:"mapId"`
			X1    float64 `json:"x1"`
		} `json:"charts"`
	}
	if err := json.Unmarshal(a.Body, &c); err != nil {
		t.Fatal(err)
	}
	for _, ch := range c.Charts {
		if ch.MapID == id {
			if ch.X1 != 1 {
				t.Errorf("X401: rectangle rewritten, x1=%v", ch.X1)
			}
			return
		}
	}
	t.Error("X401: chart missing")
}

func TestX501_FloodIsLimitedAndServiceRecovers(t *testing.T) {
	if tgt.RateURL == "" || tgt.RateIngestKey == "" {
		t.Skip("X501 needs the rate-limited instance")
	}
	ra := IngestKeyHolder(tgt.RateURL, tgt.RateIngestKey)
	var mu sync.Mutex
	counts := map[int]int{}
	var wg sync.WaitGroup
	for w := 0; w < 20; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				a, err := ra.Do("POST", "/v1/pings", js(t, pingBody(nil, nil)), map[string]string{"Content-Type": "application/json"})
				if err != nil {
					continue
				}
				if l := Leaks(a); len(l) > 0 {
					t.Errorf("X501 leak %v", l)
				}
				mu.Lock()
				counts[a.Status]++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	for s := range counts {
		if s >= 500 {
			t.Errorf("X501: %d x %d from our code", counts[s], s)
		}
	}
	if counts[429] == 0 {
		t.Errorf("X501: 500 requests in a burst never limited: %v", counts)
	}
	// Recovery: within a few seconds of backing off the service answers again.
	deadline := time.Now().Add(15 * time.Second)
	for {
		a, err := Anonymous(tgt.RateURL).Do("GET", "/v1/health", nil, nil)
		if err == nil && a.Status == 200 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("X501: service did not recover within 15 s: %v %v", err, a.Status)
		}
		time.Sleep(time.Second)
	}
}

func TestX502_SlowHeadersAreClosed(t *testing.T) {
	if testing.Short() {
		t.Skip("takes ~15 s")
	}
	host := strings.TrimPrefix(tgt.BaseURL, "http://")
	c, err := net.DialTimeout("tcp", host, 5*time.Second)
	if err != nil {
		t.Skip("not plain http: ", err)
	}
	defer func() { _ = c.Close() }()
	_, _ = c.Write([]byte("GET /v1/health HTTP/1.1\r\nHost: x\r\nX-Slow: "))
	start := time.Now()
	_ = c.SetReadDeadline(time.Now().Add(25 * time.Second))
	buf := make([]byte, 512)
	_, rerr := c.Read(buf) // a 408/close or EOF is fine; a timeout means the server kept waiting
	if ne, ok := rerr.(net.Error); ok && ne.Timeout() {
		t.Fatalf("X502: server still holding the half-sent request after %s", time.Since(start))
	}
	if d := time.Since(start); d > 16*time.Second {
		t.Errorf("X502: closed only after %s (want <= 15 s)", d)
	}
	if a := get(t, anon(), "/v1/health"); a.Status != 200 {
		t.Errorf("X502: other clients affected")
	}
}

// Own instance: it locks the test machine's IP out of that instance for 5 minutes.
func TestX105_BruteForceLocksOutAndXFFDoesNotEvade(t *testing.T) {
	lock := os.Getenv("CL_LOCK_URL")
	if lock == "" {
		t.Skip("X105 needs its own instance that ignores X-Forwarded-For (CL_LOCK_URL)")
	}
	locked := -1
	for i := 0; i < 60; i++ {
		// Every guess claims a different client; an instance that ignores XFF must still see one IP.
		a, err := SendRaw(lock, Raw{Target: "/v1/charts", Headers: [][2]string{
			{"Authorization", "Bearer clk_v_" + Itoa(10000000+i) + "_" + strings.Repeat("A", 43)},
			{"X-Forwarded-For", "203.0.113." + Itoa(i%250+1)}}})
		if err != nil {
			t.Fatal(err)
		}
		noLeak(t, "X105", a)
		if a.Status == 200 {
			t.Fatalf("X105: guess %d authenticated", i)
		}
		if a.Status == 429 && locked < 0 {
			locked = i
		}
	}
	if locked < 0 || locked > 12 {
		t.Errorf("X105: lockout after %d guesses (want <= 12, -1 = never)", locked)
	}
}
