package store

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// SQLite stores times as text in the fixed-width timeLayout.
var sqliteDialect = dialect{
	like:  "LIKE",
	epoch: "CAST(strftime('%s', occurred_at) AS INTEGER)",
	time:  func(t time.Time) any { return formatTime(t) },
}

// OpenSQLite opens (and migrates) the SQLite database at path.
func OpenSQLite(path string) (Store, error) {
	if err := ensureDir(path); err != nil {
		return nil, err
	}
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite is single-writer; keep things simple.
	if err := migrateSQLite(db); err != nil {
		db.Close()
		return nil, err
	}
	return &sqlStore{db: db, d: sqliteDialect}, nil
}

const sqliteSchema = `
CREATE TABLE IF NOT EXISTS events (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  resource_version TEXT NOT NULL,
  type             TEXT NOT NULL,
  occurred_at      TEXT NOT NULL,
  namespace        TEXT,
  resource_kind    TEXT,
  resource_name    TEXT,
  actor_type       TEXT,
  actor_id         TEXT,
  envelope         TEXT NOT NULL,
  UNIQUE(resource_version, type, resource_kind, resource_name)
);
CREATE INDEX IF NOT EXISTS idx_events_occurred_at ON events(occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_events_type        ON events(type);
CREATE INDEX IF NOT EXISTS idx_events_namespace   ON events(namespace);
CREATE INDEX IF NOT EXISTS idx_events_actor       ON events(actor_id);

CREATE TABLE IF NOT EXISTS meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
`

// addedColumns were added when the events API moved to Kubernetes Events.
// Rows stored before keep NULL in them.
var addedColumns = []string{"event_name", "cluster", "reason", "severity", "message", "instance", "provider"}

// A relist returns events already stored, possibly with a newer
// resourceVersion, so events are identified by their Kubernetes name.
const eventIndexes = `
CREATE UNIQUE INDEX IF NOT EXISTS idx_events_identity
  ON events(COALESCE(cluster, ''), COALESCE(namespace, ''), event_name) WHERE event_name IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_events_kind   ON events(resource_kind);
CREATE INDEX IF NOT EXISTS idx_events_reason ON events(reason);
CREATE INDEX IF NOT EXISTS idx_events_provider ON events(provider);
`

func migrateSQLite(db *sql.DB) error {
	if _, err := db.Exec(sqliteSchema); err != nil {
		return err
	}
	existing := map[string]bool{}
	rows, err := db.Query(`SELECT name FROM pragma_table_info('events')`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		existing[name] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, col := range addedColumns {
		if existing[col] {
			continue
		}
		if _, err := db.Exec(`ALTER TABLE events ADD COLUMN ` + col + ` TEXT`); err != nil {
			return fmt.Errorf("add column %s: %w", col, err)
		}
	}
	if _, err := db.Exec(eventIndexes); err != nil {
		return err
	}
	// Earlier versions stored RFC 3339 times with a variable number of
	// fractional digits, which don't sort as text.
	_, err = db.Exec(`UPDATE events SET occurred_at = COALESCE(strftime('%Y-%m-%dT%H:%M:%fZ', occurred_at), occurred_at)
		WHERE occurred_at NOT GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9]Z'`)
	if err != nil {
		return err
	}
	// An Instance is its own instance, also for events recorded before the
	// events API reported it.
	_, err = db.Exec(`UPDATE events SET instance = resource_name
		WHERE instance IS NULL AND resource_kind = 'Instance' AND event_name IS NOT NULL`)
	return err
}
