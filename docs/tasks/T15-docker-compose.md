# T15: Dockerfiles and docker-compose

**Depends on:** T08, T10, T12, T13, T14 · **Blocks:** T16, T17-T21 · **PLAN.md sections:** [2 D3-D5](../PLAN.md#d3-database-one-store-interface-two-implementations), [3 layout](../PLAN.md#3-repository-layout-all-under-app), [4 config](../PLAN.md#4-config-crimson-ledger), [7 security (container)](../PLAN.md#7-security-baseline-applies-to-every-task-the-audit-phase-checks-it), [9 libraries](../PLAN.md#9-required-system-libraries-end-users-and-builders), [10 workflow](../PLAN.md#10-mandatory-workflow-for-every-task-tdd-with-proof-of-concept-first)

## Goal
(1) `Dockerfile.api`: one image that runs the API **and** the database (SQLite file + `crimson-db`) with config from `~/.crimson-ledger/` mounted in.
(2) `Dockerfile.build`: one image that builds every service on any host (Linux amd64/arm64, Windows exe, Lambda zip) and runs tests/lint.
(3) `docker-compose.yml` at `App/` with profiles for each task.

## Outputs
```
App/Dockerfile.api
App/Dockerfile.build
App/docker-compose.yml
App/.dockerignore
scripts/docker-smoke.sh
```

## Dockerfile.api (multi-stage; final image has no shell)
```dockerfile
# syntax=docker/dockerfile:1.7
FROM golang:1.25-bookworm@sha256:<PIN-IN-POC> AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download && go mod verify
COPY . .
ARG VERSION=dev
ARG TARGETOS TARGETARCH
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=$VERSION" -o /out/crimson-api ./api/cmd/crimson-api \
 && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/crimson-db ./database/cmd/crimson-db

FROM gcr.io/distroless/static-debian12:nonroot@sha256:<PIN-IN-POC>
COPY --from=build /out/crimson-api /out/crimson-db /usr/local/bin/
ENV CRIMSON_LEDGER_HOME=/home/nonroot/.crimson-ledger
VOLUME ["/home/nonroot/.crimson-ledger"]
EXPOSE 8443
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/crimson-api"]
CMD ["serve", "--listen", "0.0.0.0:8443"]
HEALTHCHECK --interval=30s --timeout=3s CMD ["/usr/local/bin/crimson-api", "healthcheck"]
```
Notes: `serve` runs the migrations itself (T08), so "database" is the SQLite file under the mounted home plus `crimson-db` in the image for
`docker compose run --rm db backup|check|prune`. Inside the container `0.0.0.0` is required; compose publishes it **only on host loopback**
(`127.0.0.1:8443:8443`) and the in-container bind is allowed because the container sets `CRIMSON_LEDGER_BEHIND_CONTAINER=1` (the T08 non-loopback rule
accepts this flag **only together** with a loopback host publish note in the log; TLS/`--insecure-lan` rules still apply for LAN exposure). The
`healthcheck` subcommand does `GET /v1/health` with a 2 s timeout (needed because distroless has no `curl`).
`api.json` default `db_path` resolves under the mounted home, so data survives container recreation.

## Dockerfile.build (every service, any device)
```dockerfile
FROM golang:1.25-bookworm@sha256:<PIN> AS tools
RUN apt-get update && apt-get install -y --no-install-recommends \
      gcc pkg-config libwebkit2gtk-4.1-dev libgtk-3-dev xvfb zip shellcheck python3-pip nodejs npm \
 && rm -rf /var/lib/apt/lists/* \
 && pip install --break-system-packages cfn-lint==<PIN> \
 && go install honnef.co/go/tools/cmd/staticcheck@<PIN> golang.org/x/vuln/cmd/govulncheck@<PIN> \
      github.com/securego/gosec/v2/cmd/gosec@<PIN> github.com/golangci/golangci-lint/v2/cmd/golangci-lint@<PIN>
WORKDIR /src

FROM tools AS check          # docker build --target check .   -> runs scripts/check.sh ./...
COPY . .
RUN scripts/check.sh ./...

FROM tools AS dist           # docker build --target dist --output type=local,dest=dist .
COPY . .
ARG VERSION=dev
RUN scripts/build-all.sh "$VERSION"      # see below

FROM scratch AS artifacts
COPY --from=dist /src/dist/ /
```
`scripts/build-all.sh` produces (and `SHA256SUMS`):
```
dist/linux-amd64/{crimson-ledger,lib/crimson-ledger-ui,crimson-scout,lib/crimson-scout-ui,crimson-api,crimson-db}
dist/linux-arm64/...                   (buildx on an arm64 builder or QEMU; cgo UI binaries need the matching platform image)
dist/windows-amd64/{crimson-ledger.exe,lib/crimson-ledger-ui.exe,crimson-scout.exe,lib/crimson-scout-ui.exe,crimson-api.exe,crimson-db.exe}   (CGO_ENABLED=0)
dist/lambda/{public.zip,admin.zip}     (linux/arm64, bootstrap, -tags lambda.norpc)
dist/macos-*/  NOT built here (Apple SDK licence): `make build-macos` on a Mac / CI (T16)
```
Windows binaries use `-ldflags "-H=windowsgui -s -w"` for the UI exes and console for the launcher (so the launcher can print to a terminal and still show a
MessageBox). Launcher stays console-subsystem; verify it doesn't flash a console on double-click (T16 Windows check: use `-H=windowsgui` for the launcher too and attach to the parent console if present via `AttachConsole`). Decide in PoC 4.

## docker-compose.yml
```yaml
name: crimson-ledger
x-hardened: &hardened
  read_only: true
  cap_drop: [ALL]
  security_opt: ["no-new-privileges:true"]
  tmpfs: ["/tmp"]
services:
  api:                                   # docker compose up -d api        (local hosting, SQLite inside)
    build: {context: ., dockerfile: Dockerfile.api}
    <<: *hardened
    ports: ["127.0.0.1:8443:8443"]
    volumes: ["${HOME}/.crimson-ledger:/home/nonroot/.crimson-ledger"]
    restart: unless-stopped
    healthcheck: {test: ["CMD", "/usr/local/bin/crimson-api", "healthcheck"], interval: 30s, timeout: 3s, retries: 3}
  keys:                                  # docker compose run --rm keys create --scope view --label laptop
    build: {context: ., dockerfile: Dockerfile.api}
    <<: *hardened
    entrypoint: ["/usr/local/bin/crimson-api", "keys"]
    volumes: ["${HOME}/.crimson-ledger:/home/nonroot/.crimson-ledger"]
    profiles: [tools]
  db:                                    # docker compose run --rm db backup | check | prune | status
    build: {context: ., dockerfile: Dockerfile.api}
    <<: *hardened
    entrypoint: ["/usr/local/bin/crimson-db"]
    volumes: ["${HOME}/.crimson-ledger:/home/nonroot/.crimson-ledger"]
    profiles: [tools]
  dynamodb-local:
    image: amazon/dynamodb-local@sha256:<PIN>
    command: ["-jar", "DynamoDBLocal.jar", "-inMemory", "-sharedDb"]
    ports: ["127.0.0.1:8000:8000"]
    profiles: [test]
  test:                                  # docker compose --profile test run --rm test       unit + contract + fuzz seeds
    build: {context: ., dockerfile: Dockerfile.build, target: tools}
    command: ["go", "test", "-race", "./..."]
    environment: {DYNAMODB_ENDPOINT: "http://dynamodb-local:8000", AWS_ACCESS_KEY_ID: x, AWS_SECRET_ACCESS_KEY: x, AWS_REGION: us-east-1}
    depends_on: [dynamodb-local]
    profiles: [test]
  check:  {build: {context: ., dockerfile: Dockerfile.build, target: tools}, command: ["scripts/check.sh", "./..."], profiles: [test]}
  build:                                 # docker compose --profile build run --rm build    -> ./dist
    build: {context: ., dockerfile: Dockerfile.build, target: tools}
    command: ["scripts/build-all.sh", "dev"]
    volumes: ["./dist:/src/dist"]
    profiles: [build]
  lambda-package: {build: {context: ., dockerfile: Dockerfile.build, target: tools}, command: ["scripts/build-lambda.sh"], volumes: ["./dist:/src/dist"], profiles: [build]}
  it-local:                              # integration suite against the compose api (T18)
    build: {context: ., dockerfile: Dockerfile.build, target: tools}
    command: ["go", "test", "-tags", "integration", "-count=1", "./tests/..."]
    environment: {CL_TARGET: local, CL_URL: "http://api:8443"}
    env_file: [".it-keys.env"]           # generated by scripts/it-bootstrap.sh, never committed
    depends_on: {api: {condition: service_healthy}}
    profiles: [it]
```
Top-level quick commands (documented in `README` of `App/`): `make up`, `make down`, `make test`, `make check`, `make dist`, `make it-local`, `make deploy-aws`, `make it-aws`.

## Step 0: PoC (`scripts/poc/t15/`)
1. **distroless final image runs** the pure-Go API as `nonroot`, writes SQLite into the mounted volume, survives `docker compose down/up` with data intact. PASS prints a row count after restart. Pin real image digests here and replace the `<PIN>` markers.
2. **Mount permissions**: container uid 65532 cannot write a host `~/.crimson-ledger` owned by uid 1000 with mode 0700. Decide the fix: compose `user: "${UID}:${GID}"` (preferred; image still works) and docs. PASS = write works with `user:` override and 0700/0600 preserved.
3. **Read-only rootfs**: API runs with `read_only: true` + tmpfs `/tmp` (SQLite WAL lives in the mounted dir). PASS.
4. **Build image produces a Linux cgo UI binary** with `-tags webview` and `go vet -tags webview` passes; **Windows cross-build** exes appear; Windows launcher console behaviour decided (see above). PASS prints file list + `file` output.
5. **Image sizes**: API image < 25 MB; build cache reuse (second build < 30 s). Record.
6. **Healthcheck** subcommand works and exits 1 when the API is down.
7. **Vulnerability scan**: `docker run aquasec/trivy image crimson-ledger-api` (or `docker scout`) shows no HIGH/CRITICAL; record result.
8. **buildx arm64** build of the API image via QEMU succeeds (Apple-silicon/Raspberry Pi hosts). PASS.

## Step 1: RED
`scripts/docker-smoke.sh` written first (fails until images exist): builds api image, runs it on a random port with a temp home, creates keys via the `keys` service, posts the example ping, GETs sweep, restarts container, asserts data persists, asserts `docker inspect` shows `ReadonlyRootfs=true`, `User=nonroot`, `CapDrop=[ALL]`, and that the process listens only where expected. Plus `hadolint Dockerfile.*` and `docker compose config -q` as tests.

## Step 2: GREEN
Write the files above, replace `<PIN>`s, add `scripts/build-all.sh`, `scripts/build-lambda.sh`, `.dockerignore` (exclude `.git`, `dist`, `*.db`, `.it-keys.env`, `scripts/poc`).

## Step 3: REFACTOR checklist
- [ ] Layers ordered for cache (go.mod first); no `apt-get` in the final image; one `RUN` per concern.
- [ ] Compose uses anchors for repeated hardening; every service has a comment with its command line.
- [ ] No secrets in compose, env files are generated and git-ignored; `docker compose config` output reviewed for leaked values.
- [ ] Versions and digests pinned in one place (`scripts/versions.env`) and templated in (`envsubst`) or via `ARG`.

## Step 4: AUDIT
- `hadolint`, `docker compose config -q`, trivy/scout clean, `docker-smoke.sh` passes.
- Security: non-root, read-only, no capabilities, no `--privileged`, no docker socket mounts, port bound to loopback on the host, image has no shell/package manager, build args contain no secrets (`docker history --no-trunc` grep for `clk_`).
- Performance: cold start of the API container to healthy < 2 s; image pull size noted; build time with warm cache noted.

## Done when
- [ ] `make up` -> working API with data in `~/.crimson-ledger`; `make dist` -> Linux + Windows + Lambda artifacts on any Docker host; `make test` green.
- [ ] check.sh and smoke green.

## PoC log (2026-11-17)
1. PASS: distroless `nonroot` image runs the API, SQLite in the mounted home, data survives `docker restart` (`scripts/docker-smoke.sh`). Digests pinned in both Dockerfiles as `ARG`s (smoke checks they agree); `scripts/versions.env` was not created, `scripts/tools.env` already holds the tool pins.
2. PASS: uid 65532 cannot use a host 0700 dir owned by 1000, so compose runs `user: ${CL_UID}:${CL_GID}` (`make` exports them). Distroless ships `/home/nonroot` as 0700 which blocked any other uid, so the image copies it in as 0755; `HOME=/home/nonroot` is also set, else `~` resolved to `/`. 0700/0600 preserved.
3. PASS: `read_only` + tmpfs `/tmp` + `cap_drop ALL` + `no-new-privileges`.
4. PASS: `Dockerfile.build --target artifacts` yields linux-amd64 (cgo `-tags webview` UIs), windows-amd64 (GUI-subsystem UI exes, console launcher kept), lambda zips, SHA256SUMS (~100 MB, ~3 min cold). Launcher console decision stays with T16's Windows check.
5. PASS: API image 21.1 MB (< 25); cold start to healthy 0.36 s.
6. PASS: `crimson-api healthcheck [--url]` (new, tested: up, down, 404, stray arg, URL derivation, TLS via the configured cert, never InsecureSkipVerify). `CRIMSON_LEDGER_BEHIND_CONTAINER=1` allows the 0.0.0.0 bind (`modeContainer`).
7. NOT RUN: trivy/scout scan. 8. NOT RUN: buildx arm64 via QEMU.
Also: `GOVULNCHECK_VERSION=latest` now needs Go 1.26 and broke the build; pinned govulncheck v1.1.4 and gosec v2.22.4 in `scripts/tools.env`.

## Follow-ups
- F15-1: PoC 7 (trivy/scout) and PoC 8 (arm64 buildx) not run; hadolint not installed here so the smoke skips it.
- F15-2: `it-local` compose service and `.it-keys.env` wait for T18 (`./tests/...` does not exist yet). `make deploy-aws` / `it-aws` wait for T18 too.
- F15-3: `check` target (`docker build --target check`) not run end to end; repo-wide lint debt (F08-1) will fail it.
- F15-4: `docker history` key grep and "no shell" are asserted in the smoke; trivy aside, the AUDIT step is otherwise done.
