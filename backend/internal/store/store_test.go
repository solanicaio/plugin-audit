package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func newEvent(name, cursor, kind, reason string) *Event {
	return &Event{
		Name:         name,
		Cursor:       cursor,
		Type:         "instance.created",
		Reason:       reason,
		OccurredAt:   time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC),
		Cluster:      "main",
		Namespace:    "dbs",
		ResourceKind: kind,
		ResourceName: "db1",
		Envelope:     []byte(`{"name":"` + name + `"}`),
	}
}

func TestInsertDeduplicatesByEventName(t *testing.T) {
	ctx := context.Background()
	st, err := OpenSQLite(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// A relist returns the same Kubernetes Event, possibly with a newer
	// resourceVersion.
	for _, e := range []*Event{
		newEvent("db1.abc", "100", "Instance", "Created"),
		newEvent("db1.abc", "105", "Instance", "Created"),
		newEvent("db1.def", "110", "Instance", "Created"),
	} {
		if err := st.Insert(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.Query(ctx, Filter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}
}

func TestQueryFiltersAndFacets(t *testing.T) {
	ctx := context.Background()
	st, err := OpenSQLite(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, e := range []*Event{
		newEvent("a", "1", "Instance", "Created"),
		newEvent("b", "2", "Backup", "Created"),
		newEvent("c", "3", "Instance", "Deleted"),
	} {
		if err := st.Insert(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.Query(ctx, Filter{Kinds: []string{"Instance"}, Reasons: []string{"Deleted"}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "c" {
		t.Fatalf("got %+v, want only event c", got)
	}
	// A facet is counted without its own selection, so the other kinds stay
	// selectable, and with the selection of the other facets.
	f, err := st.Facets(ctx, Filter{Kinds: []string{"Instance"}, Reasons: []string{"Created"}})
	if err != nil {
		t.Fatal(err)
	}
	want := Facets{
		Kinds:      []FacetValue{{"Backup", 1}, {"Instance", 1}},
		Reasons:    []FacetValue{{"Created", 1}, {"Deleted", 1}},
		Namespaces: []FacetValue{{"dbs", 1}},
		Actors:     []FacetValue{},
		Providers:  []FacetValue{},
	}
	if !reflect.DeepEqual(*f, want) {
		t.Fatalf("got facets %+v, want %+v", *f, want)
	}
}

func TestQueryByInstanceProviderAndSeverity(t *testing.T) {
	ctx := context.Background()
	st, err := OpenSQLite(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, in := range []struct {
		name, kind, object, instance, provider, severity string
	}{
		{"a", "Instance", "orders-db", "orders-db", "percona-server-mysql", "Normal"},
		{"b", "Backup", "nightly-1", "orders-db", "percona-server-mysql", "Warning"},
		{"c", "Instance", "users_db", "users_db", "percona-postgresql", "Normal"},
		{"d", "User", "admin", "", "", ""},
	} {
		e := newEvent(in.name, in.name, in.kind, "Created")
		e.ResourceName, e.Instance, e.Provider, e.Severity = in.object, in.instance, in.provider, in.severity
		if err := st.Insert(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name string
		f    Filter
		want string
	}{
		{"provider", Filter{Providers: []string{"percona-server-mysql"}}, "b,a"},
		{"name matches the instance of a backup", Filter{Name: "orders"}, "b,a"},
		{"name matches the object", Filter{Name: "nightly"}, "b"},
		{"name is not a pattern", Filter{Name: "%"}, ""},
		{"name with an underscore", Filter{Name: "s_db"}, "c"},
		{"warnings", Filter{Severities: []string{"Warning"}}, "b"},
		{"no severity is normal", Filter{Severities: []string{"Normal"}, Kinds: []string{"User"}}, "d"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.f.Limit = 10
			got, err := st.Query(ctx, tc.f)
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, e := range got {
				names = append(names, e.Name)
			}
			if strings.Join(names, ",") != tc.want {
				t.Fatalf("got %v, want %s", names, tc.want)
			}
		})
	}
	f, err := st.Facets(ctx, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if want := []FacetValue{{"percona-server-mysql", 2}, {"percona-postgresql", 1}}; !reflect.DeepEqual(f.Providers, want) {
		t.Fatalf("got providers %+v, want %+v", f.Providers, want)
	}
}

func TestHistogram(t *testing.T) {
	ctx := context.Background()
	st, err := OpenSQLite(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	start := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	for i, in := range []struct {
		at       time.Duration
		severity string
	}{
		{0, "Normal"},
		{4*time.Minute + 59*time.Second, "Warning"},
		{5 * time.Minute, ""},
		{17 * time.Minute, "Warning"},
		// Before the range.
		{-time.Second, "Normal"},
	} {
		e := newEvent(strconv.Itoa(i), strconv.Itoa(i), "Instance", "Created")
		e.OccurredAt, e.Severity = start.Add(in.at), in.severity
		if err := st.Insert(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.Histogram(ctx, Filter{}, start, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	want := []Bucket{
		{start, 1, 1},
		{start.Add(5 * time.Minute), 1, 0},
		{start.Add(15 * time.Minute), 0, 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}

	oldest, newest, ok, err := st.Span(ctx, Filter{Severities: []string{"Warning"}})
	if err != nil || !ok {
		t.Fatalf("span: %v, %v", ok, err)
	}
	if !oldest.Equal(start.Add(4*time.Minute+59*time.Second)) || !newest.Equal(start.Add(17*time.Minute)) {
		t.Fatalf("got span %s..%s", oldest, newest)
	}
	if _, _, ok, err := st.Span(ctx, Filter{Kinds: []string{"Nope"}}); ok || err != nil {
		t.Fatalf("span of no events: %v, %v", ok, err)
	}
}

// TestMigratesTheOldSchema opens a database written by v0.1.x, before the
// events API moved to Kubernetes Events.
func TestMigratesTheOldSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "audit.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
CREATE TABLE events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  resource_version TEXT NOT NULL,
  type TEXT NOT NULL,
  occurred_at TEXT NOT NULL,
  namespace TEXT,
  resource_kind TEXT,
  resource_name TEXT,
  actor_type TEXT,
  actor_id TEXT,
  envelope TEXT NOT NULL,
  UNIQUE(resource_version, type, resource_kind, resource_name)
);
CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
INSERT INTO events (resource_version, type, occurred_at, namespace, resource_kind, resource_name, envelope)
  VALUES ('42', 'database.created', '2026-01-01T00:00:00Z', 'dbs', 'DatabaseCluster', 'old', '{}');
INSERT INTO meta (key, value) VALUES ('cursor', '42');`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}

	st, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cursor, err := st.LatestCursor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cursor != "" {
		t.Errorf("the old cursor %q was kept, want a relist", cursor)
	}
	if err := st.Insert(ctx, newEvent("db1.abc", "100", "Instance", "Created")); err != nil {
		t.Fatal(err)
	}
	got, err := st.Query(ctx, Filter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Reason != "Created" || got[1].ResourceName != "old" {
		t.Fatalf("got %+v, want the new and the old event", got)
	}
	// The old time format is rewritten, so time filters compare correctly.
	since := time.Date(2025, 12, 31, 23, 59, 59, 500e6, time.UTC)
	got, err = st.Query(ctx, Filter{Since: &since, Until: &since, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want no event", got)
	}
	until := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	got, err = st.Query(ctx, Filter{Until: &until, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ResourceName != "old" {
		t.Fatalf("got %+v, want the old event", got)
	}
}

// TestQueryOrdersByTimeAndPages checks that events inserted out of order,
// as a relist does, are returned newest first, and that paging doesn't
// skip events that share a timestamp.
func TestQueryOrdersByTimeAndPages(t *testing.T) {
	ctx := context.Background()
	st, err := OpenSQLite(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	base := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	for _, in := range []struct {
		name string
		at   time.Duration
	}{
		{"t3", 3 * time.Second},
		// 1.5s and 2s compare in the wrong order as RFC 3339 text.
		{"t1.5", 1500 * time.Millisecond},
		{"t2a", 2 * time.Second},
		{"t2b", 2 * time.Second},
		{"t1", time.Second},
	} {
		e := newEvent(in.name, in.name, "Instance", "Created")
		e.OccurredAt = base.Add(in.at)
		if err := st.Insert(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	var names []string
	f := Filter{Limit: 2}
	for page := 0; page < 5; page++ {
		got, err := st.Query(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range got {
			names = append(names, e.Name)
		}
		if len(got) < f.Limit {
			break
		}
		if f.Before, err = ParsePageToken(PageToken(got[len(got)-1])); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"t3", "t2b", "t2a", "t1.5", "t1"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", names, want)
	}

	since := base.Add(1800 * time.Millisecond)
	got, err := st.Query(ctx, Filter{Since: &since, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d events since 1.8s, want 3", len(got))
	}
}

// TestBackfillsInstances checks that Instance events stored before the
// instance column get their own name as instance.
func TestBackfillsInstances(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "audit.db")
	st, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []*Event{
		newEvent("a", "1", "Instance", "Created"),
		newEvent("b", "2", "Backup", "Created"),
	} {
		if err := st.Insert(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	st.Close()

	st, err = OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	got, err := st.Query(ctx, Filter{Name: "db1", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Instance != "db1" || got[0].Instance != "" {
		t.Fatalf("got %+v, want db1 as the instance of the Instance event only", got)
	}
}
