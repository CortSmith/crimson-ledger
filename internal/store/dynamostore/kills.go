package dynamostore

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"crimsonledger/internal/domain"
)

const (
	// conditionFailedCode is the cancellation reason of a transaction item whose condition failed.
	conditionFailedCode = "ConditionalCheckFailed"
	addKill             = "ADD kills :one SET lastKill = if_not_exists(lastKill, :t)"
	raiseLastKill       = "SET lastKill = :t"
	lastKillOlder       = "lastKill < :t"
)

// applyKills records each kill once and counts it once: the kill item and the killer's total change in one
// transaction guarded by attribute_not_exists, so a retried kill cannot be counted twice.
func (s *Store) applyKills(ctx context.Context, p domain.Ping) error {
	for _, k := range p.Kills {
		fresh, err := s.putKill(ctx, p.Ruleset, k)
		if err != nil {
			return fmt.Errorf("apply kill: %w", err)
		}
		if fresh {
			if err := s.raiseLastKill(ctx, p.Ruleset, k); err != nil {
				return fmt.Errorf("apply kill: %w", err)
			}
		}
	}
	return nil
}

// putKill reports false when the kill was already stored.
func (s *Store) putKill(ctx context.Context, rs domain.Ruleset, k domain.Kill) (bool, error) {
	it := key(killPK(rs, k.Killer), killSK(k.T, k.Victim))
	it["mp"], it["ttl"] = num(int64(k.MapID)), num(ttlSeconds(k.T, Retention))
	_, err := s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
		{Put: &types.Put{TableName: aws.String(s.table), Item: it, ConditionExpression: aws.String("attribute_not_exists(PK)")}},
		{Update: &types.Update{
			TableName: aws.String(s.table), Key: key(bountyPK(rs), k.Killer), UpdateExpression: aws.String(addKill),
			ExpressionAttributeValues: item{":one": num(1), ":t": num(k.T)},
		}},
	}})
	if isDuplicateKill(err) {
		return false, nil
	}
	return err == nil, mapError(err)
}

func isDuplicateKill(err error) bool {
	var tce *types.TransactionCanceledException
	if !errors.As(err, &tce) {
		return false
	}
	for _, r := range tce.CancellationReasons {
		if aws.ToString(r.Code) == conditionFailedCode {
			return true
		}
	}
	return false
}

// raiseLastKill keeps the newest kill time (the transaction cannot take a max, so it is a second, conditional write).
func (s *Store) raiseLastKill(ctx context.Context, rs domain.Ruleset, k domain.Kill) error {
	_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.table), Key: key(bountyPK(rs), k.Killer), UpdateExpression: aws.String(raiseLastKill),
		ConditionExpression: aws.String(lastKillOlder), ExpressionAttributeValues: item{":t": num(k.T)},
	})
	if err != nil && !isConditionFailed(err) {
		return mapError(err)
	}
	return nil
}
