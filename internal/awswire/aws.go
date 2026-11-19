package awswire

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"crimsonledger/internal/apiserver"
	"crimsonledger/internal/store/dynamostore"
)

// EnvFromOS reads TABLE and PEPPER_PARAM.
func EnvFromOS() Env { return Env{Table: os.Getenv("TABLE"), PepperParam: os.Getenv("PEPPER_PARAM")} }

// Logger is the JSON logger both functions use (CloudWatch).
func Logger() *slog.Logger { return slog.New(slog.NewJSONHandler(os.Stdout, nil)) }

// InitAWS builds the real backends: DynamoDB store and limiter, pepper from SSM.
func InitAWS(env Env) func(context.Context) (Backends, error) {
	return func(ctx context.Context) (Backends, error) {
		cfg, err := config.LoadDefaultConfig(ctx)
		if err != nil {
			return Backends{}, fmt.Errorf("load aws config: %w", err)
		}
		pepper, err := LoadPepper(ctx, ssm.NewFromConfig(cfg), env.PepperParam)
		if err != nil {
			return Backends{}, err
		}
		db := dynamodb.NewFromConfig(cfg)
		return Backends{
			Store:   dynamostore.New(db, env.Table),
			Limiter: apiserver.NewDynamoLimiter(db, env.Table, time.Now),
			Pepper:  pepper,
		}, nil
	}
}

// NewAdmin builds the admin command handler from the same backends.
func NewAdmin(ctx context.Context, env Env) (*Admin, error) {
	b, err := InitAWS(env)(ctx)
	if err != nil {
		return nil, err
	}
	return &Admin{Keys: b.Store.Keys(), Pepper: b.Pepper, Now: time.Now, Log: Logger()}, nil
}
