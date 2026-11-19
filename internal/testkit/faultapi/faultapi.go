// Package faultapi is an adversarial HTTP server for client tests: it behaves like the Crimson Ledger
// API until one fault mode is switched on, then misbehaves in exactly one documented way. It binds
// 127.0.0.1 only. Mode names (the Mode* constants) are the strings used in the T17 matrices and in
// the T18/T19 suites.
//
//	srv := faultapi.New(t, faultapi.Status(503))
//	resp, err := srv.Client().Get(srv.URL + "/v1/sweep")
package faultapi

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Mode names, one per fault.
const (
	ModeStatus           = "Status"
	ModeSlowBody         = "SlowBody"
	ModeSlowHeaders      = "SlowHeaders"
	ModeTruncate         = "Truncate"
	ModeWrongContentType = "WrongContentType"
	ModeHugeBody         = "HugeBody"
	ModeRedirect         = "Redirect"
	ModeRetryAfter       = "RetryAfter"
	ModeFlaky            = "Flaky"
	ModeSchemaDrift      = "SchemaDrift"
	ModeEvilStrings      = "EvilStrings"
	ModeNaNCoords        = "NaNCoords"
	ModeDeepJSON         = "DeepJSON"
	ModeCloseMidBody     = "CloseMidBody"
	ModeTLSBadCert       = "TLSBadCert"
	ModeHTTPDowngrade    = "HTTPDowngrade"
)

// config is what the options fill in.
type config struct {
	mode   string
	status int
	delay  time.Duration
	n      int
	m      int
	to     string
}

// Option switches on one fault mode.
type Option func(*config)

// Status answers every request with the given HTTP status and a standard error body.
func Status(code int) Option { return func(c *config) { c.mode, c.status = ModeStatus, code } }

// SlowBody sends the headers at once, then waits d before the body.
func SlowBody(d time.Duration) Option { return func(c *config) { c.mode, c.delay = ModeSlowBody, d } }

// SlowHeaders waits d before sending anything.
func SlowHeaders(d time.Duration) Option {
	return func(c *config) { c.mode, c.delay = ModeSlowHeaders, d }
}

// Truncate promises a longer body than it sends: n bytes, then the connection closes.
func Truncate(n int) Option { return func(c *config) { c.mode, c.n = ModeTruncate, n } }

// WrongContentType serves valid JSON labelled text/html.
func WrongContentType() Option { return func(c *config) { c.mode = ModeWrongContentType } }

// HugeBody streams n bytes of JSON.
func HugeBody(n int) Option { return func(c *config) { c.mode, c.n = ModeHugeBody, n } }

// Redirect answers 302 to the given location.
func Redirect(to string) Option { return func(c *config) { c.mode, c.to = ModeRedirect, to } }

// RetryAfter answers 429 with a Retry-After of sec seconds.
func RetryAfter(sec int) Option { return func(c *config) { c.mode, c.n = ModeRetryAfter, sec } }

// Flaky fails (503) the first n requests of every m, then answers normally.
func Flaky(n, m int) Option { return func(c *config) { c.mode, c.n, c.m = ModeFlaky, n, m } }

// SchemaDrift answers 200 with unknown extra fields and required fields missing.
func SchemaDrift() Option { return func(c *config) { c.mode = ModeSchemaDrift } }

// EvilStrings answers a sweep whose names carry XSS, bidi and control characters.
func EvilStrings() Option { return func(c *config) { c.mode = ModeEvilStrings } }

// NaNCoords answers a sweep whose coordinates are NaN and 1e999 (not valid JSON numbers).
func NaNCoords() Option { return func(c *config) { c.mode = ModeNaNCoords } }

// DeepJSON answers 200 with arrays nested depth levels deep.
func DeepJSON(depth int) Option { return func(c *config) { c.mode, c.n = ModeDeepJSON, depth } }

// CloseMidBody sends half of a body and then drops the connection.
func CloseMidBody() Option { return func(c *config) { c.mode = ModeCloseMidBody } }

