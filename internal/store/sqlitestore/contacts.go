package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"crimsonledger/internal/domain"
)

const (
	insertContactSQL = `INSERT INTO contacts (ruleset, guid, name, raw, reaction, first_seen, last_seen, last_map, last_x, last_y, last_kind)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	// A newer ping moves the contact; a contact without fixes keeps its old position (COALESCE).
	updateContactSQL = `UPDATE contacts SET name = ?, raw = ?, reaction = ?, last_seen = ?,
		last_map = COALESCE(?, last_map), last_x = COALESCE(?, last_x), last_y = COALESCE(?, last_y), last_kind = COALESCE(?, last_kind)
		WHERE ruleset = ? AND guid = ?`
	lowerFirstSeenSQL = `UPDATE contacts SET first_seen = MIN(first_seen, ?) WHERE ruleset = ? AND guid = ?`
)

// upsertContacts writes every contact of the ping. An older ping never moves a contact backwards,
// but it may still lower first_seen (out-of-order uploads).
func upsertContacts(ctx context.Context, tx *sql.Tx, p domain.Ping) error {
	for _, c := range p.Contacts {
		if err := upsertContact(ctx, tx, p.Ruleset, c); err != nil {
			return fmt.Errorf("upsert contact %s: %w", c.GUID, err)
		}
	}
	return nil
}

func upsertContact(ctx context.Context, tx *sql.Tx, rs domain.Ruleset, c domain.Contact) error {
	newest := newestFix(c)
	var oldName string
	var oldSeen int64
	err := tx.QueryRowContext(ctx, `SELECT name, last_seen FROM contacts WHERE ruleset = ? AND guid = ?`, string(rs), c.GUID).Scan(&oldName, &oldSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return insertContact(ctx, tx, rs, c, newest)
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, lowerFirstSeenSQL, firstSeen(c), string(rs), c.GUID); err != nil {
		return err
	}
	if c.LastSeen < oldSeen {
		return nil
	}
	pos := positionArgs(newest)
	args := append([]any{c.Name, c.Raw, string(c.Reaction), c.LastSeen}, pos...)
	if _, err := tx.ExecContext(ctx, updateContactSQL, append(args, string(rs), c.GUID)...); err != nil {
		return err
	}
	if domain.NormalizeName(oldName) == domain.NormalizeName(c.Name) {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM contact_words WHERE ruleset = ? AND guid = ?`, string(rs), c.GUID); err != nil {
		return err
	}
	return insertWords(ctx, tx, rs, c)
}

func insertContact(ctx context.Context, tx *sql.Tx, rs domain.Ruleset, c domain.Contact, newest *domain.Fix) error {
	args := []any{string(rs), c.GUID, c.Name, c.Raw, string(c.Reaction), firstSeen(c), c.LastSeen}
	if _, err := tx.ExecContext(ctx, insertContactSQL, append(args, positionArgs(newest)...)...); err != nil {
		return err
	}
	return insertWords(ctx, tx, rs, c)
}

func insertWords(ctx context.Context, tx *sql.Tx, rs domain.Ruleset, c domain.Contact) error {
	for _, w := range words(c.Name) {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO contact_words (ruleset, word, guid) VALUES (?, ?, ?)`, string(rs), w, c.GUID); err != nil {
			return err
		}
	}
	return nil
}

// newestFix returns the fix with the greatest time, or nil if the contact carries none.
func newestFix(c domain.Contact) *domain.Fix {
	var best *domain.Fix
	for i := range c.Fixes {
		if best == nil || c.Fixes[i].T > best.T {
			best = &c.Fixes[i]
		}
	}
	return best
}

// positionArgs are last_map, last_x, last_y, last_kind (all NULL without a fix).
func positionArgs(f *domain.Fix) []any {
	if f == nil {
		return []any{nil, nil, nil, nil}
	}
	return []any{f.MapID, f.X, f.Y, string(f.Kind)}
}

// firstSeen is the earliest time the ping proves the contact existed.
func firstSeen(c domain.Contact) int64 {
	first := c.LastSeen
	for _, f := range c.Fixes {
		first = min(first, f.T)
	}
	return first
}
