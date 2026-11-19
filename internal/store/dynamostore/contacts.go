package dynamostore

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"crimsonledger/internal/domain"
)

const (
	// A newer ping moves the contact. A contact without fixes keeps its old position, so the position
	// attributes are only part of the expression when the ping has a fix (see contactUpdate).
	setContact = "SET nm = :nm, rw = :raw, rx = :rx, ls = :t, GSI1PK = :gp, GSI1SK = :gs, fs = if_not_exists(fs, :fs)"
	setPos     = ", mp = :mp, x = :x, y = :y, kd = :kd"
	// An older ping never moves a contact backwards.
	notOlder = "attribute_not_exists(ls) OR ls <= :t"
	lowerFS  = "SET fs = :fs"
	whenHigh = "fs > :fs"
)

// upsertContacts writes every contact of the ping. A stale ping never moves a contact backwards,
// but it may still lower firstSeen (out-of-order uploads).
func (s *Store) upsertContacts(ctx context.Context, p domain.Ping) error {
	for _, c := range p.Contacts {
		if err := s.upsertContact(ctx, p.Ruleset, c); err != nil {
			return fmt.Errorf("upsert contact %s: %w", c.GUID, err)
		}
	}
	return nil
}

func (s *Store) upsertContact(ctx context.Context, rs domain.Ruleset, c domain.Contact) error {
	in := contactUpdate(s.table, rs, c)
	res, err := s.db.UpdateItem(ctx, in)
	old := item(nil)
	switch {
	case err == nil:
		old = res.Attributes
		if err := s.dropStaleWords(ctx, rs, c, old); err != nil {
			return err
		}
	case isConditionFailed(err):
		old = conditionOldItem(err) // a newer ping already won; only firstSeen may still move
	default:
		return mapError(err)
	}
	return s.lowerFirstSeen(ctx, rs, c, old)
}

// contactUpdate builds the conditional upsert; ALL_OLD returns the item as it was so no extra read is needed.
func contactUpdate(tableName string, rs domain.Ruleset, c domain.Contact) *dynamodb.UpdateItemInput {
	pk, sk := contactKey(rs, c.GUID)
	gp, gs := contactIndexKey(rs, c.LastSeen)
	expr := setContact
	vals := item{":nm": str(c.Name), ":raw": str(c.Raw), ":rx": str(string(c.Reaction)), ":t": num(c.LastSeen),
		":gp": str(gp), ":gs": str(gs), ":fs": num(firstSeen(c))}
	if f := newestFix(c); f != nil {
		expr += setPos
		vals[":mp"], vals[":x"], vals[":y"], vals[":kd"] = num(int64(f.MapID)), flt(f.X), flt(f.Y), str(string(f.Kind))
	}
	return &dynamodb.UpdateItemInput{
		TableName: aws.String(tableName), Key: key(pk, sk), UpdateExpression: aws.String(expr),
		ConditionExpression: aws.String(notOlder), ExpressionAttributeValues: vals,
		ReturnValues:                        types.ReturnValueAllOld,
		ReturnValuesOnConditionCheckFailure: types.ReturnValuesOnConditionCheckFailureAllOld,
	}
}

// dropStaleWords removes the search words of the previous name when the name changed.
func (s *Store) dropStaleWords(ctx context.Context, rs domain.Ruleset, c domain.Contact, old item) error {
	oldName := getStr(old, "nm")
	if len(old) == 0 || domain.NormalizeName(oldName) == domain.NormalizeName(c.Name) {
		return nil
	}
	keep := map[string]bool{}
	for _, w := range nameWords(c.Name) {
		keep[w] = true
	}
	var reqs []types.WriteRequest
	for _, w := range nameWords(oldName) {
		if !keep[w] {
			reqs = append(reqs, deleteRequest(wordPK(rs, w), wordSK(w, c.GUID)))
		}
	}
	return s.batchWrite(ctx, reqs)
}

// lowerFirstSeen moves firstSeen back when this ping proves the contact existed earlier.
func (s *Store) lowerFirstSeen(ctx context.Context, rs domain.Ruleset, c domain.Contact, old item) error {
	fs := firstSeen(c)
	if len(old) == 0 || getInt(old, "fs") <= fs {
		return nil
	}
	pk, sk := contactKey(rs, c.GUID)
	_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.table), Key: key(pk, sk), UpdateExpression: aws.String(lowerFS),
		ConditionExpression: aws.String(whenHigh), ExpressionAttributeValues: item{":fs": num(fs)},
	})
	if err != nil && !isConditionFailed(err) {
		return mapError(err)
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

// firstSeen is the earliest time the ping proves the contact existed.
func firstSeen(c domain.Contact) int64 {
	first := c.LastSeen
	for _, f := range c.Fixes {
		first = min(first, f.T)
	}
	return first
}
