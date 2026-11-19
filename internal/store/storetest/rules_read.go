package storetest

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"time"

	"crimsonledger/internal/domain"
	"crimsonledger/internal/store"
)

func rule06Search(e *env) {
	now := e.ago(time.Minute)
	e.apply(e.aPing(withContacts(
		aContact(guid(20), "Mc Cheeki", e.ago(3*time.Minute)),
		aContact(guid(21), "Bob Mcgee", e.ago(2*time.Minute)),
		aContact(guid(22), "Ñandú", now),
		aContact(guid(23), "Mcz", now),
	)))
	names := func(q string) []string {
		var out []string
		for _, p := range e.search(domain.RulesetPvP, q) {
			out = append(out, p.Name)
		}
		return out
	}
	cases := []struct {
		q    string
		want []string
	}{
		{"mc", []string{"Mcz", "Bob Mcgee", "Mc Cheeki"}},
		{"MCG", []string{"Bob Mcgee"}},
		{"nandu", []string{"Ñandú"}},
		{"cheeki", []string{"Mc Cheeki"}},
		{"mc ch", []string{"Mc Cheeki"}},
		{"m", nil},
		{"zzz", nil},
	}
	for _, c := range cases {
		if got := names(c.q); !reflect.DeepEqual(got, c.want) {
			e.t.Errorf("search %q = %v; want %v", c.q, got, c.want)
		}
	}
}

func rule07Sweep(e *env) {
	f := func(m int) []domain.Fix { return []domain.Fix{aFix(e.ago(time.Minute), m, 5, 5)} }
	e.apply(e.aPing(withContacts(
		aContact(guid(31), "B", e.ago(2*time.Minute), f(1411)...),
		aContact(guid(30), "A", e.ago(2*time.Minute), f(1412)...),
		aContact(guid(32), "C", e.ago(1*time.Minute), f(1411)...),
		aContact(guid(33), "Old", e.ago(20*time.Minute), f(1411)...),
	)))
	q := store.SweepQuery{Ruleset: domain.RulesetPvP, Now: e.clock.Now()}
	check := func(name string, q store.SweepQuery, want ...string) {
		got, err := e.s.Sweep(e.ctx, q)
		if err != nil || !reflect.DeepEqual(sweepGUIDs(got), want) {
			e.t.Errorf("%s = %v, %v; want %v", name, sweepGUIDs(got), err, want)
		}
	}
	check("default window", q, guid(32), guid(30), guid(31))
	q.MaxAge = time.Hour
	check("one hour", q, guid(32), guid(30), guid(31), guid(33))
	q.MaxAge = 0
	m := 1411
	q.MapID = &m
	check("map filter", q, guid(32), guid(31))
	q.GUID = guid(31)
	check("map and guid", q, guid(31))
	q.MapID = nil
	check("guid only", q, guid(31))
}

func rule08Rulesets(e *env) {
	g := guid(40)
	e.apply(e.aPing(withRuleset(domain.RulesetPvP), withContacts(aContact(g, "Pvper", e.ago(time.Minute), aFix(e.ago(time.Minute), 1, 1, 1))),
		withKills(aKill(e.ago(time.Minute), g, guid(41)))))
	if got := e.sweep(domain.RulesetNormal); len(got) != 0 {
		e.t.Errorf("normal sweep = %+v; want none", got)
	}
	if got := e.search(domain.RulesetNormal, "pvper"); len(got) != 0 {
		e.t.Errorf("normal search = %+v; want none", got)
	}
	if got := e.bounty(domain.RulesetNormal); len(got) != 0 {
		e.t.Errorf("normal bounty = %+v; want none", got)
	}
	if _, err := e.s.Trail(e.ctx, domain.RulesetNormal, g, store.TrailFilter{}); !errors.Is(err, store.ErrNotFound) {
		e.t.Errorf("normal trail error = %v; want ErrNotFound", err)
	}
	if v := e.version(domain.RulesetNormal); v != 0 {
		e.t.Errorf("normal version = %d; want 0", v)
	}
}

func rule09Version(e *env) {
	if v := e.version(domain.RulesetPvP); v != 0 {
		e.t.Fatalf("initial version = %d; want 0", v)
	}
	p1, p2 := e.aPing(), e.aPing()
	e.apply(p1)
	e.apply(p2)
	e.apply(p2)
	if v := e.version(domain.RulesetPvP); v != 2 {
		e.t.Errorf("version = %d; want 2 (duplicates do not count)", v)
	}
}

