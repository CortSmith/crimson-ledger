package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"crimsonledger/internal/domain"
	"crimsonledger/internal/store"
)

// ApplyPing stores a validated ping in one transaction. It is idempotent on p.BatchID.
func (s *Store) ApplyPing(ctx context.Context, p domain.Ping, now time.Time) (store.ApplyResult, error) {
	if err := ctx.Err(); err != nil {
		return store.ApplyResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return store.ApplyResult{}, fmt.Errorf("apply ping %s: %w", p.BatchID, err)
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit
	res, err := applyInTx(ctx, tx, p, now)
	if err != nil {
		return store.ApplyResult{}, fmt.Errorf("apply ping %s: %w", p.BatchID, err)
	}
	if res.Duplicate {
		return res, nil
	}
	if err := tx.Commit(); err != nil {
		return store.ApplyResult{}, fmt.Errorf("apply ping %s: commit: %w", p.BatchID, err)
	}
	return res, nil
}

func applyInTx(ctx context.Context, tx *sql.Tx, p domain.Ping, now time.Time) (store.ApplyResult, error) {
	seen, err := checkBatch(ctx, tx, p.BatchID)
	if err != nil || seen {
		return store.ApplyResult{Duplicate: seen}, err
	}
	steps := []func(context.Context, *sql.Tx, domain.Ping) error{upsertContacts, insertFixes, upsertCharts, insertKills, bumpVersion}
	for _, step := range steps {
		if err := step(ctx, tx, p); err != nil {
			return store.ApplyResult{}, err
		}
	}
	if err := recordBatch(ctx, tx, p.BatchID, now); err != nil {
		return store.ApplyResult{}, err
	}
	res := store.ApplyResult{Accepted: true, Contacts: len(p.Contacts), Kills: len(p.Kills)}
	for _, c := range p.Contacts {
		res.Fixes += len(c.Fixes)
	}
	return res, nil
}

func checkBatch(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	var one int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM batches WHERE batch_id = ?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check batch: %w", err)
	}
	return true, nil
}

func recordBatch(ctx context.Context, tx *sql.Tx, id string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO batches (batch_id, received_at) VALUES (?, ?)`, id, toMillis(now)); err != nil {
		return fmt.Errorf("record batch: %w", err)
	}
	return nil
}

func bumpVersion(ctx context.Context, tx *sql.Tx, p domain.Ping) error {
	const q = `INSERT INTO ping_versions (ruleset, version) VALUES (?, 1)
		ON CONFLICT (ruleset) DO UPDATE SET version = version + 1`
	if _, err := tx.ExecContext(ctx, q, string(p.Ruleset)); err != nil {
		return fmt.Errorf("bump version: %w", err)
	}
	return nil
}

func insertFixes(ctx context.Context, tx *sql.Tx, p domain.Ping) error {
	const q = `INSERT OR IGNORE INTO fixes (ruleset, guid, t, map_id, x, y, kind) VALUES (?, ?, ?, ?, ?, ?, ?)`
	stmt, err := tx.PrepareContext(ctx, q)
	if err != nil {
		return fmt.Errorf("prepare fixes: %w", err)
	}
	defer func() { _ = stmt.Close() }()
	for _, c := range p.Contacts {
		for _, f := range c.Fixes {
			if _, err := stmt.ExecContext(ctx, string(p.Ruleset), c.GUID, f.T, f.MapID, f.X, f.Y, string(f.Kind)); err != nil {
				return fmt.Errorf("insert fix of %s: %w", c.GUID, err)
			}
		}
	}
	return nil
}

func upsertCharts(ctx context.Context, tx *sql.Tx, p domain.Ping) error {
	// First write wins: a later rectangle for a known map is ignored, so a rogue key cannot move maps.
	const q = `INSERT OR IGNORE INTO charts (map_id, zone, x0, y0, x1, y1) VALUES (?, ?, ?, ?, ?, ?)`
	for _, c := range p.Charts {
		if _, err := tx.ExecContext(ctx, q, c.MapID, c.Zone, c.X0, c.Y0, c.X1, c.Y1); err != nil {
			return fmt.Errorf("insert chart %d: %w", c.MapID, err)
		}
	}
	return nil
}

func insertKills(ctx context.Context, tx *sql.Tx, p domain.Ping) error {
	const q = `INSERT OR IGNORE INTO kills (ruleset, t, killer, victim, map_id) VALUES (?, ?, ?, ?, ?)`
	for _, k := range p.Kills {
		if _, err := tx.ExecContext(ctx, q, string(p.Ruleset), k.T, k.Killer, k.Victim, k.MapID); err != nil {
			return fmt.Errorf("insert kill: %w", err)
		}
	}
	return nil
}

// words returns the distinct normalized words of a name, the search index keys.
func words(name string) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range strings.Fields(domain.NormalizeName(name)) {
		if !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}
