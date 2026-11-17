package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openeverest/plugin-audit/backend/internal/store"
)

var t0 = time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)

func newTestHandlers(t *testing.T, events ...*store.Event) *handlers {
	t.Helper()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for i, e := range events {
		e.Name, e.Cursor, e.Type, e.Envelope = "e"+string(rune('a'+i)), "c"+string(rune('a'+i)), "x", []byte(`{}`)
		if err := st.Insert(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
	return &handlers{st: st, now: func() time.Time { return t0.Add(time.Hour) }}
}

func post(t *testing.T, handler http.HandlerFunc, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
	return rec
}

func TestHistogram(t *testing.T) {
	h := newTestHandlers(t,
		&store.Event{OccurredAt: t0.Add(2 * time.Minute), Severity: "Warning"},
		&store.Event{OccurredAt: t0.Add(31 * time.Minute)},
	)
	for _, tc := range []struct {
		name, body string
		step       int64
		buckets    int
		counts     map[int][2]int64
	}{
		// From the oldest event to now: 58 minutes in 30-second buckets.
		{"all", `{}`, 30, 117, map[int][2]int64{0: {0, 1}, 58: {1, 0}}},
		{"since", `{"since":"2026-11-18T09:00:00Z"}`, 60, 121, map[int][2]int64{62: {0, 1}, 91: {1, 0}}},
		{"range", `{"since":"2026-11-18T10:00:00Z","until":"2026-11-18T10:05:00Z"}`, 10, 31, map[int][2]int64{12: {0, 1}}},
		{"filtered", `{"severities":["Normal"]}`, 30, 59, map[int][2]int64{0: {1, 0}}},
		{"none", `{"kinds":["Nope"]}`, 0, 0, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := post(t, h.histogram, tc.body)
			if rec.Code != http.StatusOK {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
			var got histogramResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Step != tc.step || len(got.Buckets) != tc.buckets {
				t.Fatalf("got step %d and %d buckets, want %d and %d", got.Step, len(got.Buckets), tc.step, tc.buckets)
			}
			for i, b := range got.Buckets {
				if i > 0 && !b.Start.Equal(got.Buckets[i-1].Start.Add(time.Duration(got.Step)*time.Second)) {
					t.Fatalf("bucket %d starts at %s", i, b.Start)
				}
				if c := tc.counts[i]; b.Normal != c[0] || b.Warning != c[1] {
					t.Fatalf("bucket %d at %s: got %d/%d, want %v", i, b.Start, b.Normal, b.Warning, c)
				}
			}
		})
	}
}

func TestStepFor(t *testing.T) {
	for d, want := range map[time.Duration]time.Duration{
		0:                         10 * time.Second,
		15 * time.Minute:          10 * time.Second,
		time.Hour:                 30 * time.Second,
		24 * time.Hour:            15 * time.Minute,
		7 * 24 * time.Hour:        3 * time.Hour,
		30 * 24 * time.Hour:       6 * time.Hour,
		10 * 365 * 24 * time.Hour: 30 * 24 * time.Hour,
	} {
		if got := stepFor(d); got != want {
			t.Errorf("stepFor(%s) = %s, want %s", d, got, want)
		}
	}
}

func TestExport(t *testing.T) {
	h := newTestHandlers(t,
		&store.Event{OccurredAt: t0, ResourceKind: "Instance", ResourceName: "db1", Reason: "Created"},
		&store.Event{OccurredAt: t0.Add(time.Minute), ResourceKind: "User", ResourceName: "=HYPERLINK(1)",
			Reason: "LoginFailed", Severity: "Warning", Message: "bad, \"password\""},
	)

	rec := post(t, h.export, `{"format":"csv"}`)
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	rows, err := csv.NewReader(rec.Body).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || strings.Join(rows[0], ",") != strings.Join(csvHeader, ",") {
		t.Fatalf("got %q", rows)
	}
	if rows[1][7] != "'=HYPERLINK(1)" || rows[1][12] != `bad, "password"` || rows[2][7] != "db1" {
		t.Fatalf("got rows %q", rows[1:])
	}

	rec = post(t, h.export, `{"format":"json","kinds":["Instance"]}`)
	var events []store.Event
	if err := json.Unmarshal(rec.Body.Bytes(), &events); err != nil {
		t.Fatalf("%v: %s", err, rec.Body)
	}
	if len(events) != 1 || events[0].ResourceName != "db1" {
		t.Fatalf("got %+v", events)
	}
	if rec := post(t, h.export, `{"format":"json","kinds":["Nope"]}`); strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("got %s for no events", rec.Body)
	}
	if rec := post(t, h.export, `{"format":"xml"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d for an unknown format", rec.Code)
	}
}
