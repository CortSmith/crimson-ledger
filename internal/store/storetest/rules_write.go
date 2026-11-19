package storetest

import (
	"errors"
	"time"

	"crimsonledger/internal/domain"
	"crimsonledger/internal/store"
)

func rule01DuplicateBatch(e *env) {
	p := e.aPing(withContacts(aContact(guid(1), "Mc Cheeki", e.ago(time.Minute), aFix(e.ago(time.Minute), 1411, 10, 10))))
	first := e.apply(p)
	v := e.version(p.Ruleset)
	second := e.apply(p)
	if !first.Accepted || first.Duplicate || first.Contacts != 1 || first.Fixes != 1 {
		e.t.Errorf("first = %+v; want accepted with 1 contact, 1 fix", first)
	}
	if second.Accepted || !second.Duplicate {
		e.t.Errorf("second = %+v; want Duplicate and not Accepted", second)
	}
	if got := e.version(p.Ruleset); got != v {
		e.t.Errorf("version after duplicate = %d; want %d", got, v)
	}
}

func rule02OutOfOrder(e *env) {
	g := guid(2)
	e.apply(e.aPing(withContacts(aContact(g, "Newer", e.ago(1*time.Minute), aFix(e.ago(1*time.Minute), 1411, 20, 20)))))
	e.apply(e.aPing(withContacts(aContact(g, "Older", e.ago(5*time.Minute), aFix(e.ago(5*time.Minute), 1411, 80, 80)))))
	got := e.sweep(domain.RulesetPvP)
	if len(got) != 1 || got[0].Name != "Newer" || got[0].X != 20 || got[0].LastSeen != e.ago(time.Minute) {
		e.t.Fatalf("sweep = %+v; want the newer position and name kept", got)
	}
	if first := e.search(domain.RulesetPvP, "older"); len(first) != 0 {
		e.t.Errorf("search for the stale name found %+v; want none", first)
	}
	if s := e.search(domain.RulesetPvP, "newer"); len(s) != 1 || s[0].FirstSeen != e.ago(5*time.Minute) {
		e.t.Errorf("search = %+v; want FirstSeen lowered to the older ping", s)
	}
}

func rule03Fixes(e *env) {
	g := guid(3)
	fixes := []domain.Fix{
		aFix(e.ago(4*time.Minute), 1411, 1, 1), aFix(e.ago(3*time.Minute), 1411, 2, 2),
		aFix(e.ago(2*time.Minute), 1412, 3, 3), aFix(e.ago(1*time.Minute), 1411, 4, 4),
	}
	e.apply(e.aPing(withContacts(aContact(g, "Walker", e.ago(time.Minute), fixes...))))
	e.apply(e.aPing(withContacts(aContact(g, "Walker", e.ago(time.Minute), fixes[3]))))
	e.apply(e.aPing(withCharts(aChart(1411, "Durotar", 1, 1))))

	all := e.trail(g, store.TrailFilter{})
	if all.Total != 4 || len(all.Fixes) != 4 || all.Fixes[0].X != 4 || all.Fixes[3].X != 1 {
		e.t.Fatalf("trail = %+v; want 4 fixes newest first, no duplicates", all)
	}
	if len(all.Maps) != 2 || all.Maps[0].MapID != 1411 || all.Maps[0].Count != 3 || all.Maps[0].Zone != "Durotar" || all.Maps[1].Zone != "" {
		e.t.Errorf("maps = %+v; want 1411 (3, Durotar) then 1412 (1, no zone)", all.Maps)
	}
	trailFilters(e, g)
}

func trailFilters(e *env, g string) {
	m := 1411
	if f := e.trail(g, store.TrailFilter{MapID: &m}); len(f.Fixes) != 3 || f.Total != 4 {
		e.t.Errorf("map filter: %d fixes, total %d; want 3 and 4", len(f.Fixes), f.Total)
	}
	if f := e.trail(g, store.TrailFilter{Since: e.ago(2 * time.Minute)}); len(f.Fixes) != 2 {
		e.t.Errorf("since filter: %d fixes; want 2", len(f.Fixes))
	}
	if f := e.trail(g, store.TrailFilter{Limit: 1}); len(f.Fixes) != 1 || f.Fixes[0].X != 4 {
		e.t.Errorf("limit 1 = %+v; want only the newest fix", f.Fixes)
	}
	if _, err := e.s.Trail(e.ctx, domain.RulesetPvP, guid(99), store.TrailFilter{}); !errors.Is(err, store.ErrNotFound) {
		e.t.Errorf("unknown guid error = %v; want ErrNotFound", err)
	}
}

func rule04Kills(e *env) {
	a, b, c := guid(10), guid(11), guid(12)
	k := func(ago time.Duration, killer, victim string) domain.Kill { return aKill(e.ago(ago), killer, victim) }
	e.apply(e.aPing(
		withContacts(aContact(a, "Alpha", e.ago(time.Minute)), aContact(b, "Bravo", e.ago(time.Minute)), aContact(c, "Charlie", e.ago(time.Minute))),
		withKills(k(5*time.Minute, a, c), k(4*time.Minute, b, c), k(3*time.Minute, b, a), k(2*time.Minute, c, a)),
	))
	e.apply(e.aPing(withKills(k(5*time.Minute, a, c)))) // the same kill again: not counted twice
	got := e.bounty(domain.RulesetPvP)
	want := []string{b, c, a} // b has 2; a and c have 1 each; c's last kill is newer than a's
	if len(got) != 3 || got[0].GUID != want[0] || got[0].Kills != 2 || got[1].GUID != want[1] || got[2].GUID != want[2] {
		e.t.Fatalf("bounty = %+v; want order %v with b on 2 kills", got, want)
	}
	if got[0].Name != "Bravo" || got[0].LastKill != e.ago(3*time.Minute) {
		e.t.Errorf("top = %+v; want name Bravo and its newest kill time", got[0])
	}
}

func rule05Charts(e *env) {
	e.apply(e.aPing(withCharts(aChart(1411, "Durotar", 1, 1))))
	e.apply(e.aPing(withCharts(aChart(1411, "Hijacked", 9, 9), aChart(1412, "Mulgore", 2, 2))))
	got, err := e.s.Charts(e.ctx)
	if err != nil {
		e.t.Fatalf("Charts: %v", err)
	}
	if len(got) != 2 || got[0].MapID != 1411 || got[0].Zone != "Durotar" || got[0].X1 != 1 || got[1].Zone != "Mulgore" {
		e.t.Errorf("charts = %+v; want the first 1411 rectangle kept and 1412 added, ordered by mapId", got)
	}
}
