.PHONY: check test lint build-linux build-windows build-macos up down keys dist docker-test docker-check docker-smoke
export CL_UID := $(shell id -u)
export CL_GID := $(shell id -g)
COMPOSE := docker compose

check:         ; scripts/check.sh ./...
test:          ; go test -race ./...
lint:          ; golangci-lint run ./...
build-linux:   ; CGO_ENABLED=1 go build -tags webview ./viewer/... ./logger/...   # run inside Dockerfile.build
build-windows: ; GOOS=windows CGO_ENABLED=0 go build -tags webview ./viewer/... ./logger/...
build-macos:   ; GOOS=darwin CGO_ENABLED=1 go build -tags webview ./viewer/... ./logger/...   # run on a Mac only

# Docker (T15). `make up` hosts the API on 127.0.0.1:8443 with data in ~/.crimson-ledger.
up:            ; mkdir -p -m 700 $${CL_HOME:-$$HOME/.crimson-ledger} && $(COMPOSE) up -d --build api
down:          ; $(COMPOSE) down
keys:          ; $(COMPOSE) run --rm keys $(ARGS)
docker-test:   ; $(COMPOSE) --profile test run --rm --build test
docker-check:  ; $(COMPOSE) --profile test run --rm --build check
dist:          ; $(COMPOSE) --profile build run --rm --build build
docker-smoke:  ; scripts/docker-smoke.sh

# Integration suite (T18). it-local builds and starts throwaway API instances itself.
.PHONY: it-local it-fault
it-local:      ; scripts/it-bootstrap.sh
it-fault:      ; go test -tags integration -count=1 -run 'TestC_' ./tests/integration/...

# Bad-actor suite (T19), local target. Starts throwaway instances like it-local.
.PHONY: abuse-local
abuse-local:   ; scripts/abuse-local.sh
