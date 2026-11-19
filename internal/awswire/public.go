package awswire

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"

	"crimsonledger/internal/apiserver"
	"crimsonledger/internal/store"
)

// Env is the configuration the public function reads from its environment.
type Env struct {
	Table       string // TABLE
	PepperParam string // PEPPER_PARAM
}

// Backends is what cold-start init produces. Production builds it from AWS clients; tests use fakes.
type Backends struct {
	Store   store.Store
	Limiter apiserver.Limiter
	Pepper  []byte
}

// PublicFunc is the API Gateway entry point (HTTP API, payload format 2.0). requestContext.http.sourceIp is the caller's address.
type PublicFunc func(context.Context, events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error)

// Public returns the HTTP API handler. initFn runs on the first request and is retried on the next one
// if it failed (a container that met an SSM outage must not stay broken); until it succeeds every request
// gets 503 with Retry-After. It never answers without authentication.
func Public(initFn func(context.Context) (Backends, error), log *slog.Logger, now func() time.Time) PublicFunc {
	var (
		mu      sync.Mutex
		adapter *httpadapter.HandlerAdapterV2
	)
	get := func(ctx context.Context) (*httpadapter.HandlerAdapterV2, error) {
		mu.Lock()
		defer mu.Unlock()
		if adapter != nil {
			return adapter, nil
		}
		b, err := initFn(ctx)
		if err != nil {
			return nil, err
		}
		opts := apiserver.DefaultOptions()
		api, err := apiserver.New(apiserver.Deps{Store: b.Store, Pepper: b.Pepper, Limiter: b.Limiter, Now: now, Log: log, Opts: opts})
		if err != nil {
			return nil, err
		}
		adapter = httpadapter.NewV2(api)
		return adapter, nil
	}
	return func(ctx context.Context, ev events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
		a, err := get(ctx)
		if err != nil {
			log.Error("cold start failed", "err", err.Error())
			return unavailable(), nil
		}
		return a.ProxyWithContext(ctx, ev)
	}
}

func unavailable() events.APIGatewayV2HTTPResponse {
	return events.APIGatewayV2HTTPResponse{
		StatusCode: http.StatusServiceUnavailable,
		Headers: map[string]string{
			"Content-Type":           "application/json; charset=utf-8",
			"Retry-After":            "5",
			"Cache-Control":          "no-store",
			"X-Content-Type-Options": "nosniff",
		},
		Body: `{"error":{"code":"unavailable","message":"temporarily unavailable"}}`,
	}
}
