package apiserver

import (
	"context"
	"errors"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"crimsonledger/internal/store"
)

type fakeCounter struct {
	mu     sync.Mutex
	counts map[string]int
	ttls   map[string]string
	err    error
}

func (f *fakeCounter) UpdateItem(_ context.Context, in *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	pk := in.Key["PK"].(*types.AttributeValueMemberS).Value
	f.counts[pk]++
	f.ttls[pk] = in.ExpressionAttributeValues[":ttl"].(*types.AttributeValueMemberN).Value
	return &dynamodb.UpdateItemOutput{Attributes: map[string]types.AttributeValue{
		"count": &types.AttributeValueMemberN{Value: strconv.Itoa(f.counts[pk])},
	}}, nil
}

func newFake() *fakeCounter { return &fakeCounter{counts: map[string]int{}, ttls: map[string]string{}} }

func TestDynamoLimiter_FixedWindow(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 10, 0, time.UTC)
	f := newFake()
	l := newDynamoLimiter(f, "t", func() time.Time { return now })
	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		if ok, _, err := l.Allow(ctx, "ip:1.2.3.4", 3, time.Minute); err != nil || !ok {
			t.Fatalf("request %d: ok=%v err=%v", i, ok, err)
		}
	}
	ok, wait, err := l.Allow(ctx, "ip:1.2.3.4", 3, time.Minute)
	if err != nil || ok {
		t.Fatalf("4th request must be refused, ok=%v err=%v", ok, err)
	}
	if wait != 50*time.Second {
		t.Fatalf("retry after = %v, want 50s (rest of the window)", wait)
	}
	if ok, _, _ := l.Allow(ctx, "ip:9.9.9.9", 3, time.Minute); !ok {
		t.Fatal("another key has its own budget")
	}
	now = now.Add(51 * time.Second) // next window
	if ok, _, err := l.Allow(ctx, "ip:1.2.3.4", 3, time.Minute); err != nil || !ok {
		t.Fatalf("new window must allow again, ok=%v err=%v", ok, err)
	}
}

func TestDynamoLimiter_TTLPresentAndAfterWindow(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 10, 0, time.UTC)
	f := newFake()
	l := newDynamoLimiter(f, "t", func() time.Time { return now })
	if _, _, err := l.Allow(context.Background(), "k", 5, time.Minute); err != nil {
		t.Fatal(err)
	}
	for _, v := range f.ttls {
		ttl, _ := strconv.ParseInt(v, 10, 64)
		if want := time.Date(2026, 11, 17, 12, 1, 0, 0, time.UTC).Add(ttlSlack).Unix(); ttl != want {
			t.Fatalf("ttl %d, want %d", ttl, want)
		}
	}
}

func TestDynamoLimiter_ErrorIsReturned(t *testing.T) {
	boom := errors.New("throttled")
	f := newFake()
	f.err = boom
	ok, _, err := newDynamoLimiter(f, "t", nil).Allow(context.Background(), "k", 5, time.Minute)
	if ok || !errors.Is(err, boom) {
		t.Fatalf("must fail closed with the cause, ok=%v err=%v", ok, err)
	}
}

type failingLimiter struct{}

func (failingLimiter) Allow(context.Context, string, int, time.Duration) (bool, time.Duration, error) {
	return false, 0, errors.New("dynamodb down")
}

func TestLimiterError_FailsClosedWith503(t *testing.T) {
	opts := DefaultOptions()
	h, err := New(Deps{Store: &fakeStore{keys: &fakeKeys{recs: map[string]store.KeyRecord{}}}, Pepper: testPepper, Limiter: failingLimiter{}, Opts: opts})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/health", nil))
	if rec.Code != 503 || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("limiter outage = %d retry %q, want 503 with Retry-After", rec.Code, rec.Header().Get("Retry-After"))
	}
}
