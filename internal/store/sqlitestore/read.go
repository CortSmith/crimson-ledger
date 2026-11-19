package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"crimsonledger/internal/domain"
	"crimsonledger/internal/store"
)

// Sweep SQL is chosen between four fixed strings (map filter x guid filter); nothing is concatenated.
const (
	sweepCols  = `SELECT guid, name, reaction, last_seen, last_map, last_x, last_y, last_kind FROM contacts WHERE ruleset = ? AND last_seen >= ? AND last_map IS NOT NULL`
	sweepOrder = ` ORDER BY last_seen DESC, guid ASC LIMIT ?`
	sweepAll   = sweepCols + sweepOrder
	sweepMap   = sweepCols + ` AND last_map = ?` + sweepOrder
	sweepGUID  = sweepCols + ` AND guid = ?` + sweepOrder
	sweepBoth  = sweepCols + ` AND last_map = ? AND guid = ?` + sweepOrder
)

// Sweep returns contacts seen within MaxAge of q.Now, newest first.
func (s *Store) Sweep(ctx context.Context, q store.SweepQuery) ([]domain.SweepContact, error) {
	age := q.MaxAge
	if age <= 0 {
		age = store.DefaultSweepAge
	}
	age = min(age, store.MaxSweepAge)
	args := []any{string(q.Ruleset), toMillis(q.Now.Add(-age))}
	query := sweepAll
	switch {
	case q.MapID != nil && q.GUID != "":
		query, args = sweepBoth, append(args, *q.MapID, q.GUID)
	case q.MapID != nil:
		query, args = sweepMap, append(args, *q.MapID)
	case q.GUID != "":
		query, args = sweepGUID, append(args, q.GUID)
	}
	rows, err := s.db.QueryContext(ctx, query, append(args, store.MaxSweepRows)...)
	if err != nil {
		return nil, fmt.Errorf("sweep: %w", err)
	}
	return collect(rows, "sweep", func(r *sql.Rows) (c domain.SweepContact, err error) {
		var reaction, kind string
		err = r.Scan(&c.GUID, &c.Name, &reaction, &c.LastSeen, &c.MapID, &c.X, &c.Y, &kind)
		c.Reaction, c.Kind = domain.Reaction(reaction), domain.FixKind(kind)
		return c, err
	})
}

const searchSQL = `SELECT c.guid, c.name, c.reaction, c.first_seen, c.last_seen,
	(SELECT COUNT(*) FROM fixes f WHERE f.ruleset = c.ruleset AND f.guid = c.guid)
	FROM contacts c
	WHERE c.ruleset = ? AND c.guid IN (SELECT guid FROM contact_words WHERE ruleset = ? AND word >= ? AND word < ?)
	ORDER BY c.last_seen DESC, c.guid ASC LIMIT ?`

// maxSearchScan bounds the candidates a multi-word query filters in Go.
const maxSearchScan = 500

// SearchPlayers finds contacts where the normalized q is a prefix of any word of the normalized name.
func (s *Store) SearchPlayers(ctx context.Context, rs domain.Ruleset, q string, limit int) ([]store.PlayerSummary, error) {
	norm := domain.NormalizeName(q)
	if len([]rune(norm)) < store.MinSearchRunes {
		return nil, nil
	}
	if limit <= 0 || limit > store.MaxPlayerRows {
		limit = store.MaxPlayerRows
	}
	first := strings.Fields(norm)[0]
	rows, err := s.db.QueryContext(ctx, searchSQL, string(rs), string(rs), first, first+"\U0010FFFF", maxSearchScan)
	if err != nil {
		return nil, fmt.Errorf("search players: %w", err)
	}
	all, err := collect(rows, "search players", func(r *sql.Rows) (p store.PlayerSummary, err error) {
		var reaction string
		err = r.Scan(&p.GUID, &p.Name, &reaction, &p.FirstSeen, &p.LastSeen, &p.Fixes)
		p.Reaction = domain.Reaction(reaction)
		return p, err
	})
	if err != nil {
		return nil, err
	}
	var out []store.PlayerSummary
	for _, p := range all {
		if hasWordPrefix(domain.NormalizeName(p.Name), norm) && len(out) < limit {
			out = append(out, p)
		}
	}
	return out, nil
}

// hasWordPrefix reports whether prefix starts at the beginning of any word of name.
func hasWordPrefix(name, prefix string) bool {
	for i := 0; i < len(name); i++ {
		if (i == 0 || name[i-1] == ' ') && strings.HasPrefix(name[i:], prefix) {
			return true
		}
	}
	return false
}

