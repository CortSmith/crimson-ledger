package pipeline

import (
	"fmt"
	"io"
	"time"

	"crimsonledger/internal/domain"
	"crimsonledger/internal/logparse/kills"
	"crimsonledger/internal/logparse/parser"
	"crimsonledger/internal/logparse/ruleset"
	"crimsonledger/internal/logparse/tracker"
)

// Batcher applies events to a tracker and a kill detector and cuts the result into pings that respect
// the server's limits. It is owned by one goroutine.
type Batcher struct {
	source      string
	fixedRules  domain.Ruleset // "" = auto-detect
	detected    domain.Ruleset
	maxContacts int
	rand        io.Reader
	tr          *tracker.Tracker
	kd          *kills.Detector
	pending     []domain.Kill
	contacts    int // contacts sent so far (a counter for the UI)
}

// BatcherOptions configure a Batcher.
type BatcherOptions struct {
	Source      string
	Ruleset     string    // "auto" or a ruleset name
	MaxContacts int       // per ping, 1..domain.MaxContacts
	Rand        io.Reader // source of UUID bytes
}

// NewBatcher returns a Batcher.
func NewBatcher(o BatcherOptions) *Batcher {
	b := &Batcher{source: o.Source, maxContacts: o.MaxContacts, rand: o.Rand, tr: tracker.New(""), kd: kills.New(), detected: domain.RulesetPvP}
	if b.maxContacts <= 0 || b.maxContacts > domain.MaxContacts {
		b.maxContacts = domain.MaxContacts
	}
	if id, ok := ruleset.Parse(o.Ruleset); ok {
		b.fixedRules = domain.Ruleset(id)
	}
	return b
}

// Apply feeds one event.
func (b *Batcher) Apply(ev parser.Event) {
	b.tr.Apply(ev)
	if k, ok := b.kd.Apply(ev); ok && len(b.pending) < domain.MaxKills*4 {
		b.pending = append(b.pending, k)
	}
	if b.fixedRules == "" {
		b.detect(ev)
	}
}

func (b *Batcher) detect(ev parser.Event) {
	for _, raw := range [...]string{ev.Src.Name, ev.Dst.Name} {
		if raw == "" {
			continue
		}
		if id, ok := ruleset.DetectFromRawName(raw); ok {
			b.detected = domain.Ruleset(id)
		}
	}
}

// ContactsSent is how many contact records were put into pings so far.
func (b *Batcher) ContactsSent() int { return b.contacts }

// Flush returns the pings to send now: none when nothing changed, several when more than maxContacts
// contacts changed. Kills ride on the first pings. Every ping has a fresh batchId.
func (b *Batcher) Flush(now time.Time) ([]domain.Ping, error) {
	contacts := b.tr.Flush(now)
	if len(contacts) == 0 && len(b.pending) == 0 {
		return nil, nil
	}
	var out []domain.Ping
	for len(contacts) > 0 || len(b.pending) > 0 {
		n := min(len(contacts), b.maxContacts)
		p, err := b.newPing(now, contacts[:n])
		if err != nil {
			return out, err
		}
		contacts = contacts[n:]
		k := min(len(b.pending), domain.MaxKills)
		p.Kills, b.pending = b.pending[:k:k], b.pending[k:]
		p.Charts = b.chartsFor(p)
		b.contacts += n
		out = append(out, p)
	}
	return out, nil
}

func (b *Batcher) newPing(now time.Time, contacts []domain.Contact) (domain.Ping, error) {
	id, err := newUUIDv4(b.rand)
	if err != nil {
		return domain.Ping{}, fmt.Errorf("make batch id: %w", err)
	}
	rules := b.fixedRules
	if rules == "" {
		rules = b.detected
	}
	return domain.Ping{BatchID: id, Source: b.source, Ruleset: rules, SentAt: now.UnixMilli(),
		Contacts: append([]domain.Contact{}, contacts...), Charts: []domain.Chart{}, Kills: []domain.Kill{}}, nil
}

func (b *Batcher) chartsFor(p domain.Ping) []domain.Chart {
	ids := map[int]struct{}{}
	for _, c := range p.Contacts {
		for _, f := range c.Fixes {
			ids[f.MapID] = struct{}{}
		}
	}
	for _, k := range p.Kills {
		ids[k.MapID] = struct{}{}
	}
	charts := b.tr.Charts(ids)
	if len(charts) > domain.MaxCharts {
		charts = charts[:domain.MaxCharts]
	}
	if charts == nil {
		charts = []domain.Chart{}
	}
	return charts
}

// newUUIDv4 builds a version 4 UUID from 16 bytes of r.
func newUUIDv4(r io.Reader) (string, error) {
	var b [16]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