func rule10Keys(e *env) {
	ks := e.s.Keys()
	now := e.clock.Now()
	exp := now.Add(time.Hour)
	rec := store.KeyRecord{ID: "k1", Scope: store.ScopeView, Label: "laptop", Hash: []byte{0, 1, 0xff, 0x80}, CreatedAt: now, ExpiresAt: &exp}
	if err := ks.Put(e.ctx, rec); err != nil {
		e.t.Fatalf("Put: %v", err)
	}
	if err := ks.Put(e.ctx, rec); !errors.Is(err, store.ErrConflict) {
		e.t.Errorf("second Put error = %v; want ErrConflict", err)
	}
	got, err := ks.Get(e.ctx, "k1")
	if err != nil || got.Scope != store.ScopeView || got.Label != "laptop" || !reflect.DeepEqual(got.Hash, rec.Hash) ||
		!got.CreatedAt.Equal(now) || got.ExpiresAt == nil || !got.ExpiresAt.Equal(exp) || got.Disabled || got.LastUsedAt != nil {
		e.t.Fatalf("Get = %+v, %v; want the stored record with identical hash bytes", got, err)
	}
	keysUnknown(e, ks)
	keysDisableAndList(e, ks)
}

func keysUnknown(e *env, ks store.KeyStore) {
	if _, err := ks.Get(e.ctx, "nope"); !errors.Is(err, store.ErrNotFound) {
		e.t.Errorf("Get unknown error = %v; want ErrNotFound", err)
	}
	if err := ks.SetDisabled(e.ctx, "nope", true); !errors.Is(err, store.ErrNotFound) {
		e.t.Errorf("SetDisabled unknown error = %v; want ErrNotFound", err)
	}
}

func keysDisableAndList(e *env, ks store.KeyStore) {
	e.clock.Advance(time.Minute)
	if err := ks.SetDisabled(e.ctx, "k1", true); err != nil {
		e.t.Fatalf("SetDisabled: %v", err)
	}
	if err := ks.Touch(e.ctx, "k1", e.clock.Now()); err != nil {
		e.t.Fatalf("Touch: %v", err)
	}
	if err := ks.Put(e.ctx, store.KeyRecord{ID: "k2", Scope: store.ScopeIngest, Hash: []byte{9}, CreatedAt: e.clock.Now()}); err != nil {
		e.t.Fatalf("Put k2: %v", err)
	}
	list, err := ks.List(e.ctx)
	if err != nil || len(list) != 2 || list[0].ID != "k1" || !list[0].Disabled || list[0].LastUsedAt == nil || list[1].ID != "k2" {
		e.t.Errorf("List = %+v, %v; want k1 (disabled, used) then k2", list, err)
	}
}

func rule11Prune(e *env) {
	g := guid(50)
	old, fresh := e.ago(48*time.Hour), e.ago(time.Minute)
	p := e.aPing(withContacts(aContact(g, "Keeper", fresh, aFix(old, 1, 1, 1), aFix(fresh, 1, 2, 2))),
		withKills(aKill(old, g, guid(51)), aKill(fresh, g, guid(52))), withCharts(aChart(1, "Z", 1, 1)))
	e.apply(p)
	got, err := e.s.Prune(e.ctx, e.clock.Now().Add(-24*time.Hour))
	if err != nil || got.Fixes != 1 || got.Kills != 1 {
		e.t.Fatalf("Prune = %+v, %v; want 1 fix and 1 kill removed", got, err)
	}
	if tr := e.trail(g, store.TrailFilter{}); tr.Total != 1 {
		e.t.Errorf("trail total after prune = %d; want 1", tr.Total)
	}
	if b := e.bounty(domain.RulesetPvP); len(b) != 1 || b[0].Kills != 1 {
		e.t.Errorf("bounty after prune = %+v; want 1 kill left", b)
	}
	if len(e.sweep(domain.RulesetPvP)) != 1 {
		e.t.Error("contact was pruned; contacts must survive")
	}
	if c, _ := e.s.Charts(e.ctx); len(c) != 1 {
		e.t.Error("chart was pruned; charts must survive")
	}
	pruneBatchMarker(e, p)
}