// Bounty ranks killers by kills, then newest kill, then guid.
func (s *Store) Bounty(ctx context.Context, rs domain.Ruleset, limit int) ([]domain.KillTotal, error) {
	const q = `SELECT k.killer, COALESCE(c.name, k.killer), COUNT(*), MAX(k.t)
		FROM kills k LEFT JOIN contacts c ON c.ruleset = k.ruleset AND c.guid = k.killer
		WHERE k.ruleset = ? GROUP BY k.killer
		ORDER BY COUNT(*) DESC, MAX(k.t) DESC, k.killer ASC LIMIT ?`
	if limit <= 0 || limit > store.MaxBountyRows {
		limit = store.MaxBountyRows
	}
	rows, err := s.db.QueryContext(ctx, q, string(rs), limit)
	if err != nil {
		return nil, fmt.Errorf("bounty: %w", err)
	}
	return collect(rows, "bounty", func(r *sql.Rows) (k domain.KillTotal, err error) {
		err = r.Scan(&k.GUID, &k.Name, &k.Kills, &k.LastKill)
		return k, err
	})
}

// Charts returns every map rectangle ordered by map id.
func (s *Store) Charts(ctx context.Context) ([]domain.Chart, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT map_id, zone, x0, y0, x1, y1 FROM charts ORDER BY map_id`)
	if err != nil {
		return nil, fmt.Errorf("charts: %w", err)
	}
	return collect(rows, "charts", func(r *sql.Rows) (c domain.Chart, err error) {
		err = r.Scan(&c.MapID, &c.Zone, &c.X0, &c.Y0, &c.X1, &c.Y1)
		return c, err
	})
}

// Trail returns a contact's fixes newest first plus per-map counts.
func (s *Store) Trail(ctx context.Context, rs domain.Ruleset, guid string, f store.TrailFilter) (store.Trail, error) {
	t := store.Trail{GUID: guid}
	err := s.db.QueryRowContext(ctx, `SELECT name FROM contacts WHERE ruleset = ? AND guid = ?`, string(rs), guid).Scan(&t.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return store.Trail{}, fmt.Errorf("trail %s: %w", guid, store.ErrNotFound)
	}
	if err != nil {
		return store.Trail{}, fmt.Errorf("trail %s: %w", guid, err)
	}
	if t.Maps, err = s.trailMaps(ctx, rs, guid); err != nil {
		return store.Trail{}, err
	}
	for _, m := range t.Maps {
		t.Total += m.Count
	}
	if t.Fixes, err = s.trailFixes(ctx, rs, guid, f); err != nil {
		return store.Trail{}, err
	}
	return t, nil
}

func (s *Store) trailMaps(ctx context.Context, rs domain.Ruleset, guid string) ([]store.TrailMap, error) {
	const q = `SELECT f.map_id, COALESCE(c.zone, ''), COUNT(*) FROM fixes f LEFT JOIN charts c ON c.map_id = f.map_id
		WHERE f.ruleset = ? AND f.guid = ? GROUP BY f.map_id ORDER BY COUNT(*) DESC, f.map_id ASC`
	rows, err := s.db.QueryContext(ctx, q, string(rs), guid)
	if err != nil {
		return nil, fmt.Errorf("trail maps %s: %w", guid, err)
	}
	return collect(rows, "trail maps", func(r *sql.Rows) (m store.TrailMap, err error) {
		err = r.Scan(&m.MapID, &m.Zone, &m.Count)
		return m, err
	})
}

func (s *Store) trailFixes(ctx context.Context, rs domain.Ruleset, guid string, f store.TrailFilter) ([]domain.Fix, error) {
	const q = `SELECT t, map_id, x, y, kind FROM fixes WHERE ruleset = ? AND guid = ? AND t >= ? AND (?4 IS NULL OR map_id = ?4)
		ORDER BY t DESC, map_id ASC LIMIT ?`
	limit := f.Limit
	if limit <= 0 || limit > store.MaxTrailRows {
		limit = store.MaxTrailRows
	}
	var mapID any
	if f.MapID != nil {
		mapID = *f.MapID
	}
	rows, err := s.db.QueryContext(ctx, q, string(rs), guid, f.Since, mapID, limit)
	if err != nil {
		return nil, fmt.Errorf("trail fixes %s: %w", guid, err)
	}
	return collect(rows, "trail fixes", func(r *sql.Rows) (x domain.Fix, err error) {
		var kind string
		err = r.Scan(&x.T, &x.MapID, &x.X, &x.Y, &kind)
		x.Kind = domain.FixKind(kind)
		return x, err
	})
}

// collect scans every row with scan, closing rows.
func collect[T any](rows *sql.Rows, what string, scan func(*sql.Rows) (T, error)) ([]T, error) {
	defer func() { _ = rows.Close() }()
	var out []T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("%s: scan: %w", what, err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	return out, nil
}
