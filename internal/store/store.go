package store

import (
	"context"
	"errors"
	"time"

	"crimsonledger/internal/domain"
)

// Sentinel errors callers branch on.
var (
	// ErrNotFound means the requested record does not exist.
	ErrNotFound = errors.New("not found")
	// ErrConflict means the record already exists.
	ErrConflict = errors.New("conflict")
	// ErrUnavailable means the database is throttling or unreachable; callers should retry later.
	ErrUnavailable = errors.New("store unavailable")
)

// Limits shared by every implementation.
const (
	// DefaultSweepAge is the sweep window when SweepQuery.MaxAge is zero (PLAN.md section 5.1: 900 s).
	DefaultSweepAge = 15 * time.Minute
	// MaxSweepAge caps SweepQuery.MaxAge: fixes older than the 24 h ping window are not live.
	MaxSweepAge = 24 * time.Hour
	// MaxSweepRows caps one sweep response.
	MaxSweepRows = 2000
	// MaxTrailRows caps one trail response (PLAN.md section 5.1: limit <= 2000).
	MaxTrailRows = 2000
	// MaxPlayerRows caps one player search (PLAN.md section 5.1: limit <= 50).
	MaxPlayerRows = 50
	// MaxBountyRows caps one bounty ranking (PLAN.md section 5.1: limit <= 100).
	MaxBountyRows = 100
	// MinSearchRunes is the shortest search text that returns anything.
	MinSearchRunes = 2
)

// Store is the one interface the API uses to talk to a database. The contract suite in
// storetest is its specification: every implementation must pass it.
type Store interface {
	// ApplyPing stores a validated ping. It is idempotent on p.BatchID: a repeat returns
	// Duplicate=true and changes nothing.
	ApplyPing(ctx context.Context, p domain.Ping, now time.Time) (ApplyResult, error)
	// Version is a cheap number that changes whenever data for the ruleset changes; used for ETags.
	Version(ctx context.Context, rs domain.Ruleset) (int64, error)

	// Sweep returns the live contacts of a ruleset, newest first.
	Sweep(ctx context.Context, q SweepQuery) ([]domain.SweepContact, error)
	// SearchPlayers finds contacts whose normalized name has a word starting with q.
	SearchPlayers(ctx context.Context, rs domain.Ruleset, q string, limit int) ([]PlayerSummary, error)
	// Trail returns a contact's fixes, newest first. ErrNotFound for an unknown guid.
	Trail(ctx context.Context, rs domain.Ruleset, guid string, f TrailFilter) (Trail, error)
	// Bounty ranks killers by kill count.
	Bounty(ctx context.Context, rs domain.Ruleset, limit int) ([]domain.KillTotal, error)
	// Charts returns every known map rectangle.
	Charts(ctx context.Context) ([]domain.Chart, error)

	// Keys returns the API key store.
	Keys() KeyStore
	// Prune deletes fixes, kills and batch markers older than cutoff. A no-op on DynamoDB (TTL does it).
	Prune(ctx context.Context, cutoff time.Time) (PruneResult, error)
	// Close releases the database.
	Close() error
}

// KeyStore persists hashed API keys.
type KeyStore interface {
	// Put adds a key; ErrConflict if the id exists.
	Put(ctx context.Context, k KeyRecord) error
	// Get returns a key; ErrNotFound if the id is unknown.
	Get(ctx context.Context, id string) (KeyRecord, error)
	// List returns every key, oldest first.
	List(ctx context.Context) ([]KeyRecord, error)
	// SetDisabled revokes or restores a key; ErrNotFound if the id is unknown.
	SetDisabled(ctx context.Context, id string, disabled bool) error
	// Touch records last_used_at. Best effort: an implementation may throttle it.
	Touch(ctx context.Context, id string, now time.Time) error
}

// Scope is what an API key may do.
type Scope string

// Key scopes.
const (
	ScopeIngest Scope = "ingest"
	ScopeView   Scope = "view"
)

// KeyRecord is one stored API key. Hash is the verifier, never the secret itself.
type KeyRecord struct {
	ID         string
	Scope      Scope
	Label      string
	Hash       []byte
	CreatedAt  time.Time
	ExpiresAt  *time.Time
	Disabled   bool
	LastUsedAt *time.Time
}

// SweepQuery selects live contacts.
type SweepQuery struct {
	Ruleset domain.Ruleset
	Now     time.Time
	MaxAge  time.Duration // zero means DefaultSweepAge; capped at MaxSweepAge
	MapID   *int          // optional filter on the last known map
	GUID    string        // optional single contact
}

// TrailFilter narrows a trail. Zero values mean "no filter" / default limit.
type TrailFilter struct {
	MapID *int
	Since int64 // epoch ms; only fixes with t >= Since
	Limit int   // zero means MaxTrailRows
}

// Trail is a contact's position history.
type Trail struct {
	GUID  string
	Name  string
	Total int // all fixes stored for the guid, ignoring the filter
	Maps  []TrailMap
	Fixes []domain.Fix
}

// TrailMap counts a contact's fixes on one map (zone is empty until a chart is known).
type TrailMap struct {
	MapID int
	Zone  string
	Count int
}

// PlayerSummary is one row of a name search.
type PlayerSummary struct {
	GUID      string
	Name      string
	Reaction  domain.Reaction
	Fixes     int
	FirstSeen int64
	LastSeen  int64
}

// ApplyResult describes what ApplyPing did.
type ApplyResult struct {
	Accepted, Duplicate    bool
	Contacts, Fixes, Kills int
}

// PruneResult counts what Prune removed.
type PruneResult struct{ Fixes, Kills, Batches int }
