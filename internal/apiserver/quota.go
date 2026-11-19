package apiserver

import (
	"sync"
	"time"

	"crimsonledger/internal/domain"
)

// quotaWindow is the length of one quota period.
const quotaWindow = time.Hour

// quota counts, per ingest key and hour, the kills and distinct contacts it has reported. It is held in
// memory: on Lambda each container counts separately, which still bounds a leaked key to a small multiple.
type quota struct {
	mu   sync.Mutex
	keys map[string]*usage
}

type usage struct {
	start    time.Time
	kills    int
	contacts map[string]struct{}
}

func newQuota() *quota { return &quota{keys: map[string]*usage{}} }

// current returns the key's usage for the window containing now, starting a fresh one when it has ended.
// The caller holds q.mu.
func (q *quota) current(keyID string, now time.Time) *usage {
	u := q.keys[keyID]
	if u == nil || now.Sub(u.start) >= quotaWindow {
		u = &usage{start: now, contacts: map[string]struct{}{}}
		q.keys[keyID] = u
	}
	return u
}

// over returns how long to wait when accepting p would push the key past an hourly cap, else zero.
func (q *quota) over(keyID string, p domain.Ping, now time.Time) time.Duration {
	q.mu.Lock()
	defer q.mu.Unlock()
	u := q.current(keyID, now)
	fresh := 0
	for _, c := range p.Contacts {
		if _, seen := u.contacts[c.GUID]; !seen {
			fresh++
		}
	}
	if u.kills+len(p.Kills) > MaxKillsPerKeyHour || len(u.contacts)+fresh > MaxContactsPerKeyHour {
		return u.start.Add(quotaWindow).Sub(now)
	}
	return 0
}

// charge records an accepted ping against the key.
func (q *quota) charge(keyID string, p domain.Ping, now time.Time) {
	q.mu.Lock()
	defer q.mu.Unlock()
	u := q.current(keyID, now)
	u.kills += len(p.Kills)
	for _, c := range p.Contacts {
		u.contacts[c.GUID] = struct{}{}
	}
}
