package dynamostore

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"crimsonledger/internal/domain"
	"crimsonledger/internal/store"
)

// Version is the count of accepted pings; reads are consistent so an ETag never trails a write.
func (s *Store) Version(ctx context.Context, rs domain.Ruleset) (int64, error) {
	res, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{TableName: table(s), Key: key(versionPK(rs), skNone), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return 0, fmt.Errorf("version: %w", mapError(err))
	}
	return getInt(res.Item, "v"), nil
}

// Sweep returns contacts seen within MaxAge of q.Now, newest first. One guid is a GetItem; otherwise one GSI1 query.
func (s *Store) Sweep(ctx context.Context, q store.SweepQuery) ([]domain.SweepContact, error) {
	age := q.MaxAge
	if age <= 0 {
		age = store.DefaultSweepAge
	}
	since := q.Now.Add(-min(age, store.MaxSweepAge)).UnixMilli()
	var items []item
	var err error
	if q.GUID != "" {
		items, err = s.sweepOne(ctx, q)
	} else {
		items, err = s.sweepWindow(ctx, q.Ruleset, since)
	}
	if err != nil {
		return nil, fmt.Errorf("sweep: %w", err)
	}
	return sweepRows(items, q, since), nil
}

func (s *Store) sweepOne(ctx context.Context, q store.SweepQuery) ([]item, error) {
	it, found, err := s.getContact(ctx, q.Ruleset, q.GUID)
	if err != nil || !found {
		return nil, err
	}
	return []item{it}, nil
}

func (s *Store) sweepWindow(ctx context.Context, rs domain.Ruleset, since int64) ([]item, error) {
	pk, lo := contactIndexKey(rs, since)
	return s.queryAll(ctx, &dynamodb.QueryInput{
		IndexName:                 aws.String(indexName),
		KeyConditionExpression:    aws.String("GSI1PK = :p AND GSI1SK >= :lo"),
		ExpressionAttributeValues: item{":p": str(pk), ":lo": str(lo)},
		ScanIndexForward:          aws.Bool(false),
	}, store.MaxSweepRows)
}

// sweepRows applies the window, map and position filters in memory and orders newest first, guid ascending.
func sweepRows(items []item, q store.SweepQuery, since int64) []domain.SweepContact {
	var out []domain.SweepContact
	for _, it := range items {
		c := domain.SweepContact{
			GUID: strings.TrimPrefix(getStr(it, "PK"), prefixContact+string(q.Ruleset)+sep), Name: getStr(it, "nm"),
			Reaction: domain.Reaction(getStr(it, "rx")), LastSeen: getInt(it, "ls"),
			MapID: int(getInt(it, "mp")), X: getFloat(it, "x"), Y: getFloat(it, "y"), Kind: domain.FixKind(getStr(it, "kd")),
		}
		if !has(it, "mp") || c.LastSeen < since || (q.MapID != nil && c.MapID != *q.MapID) {
			continue
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LastSeen != out[j].LastSeen {
			return out[i].LastSeen > out[j].LastSeen
		}
		return out[i].GUID < out[j].GUID
	})
	return out[:min(len(out), store.MaxSweepRows)]
}

// SearchPlayers finds contacts where the normalized q is a prefix of any word of the normalized name.
// The word index narrows candidates by the first query word; the whole query is then checked against the name.
func (s *Store) SearchPlayers(ctx context.Context, rs domain.Ruleset, q string, limit int) ([]store.PlayerSummary, error) {
	norm := domain.NormalizeName(q)
	if len([]rune(norm)) < store.MinSearchRunes {
		return nil, nil
	}
	if limit <= 0 || limit > store.MaxPlayerRows {
		limit = store.MaxPlayerRows
	}
	guids, err := s.wordCandidates(ctx, rs, strings.Fields(norm)[0])
	if err != nil {
		return nil, fmt.Errorf("search players: %w", err)
	}
	contacts, err := s.contactsByGUID(ctx, rs, guids)
	if err != nil {
		return nil, fmt.Errorf("search players: %w", err)
	}
	var out []store.PlayerSummary
	for _, it := range contacts {
		if hasWordPrefix(domain.NormalizeName(getStr(it, "nm")), norm) {
			out = append(out, summaryOf(rs, it))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LastSeen != out[j].LastSeen {
			return out[i].LastSeen > out[j].LastSeen
		}
		return out[i].GUID < out[j].GUID
	})
	out = out[:min(len(out), limit)]
	for i := range out {
		if out[i].Fixes, err = s.countFixes(ctx, rs, out[i].GUID); err != nil {
			return nil, fmt.Errorf("search players: %w", err)
		}
	}
	return out, nil
}

