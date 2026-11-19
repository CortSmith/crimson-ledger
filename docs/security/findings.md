# Security findings (T19 bad-actor suite)

| ID | Attack | Severity | Status | Notes |
|---|---|---|---|---|
| F-X106 | Key-id existence timing: unknown id ~1.5x slower than wrong secret on a real id (key cache misses hit the store) | Low | Accepted | Misses are not cached by design; per-IP lockout (10 failures/5 min) prevents sampling. Review in T20. |
| - | no other open | - | - | The first local run of X101-X104, X201-X207, X301-X311, X402-X409, X503-X504, X802 found no server defect. |

Test-side corrections made while getting green (not server findings): a trailing space in a header value is stripped by HTTP
so it is the same token (X103); Go's client cannot send NUL so that case goes over raw TCP; the sweep ETag carries a time
bucket so state is compared by its version number only; X404 needs its own ingest key because it exhausts the hourly cap.

## Accepted residual risk
* X305: an authorised view key can enumerate player names slowly (rate limited). Any API user can. Review in T20.
* Open: a leaked view key reads every ruleset. T20 decides on a per-key ruleset allowlist.

## T20 Phase A gaps (2026-11-17, from threat-model.md)
* F-T20-1 (Info): no test asserts request-id/key-id audit logging content (API Repudiation cell).
* F-T20-2 (Info): no test for database disk-full/lock behaviour in the abuse suite (T17 covers some).
* F-T20-3 (Low): logger "no listener" is unchecked at runtime (`ss`); X606 not located by test name in T12.
