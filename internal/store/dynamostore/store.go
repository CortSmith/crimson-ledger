package dynamostore

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go"

	"crimsonledger/internal/domain"
	"crimsonledger/internal/store"
)

const (
	// Retention is how long fixes and kills live before DynamoDB TTL removes them (PLAN.md section 7: 24 h ping window).
	Retention = 24 * time.Hour
	// BatchMarkerTTL is how long a batch id is remembered for idempotency.
	BatchMarkerTTL = 24 * time.Hour
	// MaxBountyKillers bounds the killers read for one bounty ranking.
	MaxBountyKillers = 5000
	// maxSearchScan bounds the candidates a name search filters in memory (same as the SQLite store).
	maxSearchScan = 500
	// batchWriteLimit is the DynamoDB cap of items per BatchWriteItem call.
	batchWriteLimit = 25
	// batchGetLimit is the DynamoDB cap of keys per BatchGetItem call.
	batchGetLimit = 100
	// maxBatchTries caps retries of UnprocessedItems before giving up with ErrUnavailable.
	maxBatchTries = 5
	// touchInterval is how often Touch really writes last-used (it saves write capacity).
	touchInterval = time.Minute
	// indexName is the one global secondary index.
	indexName = "GSI1"
)

// api is the part of the DynamoDB client this store uses; tests substitute a fake to inject throttling.
type api interface {
	GetItem(ctx context.Context, in *dynamodb.GetItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	Query(ctx context.Context, in *dynamodb.QueryInput, opts ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
	UpdateItem(ctx context.Context, in *dynamodb.UpdateItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
	PutItem(ctx context.Context, in *dynamodb.PutItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	BatchWriteItem(ctx context.Context, in *dynamodb.BatchWriteItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.BatchWriteItemOutput, error)
	BatchGetItem(ctx context.Context, in *dynamodb.BatchGetItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.BatchGetItemOutput, error)
	TransactWriteItems(ctx context.Context, in *dynamodb.TransactWriteItemsInput, opts ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error)
}

type item = map[string]types.AttributeValue

// Store is a store.Store on one DynamoDB table.
type Store struct {
	db    api
	table string
	sleep func(context.Context, time.Duration) error // injected so retry tests do not wait
}

var _ store.Store = (*Store)(nil)

// New wraps an existing client; table is the table name.
func New(client *dynamodb.Client, table string) *Store { return newWithAPI(client, table) }

func newWithAPI(db api, table string) *Store {
	return &Store{db: db, table: table, sleep: sleepContext}
}

// Open builds a client from the default AWS configuration (environment, shared config or Lambda role).
func Open(ctx context.Context, table string) (*Store, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	return New(dynamodb.NewFromConfig(cfg), table), nil
}

// Close releases nothing: the AWS client holds no connection that needs closing.
func (s *Store) Close() error { return nil }

// Prune does nothing: DynamoDB TTL removes expired fixes, kills and batch markers.
func (s *Store) Prune(context.Context, time.Time) (store.PruneResult, error) {
	return store.PruneResult{}, nil
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// throttleCodes are the API error codes that mean "slow down".
var throttleCodes = map[string]bool{
	"ProvisionedThroughputExceededException": true,
	"ThrottlingException":                    true,
	"RequestLimitExceeded":                   true,
}

// mapError turns throttling into store.ErrUnavailable and leaves everything else alone.
func mapError(err error) error {
	var apiErr smithy.APIError
	if err != nil && errors.As(err, &apiErr) && throttleCodes[apiErr.ErrorCode()] {
		return fmt.Errorf("%w: %w", store.ErrUnavailable, err)
	}
	return err
}

// isConditionFailed reports whether err is a failed ConditionExpression (an expected outcome, not a fault).
func isConditionFailed(err error) bool {
	var ccf *types.ConditionalCheckFailedException
	return errors.As(err, &ccf)
}

// conditionOldItem returns the item a failed condition reported (ReturnValuesOnConditionCheckFailure).
func conditionOldItem(err error) item {
	var ccf *types.ConditionalCheckFailedException
	if errors.As(err, &ccf) {
		return ccf.Item
	}
	return nil
}

// str, num and bin build attribute values.
func str(v string) types.AttributeValue { return &types.AttributeValueMemberS{Value: v} }
func num(v int64) types.AttributeValue {
	return &types.AttributeValueMemberN{Value: strconv.FormatInt(v, 10)}
}
func flt(v float64) types.AttributeValue {
	return &types.AttributeValueMemberN{Value: strconv.FormatFloat(v, 'g', -1, 64)}
}
func bin(v []byte) types.AttributeValue { return &types.AttributeValueMemberB{Value: v} }

func key(pk, sk string) item { return item{"PK": str(pk), "SK": str(sk)} }

// getStr, getInt and getFloat read attributes, returning zero values when absent.
func getStr(it item, name string) string {
	if v, ok := it[name].(*types.AttributeValueMemberS); ok {
		return v.Value
	}
	return ""
}

func getInt(it item, name string) int64 {
	if v, ok := it[name].(*types.AttributeValueMemberN); ok {
		n, _ := strconv.ParseInt(v.Value, 10, 64)
		return n
	}
	return 0
}

func getFloat(it item, name string) float64 {
	if v, ok := it[name].(*types.AttributeValueMemberN); ok {
		f, _ := strconv.ParseFloat(v.Value, 64)
		return f
	}
	return 0
}

func getBytes(it item, name string) []byte {
	if v, ok := it[name].(*types.AttributeValueMemberB); ok {
		return v.Value
	}
	return nil
}

func has(it item, name string) bool { _, ok := it[name]; return ok }

// ttlSeconds is the TTL attribute for something that expires d after t (epoch milliseconds).
func ttlSeconds(t int64, d time.Duration) int64 { return t/1000 + int64(d/time.Second) }

func table(s *Store) *string { return aws.String(s.table) }

// queryAll runs q and follows LastEvaluatedKey until the result is complete or limit items were read (0 = unlimited).
func (s *Store) queryAll(ctx context.Context, q *dynamodb.QueryInput, limit int) ([]item, error) {
	q.TableName = table(s)
	var out []item
	for {
		res, err := s.db.Query(ctx, q)
		if err != nil {
			return nil, mapError(err)
		}
		out = append(out, res.Items...)
		if res.LastEvaluatedKey == nil || (limit > 0 && len(out) >= limit) {
			return out, nil
		}
		q.ExclusiveStartKey = res.LastEvaluatedKey
	}
}

// getContact reads one contact item; found is false when it does not exist.
func (s *Store) getContact(ctx context.Context, rs domain.Ruleset, guid string) (it item, found bool, err error) {
	pk, sk := contactKey(rs, guid)
	res, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{TableName: table(s), Key: key(pk, sk)})
	if err != nil {
		return nil, false, mapError(err)
	}
	return res.Item, len(res.Item) > 0, nil
}

// batchGet reads keys with BatchGetItem in chunks, retrying unprocessed keys; the result is unordered.
func (s *Store) batchGet(ctx context.Context, keys []item) ([]item, error) {
	var out []item
	for len(keys) > 0 {
		chunk := keys[:min(len(keys), batchGetLimit)]
		keys = keys[len(chunk):]
		got, err := s.batchGetChunk(ctx, chunk)
		if err != nil {
			return nil, err
		}
		out = append(out, got...)
	}
	return out, nil
}

func (s *Store) batchGetChunk(ctx context.Context, keys []item) ([]item, error) {
	var out []item
	pending := map[string]types.KeysAndAttributes{s.table: {Keys: keys}}
	for try := 0; len(pending) > 0; try++ {
		if try >= maxBatchTries {
			return nil, fmt.Errorf("%w: batch get still has unprocessed keys", store.ErrUnavailable)
		}
		if err := s.backoff(ctx, try); err != nil {
			return nil, err
		}
		res, err := s.db.BatchGetItem(ctx, &dynamodb.BatchGetItemInput{RequestItems: pending})
		if err != nil {
			return nil, mapError(err)
		}
		out = append(out, res.Responses[s.table]...)
		pending = res.UnprocessedKeys
	}
	return out, nil
}

// batchWrite sends put and delete requests 25 at a time, retrying UnprocessedItems with capped backoff.
func (s *Store) batchWrite(ctx context.Context, reqs []types.WriteRequest) error {
	for len(reqs) > 0 {
		chunk := reqs[:min(len(reqs), batchWriteLimit)]
		reqs = reqs[len(chunk):]
		if err := s.batchWriteChunk(ctx, chunk); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) batchWriteChunk(ctx context.Context, chunk []types.WriteRequest) error {
	pending := map[string][]types.WriteRequest{s.table: chunk}
	for try := 0; len(pending) > 0; try++ {
		if try >= maxBatchTries {
			return fmt.Errorf("%w: batch write still has unprocessed items", store.ErrUnavailable)
		}
		if err := s.backoff(ctx, try); err != nil {
			return err
		}
		res, err := s.db.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{RequestItems: pending})
		if err != nil {
			return mapError(err)
		}
		pending = res.UnprocessedItems
	}
	return nil
}

// backoff waits 0 before the first try, then 50 ms doubling (capped at 800 ms), plus up to 50 % jitter.
func (s *Store) backoff(ctx context.Context, try int) error {
	if try == 0 {
		return ctx.Err()
	}
	d := min(50*time.Millisecond<<(try-1), 800*time.Millisecond)
	return s.sleep(ctx, d+jitter(d))
}

func putRequest(it item) types.WriteRequest {
	return types.WriteRequest{PutRequest: &types.PutRequest{Item: it}}
}

func deleteRequest(pk, sk string) types.WriteRequest {
	return types.WriteRequest{DeleteRequest: &types.DeleteRequest{Key: key(pk, sk)}}
}