func pruneBatchMarker(e *env, p domain.Ping) {
	if dup := e.apply(p); !dup.Duplicate {
		e.t.Errorf("batch marker pruned too early: re-send = %+v; want Duplicate", dup)
	}
	e.clock.Advance(48 * time.Hour)
	if got, err := e.s.Prune(e.ctx, e.clock.Now().Add(-24*time.Hour)); err != nil || got.Batches != 1 {
		e.t.Fatalf("second Prune = %+v, %v; want the batch marker removed", got, err)
	}
	if again := e.apply(p); !again.Accepted {
		e.t.Errorf("re-send after marker pruned = %+v; want accepted", again)
	}
}

func rule12Cancel(e *env) {
	ctx, cancel := context.WithCancel(e.ctx)
	cancel()
	p := e.aPing(withContacts(aContact(guid(60), "Ghost", e.ago(time.Minute), aFix(e.ago(time.Minute), 1, 1, 1))))
	if _, err := e.s.ApplyPing(ctx, p, e.clock.Now()); !errors.Is(err, context.Canceled) {
		e.t.Fatalf("ApplyPing error = %v; want context.Canceled", err)
	}
	if len(e.sweep(domain.RulesetPvP)) != 0 || e.version(domain.RulesetPvP) != 0 {
		e.t.Error("a cancelled ping left data behind")
	}
	if r := e.apply(p); !r.Accepted {
		e.t.Errorf("retry after cancel = %+v; want accepted (no batch marker was left)", r)
	}
}

// snapshot is the observable state of a store, compared between serial and concurrent runs.
type snapshot struct {
	Version int64
	Sweep   []domain.SweepContact
	Trails  []store.Trail
	Bounty  []domain.KillTotal
}

func (e *env) snapshot(s store.Store, guids []string) snapshot {
	e.t.Helper()
	sw, err := s.Sweep(e.ctx, store.SweepQuery{Ruleset: domain.RulesetPvP, Now: e.clock.Now(), MaxAge: time.Hour})
	if err != nil {
		e.t.Fatalf("snapshot sweep: %v", err)
	}
	v, _ := s.Version(e.ctx, domain.RulesetPvP)
	b, _ := s.Bounty(e.ctx, domain.RulesetPvP, store.MaxBountyRows)
	snap := snapshot{Version: v, Sweep: sw, Bounty: b}
	for _, g := range guids {
		tr, err := s.Trail(e.ctx, domain.RulesetPvP, g, store.TrailFilter{})
		if err != nil {
			e.t.Fatalf("snapshot trail %s: %v", g, err)
		}
		snap.Trails = append(snap.Trails, tr)
	}
	return snap
}

// sharedPings builds one ping per worker; every ping touches the same contacts.
func (e *env) sharedPings(workers int, guids []string) []domain.Ping {
	var pings []domain.Ping
	for w := 0; w < workers; w++ {
		var cs []domain.Contact
		for i, g := range guids {
			at := e.ago(time.Duration(w+i+1) * time.Minute)
			cs = append(cs, aContact(g, "Shared", at, aFix(at, 1411, float64(w), float64(i))))
		}
		kill := aKill(e.ago(time.Duration(w+1)*time.Minute), guids[w%len(guids)], guids[(w+1)%len(guids)])
		pings = append(pings, e.aPing(withContacts(cs...), withKills(kill)))
	}
	return pings
}

func rule13Concurrency(e *env) {
	const workers, shared = 8, 5
	var guids []string
	for i := 0; i < shared; i++ {
		guids = append(guids, guid(70+i))
	}
	pings := e.sharedPings(workers, guids)
	other := e.fresh()
	for _, p := range pings {
		if _, err := other.ApplyPing(e.ctx, p, e.clock.Now()); err != nil {
			e.t.Fatalf("serial apply: %v", err)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for _, p := range pings {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.s.ApplyPing(e.ctx, p, e.clock.Now())
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			e.t.Fatalf("concurrent apply: %v", err)
		}
	}
	want, got := e.snapshot(other, guids), e.snapshot(e.s, guids)
	if !reflect.DeepEqual(got, want) {
		e.t.Errorf("concurrent state differs from serial state\n got  %+v\n want %+v", got, want)
	}
}