// TLSBadCert serves HTTPS with a self-signed certificate that default clients reject.
func TLSBadCert() Option { return func(c *config) { c.mode = ModeTLSBadCert } }

// HTTPDowngrade serves HTTPS but redirects every request to a plain-HTTP sibling server.
func HTTPDowngrade() Option { return func(c *config) { c.mode = ModeHTTPDowngrade } }

// Server is a running fault server.
type Server struct {
	// URL is the base URL (http:// or https://), always on 127.0.0.1.
	URL string

	ts       *httptest.Server
	sibling  *httptest.Server
	requests atomic.Int64
	done     chan struct{}
	cfg      config
}

// New starts a server in the requested mode (no option: healthy) and closes it with the test.
func New(t testing.TB, opt ...Option) *Server {
	t.Helper()
	s := &Server{done: make(chan struct{})}
	for _, o := range opt {
		o(&s.cfg)
	}
	s.ts = httptest.NewUnstartedServer(http.HandlerFunc(s.serve))
	s.ts.Listener = mustLoopback(t)
	if s.cfg.mode == ModeTLSBadCert || s.cfg.mode == ModeHTTPDowngrade {
		s.ts.StartTLS()
	} else {
		s.ts.Start()
	}
	if s.cfg.mode == ModeHTTPDowngrade {
		s.sibling = httptest.NewServer(http.HandlerFunc(healthy))
		s.cfg.to = s.sibling.URL
	}
	s.URL = s.ts.URL
	t.Cleanup(s.Close)
	return s
}

// Client returns an HTTP client that trusts this server's certificate (plain for HTTP servers).
// TLSBadCert tests build their own default client instead, which must fail.
func (s *Server) Client() *http.Client { return s.ts.Client() }

// Requests is how many requests the server has received.
func (s *Server) Requests() int { return int(s.requests.Load()) }

// Close stops the server and releases any request still being delayed.
func (s *Server) Close() {
	select {
	case <-s.done:
		return
	default:
		close(s.done)
	}
	s.ts.CloseClientConnections()
	s.ts.Close()
	if s.sibling != nil {
		s.sibling.Close()
	}
}

func mustLoopback(t testing.TB) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("faultapi listen: %v", err)
	}
	return l
}

const okBody = `{"ok":true,"contacts":[]}`

func healthy(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, okBody) }

func writeJSON(w http.ResponseWriter, code int, body string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(body))
}

func (s *Server) sleep(d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-s.done:
		return false
	}
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	n := s.requests.Add(1)
	if h, ok := s.handlers()[s.cfg.mode]; ok {
		h(w, r, n)
		return
	}
	healthy(w, r)
}

type modeHandler func(w http.ResponseWriter, r *http.Request, n int64)

// fixed returns a handler that always answers the same JSON body.
func fixed(code int, body string) modeHandler {
	return func(w http.ResponseWriter, _ *http.Request, _ int64) { writeJSON(w, code, body) }
}

func (s *Server) handlers() map[string]modeHandler {
	return map[string]modeHandler{
		ModeStatus: func(w http.ResponseWriter, _ *http.Request, _ int64) {
			writeJSON(w, s.cfg.status, fmt.Sprintf(`{"error":{"code":"fault","message":"status %d"}}`, s.cfg.status))
		},
		ModeSlowHeaders: func(w http.ResponseWriter, r *http.Request, _ int64) {
			if s.sleep(s.cfg.delay) {
				healthy(w, r)
			}
		},
		ModeSlowBody:         s.slowBody,
		ModeTruncate:         s.truncate,
		ModeCloseMidBody:     s.closeMidBody,
		ModeWrongContentType: wrongContentType,
		ModeHugeBody:         func(w http.ResponseWriter, _ *http.Request, _ int64) { s.huge(w) },
		ModeRedirect:         s.redirect,
		ModeHTTPDowngrade:    s.redirect,
		ModeRetryAfter:       s.retryAfter,
		ModeFlaky:            s.flaky,
		ModeSchemaDrift:      fixed(200, `{"unexpected":{"deeply":["new"]},"contacts":[{"guid":"Player-1-ABCD","surprise":1}]}`),
		ModeEvilStrings:      fixed(200, evilSweep),
		ModeNaNCoords:        fixed(200, `{"contacts":[{"guid":"Player-1-ABCD","name":"Nan","mapId":1411,"x":NaN,"y":1e999}]}`),
		ModeDeepJSON: func(w http.ResponseWriter, _ *http.Request, _ int64) {
			writeJSON(w, 200, strings.Repeat("[", s.cfg.n)+strings.Repeat("]", s.cfg.n))
		},
	}
}

