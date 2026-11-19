# DynamoDB layout

One table (`table.json`, default name `crimson-ledger`), keys `PK` and `SK` (strings), TTL attribute `ttl` (epoch seconds),
one global secondary index `GSI1` (`GSI1PK`, `GSI1SK`, projects everything). The code in
`internal/store/dynamostore/keys.go` is the only place that builds keys; `TestKeyFormats_AreDocumentedInReadme`
keeps this table and that file in step. Times are zero-padded to 13 digits and map ids to 6, so string order is numeric order.

| Entity | PK | SK | Attributes |
|---|---|---|---|
| Contact | `C#<ruleset>#<guid>` | `META` | `nm` name, `rw` raw, `rx` reaction, `fs` firstSeen, `ls` lastSeen, `mp` `x` `y` `kd` last position; `GSI1PK=R#<ruleset>`, `GSI1SK=<lastSeen>` |
| Fix | `F#<ruleset>#<guid>` | `<t>#<mapId>` | `x` `y` `kd`, `ttl = t + 24 h` |
| Name word | `W#<ruleset>#<first 2 runes of word>` | `<word>#<guid>` | prefix search with `begins_with(SK, q)`; `#` inside words becomes `_` |
| Kill | `K#<ruleset>#<killer>` | `<t>#<victim>` | `mp`, `ttl = t + 24 h` |
| Bounty total | `B#<ruleset>` | `<killer guid>` | `kills`, `lastKill` |
| Chart | `CHART` | `<mapId>` | `zone`, `x0` `y0` `x1` `y1` (first write wins) |
| Batch marker | `BATCH#<batchId>` | `-` | `ttl = now + 24 h` |
| Version | `V#<ruleset>` | `-` | `v` (atomic `ADD`) |
| API key | `KEY#<id>` | `-` | `sc` scope, `lb` label, `hs` hash, `ca` `ea` `lu` times, `dis`; `GSI1PK=KEYS`, `GSI1SK=<createdAt>#<id>` |

Attribute names are short because they are stored in every item and several full names are DynamoDB reserved words (`name`, `raw`, `map`, `kind`).
No code path calls `Scan`: `TestSource_NeverCallsScan` enforces it.

## Differences from SQLite

* `Prune` is a no-op; TTL deletes fixes, kills and batch markers (DynamoDB Local does not run TTL).
* Bounty totals are not decremented when old kill items expire (totals count all kills since the table was created).
* A name search reads at most 500 candidate guids, then 50 `COUNT` queries for the fixes column.

## Capacity (free tier: 25 WCU + 25 RCU across the table and all indexes)

Table 15 WCU / 15 RCU, `GSI1` 10 WCU / 10 RCU. A contact write costs 2 WCU (item plus index). Measured on DynamoDB Local
(see the PoC log in `docs/tasks/T09-dynamodb-store.md`). Decision rule: if sustained writes average more than 8 WCU/s over a
simulated hour, raise to 15/10 split to 25 total; if still short, switch `BillingMode` to `PAY_PER_REQUEST` and record the cost in the T21 report.
