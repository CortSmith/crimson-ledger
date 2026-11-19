//go:build integration

package integration

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var (
	tgt     Target
	runID   string        // unique per process: source name and GUID/name salt
	ipSeq   atomic.Uint32 // private client IPs for tests that provoke failed-auth lockouts
	httpCli = &http.Client{
		Timeout:       20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
)

func TestMain(m *testing.M) {
	var err error
	if tgt, err = FromEnv(os.Getenv); err != nil {
		fmt.Fprintln(os.Stderr, "integration suite:", err)
		os.Exit(2)
	}
	runID = randHex(4)
	os.Exit(m.Run())
}

func randHex(nBytes int) string {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// resp is one HTTP answer, fully read.
type resp struct {
	Status int
	Header http.Header
	Body   []byte
	Dur    time.Duration
}

// RequestID is the server's correlation id (put into every assertion message).
func (r resp) RequestID() string { return r.Header.Get("X-Request-Id") }

// ErrCode is error.code of an error body, "" if the body is not one.
func (r resp) ErrCode() string {
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(r.Body, &e)
	return e.Error.Code
}

// JSON decodes the body into v.
func (r resp) JSON(t testing.TB, v any) {
	t.Helper()
	if err := json.Unmarshal(r.Body, v); err != nil {
		t.Fatalf("body is not the expected JSON (%v) req=%s: %.200s", err, r.RequestID(), r.Body)
	}
}

// req describes one request. Zero Key means no Authorization header.
type req struct {
	Method, Path, Key string
	Header            map[string]string
	Body              []byte
	IP                string // spoofed client IP, honoured only when the target trusts X-Forwarded-For
}

func doOn(base string, r req) (resp, error) {
	if r.Method == "" {
		r.Method = http.MethodGet
	}
	hr, err := http.NewRequest(r.Method, base+r.Path, bytes.NewReader(r.Body))
	if err != nil {
		return resp{}, err
	}
	if r.Key != "" {
		hr.Header.Set("Authorization", "Bearer "+r.Key)
	}
	if r.IP != "" && tgt.SpoofIP {
		hr.Header.Set("X-Forwarded-For", r.IP)
	}
	for k, v := range r.Header {
		hr.Header.Set(k, v)
	}
	start := time.Now()
	hres, err := httpCli.Do(hr)
	if err != nil {
		return resp{}, err
	}
	defer func() { _ = hres.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(hres.Body, 8<<20))
	return resp{hres.StatusCode, hres.Header, b, time.Since(start)}, err
}

// do sends r to the main target and fails the test on a transport error.
func do(t testing.TB, r req) resp {
	t.Helper()
	out, err := doRaw(r)
	if err != nil {
		t.Fatalf("%s %s: %v", r.Method, r.Path, err)
	}
	return out
}

func doRaw(r req) (resp, error) { return doOn(tgt.BaseURL, r) }

// freshIP returns a client IP no other test uses (TEST-NET-3 range).
func freshIP() string { n := ipSeq.Add(1); return fmt.Sprintf("203.0.113.%d", n%250+1) }

func view(path string) req   { return req{Path: path, Key: tgt.ViewKey} }
func ingest(body []byte) req { return postJSON(tgt.IngestKey, body) }
func postJSON(key string, body []byte) req {
	return req{Method: http.MethodPost, Path: "/v1/pings", Key: key, Body: body, Header: map[string]string{"Content-Type": "application/json"}}
}

func gz(t testing.TB, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(b); err != nil || w.Close() != nil {
		t.Fatalf("gzip: %v", err)
	}
	return buf.Bytes()
}

// ---- builders (plain maps so tests can break any field) ----

type obj = map[string]any

func nowMS() int64 { return time.Now().UnixMilli() }

// newGUID is a valid, unique player GUID.
func newGUID() string { return "Player-1329-" + strings.ToUpper(randHex(4)) }

// uuid4 is a valid batch id.
func uuid4() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	h := hex.EncodeToString(b)
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// word returns a unique letters-only name part, e.g. "Qkzmwhtb".
func word() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	out := make([]byte, 8)
	for i, v := range b {
		out[i] = 'a' + v%26
	}
	return strings.ToUpper(string(out[:1])) + string(out[1:])
}

func fix(t int64, mapID int, x, y float64) obj {
	return obj{"t": t, "mapId": mapID, "x": x, "y": y, "kind": "exact"}
}

func contact(guid, name string, fixes ...obj) obj {
	last := nowMS()
	if len(fixes) > 0 {
		last = fixes[len(fixes)-1]["t"].(int64)
	}
	return obj{"guid": guid, "name": name, "raw": name + "-RP-US", "reaction": "hostile", "lastSeen": last, "fixes": fixes}
}

// ping builds a valid ping in ruleset rp from this run's source.
func ping(contacts []obj, kills []obj, charts []obj) obj {
	if contacts == nil {
		contacts = []obj{}
	}
	if kills == nil {
		kills = []obj{}
	}
	if charts == nil {
		charts = []obj{}
	}
	return obj{"batchId": uuid4(), "source": "it-" + runID, "ruleset": "rp", "sentAt": nowMS(),
		"contacts": contacts, "charts": charts, "kills": kills}
}

func mustJSON(t testing.TB, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// send posts a ping object with the ingest key and returns the answer.
func send(t testing.TB, p obj) resp {
	t.Helper()
	return do(t, ingest(mustJSON(t, p)))
}

func mustAccept(t testing.TB, id string, p obj) {
	t.Helper()
	r := send(t, p)
	if r.Status != 200 || !strings.Contains(string(r.Body), `"accepted":true`) {
		t.Fatalf("%s: ping not accepted: %d %s req=%s", id, r.Status, r.Body, r.RequestID())
	}
}

// eventually polls cond with backoff until timeout (no fixed sleeps in tests).
func eventually(t testing.TB, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline, wait := time.Now().Add(timeout), 20*time.Millisecond
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", timeout, what)
		}
		time.Sleep(wait)
		wait = min(wait*2, time.Second)
	}
}

// versionOf is a fingerprint of the rp state: the sweep ETag (maxAgeSec fixed so only data changes it).
func versionOf(t testing.TB) string {
	t.Helper()
	r := do(t, view("/v1/sweep?ruleset=rp"))
	return r.Header.Get("ETag")
}

type sweepBody struct {
	ServerTime int64  `json:"serverTime"`
	Ruleset    string `json:"ruleset"`
	Contacts   []struct {
		GUID     string  `json:"guid"`
		Name     string  `json:"name"`
		Reaction string  `json:"reaction"`
		LastSeen int64   `json:"lastSeen"`
		MapID    int     `json:"mapId"`
		X        float64 `json:"x"`
		Y        float64 `json:"y"`
		Kind     string  `json:"kind"`
	} `json:"contacts"`
}

func sweep(t testing.TB, query string) sweepBody {
	t.Helper()
	r := do(t, view("/v1/sweep?ruleset=rp"+query))
	if r.Status != 200 {
		t.Fatalf("sweep: %d %s req=%s", r.Status, r.Body, r.RequestID())
	}
	var s sweepBody
	r.JSON(t, &s)
	return s
}
