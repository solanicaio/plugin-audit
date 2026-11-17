// Package store persists audit events.
//
// The MVP ships a single SQLite-backed implementation. Phase 2 will introduce
// a PostgreSQL driver behind the same Store interface.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Event is the persisted form of a lifecycle event (GET
// /v1/clusters/{cluster}/events), with the fields used for filtering lifted
// out of the verbatim JSON in Envelope.
type Event struct {
	ID int64 `json:"id"`
	// Name is the name of the Kubernetes Event. With Cluster and Namespace it
	// identifies the event. Empty for events recorded before the events API
	// moved to Kubernetes Events.
	Name         string          `json:"name,omitempty"`
	Cursor       string          `json:"cursor"`
	Type         string          `json:"type"`
	Reason       string          `json:"reason,omitempty"`
	Severity     string          `json:"severity,omitempty"`
	OccurredAt   time.Time       `json:"occurredAt"`
	Cluster      string          `json:"cluster,omitempty"`
	Namespace    string          `json:"namespace,omitempty"`
	ResourceKind string          `json:"resourceKind,omitempty"`
	ResourceName string          `json:"resourceName,omitempty"`
	ActorType    string          `json:"actorType,omitempty"`
	ActorID      string          `json:"actorID,omitempty"`
	Message      string          `json:"message,omitempty"`
	Envelope     json.RawMessage `json:"envelope"`
}

// Filter narrows a Query. Empty fields are ignored.
type Filter struct {
	Kinds      []string
	Reasons    []string
	Namespaces []string
	Actors     []string
	Search     string
	Since      *time.Time
	Until      *time.Time
	Limit      int
	// Before returns the events older than a page token from a previous
	// Query, see PageToken.
	Before *Page
}

// Page is the position of an event in the newest-first order of a Query.
type Page struct {
	OccurredAt time.Time
	ID         int64
}

// PageToken returns the token of the page after e.
func PageToken(e Event) string {
	return formatTime(e.OccurredAt) + "_" + strconv.FormatInt(e.ID, 10)
}

// ParsePageToken parses a token returned by PageToken.
func ParsePageToken(token string) (*Page, error) {
	at, id, ok := strings.Cut(token, "_")
	if !ok {
		return nil, errors.New("invalid page token")
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return nil, errors.New("invalid page token")
	}
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n <= 0 {
		return nil, errors.New("invalid page token")
	}
	return &Page{OccurredAt: t, ID: n}, nil
}

// timeLayout stores times with a fixed width, so they sort as text.
const timeLayout = "2006-01-02T15:04:05.000Z07:00"

func formatTime(t time.Time) string {
	return t.UTC().Format(timeLayout)
}

// Facets are the distinct values stored for the filterable columns.
type Facets struct {
	Kinds      []string `json:"kinds"`
	Reasons    []string `json:"reasons"`
	Namespaces []string `json:"namespaces"`
	Actors     []string `json:"actors"`
}

// Stats summarises a filtered slice of the audit log.
type Stats struct {
	Total       int64            `json:"total"`
	ByKind      map[string]int64 `json:"byKind"`
	ByReason    map[string]int64 `json:"byReason"`
	ByNamespace map[string]int64 `json:"byNamespace"`
	ByActor     map[string]int64 `json:"byActor"`
	LastEventAt *time.Time       `json:"lastEventAt,omitempty"`
}

