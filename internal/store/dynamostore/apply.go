package dynamostore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"crimsonledger/internal/domain"
	"crimsonledger/internal/store"
)

// ApplyPing stores a validated ping. Every step is idempotent, so a retry after a failure is safe;
// the batch marker is written last, so only a fully applied ping is ever reported as a duplicate.
func (s *Store) ApplyPing(ctx context.Context, p domain.Ping, now time.Time) (store.ApplyResult, error) {
	if err := ctx.Err(); err != nil {
		return store.ApplyResult{}, err
	}
	seen, err := s.batchSeen(ctx, p.BatchID)
	if err != nil || seen {
		return store.ApplyResult{Duplicate: seen}, wrapApply(p, err)
	}
	for _, step := range []func(context.Context, domain.Ping) error{
		s.upsertContacts, s.putFixes, s.putWords, s.putCharts, s.applyKills, s.bumpVersion,
	} {
		if err := step(ctx, p); err != nil {
			return store.ApplyResult{}, wrapApply(p, err)
		}
	}
	if err := s.markBatch(ctx, p.BatchID, now); err != nil {
		return store.ApplyResult{}, wrapApply(p, err)
	}
	res := store.ApplyResult{Accepted: true, Contacts: len(p.Contacts), Kills: len(p.Kills)}
	for _, c := range p.Contacts {
		res.Fixes += len(c.Fixes)
	}
	return res, nil
}

func wrapApply(p domain.Ping, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("apply ping %s: %w", p.BatchID, err)
}

func (s *Store) batchSeen(ctx context.Context, id string) (bool, error) {
	res, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{TableName: table(s), Key: key(batchPK(id), skNone), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return false, mapError(err)
	}
	return len(res.Item) > 0, nil
}

// markBatch remembers the batch id; losing a race to a concurrent identical batch is fine.
func (s *Store) markBatch(ctx context.Context, id string, now time.Time) error {
	it := key(batchPK(id), skNone)
	it["ttl"] = num(now.Add(BatchMarkerTTL).Unix())
	_, err := s.db.PutItem(ctx, &dynamodb.PutItemInput{TableName: table(s), Item: it})
	return mapError(err)
}

func (s *Store) bumpVersion(ctx context.Context, p domain.Ping) error {
	_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: table(s), Key: key(versionPK(p.Ruleset), skNone),
		UpdateExpression:          aws.String("ADD v :one"),
		ExpressionAttributeValues: item{":one": num(1)},
	})
	return mapError(err)
}

// putFixes writes every fix with BatchWriteItem; a fix has a deterministic key, so re-writing it is harmless.
func (s *Store) putFixes(ctx context.Context, p domain.Ping) error {
	var reqs []types.WriteRequest
	for _, c := range p.Contacts {
		for _, f := range c.Fixes {
			it := key(fixPK(p.Ruleset, c.GUID), fixSK(f.T, f.MapID))
			it["x"], it["y"], it["kd"] = flt(f.X), flt(f.Y), str(string(f.Kind))
			it["ttl"] = num(ttlSeconds(f.T, Retention))
			reqs = append(reqs, putRequest(it))
		}
	}
	return s.batchWrite(ctx, reqs)
}

// putWords indexes the words of every contact name. Contacts whose name changed are handled in upsertContact.
func (s *Store) putWords(ctx context.Context, p domain.Ping) error {
	var reqs []types.WriteRequest
	for _, c := range p.Contacts {
		reqs = append(reqs, wordPuts(p.Ruleset, c)...)
	}
	return s.batchWrite(ctx, reqs)
}

func wordPuts(rs domain.Ruleset, c domain.Contact) []types.WriteRequest {
	var reqs []types.WriteRequest
	for _, w := range nameWords(c.Name) {
		reqs = append(reqs, putRequest(key(wordPK(rs, w), wordSK(w, c.GUID))))
	}
	return reqs
}

// nameWords returns the distinct normalized words of a name, the search index keys.
func nameWords(name string) []string {
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

// putCharts stores each map rectangle once: the first write wins, so a rogue key cannot move maps.
func (s *Store) putCharts(ctx context.Context, p domain.Ping) error {
	for _, c := range p.Charts {
		it := key(keyChart, chartSK(c.MapID))
		it["zone"], it["x0"], it["y0"], it["x1"], it["y1"] = str(c.Zone), flt(c.X0), flt(c.Y0), flt(c.X1), flt(c.Y1)
		_, err := s.db.PutItem(ctx, &dynamodb.PutItemInput{TableName: table(s), Item: it, ConditionExpression: aws.String("attribute_not_exists(PK)")})
		if err != nil && !isConditionFailed(err) {
			return fmt.Errorf("put chart %d: %w", c.MapID, mapError(err))
		}
	}
	return nil
}
