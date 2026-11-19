// Command crimson-api-admin is the IAM-only key management function: no URL, invoked with the AWS CLI.
package main

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/aws/aws-lambda-go/lambda"

	"crimsonledger/internal/awswire"
)

func main() {
	var (
		mu    sync.Mutex
		admin *awswire.Admin
	)
	lambda.Start(func(ctx context.Context, raw json.RawMessage) (awswire.AdminResponse, error) {
		mu.Lock()
		if admin == nil {
			a, err := awswire.NewAdmin(ctx, awswire.EnvFromOS())
			if err != nil {
				mu.Unlock()
				awswire.Logger().Error("admin init failed", "err", err.Error())
				return awswire.AdminResponse{Status: 503, Error: "not ready; try again"}, nil
			}
			admin = a
		}
		a := admin
		mu.Unlock()
		return a.Handle(ctx, raw)
	})
}
