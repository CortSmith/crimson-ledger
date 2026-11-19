package apiserver

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"crimsonledger/internal/domain"
	"crimsonledger/internal/store"
)

// Query limits (T07 Rules).
const (
	// maxRawQueryBytes bounds the whole query string before it is parsed.
	maxRawQueryBytes = 1024
	// maxSearchRunes is the longest player search text (a WoW name is at most 12 letters; PLAN 5.2 allows 24).
	maxSearchRunes = 24
	// Default and maximum row counts per endpoint (PLAN.md section 5.1).
	defaultPlayerLimit, maxPlayerLimit = 20, store.MaxPlayerRows
	defaultTrailLimit, maxTrailLimit   = 500, store.MaxTrailRows
	defaultBountyLimit, maxBountyLimit = 25, store.MaxBountyRows
	// maxMapID bounds map ids; real ids are below 10 000.
	maxMapID = 1 << 20
)

// paramSpec lists the allowed query parameter names and the validator of each.
type paramSpec map[string]func(string) error

// badParamError is a 400 naming the offending parameter and why.
type badParamError struct{ name, reason string }

func (e *badParamError) Error() string {
	return fmt.Sprintf("query parameter %q: %s", e.name, e.reason)
}

func badParam(name, reason string) error { return &badParamError{name, reason} }

// strictParams parses the query string and rejects unknown, repeated and malformed parameters.
// A missing parameter is not an error here: required ones are checked by the typed parsers.
func strictParams(r *http.Request, spec paramSpec) (url.Values, error) {
	if len(r.URL.RawQuery) > maxRawQueryBytes {
		return nil, badParam("query", fmt.Sprintf("longer than %d bytes", maxRawQueryBytes))
	}
	v, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, badParam("query", "malformed")
	}
	for name, vals := range v {
		check, ok := spec[name]
		switch {
		case !ok:
			return nil, badParam(name, "unknown parameter")
		case len(vals) != 1:
			return nil, badParam(name, "must appear once")
		}
		if err := check(vals[0]); err != nil {
			return nil, badParam(name, err.Error())
		}
	}
	return v, nil
}

func validRuleset(s string) error {
	if !domain.Ruleset(s).Valid() {
		return errors.New("must be normal, pvp, rp or rppvp")
	}
	return nil
}

// validInt accepts a base-10 integer in [lo, hi]. The message names the range so clients can correct themselves.
func validInt(lo, hi int) func(string) error {
	return func(s string) error {
		n, err := strconv.Atoi(s)
		if err != nil || n < lo || n > hi {
			return fmt.Errorf("must be a whole number from %d to %d", lo, hi)
		}
		return nil
	}
}

func validSince(s string) error {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return errors.New("must be epoch milliseconds, zero or more")
	}
	return nil
}

func validGUIDParam(s string) error {
	if _, ok := domain.ParseGUID(s); !ok {
		return errors.New("not a valid guid")
	}
	return nil
}

func validSearch(s string) error {
	if strings.ContainsFunc(s, unicode.IsControl) || !utf8.ValidString(s) {
		return errors.New("contains control or invalid characters")
	}
	n := utf8.RuneCountInString(domain.NormalizeName(s))
	if n < store.MinSearchRunes || n > maxSearchRunes {
		return fmt.Errorf("must be %d to %d characters", store.MinSearchRunes, maxSearchRunes)
	}
	return nil
}

// rulesetParam is required on every ruleset-scoped endpoint: there is no "all" default.
func rulesetParam(v url.Values) (domain.Ruleset, error) {
	if !v.Has("ruleset") {
		return "", badParam("ruleset", "is required")
	}
	return domain.Ruleset(v.Get("ruleset")), nil
}

// intParam reads an optional integer parameter that validInt already checked.
func intParam(v url.Values, name string, def int) int {
	if !v.Has(name) {
		return def
	}
	n, _ := strconv.Atoi(v.Get(name))
	return n
}

// readQuery is what the shared read skeleton needs from a parsed query.
type readQuery interface {
	// scope is the ruleset the ETag version is read for.
	scope() domain.Ruleset
	// canonical is a stable text form of the query, used in the ETag.
	canonical() string
	// timeBound reports whether the answer changes with the clock, not only with writes.
	timeBound() bool
}

type sweepQuery struct {
	Ruleset domain.Ruleset
	MaxAge  int // seconds
	MapID   *int
	GUID    string
}

