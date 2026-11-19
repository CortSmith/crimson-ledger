//go:build integration

package abuse

import (
	"bytes"
	"compress/gzip"
	"os"
	"strings"
	"testing"
)

var jsonH = map[string]string{"Content-Type": "application/json"}

func post(t testing.TB, a Actor, body []byte) Answer {
	return do(t, a, "POST", "/v1/pings", body, jsonH)
}

func gzipOf(b []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write(b)
	_ = w.Close()
	return buf.Bytes()
}

func TestX402_TimestampsOutsideTheWindow(t *testing.T) {
	unchanged(t, "X402", func() {
		for name, ts := range map[string]int64{"future +5min+1s": nowMS() + 5*60*1000 + 1000, "far future": nowMS() + 1e12,
			"past -25h": nowMS() - 25*3600*1000, "zero": 0, "negative": -1} {
			p := pingBody([]obj{contactOf(guid(), "Timeskew", ts)}, nil)
			if a := post(t, writer(), js(t, p)); a.Status != 400 {
				t.Errorf("X402 %s: want 400, got %d", name, a.Status)
			}
		}
		p := pingBody(nil, nil)
		p["sentAt"] = nowMS() + 1e11
		if a := post(t, writer(), js(t, p)); a.Status != 400 {
			t.Errorf("X402 sentAt skew: want 400, got %d", a.Status)
		}
	})
}

func TestX403_SelfKillAndDuplicateKills(t *testing.T) {
	g := guid()
	k := obj{"t": nowMS(), "killer": g, "victim": g, "mapId": 1411}
	if a := post(t, writer(), js(t, pingBody(nil, []obj{k}))); a.Status != 400 {
		t.Errorf("X403 killer==victim: want 400, got %d %.100s", a.Status, a.Body)
	}
	v := guid()
	kill := obj{"t": nowMS(), "killer": g, "victim": v, "mapId": 1411}
	for i := 0; i < 3; i++ {
		post(t, writer(), js(t, pingBody(nil, []obj{kill, kill})))
	}
	a := get(t, viewer(), "/v1/bounty?ruleset=rp&limit=100")
	i := strings.Index(string(a.Body), g)
	if i < 0 || !strings.Contains(string(a.Body)[i:], `"kills":1,`) {
		t.Errorf("X403: duplicate/replayed kill was not counted exactly once: %.300s", a.Body)
	}
}

func TestX404_ContactFloodIsCapped(t *testing.T) {
	// 26 pings x 200 distinct contacts = 5200 > MaxContactsPerKeyHour (5000). Some ping must be refused 429.
	fk := os.Getenv("CL_FLOOD_INGEST_KEY")
	if fk == "" {
		t.Skip("CL_FLOOD_INGEST_KEY (a dedicated ingest key; the flood exhausts its hourly cap) not set")
	}
	flooder := IngestKeyHolder(tgt.BaseURL, fk)
	limited := false
	for i := 0; i < 26 && !limited; i++ {
		cs := make([]obj, 200)
		for j := range cs {
			cs[j] = contactOf(guid(), "Flood"+Itoa(i)+"x"+Itoa(j), nowMS())
		}
		if post(t, flooder, js(t, pingBody(cs, nil))).Status == 429 {
			limited = true
		}
	}
	if !limited {
		t.Errorf("X404 FINDING: 5200 distinct fake contacts from one ingest key were all accepted (no per-key contact cap)")
	}
}

func TestX405_HostileNamesAreInertOrRejected(t *testing.T) {
	for name, n := range map[string]string{"html": "<script>x</script>", "js url": "javascript:alert(1)", "bidi": "Abc‮Def",
		"zwj": "A‍b", "homoglyph": "Ｍc", "ctl": "A\x07b", "long": strings.Repeat("a", 25), "svg": "<svg/onload=1>", "nul": "a\x00b"} {
		g := guid()
		a := post(t, writer(), js(t, pingBody([]obj{contactOf(g, n, nowMS())}, nil)))
		if a.Status != 200 {
			continue // rejected: fine
		}
		s := get(t, viewer(), "/v1/sweep?ruleset=rp&guid="+g)
		if !strings.HasPrefix(s.Header.Get("Content-Type"), "application/json") {
			t.Errorf("X405 %s: stored name served as %q", name, s.Header.Get("Content-Type"))
		}
		if bytes.Contains(s.Body, []byte("<script")) || bytes.Contains(s.Body, []byte("<svg")) {
			t.Errorf("X405 %s: markup returned unescaped: %.200s", name, s.Body)
		}
		if name == "bidi" || name == "zwj" || name == "ctl" || name == "nul" {
			if bytes.Contains(s.Body, []byte(`‮`)) || bytes.Contains(s.Body, []byte(`‍`)) {
				t.Errorf("X405 %s FINDING: format/control characters stored in a name", name)
			}
		}
	}
}