// wordCandidates returns the guids whose name has a word starting with first. A one-rune first word
// (only possible before more words) matches whole words, which the partition key holds as is.
func (s *Store) wordCandidates(ctx context.Context, rs domain.Ruleset, first string) ([]string, error) {
	items, err := s.queryAll(ctx, &dynamodb.QueryInput{
		KeyConditionExpression:    aws.String("PK = :p AND begins_with(SK, :w)"),
		ExpressionAttributeValues: item{":p": str(wordPK(rs, first)), ":w": str(wordKey(first))},
		Limit:                     aws.Int32(maxSearchScan),
	}, maxSearchScan)
	if err != nil {
		return nil, err
	}
	guids := make([]string, 0, len(items))
	for _, it := range items[:min(len(items), maxSearchScan)] {
		guids = append(guids, guidOfWordSK(getStr(it, "SK")))
	}
	return guids, nil
}

func (s *Store) contactsByGUID(ctx context.Context, rs domain.Ruleset, guids []string) ([]item, error) {
	keys := make([]item, 0, len(guids))
	for _, g := range guids {
		pk, sk := contactKey(rs, g)
		keys = append(keys, key(pk, sk))
	}
	return s.batchGet(ctx, keys)
}

func summaryOf(rs domain.Ruleset, it item) store.PlayerSummary {
	return store.PlayerSummary{
		GUID: strings.TrimPrefix(getStr(it, "PK"), prefixContact+string(rs)+sep), Name: getStr(it, "nm"),
		Reaction: domain.Reaction(getStr(it, "rx")), FirstSeen: getInt(it, "fs"), LastSeen: getInt(it, "ls"),
	}
}

