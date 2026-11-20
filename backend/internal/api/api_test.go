package api

import (
	"context"
	"embed"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openeverest/plugin-audit/backend/internal/store"
)

// The host proxies plugin GETs without authentication, so the audit data must
// only be served on POST.
func TestAuditDataIsOnlyServedOnPOST(t *testing.T) {
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Insert(context.Background(), &store.Event{
		Name: "e1", Cursor: "1", Type: "instance.created", Reason: "Created",
		OccurredAt: time.Now(), ResourceKind: "Instance", Envelope: []byte(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	Register(mux, st, embed.FS{}, nil)

	for _, path := range []string{"/api/events", "/api/events/query", "/api/events/get", "/api/facets", "/api/stats"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code < 400 || strings.Contains(rec.Body.String(), "e1") {
			t.Errorf("GET %s: %d %s, want an error", path, rec.Code, rec.Body)
		}
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/events/query", strings.NewReader(`{"kinds":["Instance"]}`)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"name":"e1"`) {
		t.Errorf("POST /api/events/query: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/events/query", strings.NewReader(`{"types":["x"]}`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("POST /api/events/query with an unknown field: %d, want 400", rec.Code)
	}
}
