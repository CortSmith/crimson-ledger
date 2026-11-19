-- 0001: the whole v1 ledger. Contacts, fixes, kills and charts are the data the API serves;
-- batches give POST /v1/pings idempotency; ping_versions are the ETag tripwire; api_keys hold hashed keys.

-- Every ruleset has its own universe of contacts (same GUID on two servers is two rows).
CREATE TABLE contacts (
  ruleset    TEXT    NOT NULL CHECK (ruleset IN ('normal','pvp','rp','rppvp')),
  guid       TEXT    NOT NULL,
  name       TEXT    NOT NULL,
  raw        TEXT    NOT NULL,
  reaction   TEXT    NOT NULL CHECK (reaction IN ('friendly','hostile','neutral')),
  first_seen INTEGER NOT NULL,
  last_seen  INTEGER NOT NULL,
  last_map   INTEGER,
  last_x     REAL,
  last_y     REAL,
  last_kind  TEXT CHECK (last_kind IN ('exact','approx')),
  PRIMARY KEY (ruleset, guid)
) WITHOUT ROWID;
CREATE INDEX contacts_by_seen ON contacts (ruleset, last_seen DESC);

-- Name search: one row per normalized word of the name; prefix match = range scan on (ruleset, word).
CREATE TABLE contact_words (
  ruleset TEXT NOT NULL,
  word    TEXT NOT NULL,
  guid    TEXT NOT NULL,
  PRIMARY KEY (ruleset, word, guid)
) WITHOUT ROWID;

CREATE TABLE fixes (
  ruleset TEXT    NOT NULL,
  guid    TEXT    NOT NULL,
  t       INTEGER NOT NULL,
  map_id  INTEGER NOT NULL,
  x       REAL    NOT NULL,
  y       REAL    NOT NULL,
  kind    TEXT    NOT NULL CHECK (kind IN ('exact','approx')),
  PRIMARY KEY (ruleset, guid, t, map_id)
) WITHOUT ROWID;
CREATE INDEX fixes_by_time ON fixes (t); -- retention pruning

CREATE TABLE charts (
  map_id INTEGER PRIMARY KEY,
  zone   TEXT NOT NULL,
  x0 REAL NOT NULL, y0 REAL NOT NULL, x1 REAL NOT NULL, y1 REAL NOT NULL
);

CREATE TABLE kills (
  ruleset TEXT    NOT NULL,
  t       INTEGER NOT NULL,
  killer  TEXT    NOT NULL,
  victim  TEXT    NOT NULL,
  map_id  INTEGER NOT NULL,
  PRIMARY KEY (ruleset, t, killer, victim)
) WITHOUT ROWID;
CREATE INDEX kills_by_killer ON kills (ruleset, killer);

CREATE TABLE ping_versions (ruleset TEXT PRIMARY KEY, version INTEGER NOT NULL); -- the "tripwire" for ETags
CREATE TABLE batches (batch_id TEXT PRIMARY KEY, received_at INTEGER NOT NULL);   -- idempotency

CREATE TABLE api_keys (
  id           TEXT PRIMARY KEY,
  scope        TEXT    NOT NULL CHECK (scope IN ('ingest','view')),
  label        TEXT    NOT NULL,
  hash         BLOB    NOT NULL,
  created_at   INTEGER NOT NULL,
  expires_at   INTEGER,
  disabled     INTEGER NOT NULL DEFAULT 0,
  last_used_at INTEGER
);