func (s *Server) slowBody(w http.ResponseWriter, _ *http.Request, _ int64) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	w.(http.Flusher).Flush()
	if s.sleep(s.cfg.delay) {
		_, _ = w.Write([]byte(okBody))
	}
}

func (s *Server) truncate(w http.ResponseWriter, _ *http.Request, _ int64) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", fmt.Sprint(s.cfg.n+1000))
	w.WriteHeader(200)
	_, _ = w.Write(bytes.Repeat([]byte("a"), s.cfg.n))
	drop(w)
}

func (s *Server) closeMidBody(w http.ResponseWriter, _ *http.Request, _ int64) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", fmt.Sprint(len(okBody)))
	w.WriteHeader(200)
	_, _ = w.Write([]byte(okBody[:len(okBody)/2]))
	drop(w)
}

func wrongContentType(w http.ResponseWriter, _ *http.Request, _ int64) {
	w.Header().Set("Content-Type", "text/html")
	_, _ = w.Write([]byte(okBody))
}

func (s *Server) redirect(w http.ResponseWriter, r *http.Request, _ int64) {
	if r.URL.Path == s.cfg.to { // the redirect target on this same server: answer, do not loop
		healthy(w, r)
		return
	}
	http.Redirect(w, r, s.cfg.to, http.StatusFound)
}

func (s *Server) retryAfter(w http.ResponseWriter, _ *http.Request, _ int64) {
	w.Header().Set("Retry-After", fmt.Sprint(s.cfg.n))
	writeJSON(w, 429, `{"error":{"code":"rate_limited","message":"slow down"}}`)
}

func (s *Server) flaky(w http.ResponseWriter, r *http.Request, n int64) {
	if int((n-1)%int64(s.cfg.m)) < s.cfg.n {
		writeJSON(w, 503, `{"error":{"code":"unavailable","message":"try again"}}`)
		return
	}
	healthy(w, r)
}

// drop closes the connection without finishing the response.
func drop(w http.ResponseWriter) {
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	if hj, ok := w.(http.Hijacker); ok {
		if c, _, err := hj.Hijack(); err == nil {
			_ = c.Close()
		}
	}
}

func (s *Server) huge(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	chunk := bytes.Repeat([]byte(" "), 32*1024)
	for sent := 0; sent < s.cfg.n; sent += len(chunk) {
		if _, err := w.Write(chunk[:min(len(chunk), s.cfg.n-sent)]); err != nil {
			return
		}
	}
}

// evilSweep carries a script tag, a right-to-left override, a NUL and an ANSI escape in names.
const evilSweep = `{"contacts":[` +
	`{"guid":"Player-1-ABCD","name":"<img src=x onerror=alert(1)>","reaction":"hostile","lastSeen":1,"mapId":1411,"x":1,"y":1,"kind":"exact"},` +
	`{"guid":"Player-1-ABCE","name":"abc\u202edef","reaction":"hostile","lastSeen":1,"mapId":1411,"x":1,"y":1,"kind":"exact"},` +
	`{"guid":"Player-1-ABCF","name":"nul\u0000\u001b[31mred","reaction":"hostile","lastSeen":1,"mapId":1411,"x":1,"y":1,"kind":"exact"}]}`
