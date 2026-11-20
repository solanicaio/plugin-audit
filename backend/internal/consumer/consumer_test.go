package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openeverest/plugin-audit/backend/internal/config"
	"github.com/openeverest/plugin-audit/backend/internal/store"
)

func lifecycleEventJSON(name, cursor string) string {
	return fmt.Sprintf(`{"name":%q,"cursor":%q,"occurredAt":"2026-11-18T10:00:00Z","type":"instance.created",`+
		`"reason":"Created","severity":"Normal","cluster":"main","namespace":"dbs",`+
		`"object":{"apiVersion":"core.openeverest.io/v1alpha1","kind":"Instance","name":"db1","uid":"u1"},`+
		`"actor":{"type":"user","id":"admin"}}`, name, cursor)
}

// fakeAPI is a fake OpenEverest API. watch writes the event stream.
type fakeAPI struct {
	t      *testing.T
	watch  func(w http.ResponseWriter, since string)
	mu     sync.Mutex
	grants []string
	since  []string
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/v1/auth/token" {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.grants = append(f.grants, body["grant_type"])
		f.mu.Unlock()
		_, _ = io.WriteString(w, `{"access_token":"at","token_type":"Bearer","expires_in":900,"refresh_token":"rt"}`)
		return
	}
	if r.URL.Path != "/v1/clusters/main/events" || r.Header.Get("Authorization") != "Bearer at" {
		http.Error(w, "unexpected request", http.StatusBadRequest)
		return
	}
	q := r.URL.Query()
	if q.Get("watch") == "true" {
		f.mu.Lock()
		f.since = append(f.since, q.Get("since"))
		f.mu.Unlock()
		f.watch(w, q.Get("since"))
		return
	}
	// Two pages sharing the resourceVersion of the list.
	if q.Get("continue") == "" {
		fmt.Fprintf(w, `{"items":[%s],"resourceVersion":"200","continue":"next"}`, lifecycleEventJSON("e1", "150"))
		return
	}
	fmt.Fprintf(w, `{"items":[%s],"resourceVersion":"200"}`, lifecycleEventJSON("e2", "160"))
}

func newTestConsumer(t *testing.T, api *fakeAPI) (*Consumer, store.Store) {
	t.Helper()
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	c := New(config.Config{
		EverestAPIURL:   srv.URL,
		ServiceUser:     "audit",
		ServicePassword: "secret",
		Cluster:         "main",
	}, st)
	return c, st
}

func storedNames(t *testing.T, st store.Store) []string {
	t.Helper()
	events, err := st.Query(context.Background(), store.Filter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range events {
		names = append(names, e.Name)
	}
	return names
}

func TestListThenWatch(t *testing.T) {
	api := &fakeAPI{t: t, watch: func(w http.ResponseWriter, _ string) {
		_, _ = io.WriteString(w, ": connected\n\n")
		fmt.Fprintf(w, "id: 210\nevent: instance.created\ndata: %s\n\n", lifecycleEventJSON("e3", "210"))
		_, _ = io.WriteString(w, ": keepalive\n\n")
		_, _ = io.WriteString(w, "id: 300\nevent: bookmark\ndata: {}\n\n")
	}}
	c, st := newTestConsumer(t, api)
	ctx := context.Background()

	if err := c.runOnce(ctx); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("runOnce: %v, want the stream to end", err)
	}
	if got := strings.Join(storedNames(t, st), ","); got != "e3,e2,e1" {
		t.Errorf("stored %s, want e3,e2,e1", got)
	}
	if got := strings.Join(api.since, ","); got != "200" {
		t.Errorf("watched since %s, want the resourceVersion of the list", got)
	}

	// The next stream resumes from the bookmark, without listing again.
	if err := c.runOnce(ctx); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("runOnce: %v", err)
	}
	if got := strings.Join(api.since, ","); got != "200,300" {
		t.Errorf("watched since %s, want 200,300", got)
	}
	if got := len(storedNames(t, st)); got != 3 {
		t.Errorf("stored %d events, want 3", got)
	}

	// Changing the filters invalidates the cursor.
	c.cfg.Kinds = "Backup"
	if err := c.runOnce(ctx); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("runOnce: %v", err)
	}
	if got := api.since[len(api.since)-1]; got != "200" {
		t.Errorf("watched since %s after changing the filters, want a relist", got)
	}
}

