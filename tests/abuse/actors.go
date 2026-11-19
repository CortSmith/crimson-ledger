// Package abuse is the T19 bad-actor suite. Every test is an attack with an ID (X101...) and PASSES when the
// attack fails. Actors are thin wrappers over raw HTTP so the suite can send requests Go's client would
// normalise (duplicate headers, `//` paths, `%00`). This file has no build tag so the leak detector is unit-testable.
package abuse

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Raw is one hand-written HTTP/1.1 request. Headers are sent in order, repeated, exactly as given.
type Raw struct {
	Method  string
	Target  string      // request-target verbatim, e.g. "//v1/sweep" or "/v1/sweep%00"
	Headers [][2]string // verbatim, may repeat
	Body    []byte
}

// Answer is a parsed response.
type Answer struct {
	Status int
	Header http.Header
	Body   []byte
	Dur    time.Duration
}

// RequestID is the server's correlation id.
func (a Answer) RequestID() string { return a.Header.Get("X-Request-Id") }

// rawRequest renders r as HTTP/1.1 bytes, adding Content-Length only when the caller did not set one.
func rawRequest(host string, r Raw) []byte {
	var b bytes.Buffer
	m := r.Method
	if m == "" {
		m = "GET"
	}
	fmt.Fprintf(&b, "%s %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n", m, r.Target, host)
	hasCL := false
	for _, h := range r.Headers {
		if strings.EqualFold(h[0], "Content-Length") {
			hasCL = true
		}
		fmt.Fprintf(&b, "%s: %s\r\n", h[0], h[1])
	}
	if len(r.Body) > 0 && !hasCL {
		fmt.Fprintf(&b, "Content-Length: %d\r\n", len(r.Body))
	}
	b.WriteString("\r\n")
	b.Write(r.Body)
	return b.Bytes()
}

// SendRaw writes r to host:port (from baseURL) over a fresh TCP connection and reads one response.
func SendRaw(baseURL string, r Raw) (Answer, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return Answer{}, err
	}
	host := u.Host
	if u.Port() == "" {
		host += ":80"
	}
	conn, err := net.DialTimeout("tcp", host, 5*time.Second)
	if err != nil {
		return Answer{}, err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	b := rawRequest(u.Host, r)
	start := time.Now()
	if _, err := conn.Write(b); err != nil {
		return Answer{}, err
	}
	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return Answer{}, err
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return Answer{}, err
	}
	return Answer{Status: res.StatusCode, Header: res.Header, Body: body, Dur: time.Since(start)}, nil
}

// Actor is who is attacking: a key (maybe none) plus the base URL.
type Actor struct {
	Name, Base, Key string
	Client          *http.Client
}

func newClient() *http.Client {
	return &http.Client{Timeout: 20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// Anonymous has no key.
func Anonymous(base string) Actor { return Actor{"anonymous", base, "", newClient()} }

// ViewKeyHolder reads only.
func ViewKeyHolder(base, key string) Actor { return Actor{"view-key", base, key, newClient()} }

// IngestKeyHolder writes only.
func IngestKeyHolder(base, key string) Actor { return Actor{"ingest-key", base, key, newClient()} }

// WithKey returns the same actor holding another credential (revoked, expired, forged...).
func (a Actor) WithKey(name, key string) Actor { a.Name, a.Key = name, key; return a }

// Do sends one request through net/http. headers override the actor's Authorization.
func (a Actor) Do(method, target string, body []byte, headers map[string]string) (Answer, error) {
	req, err := http.NewRequestWithContext(context.Background(), method, a.Base+target, bytes.NewReader(body))
	if err != nil {
		return Answer{}, err
	}
	if a.Key != "" {
		req.Header.Set("Authorization", "Bearer "+a.Key)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	start := time.Now()
	res, err := a.Client.Do(req)
	if err != nil {
		return Answer{}, err
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	return Answer{res.StatusCode, res.Header, b, time.Since(start)}, err
}

// ---- generic leak detector: runs on every response in the suite ----

var leakRes = []*regexp.Regexp{
	regexp.MustCompile(`clk_[iv]_`),
	regexp.MustCompile(`(?i)sqlite`),
	regexp.MustCompile(`(?i)dynamodb`),
	regexp.MustCompile(`goroutine \d+`),
	regexp.MustCompile(`(?i)\bpanic\b`),
	regexp.MustCompile(`/home/|/var/|/tmp/|/usr/`),
	regexp.MustCompile(`arn:aws`),
	regexp.MustCompile(`\.go:\d+`),
	regexp.MustCompile(`(?i)runtime error|nil pointer|stack trace`),
	regexp.MustCompile(`(?i)pepper`),
}

// Leaks returns what in the answer must never leave the system ("" slice = clean). Headers are checked too.
func Leaks(a Answer) []string {
	var out []string
	hay := string(a.Body)
	for k, vs := range a.Header {
		hay += "\n" + k + ": " + strings.Join(vs, ",")
	}
	for _, re := range leakRes {
		if m := re.FindString(hay); m != "" {
			out = append(out, m)
		}
	}
	return out
}

// ErrorBodyShape reports whether body is exactly {"error":{"code","message","requestId"}}.
var errShape = regexp.MustCompile(`^\{"error":\{"code":"[a-z_]+","message":"[^"]*","requestId":"[^"]*"\}\}\s*$`)

// IsUniformError is true for the documented error body shape.
func IsUniformError(a Answer) bool { return errShape.Match(a.Body) }

// Itoa is a tiny helper for building payloads.
func Itoa(n int) string { return strconv.Itoa(n) }
