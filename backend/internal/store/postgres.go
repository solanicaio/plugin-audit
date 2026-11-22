package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// PostgreSQL stores times as TIMESTAMPTZ, truncated to milliseconds like
// SQLite, so that page tokens match stored times exactly.
var postgresDialect = dialect{
	like:     "ILIKE",
	epoch:    "FLOOR(EXTRACT(EPOCH FROM occurred_at))::BIGINT",
	numbered: true,
	time:     func(t time.Time) any { return t.UTC().Truncate(time.Millisecond) },
}

// OpenPostgres opens (and migrates) the PostgreSQL database at uri, e.g.
// postgres://user:pass@host:5432/db?sslmode=require.
func OpenPostgres(uri string) (Store, error) {
	if uri == "" {
		return nil, errors.New("no PostgreSQL connection URI")
	}
	db, err := sql.Open("pgx", uri)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to postgres: %w", err)
	}
	if _, err := db.Exec(postgresSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate postgres: %w", err)
	}
	return &sqlStore{db: db, d: postgresDialect}, nil
}

const postgresSchema = `
CREATE TABLE IF NOT EXISTS events (
  id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  resource_version TEXT NOT NULL,
  type             TEXT NOT NULL,
  occurred_at      TIMESTAMPTZ NOT NULL,
  namespace        TEXT,
  resource_kind    TEXT,
  resource_name    TEXT,
  actor_type       TEXT,
  actor_id         TEXT,
  envelope         TEXT NOT NULL,
  event_name       TEXT,
  cluster          TEXT,
  reason           TEXT,
  severity         TEXT,
  message          TEXT,
  instance         TEXT,
  provider         TEXT,
  UNIQUE(resource_version, type, resource_kind, resource_name)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_events_identity
  ON events(COALESCE(cluster, ''), COALESCE(namespace, ''), event_name) WHERE event_name IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_events_occurred_at ON events(occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_events_type        ON events(type);
CREATE INDEX IF NOT EXISTS idx_events_namespace   ON events(namespace);
CREATE INDEX IF NOT EXISTS idx_events_actor       ON events(actor_id);
CREATE INDEX IF NOT EXISTS idx_events_kind        ON events(resource_kind);
CREATE INDEX IF NOT EXISTS idx_events_reason      ON events(reason);
CREATE INDEX IF NOT EXISTS idx_events_provider    ON events(provider);

CREATE TABLE IF NOT EXISTS meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
`
