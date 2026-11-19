package dynamostore

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go"

	"crimsonledger/internal/domain"
	"crimsonledger/internal/store"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// fakeAPI answers every call with a canned result so tests can inject throttling and partial batches.
type fakeAPI struct {
	api         // nil: calls the test does not expect panic
	getErr      error
	batchWrites int
	unprocessed int // how many BatchWriteItem calls leave everything unprocessed
}

func (f *fakeAPI) GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return &dynamodb.GetItemOutput{}, f.getErr
}

func (f *fakeAPI) BatchWriteItem(_ context.Context, in *dynamodb.BatchWriteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.BatchWriteItemOutput, error) {
	f.batchWrites++
	if f.batchWrites <= f.unprocessed {
		return &dynamodb.BatchWriteItemOutput{UnprocessedItems: in.RequestItems}, nil
	}
	return &dynamodb.BatchWriteItemOutput{}, nil
}

func noSleep(s *Store) *Store {
	s.sleep = func(context.Context, time.Duration) error { return nil }
	return s
}

func oneFix() []types.WriteRequest { return []types.WriteRequest{putRequest(key("F#pvp#g", "1#1"))} }

func TestBatchWrite_RetriesUnprocessedItems(t *testing.T) {
	f := &fakeAPI{unprocessed: 2}
	s := noSleep(newWithAPI(f, "t"))
	if err := s.batchWrite(context.Background(), oneFix()); err != nil {
		t.Fatalf("batchWrite: %v", err)
	}
	if f.batchWrites != 3 {
		t.Errorf("calls = %d; want 3 (two throttled, one accepted)", f.batchWrites)
	}
}

func TestBatchWrite_GivesUpWithErrUnavailable(t *testing.T) {
	f := &fakeAPI{unprocessed: 100}
	s := noSleep(newWithAPI(f, "t"))
	err := s.batchWrite(context.Background(), oneFix())
	if !errors.Is(err, store.ErrUnavailable) || f.batchWrites != maxBatchTries {
		t.Fatalf("err = %v after %d calls; want ErrUnavailable after %d", err, f.batchWrites, maxBatchTries)
	}
}

func TestBatchWrite_SplitsIntoChunksOf25(t *testing.T) {
	f := &fakeAPI{}
	s := noSleep(newWithAPI(f, "t"))
	var reqs []types.WriteRequest
	for i := 0; i < 60; i++ {
		reqs = append(reqs, oneFix()...)
	}
	if err := s.batchWrite(context.Background(), reqs); err != nil || f.batchWrites != 3 {
		t.Fatalf("err = %v, calls = %d; want 3 calls for 60 requests", err, f.batchWrites)
	}
}

type throttleErr struct{ smithy.APIError }

func (throttleErr) Error() string     { return "throttled" }
func (throttleErr) ErrorCode() string { return "ProvisionedThroughputExceededException" }

func TestThrottle_MapsToErrUnavailable(t *testing.T) {
	s := noSleep(newWithAPI(&fakeAPI{getErr: throttleErr{}}, "t"))
	if _, err := s.Version(context.Background(), domain.RulesetPvP); !errors.Is(err, store.ErrUnavailable) {
		t.Fatalf("Version error = %v; want ErrUnavailable", err)
	}
	_, err := s.ApplyPing(context.Background(), domain.Ping{BatchID: "b", Ruleset: domain.RulesetPvP}, time.Now())
	if !errors.Is(err, store.ErrUnavailable) {
		t.Fatalf("ApplyPing error = %v; want ErrUnavailable", err)
	}
}

func TestMapError_LeavesOtherErrorsAlone(t *testing.T) {
	plain := errors.New("boom")
	if got := mapError(plain); !errors.Is(got, plain) || mapError(nil) != nil {
		t.Fatalf("mapError changed a non-throttle error: %v", got)
	}
}

func TestSource_NeverCallsScan(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), "_test.go") || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		if strings.Contains(readFile(t, e.Name()), ".Scan(") {
			t.Errorf("%s calls Scan; every read must be a keyed Query or Get", e.Name())
		}
	}
}
