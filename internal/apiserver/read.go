package apiserver

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"crimsonledger/internal/domain"
	"crimsonledger/internal/store"
)

// gzipMinBytes is the smallest response worth compressing.
const gzipMinBytes = 1024

// Response bodies; field names are PLAN.md section 5.3.
type (
	sweepResponse struct {
		ServerTime int64                 `json:"serverTime"`
		Ruleset    domain.Ruleset        `json:"ruleset"`
		Contacts   []domain.SweepContact `json:"contacts"`
	}
	playerJSON struct {
		GUID      string          `json:"guid"`
		Name      string          `json:"name"`
		Reaction  domain.Reaction `json:"reaction"`
		Fixes     int             `json:"fixes"`
		FirstSeen int64           `json:"firstSeen"`
		LastSeen  int64           `json:"lastSeen"`
	}
	playersResponse struct {
		Players []playerJSON `json:"players"`
	}
	trailMapJSON struct {
		MapID int    `json:"mapId"`
		Zone  string `json:"zone"`
		Count int    `json:"count"`
	}
	trailResponse struct {
		GUID  string         `json:"guid"`
		Name  string         `json:"name"`
		Total int            `json:"total"`
		Maps  []trailMapJSON `json:"maps"`
		Fixes []domain.Fix   `json:"fixes"`
	}
	bountyResponse struct {
		Ranking []domain.KillTotal `json:"ranking"`
	}
	chartsResponse struct {
		Charts []domain.Chart `json:"charts"`
	}
)

// handleSweep is GET /v1/sweep.
func (s *server) handleSweep(w http.ResponseWriter, r *http.Request) {
	q, err := parseSweep(r)
	serveRead(s, w, r, q, err, func(ctx context.Context) (any, error) {
		now := s.now()
		rows, err := s.store.Sweep(ctx, store.SweepQuery{
			Ruleset: q.Ruleset, Now: now, MaxAge: secondsOf(q.MaxAge), MapID: q.MapID, GUID: q.GUID,
		})
		return sweepResponse{now.UnixMilli(), q.Ruleset, nonNil(rows)}, err
	})
}

// handlePlayers is GET /v1/players.
func (s *server) handlePlayers(w http.ResponseWriter, r *http.Request) {
	q, err := parsePlayers(r)
	serveRead(s, w, r, q, err, func(ctx context.Context) (any, error) {
		rows, err := s.store.SearchPlayers(ctx, q.Ruleset, q.Text, q.Limit)
		out := make([]playerJSON, 0, len(rows))
		for _, p := range rows {
			out = append(out, playerJSON(p))
		}
		return playersResponse{out}, err
	})
}

// handleTrail is GET /v1/players/{guid}/trail. An unknown guid is 404 whether it never existed or expired.
func (s *server) handleTrail(w http.ResponseWriter, r *http.Request) {
	q, err := parseTrail(r)
	serveRead(s, w, r, q, err, func(ctx context.Context) (any, error) {
		t, err := s.store.Trail(ctx, q.Ruleset, q.GUID, store.TrailFilter{MapID: q.MapID, Since: q.Since + 1, Limit: q.Limit})
		maps := make([]trailMapJSON, 0, len(t.Maps))
		for _, m := range t.Maps {
			maps = append(maps, trailMapJSON(m))
		}
		return trailResponse{t.GUID, t.Name, t.Total, maps, nonNil(t.Fixes)}, err
	})
}

// handleBounty is GET /v1/bounty.
func (s *server) handleBounty(w http.ResponseWriter, r *http.Request) {
	q, err := parseBounty(r)
	serveRead(s, w, r, q, err, func(ctx context.Context) (any, error) {
		rows, err := s.store.Bounty(ctx, q.Ruleset, q.Limit)
		return bountyResponse{nonNil(rows)}, err
	})
}

// handleCharts is GET /v1/charts. Map rectangles are shared by all rulesets, so the ETag comes from the body.
func (s *server) handleCharts(w http.ResponseWriter, r *http.Request) {
	if _, err := strictParams(r, paramSpec{}); err != nil {
		writeError(w, r, http.StatusBadRequest, codeInvalidRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), storeTimeout)
	defer cancel()
	rows, err := s.store.Charts(ctx)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	body, err := json.Marshal(chartsResponse{nonNil(rows)})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	tag := etagOfBody(body)
	if notModified(w, r, tag, cacheCharts) {
		return
	}
	writeTagged(w, r, tag, cacheCharts, body)
}

// serveRead is the one skeleton of the ruleset reads: version, ETag, 304 or fetch, write.
// parseErr is the result of the typed parser; fetch runs only when the client has no current copy.
func serveRead[Q readQuery](s *server, w http.ResponseWriter, r *http.Request, q Q, parseErr error, fetch func(context.Context) (any, error)) {
	if parseErr != nil {
		writeError(w, r, http.StatusBadRequest, codeInvalidRequest, parseErr.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), storeTimeout)
	defer cancel()
	version, err := s.store.Version(ctx, q.scope())
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	tag := etagFor(q, version, s.now())
	if notModified(w, r, tag, cacheRevalidate) {
		return
	}
	v, err := fetch(ctx)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, codeNotFound, "not found")
		return
	}
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	body, err := json.Marshal(v)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeTagged(w, r, tag, cacheRevalidate, body)
}

// writeTagged sends a 200 JSON body with its ETag, gzipped when the client accepts it and the body is large.
func writeTagged(w http.ResponseWriter, r *http.Request, tag, cache string, body []byte) {
	h := w.Header()
	h.Set("Content-Type", jsonContentType)
	h.Set("ETag", tag)
	h.Set("Cache-Control", cache)
	h.Add("Vary", "Accept-Encoding")
	if len(body) >= gzipMinBytes && acceptsGzip(r) {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		_, _ = zw.Write(body)
		if zw.Close() == nil {
			body = buf.Bytes()
			h.Set("Content-Encoding", "gzip")
		}
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.EqualFold(name, "gzip") && !strings.Contains(strings.ReplaceAll(params, " ", ""), "q=0") {
			return true
		}
	}
	return false
}

// nonNil makes a nil slice encode as [] instead of null.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