func (q sweepQuery) scope() domain.Ruleset { return q.Ruleset }
func (q sweepQuery) timeBound() bool       { return true }
func (q sweepQuery) canonical() string {
	m := ""
	if q.MapID != nil {
		m = strconv.Itoa(*q.MapID)
	}
	return fmt.Sprintf("sweep|%s|%d|%s|%s", q.Ruleset, q.MaxAge, m, q.GUID)
}

func parseSweep(r *http.Request) (sweepQuery, error) {
	v, err := strictParams(r, paramSpec{
		"ruleset": validRuleset, "zone": validInt(0, maxMapID), "guid": validGUIDParam,
		"maxAgeSec": validInt(1, int(store.MaxSweepAge.Seconds())),
	})
	if err != nil {
		return sweepQuery{}, err
	}
	rs, err := rulesetParam(v)
	if err != nil {
		return sweepQuery{}, err
	}
	q := sweepQuery{Ruleset: rs, MaxAge: intParam(v, "maxAgeSec", int(store.DefaultSweepAge.Seconds())), GUID: v.Get("guid")}
	if v.Has("zone") {
		zone := intParam(v, "zone", 0)
		q.MapID = &zone
	}
	return q, nil
}

type playersQuery struct {
	Ruleset domain.Ruleset
	Text    string // normalized
	Limit   int
}

func (q playersQuery) scope() domain.Ruleset { return q.Ruleset }
func (q playersQuery) timeBound() bool       { return false }
func (q playersQuery) canonical() string {
	return fmt.Sprintf("players|%s|%s|%d", q.Ruleset, q.Text, q.Limit)
}

func parsePlayers(r *http.Request) (playersQuery, error) {
	v, err := strictParams(r, paramSpec{"ruleset": validRuleset, "q": validSearch, "limit": validInt(1, maxPlayerLimit)})
	if err != nil {
		return playersQuery{}, err
	}
	rs, err := rulesetParam(v)
	if err != nil {
		return playersQuery{}, err
	}
	if !v.Has("q") {
		return playersQuery{}, badParam("q", "is required")
	}
	return playersQuery{rs, domain.NormalizeName(v.Get("q")), intParam(v, "limit", defaultPlayerLimit)}, nil
}

type trailQuery struct {
	Ruleset domain.Ruleset
	GUID    string
	MapID   *int
	Since   int64 // epoch ms, exclusive
	Limit   int
}

func (q trailQuery) scope() domain.Ruleset { return q.Ruleset }
func (q trailQuery) timeBound() bool       { return false }
func (q trailQuery) canonical() string {
	m := ""
	if q.MapID != nil {
		m = strconv.Itoa(*q.MapID)
	}
	return fmt.Sprintf("trail|%s|%s|%s|%d|%d", q.Ruleset, q.GUID, m, q.Since, q.Limit)
}

func parseTrail(r *http.Request) (trailQuery, error) {
	guid := r.PathValue("guid")
	if _, ok := domain.ParseGUID(guid); !ok {
		return trailQuery{}, badParam("guid", "not a valid guid")
	}
	v, err := strictParams(r, paramSpec{
		"ruleset": validRuleset, "map": validInt(0, maxMapID), "limit": validInt(1, maxTrailLimit), "since": validSince,
	})
	if err != nil {
		return trailQuery{}, err
	}
	rs, err := rulesetParam(v)
	if err != nil {
		return trailQuery{}, err
	}
	q := trailQuery{Ruleset: rs, GUID: guid, Limit: intParam(v, "limit", defaultTrailLimit)}
	if v.Has("map") {
		m := intParam(v, "map", 0)
		q.MapID = &m
	}
	if v.Has("since") {
		q.Since, _ = strconv.ParseInt(v.Get("since"), 10, 64)
	}
	return q, nil
}

type bountyQuery struct {
	Ruleset domain.Ruleset
	Limit   int
}

func (q bountyQuery) scope() domain.Ruleset { return q.Ruleset }
func (q bountyQuery) timeBound() bool       { return false }
func (q bountyQuery) canonical() string     { return fmt.Sprintf("bounty|%s|%d", q.Ruleset, q.Limit) }

func parseBounty(r *http.Request) (bountyQuery, error) {
	v, err := strictParams(r, paramSpec{"ruleset": validRuleset, "limit": validInt(1, maxBountyLimit)})
	if err != nil {
		return bountyQuery{}, err
	}
	rs, err := rulesetParam(v)
	if err != nil {
		return bountyQuery{}, err
	}
	return bountyQuery{rs, intParam(v, "limit", defaultBountyLimit)}, nil
}

func secondsOf(n int) time.Duration { return time.Duration(n) * time.Second }
