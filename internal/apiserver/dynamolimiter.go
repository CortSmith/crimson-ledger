package apiserver

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// counterAPI is the one DynamoDB call the limiter makes; tests substitute a fake.
type counterAPI interface {
	UpdateItem(ctx context.Context, in *dynamodb.UpdateItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
}

// ttlSlack keeps a counter item alive a little past its window, so a slow clock cannot resurrect a counter.
const ttlSlack = 2 * time.Minute

// DynamoLimiter is a fixed-window counter shared by every Lambda container (an in-memory bucket would
// give each container its own budget). Item: PK "RL#<key>#<window>", SK "-", count, ttl.
// Any error is returned to the caller, which fails closed (503) so an outage is not a free-abuse window.
type DynamoLimiter struct {
	db    counterAPI
	table string
	now   func() time.Time
}

// NewDynamoLimiter counts in table using client; now may be nil for time.Now.
func NewDynamoLimiter(client *dynamodb.Client, table string, now func() time.Time) *DynamoLimiter {
	return newDynamoLimiter(client, table, now)
}

func newDynamoLimiter(db counterAPI, table string, now func() time.Time) *DynamoLimiter {
	if now == nil {
		now = time.Now
	}
	return &DynamoLimiter{db: db, table: table, now: now}
}

// Allow implements Limiter.
func (l *DynamoLimiter) Allow(ctx context.Context, key string, limit int, per time.Duration) (bool, time.Duration, error) {
	now := l.now()
	window := now.UnixNano() / int64(per)
	end := time.Unix(0, (window+1)*int64(per))
	pk := "RL#" + key + "#" + strconv.FormatInt(window, 10)
	out, err := l.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:        aws.String(l.table),
		Key:              map[string]types.AttributeValue{"PK": &types.AttributeValueMemberS{Value: pk}, "SK": &types.AttributeValueMemberS{Value: "-"}},
		UpdateExpression: aws.String("ADD #c :one SET #t = :ttl"),
		ExpressionAttributeNames: map[string]string{
			"#c": "count", "#t": "ttl",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":one": &types.AttributeValueMemberN{Value: "1"},
			":ttl": &types.AttributeValueMemberN{Value: strconv.FormatInt(end.Add(ttlSlack).Unix(), 10)},
		},
		ReturnValues: types.ReturnValueUpdatedNew,
	})
	if err != nil {
		return false, 0, fmt.Errorf("rate counter: %w", err)
	}
	attr, ok := out.Attributes["count"].(*types.AttributeValueMemberN)
	if !ok {
		return false, 0, fmt.Errorf("rate counter: no count in response")
	}
	n, err := strconv.Atoi(attr.Value)
	if err != nil {
		return false, 0, fmt.Errorf("rate counter: %w", err)
	}
	if n <= limit {
		return true, 0, nil
	}
	return false, max(end.Sub(now), time.Second), nil
}
