package apiserver

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"crimsonledger/internal/domain"
)

const (
	// storeTimeout bounds one database call made for a request (T06 handler flow, step 6).
	storeTimeout = 10 * time.Second
	// unavailableRetryAfter is the Retry-After (seconds) sent with 503 when the store is too slow.
	unavailableRetryAfter = "2"
	// MaxKillsPerKeyHour caps the kills one ingest key may report per hour (T06 policy table).
	MaxKillsPerKeyHour = 2000
	// MaxContactsPerKeyHour caps the distinct contacts one ingest key may report per hour, so a leaked
	// key cannot flood the sweep with fake contacts (T06 policy table, T19 X404).
	MaxContactsPerKeyHour = 5000
)

const codeUnavailable = "unavailable"

// Errors readBody returns; bodyError maps each to a status.
var (
	errUnsupportedEncoding = errors.New("unsupported content encoding")
	errBadGzip             = errors.New("malformed gzip body")
)

type acceptedResponse struct {
	Accepted bool `json:"accepted"`
	Contacts int  `json:"contacts"`
	Fixes    int  `json:"fixes"`
	Kills    int  `json:"kills"`
}

type duplicateResponse struct {
	Accepted  bool `json:"accepted"`
	Duplicate bool `json:"duplicate"`
}

// handlePing is POST /v1/pings. Step order (each step can only reject, never mutate):
//  1. authentication and the ingest scope (middleware, before this runs);
//  2. Content-Type must be application/json;
//  3. the body size limit (middleware) and Content-Encoding (empty or gzip), decompressed under the same limit;
//  4. strict decode and full validation, rejected with the field path only;
//  5. the per-key hourly quota, checked before and charged after a successful store call;
//  6. store.ApplyPing under storeTimeout.
//
// The client-supplied source never influences a decision; the authenticated key id goes to the log.
func (s *server) handlePing(w http.ResponseWriter, r *http.Request) {
	if !isJSON(r.Header.Get("Content-Type")) {
		writeError(w, r, http.StatusUnsupportedMediaType, codeUnsupportedMedia, "send application/json")
		return
	}
	p, err := s.readPing(r)
	if err != nil {
		s.pingError(w, r, err)
		return
	}
	key, _ := PrincipalFrom(r.Context())
	now := s.now()
	if wait := s.quota.over(key.KeyID, p, now); wait > 0 {
		writeRateLimited(w, r, wait)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), storeTimeout)
	defer cancel()
	s.warnOnChartChanges(ctx, key.KeyID, p)
	res, err := s.store.ApplyPing(ctx, p, now)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	if res.Duplicate {
		writeJSON(w, http.StatusOK, duplicateResponse{Duplicate: true})
		return
	}
	s.quota.charge(key.KeyID, p, now)
	s.log.Info("ping accepted", "requestId", requestIDOf(r), "keyId", key.KeyID,
		"contacts", res.Contacts, "fixes", res.Fixes, "kills", res.Kills)
	writeJSON(w, http.StatusOK, acceptedResponse{true, res.Contacts, res.Fixes, res.Kills})
}

// readPing decodes and validates the request body into a Ping.
func (s *server) readPing(r *http.Request) (domain.Ping, error) {
	body, finish, err := s.openBody(r)
	if err != nil {
		return domain.Ping{}, err
	}
	p, err := domain.DecodePing(body, int64(s.opts.MaxBodyBytes))
	if err != nil {
		return domain.Ping{}, err
	}
	if err := finish(); err != nil {
		return domain.Ping{}, err
	}
	return p, domain.ValidatePing(p, s.now())
}

// openBody is the only place that knows about Content-Encoding. It returns the decoded stream and a
// finish check to run once the decoder is done (a gzip body must be exactly one member with nothing after it).
func (s *server) openBody(r *http.Request) (io.Reader, func() error, error) {
	switch strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))) {
	case "", "identity":
		return r.Body, func() error { return nil }, nil
	case "gzip":
		src := bufio.NewReader(r.Body)
		gz, err := gzip.NewReader(src)
		if err != nil {
			return nil, nil, wrapGzip(err)
		}
		gz.Multistream(false)
		finish := func() error {
			if _, err := src.Peek(1); !errors.Is(err, io.EOF) {
				return errBadGzip // trailing bytes: a second member or garbage
			}
			return nil
		}
		return &gzipReader{gz}, finish, nil
	default:
		return nil, nil, errUnsupportedEncoding
	}
}

// gzipReader turns gzip format errors into errBadGzip so they map to 400, not 500.
type gzipReader struct{ gz *gzip.Reader }

func (g *gzipReader) Read(p []byte) (int, error) {
	n, err := g.gz.Read(p)
	return n, wrapGzip(err)
}

func wrapGzip(err error) error {
	var tooLarge *http.MaxBytesError
	switch {
	case err == nil, errors.Is(err, io.EOF), errors.As(err, &tooLarge):
		return err
	default:
		return errBadGzip
	}
}

// pingError maps a body, decode or validation failure to a status.
func (s *server) pingError(w http.ResponseWriter, r *http.Request, err error) {
	var tooLarge *http.MaxBytesError
	var invalid *domain.ValidationError
	switch {
	case errors.Is(err, domain.ErrTooLarge), errors.As(err, &tooLarge):
		writeError(w, r, http.StatusRequestEntityTooLarge, codeTooLarge, "request body too large")
	case errors.Is(err, errUnsupportedEncoding):
		writeError(w, r, http.StatusUnsupportedMediaType, codeUnsupportedMedia, "Content-Encoding must be gzip or absent")
	case errors.Is(err, errBadGzip):
		writeError(w, r, http.StatusBadRequest, codeInvalidRequest, "malformed gzip body")
	case errors.As(err, &invalid):
		writeError(w, r, http.StatusBadRequest, codeInvalidPing, invalid.Error())
	case errors.Is(err, domain.ErrInvalidJSON):
		writeError(w, r, http.StatusBadRequest, codeInvalidPing, "body is not a valid ping")
	default:
		writeError(w, r, http.StatusBadRequest, codeInvalidRequest, "could not read request body")
	}
}

// storeError answers 503 when the store timed out and 500 for anything else, never showing the cause.
func (s *server) storeError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, context.DeadlineExceeded) {
		s.log.Warn("store timeout", "requestId", requestIDOf(r))
		w.Header().Set("Retry-After", unavailableRetryAfter)
		writeError(w, r, http.StatusServiceUnavailable, codeUnavailable, "temporarily unavailable")
		return
	}
	s.internal(w, r, err)
}

// warnOnChartChanges logs a WARN when the ping carries a rectangle that differs from a stored one. The store
// keeps the first rectangle per map (contract rule 5), so this is only an audit trail for a rogue key.
func (s *server) warnOnChartChanges(ctx context.Context, keyID string, p domain.Ping) {
	if len(p.Charts) == 0 {
		return
	}
	known, err := s.store.Charts(ctx)
	if err != nil {
		return // best effort; ApplyPing reports real store failures
	}
	byMap := make(map[int]domain.Chart, len(known))
	for _, c := range known {
		byMap[c.MapID] = c
	}
	for _, c := range p.Charts {
		if old, ok := byMap[c.MapID]; ok && old != c {
			s.log.Warn("chart rectangle differs from the stored one; keeping the stored one", "keyId", keyID, "mapId", c.MapID)
		}
	}
}

func isJSON(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	return err == nil && mt == "application/json"
}

// writeJSON sends v as a compact JSON body with the standard headers.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", jsonContentType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
