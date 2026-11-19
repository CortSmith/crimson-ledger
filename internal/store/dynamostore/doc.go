// Package dynamostore implements store.Store on one DynamoDB table. The key design
// (every "#" format lives in keys.go) is documented in database/dynamodb/README.md.
// It never uses Scan: every read is a GetItem, BatchGetItem or Query on a known key.
package dynamostore