func TestX406_KeySeparatorsInIdentifiers(t *testing.T) {
	for name, g := range map[string]string{"hash": "Player-1329-AB#CD", "nul": "Player-1329-AB\u0000", "colon": "Player-1329-AB:CD", "empty": ""} {
		if a := post(t, writer(), js(t, pingBody([]obj{contactOf(g, "Sepguid", nowMS())}, nil))); a.Status != 400 {
			t.Errorf("X406 guid %s: want 400, got %d", name, a.Status)
		}
	}
	g := guid()
	if a := post(t, writer(), js(t, pingBody([]obj{contactOf(g, "A#b C#rp#x W#rp#y", nowMS())}, nil))); a.Status == 200 {
		for _, q := range []string{"b", "c", "rp", "w"} {
			s := get(t, viewer(), "/v1/players?ruleset=rp&q="+q)
			if s.Status == 200 && strings.Contains(string(s.Body), "#") {
				t.Errorf("X406 FINDING: '#' survived name normalisation (search q=%s)", q)
			}
		}
	}
}

func TestX407_ReplayDoesNotDuplicate(t *testing.T) {
	g := guid()
	p := js(t, pingBody([]obj{contactOf(g, "Replayed", nowMS())}, nil))
	first := post(t, writer(), p)
	v1 := version(t)
	for i := 0; i < 3; i++ {
		a := post(t, writer(), p)
		if first.Status == 200 && !contains(a.Body, `"duplicate":true`) {
			t.Errorf("X407: replay not flagged duplicate: %.100s", a.Body)
		}
	}
	if version(t) != v1 {
		t.Errorf("X407: replays changed stored data")
	}
}

func TestX408_SourceHasNoAuthority(t *testing.T) {
	for _, src := range []string{"other-logger", "admin", "../x", "it-" + runID} {
		p := pingBody(nil, nil)
		p["source"] = src
		a := post(t, writer(), js(t, p))
		if a.Status != 200 && a.Status != 400 {
			t.Errorf("X408 source %q: status %d", src, a.Status)
		}
	}
}

func TestX409_GzipTricks(t *testing.T) {
	good := js(t, pingBody(nil, nil))
	h := map[string]string{"Content-Type": "application/json", "Content-Encoding": "gzip"}
	multi := append(gzipOf(good[:10]), gzipOf(good[10:])...)
	trailing := append(gzipOf(good), []byte("garbage")...)
	bomb := gzipOf(bytes.Repeat([]byte("a"), 64<<20))
	for name, b := range map[string][]byte{"multi-member": multi, "trailing garbage": trailing, "bomb": bomb, "not gzip": []byte("plain"), "empty": nil} {
		a := do(t, writer(), "POST", "/v1/pings", b, h)
		if a.Status == 200 && name != "multi-member" {
			t.Errorf("X409 %s: accepted", name)
		}
		if a.Status != 200 && a.Status != 400 && a.Status != 413 {
			t.Errorf("X409 %s: status %d", name, a.Status)
		}
	}
	// Content-Length lies: claims more than is sent. Server must time out/err, never accept.
	a, err := SendRaw(tgt.BaseURL, Raw{Method: "POST", Target: "/v1/pings", Body: []byte(`{"a":`),
		Headers: [][2]string{{"Authorization", "Bearer " + tgt.IngestKey}, {"Content-Type", "application/json"}, {"Content-Length", "99999"}}})
	if err == nil && a.Status == 200 {
		t.Errorf("X409 lying Content-Length accepted")
	}
	if h := get(t, anon(), "/v1/health"); h.Status != 200 {
		t.Errorf("X409: server unhealthy afterwards: %d", h.Status)
	}
}
