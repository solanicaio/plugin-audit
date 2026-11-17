// Package api wires the HTTP handlers exposed by the plugin backend.
package api

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/openeverest/plugin-audit/backend/internal/store"
)

func Register(mux *http.ServeMux, st store.Store, distFS embed.FS, icon []byte) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("GET /main.js", func(w http.ResponseWriter, _ *http.Request) {
		data, err := distFS.ReadFile("dist/main.js")
		if err != nil {
			http.Error(w, "bundle not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_, _ = w.Write(data)
	})

	mux.HandleFunc("GET /icon.png", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write(icon)
	})

	// The audit data is served on POST only. The host proxies plugin GETs
	// without authentication, for the bundle and the icon, but requires a
	// signed-in user allowed to use the plugin for the other methods.
	h := &handlers{st: st, now: time.Now}
	mux.HandleFunc("POST /api/events/query", h.query)
	mux.HandleFunc("POST /api/events/get", h.get)
	mux.HandleFunc("POST /api/facets", h.facets)
	mux.HandleFunc("POST /api/stats", h.stats)
	mux.HandleFunc("POST /api/histogram", h.histogram)
	mux.HandleFunc("POST /api/export", h.export)
}

type handlers struct {
	st store.Store
	// now is time.Now, replaced in tests.
	now func() time.Time
}

// maxBodyBytes bounds the size of a request body.
const maxBodyBytes = 64 << 10

const (
	defaultLimit = 100
	maxLimit     = 500
)

// filterRequest is the body of the query and stats requests.
type filterRequest struct {
	Kinds      []string `json:"kinds"`
	Reasons    []string `json:"reasons"`
	Namespaces []string `json:"namespaces"`
	Actors     []string `json:"actors"`
	Providers  []string `json:"providers"`
	// Severities is Normal, Warning or both.
	Severities []string `json:"severities"`
	// Name matches the object or instance names that contain it.
	Name   string `json:"name"`
	Search string `json:"search"`
	// Since and Until are RFC 3339 timestamps.
	Since *time.Time `json:"since"`
	Until *time.Time `json:"until"`
	Limit int        `json:"limit"`
	// Before is the next token of a previous query.
	Before string `json:"before"`
}

func (h *handlers) query(w http.ResponseWriter, r *http.Request) {
	f, ok := readFilter(w, r)
	if !ok {
		return
	}
	events, err := h.st.Query(r.Context(), f)
	if err != nil {
		log.Printf("query events: %v", err)
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if events == nil {
		events = []store.Event{}
	}
	resp := map[string]any{"items": events}
	if n := len(events); n == f.Limit {
		resp["next"] = store.PageToken(events[n-1])
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *handlers) get(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID int64 `json:"id"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	e, err := h.st.Get(r.Context(), req.ID)
	if err != nil {
		log.Printf("get event %d: %v", req.ID, err)
		writeError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	if e == nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (h *handlers) facets(w http.ResponseWriter, r *http.Request) {
	q, ok := readFilter(w, r)
	if !ok {
		return
	}
	f, err := h.st.Facets(r.Context(), q)
	if err != nil {
		log.Printf("facets: %v", err)
		writeError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, f)
}

func (h *handlers) stats(w http.ResponseWriter, r *http.Request) {
	f, ok := readFilter(w, r)
	if !ok {
		return
	}
	s, err := h.st.Stats(r.Context(), f)
	if err != nil {
		log.Printf("stats: %v", err)
		writeError(w, http.StatusInternalServerError, "stats failed")
		return
	}
	writeJSON(w, http.StatusOK, s)
}

// readJSON decodes the request body into v. An empty body leaves v unchanged.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return false
	}
	return true
}

func readFilter(w http.ResponseWriter, r *http.Request) (store.Filter, bool) {
	var req filterRequest
	if !readJSON(w, r, &req) {
		return store.Filter{}, false
	}
	return toFilter(w, req)
}

func toFilter(w http.ResponseWriter, req filterRequest) (store.Filter, bool) {
	switch {
	case req.Limit == 0:
		req.Limit = defaultLimit
	case req.Limit < 0 || req.Limit > maxLimit:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("limit must be 1..%d", maxLimit))
		return store.Filter{}, false
	}
	var before *store.Page
	if req.Before != "" {
		var err error
		if before, err = store.ParsePageToken(req.Before); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return store.Filter{}, false
		}
	}
	if req.Since != nil && req.Until != nil && req.Until.Before(*req.Since) {
		writeError(w, http.StatusBadRequest, "until is before since")
		return store.Filter{}, false
	}
	for _, s := range req.Severities {
		if s != "Normal" && s != "Warning" {
			writeError(w, http.StatusBadRequest, "severities must be Normal or Warning")
			return store.Filter{}, false
		}
	}
	return store.Filter{
		Kinds:      clean(req.Kinds),
		Reasons:    clean(req.Reasons),
		Namespaces: clean(req.Namespaces),
		Actors:     clean(req.Actors),
		Providers:  clean(req.Providers),
		Severities: req.Severities,
		Name:       strings.TrimSpace(req.Name),
		Search:     strings.TrimSpace(req.Search),
		Since:      req.Since,
		Until:      req.Until,
		Limit:      req.Limit,
		Before:     before,
	}, true
}

func clean(vals []string) []string {
	var out []string
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("encode response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