// Store is the abstraction over the persistence layer.
type Store interface {
	Insert(ctx context.Context, e *Event) error
	Get(ctx context.Context, id int64) (*Event, error)
	Query(ctx context.Context, f Filter) ([]Event, error)
	Facets(ctx context.Context) (*Facets, error)
	Stats(ctx context.Context, f Filter) (*Stats, error)
	LatestCursor(ctx context.Context) (string, error)
	SaveCursor(ctx context.Context, cursor string) error
	Close() error
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
	s := &sqliteStore{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

type sqliteStore struct {
	db *sql.DB
}

const schema = `
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
var addedColumns = []string{"event_name", "cluster", "reason", "severity", "message"}

// A relist returns events already stored, possibly with a newer
// resourceVersion, so events are identified by their Kubernetes name.
const eventIndexes = `
CREATE UNIQUE INDEX IF NOT EXISTS idx_events_identity
  ON events(COALESCE(cluster, ''), COALESCE(namespace, ''), event_name) WHERE event_name IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_events_kind   ON events(resource_kind);
CREATE INDEX IF NOT EXISTS idx_events_reason ON events(reason);
`

func (s *sqliteStore) migrate() error {
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	existing := map[string]bool{}
	rows, err := s.db.Query(`SELECT name FROM pragma_table_info('events')`)
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
		if _, err := s.db.Exec(`ALTER TABLE events ADD COLUMN ` + col + ` TEXT`); err != nil {
			return fmt.Errorf("add column %s: %w", col, err)
		}
	}
	if _, err := s.db.Exec(eventIndexes); err != nil {
		return err
	}
	// Earlier versions stored RFC 3339 times with a variable number of
	// fractional digits, which don't sort as text.
	_, err = s.db.Exec(`UPDATE events SET occurred_at = COALESCE(strftime('%Y-%m-%dT%H:%M:%fZ', occurred_at), occurred_at)
		WHERE occurred_at NOT GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9]Z'`)
	return err
}

func (s *sqliteStore) Insert(ctx context.Context, e *Event) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO events
		  (resource_version, type, occurred_at, namespace, resource_kind, resource_name, actor_type, actor_id, envelope,
		   event_name, cluster, reason, severity, message)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.Cursor, e.Type, formatTime(e.OccurredAt),
		nullable(e.Namespace), nullable(e.ResourceKind), nullable(e.ResourceName),
		nullable(e.ActorType), nullable(e.ActorID), string(e.Envelope),
		nullable(e.Name), nullable(e.Cluster), nullable(e.Reason), nullable(e.Severity), nullable(e.Message),
	)
	return err
}

func (s *sqliteStore) Get(ctx context.Context, id int64) (*Event, error) {
	row := s.db.QueryRowContext(ctx, baseSelect+` WHERE id = ?`, id)
	e, err := scanEvent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return e, err
}

func (s *sqliteStore) Query(ctx context.Context, f Filter) ([]Event, error) {
	q, args := buildWhere(baseSelect, f)
	q += ` ORDER BY occurred_at DESC, id DESC LIMIT ?`
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

func (s *sqliteStore) Facets(ctx context.Context) (*Facets, error) {
	out := &Facets{}
	for _, f := range []struct {
		col string
		dst *[]string
	}{
		{"resource_kind", &out.Kinds},
		{"reason", &out.Reasons},
		{"namespace", &out.Namespaces},
		{"actor_id", &out.Actors},
	} {
		vals, err := s.distinct(ctx, f.col)
		if err != nil {
			return nil, err
		}
		*f.dst = vals
	}
	return out, nil
}

// distinct returns the distinct non-empty values of col, which must be a
// constant column name.
func (s *sqliteStore) distinct(ctx context.Context, col string) ([]string, error) {
	q := fmt.Sprintf(`SELECT DISTINCT %[1]s FROM events WHERE %[1]s IS NOT NULL AND %[1]s <> '' ORDER BY %[1]s`, col)
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var v sql.NullString
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		if v.Valid {
			out = append(out, v.String)
		}
	}
	return out, rows.Err()
}

