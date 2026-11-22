// Package store persists audit events in SQLite or PostgreSQL.
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
)

// Event is the persisted form of a lifecycle event (GET
// /v1/clusters/{cluster}/events), with the fields used for filtering lifted
// out of the verbatim JSON in Envelope.
type Event struct {
	ID int64 `json:"id"`
	// Name is the name of the Kubernetes Event. With Cluster and Namespace it
	// identifies the event. Empty for events recorded before the events API
	// moved to Kubernetes Events.
	Name         string    `json:"name,omitempty"`
	Cursor       string    `json:"cursor"`
	Type         string    `json:"type"`
	Reason       string    `json:"reason,omitempty"`
	Severity     string    `json:"severity,omitempty"`
	OccurredAt   time.Time `json:"occurredAt"`
	Cluster      string    `json:"cluster,omitempty"`
	Namespace    string    `json:"namespace,omitempty"`
	ResourceKind string    `json:"resourceKind,omitempty"`
	ResourceName string    `json:"resourceName,omitempty"`
	ActorType    string    `json:"actorType,omitempty"`
	ActorID      string    `json:"actorID,omitempty"`
	Message      string    `json:"message,omitempty"`
	// Instance is the database instance the object belongs to, and Provider
	// the provider of that instance. Empty when the events API doesn't
	// report them.
	Instance string          `json:"instance,omitempty"`
	Provider string          `json:"provider,omitempty"`
	Envelope json.RawMessage `json:"envelope"`
}

