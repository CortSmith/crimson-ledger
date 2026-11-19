package dynamostore

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"crimsonledger/internal/store"
)

type keyStore struct{ s *Store }

// Keys returns the API key store.
func (s *Store) Keys() store.KeyStore { return keyStore{s} }

// Put adds a key; ErrConflict if the id exists. Keys are indexed on GSI1 (partition KEYS) so List needs no Scan.
func (k keyStore) Put(ctx context.Context, r store.KeyRecord) error {
	it := key(apiKeyPK(r.ID), skNone)
	it["sc"], it["lb"], it["hs"] = str(string(r.Scope)), str(r.Label), bin(r.Hash)
	it["ca"], it["dis"] = num(r.CreatedAt.UnixMilli()), &types.AttributeValueMemberBOOL{Value: r.Disabled}
	it["GSI1PK"], it["GSI1SK"] = str(keyAPIKeys), str(apiKeyIndexSK(r.CreatedAt.UnixMilli(), r.ID))
	if r.ExpiresAt != nil {
		it["ea"] = num(r.ExpiresAt.UnixMilli())
	}
	if r.LastUsedAt != nil {
		it["lu"] = num(r.LastUsedAt.UnixMilli())
	}
	_, err := k.s.db.PutItem(ctx, &dynamodb.PutItemInput{TableName: table(k.s), Item: it, ConditionExpression: aws.String("attribute_not_exists(PK)")})
	if isConditionFailed(err) {
		return fmt.Errorf("put key %s: %w", r.ID, store.ErrConflict)
	}
	if err != nil {
		return fmt.Errorf("put key %s: %w", r.ID, mapError(err))
	}
	return nil
}

// Get returns a key; ErrNotFound if the id is unknown.
func (k keyStore) Get(ctx context.Context, id string) (store.KeyRecord, error) {
	res, err := k.s.db.GetItem(ctx, &dynamodb.GetItemInput{TableName: table(k.s), Key: key(apiKeyPK(id), skNone), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return store.KeyRecord{}, fmt.Errorf("get key %s: %w", id, mapError(err))
	}
	if len(res.Item) == 0 {
		return store.KeyRecord{}, fmt.Errorf("get key %s: %w", id, store.ErrNotFound)
	}
	return keyFromItem(res.Item), nil
}

// List returns every key, oldest first.
func (k keyStore) List(ctx context.Context) ([]store.KeyRecord, error) {
	items, err := k.s.queryAll(ctx, &dynamodb.QueryInput{
		IndexName: aws.String(indexName), KeyConditionExpression: aws.String("GSI1PK = :p"),
		ExpressionAttributeValues: item{":p": str(keyAPIKeys)},
	}, 0)
	if err != nil {
		return nil, fmt.Errorf("list keys: %w", err)
	}
	out := make([]store.KeyRecord, 0, len(items))
	for _, it := range items {
		out = append(out, keyFromItem(it))
	}
	return out, nil
}

// SetDisabled revokes or restores a key.
func (k keyStore) SetDisabled(ctx context.Context, id string, disabled bool) error {
	_, err := k.s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: table(k.s), Key: key(apiKeyPK(id), skNone), UpdateExpression: aws.String("SET dis = :d"),
		ConditionExpression:       aws.String("attribute_exists(PK)"),
		ExpressionAttributeValues: item{":d": &types.AttributeValueMemberBOOL{Value: disabled}},
	})
	if isConditionFailed(err) {
		return fmt.Errorf("update key %s: %w", id, store.ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("update key %s: %w", id, mapError(err))
	}
	return nil
}

// Touch records last-used at most once a minute per key, which keeps the write cost of authenticating requests near zero.
func (k keyStore) Touch(ctx context.Context, id string, now time.Time) error {
	_, err := k.s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: table(k.s), Key: key(apiKeyPK(id), skNone), UpdateExpression: aws.String("SET lu = :now"),
		ConditionExpression:                 aws.String("attribute_exists(PK) AND (attribute_not_exists(lu) OR lu < :cutoff)"),
		ExpressionAttributeValues:           item{":now": num(now.UnixMilli()), ":cutoff": num(now.Add(-touchInterval).UnixMilli())},
		ReturnValuesOnConditionCheckFailure: types.ReturnValuesOnConditionCheckFailureAllOld,
	})
	if isConditionFailed(err) {
		if len(conditionOldItem(err)) == 0 {
			return fmt.Errorf("touch key %s: %w", id, store.ErrNotFound)
		}
		return nil // used less than a minute ago: nothing to write
	}
	if err != nil {
		return fmt.Errorf("touch key %s: %w", id, mapError(err))
	}
	return nil
}

func keyFromItem(it item) store.KeyRecord {
	r := store.KeyRecord{
		ID: getStr(it, "PK")[len(prefixKey):], Scope: store.Scope(getStr(it, "sc")), Label: getStr(it, "lb"),
		Hash: getBytes(it, "hs"), CreatedAt: time.UnixMilli(getInt(it, "ca")).UTC(),
	}
	if v, ok := it["dis"].(*types.AttributeValueMemberBOOL); ok {
		r.Disabled = v.Value
	}
	if has(it, "ea") {
		t := time.UnixMilli(getInt(it, "ea")).UTC()
		r.ExpiresAt = &t
	}
	if has(it, "lu") {
		t := time.UnixMilli(getInt(it, "lu")).UTC()
		r.LastUsedAt = &t
	}
	return r
}
