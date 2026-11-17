package store

import (
	"context"
	"database/sql"
	"path/filepath"
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
	f, err := st.Facets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Kinds) != 2 || len(f.Reasons) != 2 || len(f.Namespaces) != 1 || len(f.Actors) != 0 {
		t.Fatalf("unexpected facets %+v", f)
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
}