func (s *sqliteStore) Stats(ctx context.Context, f Filter) (*Stats, error) {
	out := &Stats{
		ByKind:      map[string]int64{},
		ByReason:    map[string]int64{},
		ByNamespace: map[string]int64{},
		ByActor:     map[string]int64{},
	}

	if q, args := buildWhere(`SELECT COUNT(*) FROM events`, f); true {
		if err := s.db.QueryRowContext(ctx, q, args...).Scan(&out.Total); err != nil {
			return nil, err
		}
	}

	type bucket struct {
		col   string
		dst   map[string]int64
		where string
	}
	buckets := []bucket{
		{"resource_kind", out.ByKind, "resource_kind IS NOT NULL AND resource_kind <> ''"},
		{"reason", out.ByReason, "reason IS NOT NULL AND reason <> ''"},
		{"namespace", out.ByNamespace, "namespace IS NOT NULL AND namespace <> ''"},
		{"actor_id", out.ByActor, "actor_id IS NOT NULL AND actor_id <> ''"},
	}
	for _, b := range buckets {
		base := fmt.Sprintf(`SELECT %s, COUNT(*) FROM events`, b.col)
		q, args := buildWhere(base, f)
		if b.where != "" {
			if strings.Contains(q, "WHERE") {
				q += " AND " + b.where
			} else {
				q += " WHERE " + b.where
			}
		}
		q += fmt.Sprintf(" GROUP BY %s ORDER BY COUNT(*) DESC LIMIT 25", b.col)
		rows, err := s.db.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var k string
			var n int64
			if err := rows.Scan(&k, &n); err != nil {
				rows.Close()
				return nil, err
			}
			b.dst[k] = n
		}
		rows.Close()
	}

	var lastStr sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(occurred_at) FROM events`).Scan(&lastStr); err == nil && lastStr.Valid {
		if t, perr := time.Parse(time.RFC3339Nano, lastStr.String); perr == nil {
			out.LastEventAt = &t
		}
	}
	return out, nil
}

// cursorKey is the meta key of the events API cursor. It differs from the key
// used before the events API moved to Kubernetes Events, so an upgraded
// database starts with a relist instead of resuming from a cursor the new
// API doesn't understand.
const cursorKey = "events_cursor"

func (s *sqliteStore) LatestCursor(ctx context.Context) (string, error) {
	var v sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, cursorKey).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return v.String, nil
}

func (s *sqliteStore) SaveCursor(ctx context.Context, cursor string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO meta(key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, cursorKey, cursor)
	return err
}

func (s *sqliteStore) Close() error { return s.db.Close() }

// ---- helpers ----

const baseSelect = `SELECT id, resource_version, type, occurred_at, namespace,
   resource_kind, resource_name, actor_type, actor_id, envelope,
   event_name, cluster, reason, severity, message FROM events`

type scanner interface {
	Scan(dest ...any) error
}

func scanEvent(r scanner) (*Event, error) {
	var (
		e                     Event
		ns, rk, rn            sql.NullString
		at, aid               sql.NullString
		name, cluster, reason sql.NullString
		severity, message     sql.NullString
		occurred              string
		envelopeText          string
	)
	if err := r.Scan(&e.ID, &e.Cursor, &e.Type, &occurred,
		&ns, &rk, &rn, &at, &aid, &envelopeText,
		&name, &cluster, &reason, &severity, &message); err != nil {
		return nil, err
	}
	t, err := time.Parse(time.RFC3339Nano, occurred)
	if err != nil {
		return nil, fmt.Errorf("parse occurred_at: %w", err)
	}
	e.OccurredAt = t
	e.Namespace = ns.String
	e.ResourceKind = rk.String
	e.ResourceName = rn.String
	e.ActorType = at.String
	e.ActorID = aid.String
	e.Name = name.String
	e.Cluster = cluster.String
	e.Reason = reason.String
	e.Severity = severity.String
	e.Message = message.String
	e.Envelope = json.RawMessage(envelopeText)
	return &e, nil
}

func buildWhere(base string, f Filter) (string, []any) {
	var (
		conds []string
		args  []any
	)
	in := func(col string, vals []string) {
		if len(vals) == 0 {
			return
		}
		conds = append(conds, col+" IN ("+placeholders(len(vals))+")")
		for _, v := range vals {
			args = append(args, v)
		}
	}
	in("resource_kind", f.Kinds)
	in("reason", f.Reasons)
	in("namespace", f.Namespaces)
	in("actor_id", f.Actors)
	if f.Search != "" {
		conds = append(conds, "(resource_name LIKE ? OR message LIKE ? OR envelope LIKE ?)")
		like := "%" + f.Search + "%"
		args = append(args, like, like, like)
	}
	if f.Since != nil {
		conds = append(conds, "occurred_at >= ?")
		args = append(args, formatTime(*f.Since))
	}
	if f.Until != nil {
		conds = append(conds, "occurred_at <= ?")
		args = append(args, formatTime(*f.Until))
	}
	if f.Before != nil {
		at := formatTime(f.Before.OccurredAt)
		conds = append(conds, "(occurred_at < ? OR (occurred_at = ? AND id < ?))")
		args = append(args, at, at, f.Before.ID)
	}
	q := base
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	return q, args
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?,", n-1) + "?"
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
