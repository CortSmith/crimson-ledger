# Operating the Crimson Ledger API

The API is a separate download (`crimson-api-<version>-<os>-<arch>`), because not everyone hosts one.
You can run it on your own machine or on AWS.

## Run it on your own machine

```sh
./crimson-api keys create --scope ingest --label scout-pc
./crimson-api keys create --scope view --label laptop
./crimson-api serve
```

It listens on `127.0.0.1:8443` and keeps its data in `~/.crimson-ledger`.
Keys are shown once. List them with `crimson-api keys list`.

With Docker: `make up` starts the API hardened on `127.0.0.1:8443`; `make keys ARGS="create --scope view --label me"` makes a key; `make down` stops it.

## Deploy to AWS

You need the AWS CLI v2 with credentials, Docker and `jq`. Unpack `crimson-ledger-lambda-<version>.zip`, then:

```sh
./deploy.sh --budget-email you@example.com
./deploy.sh --dry-run --yes --budget-email you@example.com   # prints every command, runs none
./deploy.sh --destroy                                        # removes everything
```

To serve the API on your own domain (`api.example.com`), add `--domain`, `--certificate-arn` and optionally `--hosted-zone-id`; the steps are in `api/deploy/README.md`, "Use your own domain name". What it creates and what it costs is listed in the same file. A 5 USD monthly budget alarm is part of it.

## Backups

- **Local:** stop the API and copy `~/.crimson-ledger` (the database file and the config). Keep it private.
- **AWS:** data expires after 24 hours by design, so there is nothing to back up. Keys live in the same table; re-create them after a restore.

## Key rotation

```sh
./crimson-api keys rotate <id>      # local: the new key is shown once
./crimson-api keys revoke <id>      # stops a key at once
```

On AWS use the admin function (see `api/deploy/README.md`). A revoked key stops working within 30 seconds.
After a rotation, paste the new key into the app and press **Test connection**.

## Upgrading

1. Check the new download against `SHA256SUMS` ([INSTALL.md](INSTALL.md)).
2. Local: stop the API, replace the binaries, start it again. Your data and keys stay.
3. AWS: unpack the new Lambda bundle and run `./deploy.sh` again. It updates the same stack.

## What a release costs in CI (free plan)

A tag `app-vX.Y.Z` runs five jobs. The macOS job is the only one that uses paid-tier credits and it runs on tags only.
Check the current credit rate of the macOS resource class in your CircleCI plan page; a build should stay under 5 minutes.
Branch pushes run only the `check` job.

## Releasing

1. Set the number in `VERSION` and commit it.
2. Tag it: `git tag app-v$(cat VERSION) && git push origin app-v$(cat VERSION)`.
3. The `app-release` job needs the CircleCI context `app-release` with `GITHUB_TOKEN`. It refuses to run for fork pull requests.
4. To sign `SHA256SUMS`, add a minisign secret key to the context and set `MINISIGN_KEY` to its path.