func (s *Store) countFixes(ctx context.Context, rs domain.Ruleset, guid string) (int, error) {
	q := &dynamodb.QueryInput{
		TableName: table(s), KeyConditionExpression: aws.String("PK = :p"),
		ExpressionAttributeValues: item{":p": str(fixPK(rs, guid))}, Select: "COUNT",
	}
	total := 0
	for {
		res, err := s.db.Query(ctx, q)
		if err != nil {
			return 0, mapError(err)
		}
		total += int(res.Count)
		if res.LastEvaluatedKey == nil {
			return total, nil
		}
		q.ExclusiveStartKey = res.LastEvaluatedKey
	}
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

// Bounty ranks killers by kills, then newest kill, then guid. The ranking reads at most MaxBountyKillers totals.
func (s *Store) Bounty(ctx context.Context, rs domain.Ruleset, limit int) ([]domain.KillTotal, error) {
	if limit <= 0 || limit > store.MaxBountyRows {
		limit = store.MaxBountyRows
	}
	items, err := s.queryAll(ctx, &dynamodb.QueryInput{
		KeyConditionExpression: aws.String("PK = :p"), ExpressionAttributeValues: item{":p": str(bountyPK(rs))},
	}, MaxBountyKillers)
	if err != nil {
		return nil, fmt.Errorf("bounty: %w", err)
	}
	totals := make([]domain.KillTotal, 0, len(items))
	for _, it := range items[:min(len(items), MaxBountyKillers)] {
		totals = append(totals, domain.KillTotal{GUID: getStr(it, "SK"), Name: getStr(it, "SK"), Kills: int(getInt(it, "kills")), LastKill: getInt(it, "lastKill")})
	}
	sort.Slice(totals, func(i, j int) bool { return killLess(totals[i], totals[j]) })
	totals = totals[:min(len(totals), limit)]
	if err := s.nameKillers(ctx, rs, totals); err != nil {
		return nil, fmt.Errorf("bounty: %w", err)
	}
	return totals, nil
}

func killLess(a, b domain.KillTotal) bool {
	if a.Kills != b.Kills {
		return a.Kills > b.Kills
	}
	if a.LastKill != b.LastKill {
		return a.LastKill > b.LastKill
	}
	return a.GUID < b.GUID
}

// nameKillers replaces each guid name with the contact's name when the contact is known.
func (s *Store) nameKillers(ctx context.Context, rs domain.Ruleset, totals []domain.KillTotal) error {
	guids := make([]string, len(totals))
	for i, t := range totals {
		guids[i] = t.GUID
	}
	contacts, err := s.contactsByGUID(ctx, rs, guids)
	if err != nil {
		return err
	}
	names := map[string]string{}
	for _, it := range contacts {
		names[strings.TrimPrefix(getStr(it, "PK"), prefixContact+string(rs)+sep)] = getStr(it, "nm")
	}
	for i := range totals {
		if n, ok := names[totals[i].GUID]; ok {
			totals[i].Name = n
		}
	}
	return nil
}

// Charts returns every map rectangle ordered by map id.
func (s *Store) Charts(ctx context.Context) ([]domain.Chart, error) {
	items, err := s.queryAll(ctx, &dynamodb.QueryInput{
		KeyConditionExpression: aws.String("PK = :p"), ExpressionAttributeValues: item{":p": str(keyChart)},
	}, 0)
	if err != nil {
		return nil, fmt.Errorf("charts: %w", err)
	}
	out := make([]domain.Chart, 0, len(items))
	for _, it := range items {
		id := parseSKInt(getStr(it, "SK"))
		out = append(out, domain.Chart{MapID: int(id), Zone: getStr(it, "zone"), X0: getFloat(it, "x0"), Y0: getFloat(it, "y0"), X1: getFloat(it, "x1"), Y1: getFloat(it, "y1")})
	}
	return out, nil
}

// Trail returns a contact's fixes newest first plus per-map counts. It reads every stored fix of the
// contact once (at most 24 h of them) so Total and Maps ignore the filter exactly like the SQLite store.
func (s *Store) Trail(ctx context.Context, rs domain.Ruleset, guid string, f store.TrailFilter) (store.Trail, error) {
	c, found, err := s.getContact(ctx, rs, guid)
	if err != nil {
		return store.Trail{}, fmt.Errorf("trail %s: %w", guid, err)
	}
	if !found {
		return store.Trail{}, fmt.Errorf("trail %s: %w", guid, store.ErrNotFound)
	}
	items, err := s.queryAll(ctx, &dynamodb.QueryInput{
		KeyConditionExpression: aws.String("PK = :p"), ExpressionAttributeValues: item{":p": str(fixPK(rs, guid))},
		ScanIndexForward: aws.Bool(false),
	}, 0)
	if err != nil {
		return store.Trail{}, fmt.Errorf("trail %s: %w", guid, err)
	}
	zones, err := s.zones(ctx)
	if err != nil {
		return store.Trail{}, fmt.Errorf("trail %s: %w", guid, err)
	}
	return buildTrail(guid, getStr(c, "nm"), items, zones, f), nil
}

func (s *Store) zones(ctx context.Context) (map[int]string, error) {
	charts, err := s.Charts(ctx)
	if err != nil {
		return nil, err
	}
	zones := make(map[int]string, len(charts))
	for _, c := range charts {
		zones[c.MapID] = c.Zone
	}
	return zones, nil
}

func buildTrail(guid, name string, items []item, zones map[int]string, f store.TrailFilter) store.Trail {
	t := store.Trail{GUID: guid, Name: name, Total: len(items)}
	counts := map[int]int{}
	for _, it := range items {
		fix := fixOf(it)
		counts[fix.MapID]++
		if fix.T >= f.Since && (f.MapID == nil || fix.MapID == *f.MapID) {
			t.Fixes = append(t.Fixes, fix)
		}
	}
	sort.SliceStable(t.Fixes, func(i, j int) bool {
		if t.Fixes[i].T != t.Fixes[j].T {
			return t.Fixes[i].T > t.Fixes[j].T
		}
		return t.Fixes[i].MapID < t.Fixes[j].MapID
	})
	limit := f.Limit
	if limit <= 0 || limit > store.MaxTrailRows {
		limit = store.MaxTrailRows
	}
	t.Fixes = t.Fixes[:min(len(t.Fixes), limit)]
	for id, n := range counts {
		t.Maps = append(t.Maps, store.TrailMap{MapID: id, Zone: zones[id], Count: n})
	}
	sort.Slice(t.Maps, func(i, j int) bool {
		if t.Maps[i].Count != t.Maps[j].Count {
			return t.Maps[i].Count > t.Maps[j].Count
		}
		return t.Maps[i].MapID < t.Maps[j].MapID
	})
	return t
}

func fixOf(it item) domain.Fix {
	id, _ := parseFixSK(getStr(it, "SK"))
	t := parseSKInt(getStr(it, "SK"))
	return domain.Fix{T: t, MapID: id, X: getFloat(it, "x"), Y: getFloat(it, "y"), Kind: domain.FixKind(getStr(it, "kd"))}
}

// parseSKInt reads the leading digits of a sort key.
func parseSKInt(sk string) int64 {
	head, _, _ := strings.Cut(sk, sep)
	var n int64
	_, _ = fmt.Sscanf(head, "%d", &n)
	return n
}