func TestCursorTooOld(t *testing.T) {
	for name, watch := range map[string]func(http.ResponseWriter, string){
		"error event": func(w http.ResponseWriter, _ string) {
			_, _ = io.WriteString(w, ": connected\n\nevent: error\ndata: {\"code\":410,\"message\":\"too old\"}\n\n")
		},
		"HTTP status": func(w http.ResponseWriter, _ string) {
			http.Error(w, "too old", http.StatusGone)
		},
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := newTestConsumer(t, &fakeAPI{t: t, watch: watch})
			if err := c.runOnce(context.Background()); !errors.Is(err, errCursorGone) {
				t.Fatalf("runOnce: %v, want errCursorGone", err)
			}
		})
	}
}

func TestStreamErrorEvent(t *testing.T) {
	c, _ := newTestConsumer(t, &fakeAPI{t: t, watch: func(w http.ResponseWriter, _ string) {
		_, _ = io.WriteString(w, "event: error\ndata: {\"code\":500,\"message\":\"boom\"}\n\n")
	}})
	err := c.runOnce(context.Background())
	if err == nil || errors.Is(err, errCursorGone) || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("runOnce: %v, want the stream error", err)
	}
}

func TestIdleStreamReconnects(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	c, _ := newTestConsumer(t, &fakeAPI{t: t, watch: func(w http.ResponseWriter, _ string) {
		_, _ = io.WriteString(w, ": connected\n\n")
		w.(http.Flusher).Flush()
		<-release
	}})
	c.idleTimeout = 50 * time.Millisecond
	done := make(chan error, 1)
	go func() { done <- c.runOnce(context.Background()) }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "idle") {
			t.Fatalf("runOnce: %v, want an idle error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the idle stream was not dropped")
	}
}

func TestTokenSourceRefreshesAndFallsBack(t *testing.T) {
	var grants []string
	refreshOK := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		grants = append(grants, body["grant_type"])
		if body["grant_type"] == "refresh_token" && (!refreshOK || body["refresh_token"] != "rt1") {
			http.Error(w, "invalid refresh token", http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{"access_token":"at","expires_in":900,"refresh_token":"rt1"}`)
	}))
	defer srv.Close()
	now := time.Now()
	ts := &tokenSource{apiURL: srv.URL, user: "audit", password: "secret", http: srv.Client(), now: func() time.Time { return now }}
	ctx := context.Background()

	for range 2 {
		if _, err := ts.Token(ctx); err != nil {
			t.Fatal(err)
		}
	}
	now = now.Add(15 * time.Minute)
	if _, err := ts.Token(ctx); err != nil {
		t.Fatal(err)
	}
	ts.Invalidate()
	refreshOK = false
	if _, err := ts.Token(ctx); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(grants, ","); got != "password,refresh_token,refresh_token,password" {
		t.Errorf("grants %s, want password,refresh_token,refresh_token,password", got)
	}
}

func TestRecordsInstanceAndProvider(t *testing.T) {
	c, st := newTestConsumer(t, &fakeAPI{t: t})
	ctx := context.Background()
	backup := `{"name":"b1","cursor":"1","occurredAt":"2026-11-18T10:00:00Z","type":"backup.created","reason":"Created",` +
		`"severity":"Normal","cluster":"main","namespace":"dbs","object":{"kind":"Backup","name":"nightly"},` +
		`"instance":"db1","provider":"percona-server-mysql"}`
	// Core versions before the events API reported the instance.
	instance := lifecycleEventJSON("i1", "2")
	for _, raw := range []string{backup, instance} {
		if err := c.record(ctx, json.RawMessage(raw)); err != nil {
			t.Fatal(err)
		}
	}
	events, err := st.Query(ctx, store.Filter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][2]string{}
	for _, e := range events {
		got[e.Name] = [2]string{e.Instance, e.Provider}
	}
	want := map[string][2]string{"b1": {"db1", "percona-server-mysql"}, "i1": {"db1", ""}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
