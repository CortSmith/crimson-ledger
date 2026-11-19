package domain

import (
	"math"
	"strconv"
	"time"
	"unicode"
	"unicode/utf8"
)

// ValidationError names the exact JSON path that failed, e.g. "contacts[3].fixes[0].x".
type ValidationError struct{ Path, Reason string }

// Error implements error.
func (e *ValidationError) Error() string { return e.Path + " " + e.Reason }

func fail(path, reason string) *ValidationError { return &ValidationError{Path: path, Reason: reason} }

func idx(base string, i int) string { return base + "[" + strconv.Itoa(i) + "]" }

// under prefixes the path of a failure found in a nested value. Paths are only
// built on failure so the accepting path allocates no strings.
func under(prefix string, err *ValidationError) *ValidationError {
	if err == nil {
		return nil
	}
	if err.Path == "" || err.Path[0] == '[' {
		err.Path = prefix + err.Path
	} else {
		err.Path = prefix + "." + err.Path
	}
	return err
}

// ValidatePing is the single gate for untrusted input. It returns the first
// failure as a *ValidationError, or nil.
func ValidatePing(p Ping, now time.Time) error {
	if err := validateHeader(&p, now); err != nil {
		return err
	}
	if len(p.Contacts) > MaxContacts {
		return fail("contacts", "too many (max 200)")
	}
	seen := make(map[string]struct{}, len(p.Contacts))
	for i := range p.Contacts {
		if err := validateContact(&p.Contacts[i], now, seen); err != nil {
			return under(idx("contacts", i), err)
		}
	}
	if err := validateCharts(p.Charts); err != nil {
		return err
	}
	if err := validateKills(p.Kills, now); err != nil {
		return err
	}
	return nil
}

func validateHeader(p *Ping, now time.Time) *ValidationError {
	if !IsUUIDv4(p.BatchID) {
		return fail("batchId", "must be a UUIDv4")
	}
	if len(p.Source) > MaxSourceLen || !sourceRE.MatchString(p.Source) {
		return fail("source", "must match [A-Za-z0-9_.-]{1,32}")
	}
	if !p.Ruleset.Valid() {
		return fail("ruleset", "unknown ruleset")
	}
	skew := now.UnixMilli() - p.SentAt
	if skew > MaxClockSkew.Milliseconds() || skew < -MaxClockSkew.Milliseconds() {
		return fail("sentAt", "clock skew too large")
	}
	return nil
}

func validateContact(c *Contact, now time.Time, seen map[string]struct{}) *ValidationError {
	if _, ok := ParseGUID(c.GUID); !ok {
		return fail("guid", "malformed GUID")
	}
	if _, dup := seen[c.GUID]; dup {
		return fail("guid", "duplicate in ping")
	}
	seen[c.GUID] = struct{}{}
	if err := validateText("name", c.Name, MaxNameRunes, false); err != nil {
		return err
	}
	if err := validateText("raw", c.Raw, MaxRawRunes, true); err != nil {
		return err
	}
	if !c.Reaction.Valid() {
		return fail("reaction", "unknown reaction")
	}
	if err := validateTime("lastSeen", c.LastSeen, now); err != nil {
		return err
	}
	if len(c.Fixes) > MaxFixesPerContact {
		return fail("fixes", "too many (max 50)")
	}
	for j := range c.Fixes {
		if err := validateFix(&c.Fixes[j], now); err != nil {
			return under(idx("fixes", j), err)
		}
	}
	return nil
}

func validateFix(f *Fix, now time.Time) *ValidationError {
	if err := validateTime("t", f.T, now); err != nil {
		return err
	}
	if f.MapID <= 0 {
		return fail("mapId", "must be positive")
	}
	if err := validateCoord("x", f.X); err != nil {
		return err
	}
	if err := validateCoord("y", f.Y); err != nil {
		return err
	}
	if !f.Kind.Valid() {
		return fail("kind", "unknown kind")
	}
	return nil
}

func validateCharts(charts []Chart) *ValidationError {
	if len(charts) > MaxCharts {
		return fail("charts", "too many (max 50)")
	}
	for i := range charts {
		if err := validateChart(&charts[i]); err != nil {
			return under(idx("charts", i), err)
		}
	}
	return nil
}

func validateChart(c *Chart) *ValidationError {
	if c.MapID <= 0 {
		return fail("mapId", "must be positive")
	}
	if err := validateText("zone", c.Zone, MaxZoneRunes, false); err != nil {
		return err
	}
	for _, f := range [...]struct {
		name string
		v    float64
	}{{"x0", c.X0}, {"y0", c.Y0}, {"x1", c.X1}, {"y1", c.Y1}} {
		if err := validateCoord(f.name, f.v); err != nil {
			return err
		}
	}
	return nil
}

func validateKills(kills []Kill, now time.Time) *ValidationError {
	if len(kills) > MaxKills {
		return fail("kills", "too many (max 200)")
	}
	for i := range kills {
		if err := validateKill(&kills[i], now); err != nil {
			return under(idx("kills", i), err)
		}
	}
	return nil
}

func validateKill(k *Kill, now time.Time) *ValidationError {
	if err := validateTime("t", k.T, now); err != nil {
		return err
	}
	if _, ok := ParseGUID(k.Killer); !ok {
		return fail("killer", "malformed GUID")
	}
	if _, ok := ParseGUID(k.Victim); !ok {
		return fail("victim", "malformed GUID")
	}
	if k.Killer == k.Victim {
		return fail("victim", "must differ from killer")
	}
	if k.MapID <= 0 {
		return fail("mapId", "must be positive")
	}
	return nil
}

func validateTime(path string, t int64, now time.Time) *ValidationError {
	n := now.UnixMilli()
	if t < n-MaxFixAge.Milliseconds() || t > n+MaxFixFuture.Milliseconds() {
		return fail(path, "outside [now-24h, now+5m]")
	}
	return nil
}

func validateCoord(path string, v float64) *ValidationError {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > MaxCoord {
		return fail(path, "out of range [0,100]")
	}
	return nil
}

// validateText rejects over-long text, invalid UTF-8, control characters and
// format characters (zero-width, bidi overrides). Empty is rejected unless allowEmpty.
func validateText(path, s string, maxRunes int, allowEmpty bool) *ValidationError {
	if s == "" {
		if allowEmpty {
			return nil
		}
		return fail(path, "must not be empty")
	}
	if len(s) > maxRunes*utf8.UTFMax || utf8.RuneCountInString(s) > maxRunes {
		return fail(path, "too long")
	}
	if !utf8.ValidString(s) {
		return fail(path, "invalid UTF-8")
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return fail(path, "contains control or format characters")
		}
	}
	return nil
}
