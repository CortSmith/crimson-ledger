package dynamostore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"crimsonledger/internal/store"
	"crimsonledger/internal/store/storetest"
)

const tableJSON = "../../../database/dynamodb/table.json"

// localClient connects to DynamoDB Local at $DYNAMODB_ENDPOINT, or skips the test.
func localClient(t *testing.T) *dynamodb.Client {
	t.Helper()
	endpoint := os.Getenv("DYNAMODB_ENDPOINT")
	if endpoint == "" {
		t.Skip("DYNAMODB_ENDPOINT is not set; start DynamoDB Local (docker compose --profile test up dynamodb-local) and set it, e.g. http://127.0.0.1:8000")
	}
	return dynamodb.New(dynamodb.Options{
		Region: "us-east-1", BaseEndpoint: aws.String(endpoint),
		Credentials: credentials.NewStaticCredentialsProvider("local", "local", ""),
	})
}

// newTable creates a uniquely named table from table.json and deletes it when the test ends.
func newTable(t *testing.T, db *dynamodb.Client) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(tableJSON))
	if err != nil {
		t.Fatalf("read table.json: %v", err)
	}
	var in dynamodb.CreateTableInput
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatalf("parse table.json: %v", err)
	}
	suffix := make([]byte, 6)
	_, _ = rand.Read(suffix)
	name := "it-" + hex.EncodeToString(suffix)
	in.TableName = aws.String(name)
	if _, err := db.CreateTable(context.Background(), &in); err != nil {
		t.Fatalf("create table: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.DeleteTable(context.Background(), &dynamodb.DeleteTableInput{TableName: aws.String(name)})
	})
	return name
}

func newDynamoStore(t *testing.T, _ *storetest.FakeClock) store.Store {
	t.Helper()
	db := localClient(t)
	return New(db, newTable(t, db))
}

func TestStore_Contract(t *testing.T) { storetest.Run(t, newDynamoStore, storetest.NoPrune()) }