// Filter narrows a Query. Empty fields are ignored.
type Filter struct {
	Kinds      []string
	Reasons    []string
	Namespaces []string
	Actors     []string
	Providers  []string
	// Severities matches Normal or Warning. Events without a severity are
	// Normal.
	Severities []string
	// Name matches the events whose object or instance name contains it.
	Name   string
	Search string
	Since  *time.Time
	Until  *time.Time
	Limit  int
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

// Facets are the values of the filterable columns, with the number of events
// that have each value. Each dimension is counted with the filter on the
// other dimensions, so selecting a value doesn't hide its alternatives.
type Facets struct {
	Kinds      []FacetValue `json:"kinds"`
	Reasons    []FacetValue `json:"reasons"`
	Namespaces []FacetValue `json:"namespaces"`
	Actors     []FacetValue `json:"actors"`
	Providers  []FacetValue `json:"providers"`
}

// FacetValue is a value of a facet and its number of events.
type FacetValue struct {
	Value string `json:"value"`
	Count int64  `json:"count"`
}

// maxFacetValues bounds the values returned per facet, most frequent first.
const maxFacetValues = 100

// Bucket counts the events in [Start, Start+step) of a Histogram.
type Bucket struct {
	Start   time.Time `json:"start"`
	Normal  int64     `json:"normal"`
	Warning int64     `json:"warning"`
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
	// Each calls fn for the events matching f, newest first, up to f.Limit.
	Each(ctx context.Context, f Filter, fn func(*Event) error) error
	Facets(ctx context.Context, f Filter) (*Facets, error)
	Stats(ctx context.Context, f Filter) (*Stats, error)
	// Span returns the times of the oldest and newest events matching f,
	// with ok false when there are none.
	Span(ctx context.Context, f Filter) (oldest, newest time.Time, ok bool, err error)
	// Histogram counts the events matching f in buckets of step from start,
	// which must be at or before f.Since. Empty buckets are omitted.
	Histogram(ctx context.Context, f Filter, start time.Time, step time.Duration) ([]Bucket, error)
	LatestCursor(ctx context.Context) (string, error)
	SaveCursor(ctx context.Context, cursor string) error
	Close() error
}

// Open opens (and migrates) the store of driver: "sqlite" with the path of
// the database file, or "postgres" with a connection URI.
func Open(driver, dsn string) (Store, error) {
	switch driver {
	case "sqlite":
		return OpenSQLite(dsn)
	case "postgres":
		return OpenPostgres(dsn)
	default:
		return nil, fmt.Errorf("unknown storage driver %q", driver)
	}
}

// dialect holds what differs between the SQL databases.
type dialect struct {
	// like is the case-insensitive LIKE operator.
	like string
	// epoch is the Unix time of occurred_at in whole seconds.
	epoch string
	// numbered placeholders are $1, $2, ... instead of ?.
	numbered bool
	// time converts a time to a query argument compared with occurred_at.
	time func(time.Time) any
}

// sqlStore is the Store over a database/sql database. Queries are written
// with ? placeholders, see bind.
type sqlStore struct {
	db *sql.DB
	d  dialect
}

func (s *sqlStore) bind(q string) string {
	if !s.d.numbered {
		return q
	}
	var b strings.Builder
	n := 0
	for _, r := range q {
		if r == '?' {
			n++
			b.WriteString("$" + strconv.Itoa(n))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (s *sqlStore) query(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return s.db.QueryContext(ctx, s.bind(q), args...)
}

func (s *sqlStore) queryRow(ctx context.Context, q string, args ...any) *sql.Row {
	return s.db.QueryRowContext(ctx, s.bind(q), args...)
}

func (s *sqlStore) Insert(ctx context.Context, e *Event) error {
	_, err := s.db.ExecContext(ctx, s.bind(`
		INSERT INTO events
		  (resource_version, type, occurred_at, namespace, resource_kind, resource_name, actor_type, actor_id, envelope,
		   event_name, cluster, reason, severity, message, instance, provider)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING`),
		e.Cursor, e.Type, s.d.time(e.OccurredAt),
		nullable(e.Namespace), nullable(e.ResourceKind), nullable(e.ResourceName),
		nullable(e.ActorType), nullable(e.ActorID), string(e.Envelope),
		nullable(e.Name), nullable(e.Cluster), nullable(e.Reason), nullable(e.Severity), nullable(e.Message),
		nullable(e.Instance), nullable(e.Provider),
	)
	return err
}

func (s *sqlStore) Get(ctx context.Context, id int64) (*Event, error) {
	row := s.queryRow(ctx, baseSelect+` WHERE id = ?`, id)
	e, err := scanEvent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return e, err
}

func (s *sqlStore) Query(ctx context.Context, f Filter) ([]Event, error) {
	var out []Event
	err := s.Each(ctx, f, func(e *Event) error {
		out = append(out, *e)
		return nil
	})
	return out, err
}

func (s *sqlStore) Each(ctx context.Context, f Filter, fn func(*Event) error) error {
	q, args := s.where(baseSelect, f)
	q += ` ORDER BY occurred_at DESC, id DESC LIMIT ?`
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	args = append(args, limit)
	rows, err := s.query(ctx, q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return err
		}
		if err := fn(e); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *sqlStore) Facets(ctx context.Context, f Filter) (*Facets, error) {
	out := &Facets{}
	for _, d := range []struct {
		col string
		dst *[]FacetValue
		// own clears the facet's own dimension from the filter.
		own func(*Filter)
	}{
		{"resource_kind", &out.Kinds, func(f *Filter) { f.Kinds = nil }},
		{"reason", &out.Reasons, func(f *Filter) { f.Reasons = nil }},
		{"namespace", &out.Namespaces, func(f *Filter) { f.Namespaces = nil }},
		{"actor_id", &out.Actors, func(f *Filter) { f.Actors = nil }},
		{"provider", &out.Providers, func(f *Filter) { f.Providers = nil }},
	} {
		df := f
		d.own(&df)
		vals, err := s.count(ctx, d.col, df)
		if err != nil {
			return nil, err
		}
		*d.dst = vals
	}
	return out, nil
}

// count returns the most frequent non-empty values of col, which must be a
// constant column name, among the events matching f.
func (s *sqlStore) count(ctx context.Context, col string, f Filter) ([]FacetValue, error) {
	f.Before = nil
	q, args := s.where(`SELECT `+col+`, COUNT(*) FROM events`, f, col+` IS NOT NULL AND `+col+` <> ''`)
	q += fmt.Sprintf(` GROUP BY %[1]s ORDER BY COUNT(*) DESC, %[1]s LIMIT %[2]d`, col, maxFacetValues)
	rows, err := s.query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FacetValue{}
	for rows.Next() {
		var v FacetValue
		if err := rows.Scan(&v.Value, &v.Count); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *sqlStore) Span(ctx context.Context, f Filter) (oldest, newest time.Time, ok bool, err error) {
	f.Before = nil
	q, args := s.where(`SELECT MIN(occurred_at), MAX(occurred_at) FROM events`, f)
	var lo, hi sqlTime
	if err = s.queryRow(ctx, q, args...).Scan(&lo, &hi); err != nil || !lo.Valid || !hi.Valid {
		return oldest, newest, false, err
	}
	return lo.Time, hi.Time, true, nil
}

func (s *sqlStore) Histogram(ctx context.Context, f Filter, start time.Time, step time.Duration) ([]Bucket, error) {
	secs := int64(step / time.Second)
	if secs <= 0 {
		return nil, errors.New("histogram step must be at least a second")
	}
	f.Before = nil
	from := start.Unix()
	// The bucket of an event is its offset from start in steps; occurred_at
	// is at or after start, so the integer division rounds down.
	base := `SELECT (` + s.d.epoch + ` - ?) / ? AS bucket,
		SUM(CASE WHEN severity = 'Warning' THEN 1 ELSE 0 END), COUNT(*) FROM events`
	q, args := s.where(base, f, "occurred_at >= ?")
	args = append([]any{from, secs}, args...)
	args = append(args, s.d.time(start))
	q += ` GROUP BY bucket ORDER BY bucket`
	rows, err := s.query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Bucket{}
	for rows.Next() {
		var n, warning, total int64
		if err := rows.Scan(&n, &warning, &total); err != nil {
			return nil, err
		}
		out = append(out, Bucket{
			Start:   time.Unix(from+n*secs, 0).UTC(),
			Normal:  total - warning,
			Warning: warning,
		})
	}
	return out, rows.Err()
}

func (s *sqlStore) Stats(ctx context.Context, f Filter) (*Stats, error) {
	out := &Stats{
		ByKind:      map[string]int64{},
		ByReason:    map[string]int64{},
		ByNamespace: map[string]int64{},
		ByActor:     map[string]int64{},
	}

	if q, args := s.where(`SELECT COUNT(*) FROM events`, f); true {
		if err := s.queryRow(ctx, q, args...).Scan(&out.Total); err != nil {
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
		q, args := s.where(base, f, b.where)
		q += fmt.Sprintf(" GROUP BY %s ORDER BY COUNT(*) DESC LIMIT 25", b.col)
		rows, err := s.query(ctx, q, args...)
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

	var last sqlTime
	if err := s.queryRow(ctx, `SELECT MAX(occurred_at) FROM events`).Scan(&last); err == nil && last.Valid {
		out.LastEventAt = &last.Time
	}
	return out, nil
}

// cursorKey is the meta key of the events API cursor. It differs from the key
// used before the events API moved to Kubernetes Events, so an upgraded
// database starts with a relist instead of resuming from a cursor the new
// API doesn't understand.
const cursorKey = "events_cursor"

func (s *sqlStore) LatestCursor(ctx context.Context) (string, error) {
	var v sql.NullString
	err := s.queryRow(ctx, `SELECT value FROM meta WHERE key = ?`, cursorKey).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return v.String, nil
}

func (s *sqlStore) SaveCursor(ctx context.Context, cursor string) error {
	_, err := s.db.ExecContext(ctx, s.bind(`
		INSERT INTO meta(key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`), cursorKey, cursor)
	return err
}

func (s *sqlStore) Close() error { return s.db.Close() }

// ---- helpers ----

const baseSelect = `SELECT id, resource_version, type, occurred_at, namespace,
   resource_kind, resource_name, actor_type, actor_id, envelope,
   event_name, cluster, reason, severity, message, instance, provider FROM events`

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
		instance, provider    sql.NullString
		occurred              sqlTime
		envelopeText          string
	)
	if err := r.Scan(&e.ID, &e.Cursor, &e.Type, &occurred,
		&ns, &rk, &rn, &at, &aid, &envelopeText,
		&name, &cluster, &reason, &severity, &message, &instance, &provider); err != nil {
		return nil, err
	}
	e.OccurredAt = occurred.Time
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
	e.Instance = instance.String
	e.Provider = provider.String
	e.Envelope = json.RawMessage(envelopeText)
	return &e, nil
}

// where appends the conditions of f, and extra conditions that take no
// arguments, to base.
func (s *sqlStore) where(base string, f Filter, extra ...string) (string, []any) {
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
	in("provider", f.Providers)
	in("COALESCE(severity, 'Normal')", f.Severities)
	// contains matches the events where any of cols contains text.
	contains := func(text string, cols ...string) {
		if text == "" {
			return
		}
		var or []string
		for _, col := range cols {
			or = append(or, col+" "+s.d.like+` ? ESCAPE '\'`)
			args = append(args, "%"+escapeLike(text)+"%")
		}
		conds = append(conds, "("+strings.Join(or, " OR ")+")")
	}
	contains(f.Name, "resource_name", "instance")
	contains(f.Search, "resource_name", "message", "envelope")
	if f.Since != nil {
		conds = append(conds, "occurred_at >= ?")
		args = append(args, s.d.time(*f.Since))
	}
	if f.Until != nil {
		conds = append(conds, "occurred_at <= ?")
		args = append(args, s.d.time(*f.Until))
	}
	if f.Before != nil {
		at := s.d.time(f.Before.OccurredAt)
		conds = append(conds, "(occurred_at < ? OR (occurred_at = ? AND id < ?))")
		args = append(args, at, at, f.Before.ID)
	}
	for _, c := range extra {
		if c != "" {
			conds = append(conds, c)
		}
	}
	q := base
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	return q, args
}

// sqlTime scans occurred_at, which SQLite stores as text and PostgreSQL as
// a timestamp. Valid is false for NULL.
type sqlTime struct {
	Time  time.Time
	Valid bool
}

func (t *sqlTime) Scan(v any) error {
	switch v := v.(type) {
	case nil:
		*t = sqlTime{}
		return nil
	case time.Time:
		*t = sqlTime{v.UTC(), true}
		return nil
	case string:
		p, err := time.Parse(time.RFC3339Nano, v)
		*t = sqlTime{p, err == nil}
		return err
	default:
		return fmt.Errorf("unexpected time %T", v)
	}
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// escapeLike makes s match literally in a LIKE pattern with ESCAPE '\'.
func escapeLike(s string) string {
	return likeEscaper.Replace(s)
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
