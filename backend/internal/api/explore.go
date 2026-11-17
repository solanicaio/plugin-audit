package api

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/openeverest/plugin-audit/backend/internal/store"
)

// maxBuckets bounds the number of buckets of a histogram.
const maxBuckets = 120

// steps are the histogram bucket sizes, smallest first.
var steps = []time.Duration{
	10 * time.Second, 30 * time.Second,
	time.Minute, 5 * time.Minute, 15 * time.Minute, 30 * time.Minute,
	time.Hour, 3 * time.Hour, 6 * time.Hour, 12 * time.Hour,
	24 * time.Hour, 7 * 24 * time.Hour, 30 * 24 * time.Hour,
}

// stepFor returns the smallest step that splits d in at most maxBuckets.
func stepFor(d time.Duration) time.Duration {
	for _, s := range steps {
		if d <= s*maxBuckets {
			return s
		}
	}
	return steps[len(steps)-1]
}

type histogramResponse struct {
	// Step is the bucket size in seconds, 0 when there are no events.
	Step    int64          `json:"step"`
	Buckets []store.Bucket `json:"buckets"`
}

// histogram counts the events matching a filter over time, by severity. The
// range is the filter's since and until, which default to the oldest
// matching event and now. All the buckets of the range are returned.
func (h *handlers) histogram(w http.ResponseWriter, r *http.Request) {
	f, ok := readFilter(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	from, to := f.Since, f.Until
	if from == nil {
		oldest, _, ok, err := h.st.Span(ctx, f)
		if err != nil {
			log.Printf("histogram span: %v", err)
			writeError(w, http.StatusInternalServerError, "histogram failed")
			return
		}
		if !ok {
			writeJSON(w, http.StatusOK, histogramResponse{Buckets: []store.Bucket{}})
			return
		}
		from = &oldest
	}
	if to == nil {
		now := h.now()
		to = &now
	}
	if to.Before(*from) {
		to = from
	}
	step := stepFor(to.Sub(*from))
	start := from.UTC().Truncate(step)
	counts, err := h.st.Histogram(ctx, f, start, step)
	if err != nil {
		log.Printf("histogram: %v", err)
		writeError(w, http.StatusInternalServerError, "histogram failed")
		return
	}
	buckets := []store.Bucket{}
	for t, i := start, 0; !t.After(*to); t = t.Add(step) {
		b := store.Bucket{Start: t}
		if i < len(counts) && counts[i].Start.Equal(t) {
			b = counts[i]
			i++
		}
		buckets = append(buckets, b)
	}
	writeJSON(w, http.StatusOK, histogramResponse{Step: int64(step / time.Second), Buckets: buckets})
}

// maxExport bounds the number of events of an export, newest first.
const maxExport = 10000

var csvHeader = []string{
	"occurredAt", "severity", "reason", "type", "cluster", "namespace",
	"kind", "name", "instance", "provider", "actorType", "actor", "message",
}

// export writes the events matching a filter, newest first, as CSV or as a
// JSON array.
func (h *handlers) export(w http.ResponseWriter, r *http.Request) {
	var req struct {
		filterRequest
		Format string `json:"format"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Format != "csv" && req.Format != "json" {
		writeError(w, http.StatusBadRequest, `format must be "csv" or "json"`)
		return
	}
	req.Limit = 0
	f, ok := toFilter(w, req.filterRequest)
	if !ok {
		return
	}
	f.Limit = maxExport

	name := "audit-" + h.now().UTC().Format("20060102-150405") + "." + req.Format
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	bw := bufio.NewWriter(w)
	var err error
	if req.Format == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		cw := csv.NewWriter(bw)
		_ = cw.Write(csvHeader)
		err = h.st.Each(r.Context(), f, func(e *store.Event) error {
			return cw.Write(csvRow(e))
		})
		cw.Flush()
		if err == nil {
			err = cw.Error()
		}
	} else {
		w.Header().Set("Content-Type", "application/json")
		_, _ = bw.WriteString("[")
		first := true
		enc := json.NewEncoder(bw)
		err = h.st.Each(r.Context(), f, func(e *store.Event) error {
			if !first {
				_, _ = bw.WriteString(",")
			}
			first = false
			return enc.Encode(e)
		})
		_, _ = bw.WriteString("]\n")
	}
	if err != nil {
		// The status is sent already; a truncated download is the best signal.
		log.Printf("export: %v", err)
		return
	}
	if err := bw.Flush(); err != nil {
		log.Printf("export: %v", err)
	}
}

func csvRow(e *store.Event) []string {
	row := []string{
		e.OccurredAt.UTC().Format(time.RFC3339Nano), e.Severity, e.Reason, e.Type, e.Cluster, e.Namespace,
		e.ResourceKind, e.ResourceName, e.Instance, e.Provider, e.ActorType, e.ActorID, e.Message,
	}
	for i, v := range row {
		row[i] = csvSafe(v)
	}
	return row
}

// csvSafe keeps spreadsheets from evaluating a value as a formula. Names and
// messages can come from users, e.g. the user name of a failed login.
func csvSafe(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}
