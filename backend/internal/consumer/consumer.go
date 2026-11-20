// Package consumer records the lifecycle events of an OpenEverest cluster.
//
// It follows the list-then-watch pattern of GET /v1/clusters/{cluster}/events:
// with no stored cursor it lists all the events and then streams the new ones
// from the resourceVersion of the list. When the server rejects the cursor as
// too old (410), it lists again.
package consumer

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/openeverest/plugin-audit/backend/internal/config"
	"github.com/openeverest/plugin-audit/backend/internal/store"
)

const (
	// listPageSize is the maximum page size of the events API.
	listPageSize = 1000
	// defaultIdleTimeout reconnects when the stream is silent for this long. The
	// server sends a keepalive every 30s.
	defaultIdleTimeout = 2 * time.Minute
	maxBackoff         = 30 * time.Second
)

// errCursorGone means the cursor is too old to stream from.
var errCursorGone = errors.New("the cursor is too old")

type Consumer struct {
	cfg         config.Config
	store       store.Store
	http        *http.Client
	tokens      *tokenSource
	idleTimeout time.Duration
}

func New(cfg config.Config, st store.Store) *Consumer {
	client := &http.Client{} // no overall timeout: the stream is long-lived
	return &Consumer{
		cfg:   cfg,
		store: st,
		http:  client,
		tokens: &tokenSource{
			apiURL:   cfg.EverestAPIURL,
			user:     cfg.ServiceUser,
			password: cfg.ServicePassword,
			http:     &http.Client{Timeout: 30 * time.Second},
			now:      time.Now,
		},
		idleTimeout: defaultIdleTimeout,
	}
}

// Run records events until ctx is done, reconnecting with exponential backoff.
func (c *Consumer) Run(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		err := c.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, errCursorGone) {
			log.Printf("%v, listing the events again", err)
			if err := c.store.SaveCursor(ctx, ""); err != nil {
				log.Printf("reset cursor: %v", err)
			}
			backoff = time.Second
			continue
		}
		if err != nil {
			log.Printf("event stream error: %v (retry in %s)", err, backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, maxBackoff)
			continue
		}
		backoff = time.Second
	}
}

// runOnce lists the events when there is no cursor and then streams them until
// the stream ends.
func (c *Consumer) runOnce(ctx context.Context) error {
	stored, err := c.store.LatestCursor(ctx)
	if err != nil {
		return fmt.Errorf("load cursor: %w", err)
	}
	cursor := c.cursorFor(stored)
	if cursor == "" {
		if cursor, err = c.list(ctx); err != nil {
			return err
		}
		if err := c.saveCursor(ctx, cursor); err != nil {
			return err
		}
	}
	return c.watch(ctx, cursor)
}

// The stored cursor is prefixed with the filters it was obtained with, so
// changing the filters lists the events again: a cursor only covers the
// events that matched its filters.
func (c *Consumer) filterKey() string {
	return c.filters().Encode()
}

func (c *Consumer) cursorFor(stored string) string {
	key, cursor, ok := strings.Cut(stored, " ")
	if !ok || key != c.filterKey() {
		return ""
	}
	return cursor
}

func (c *Consumer) saveCursor(ctx context.Context, cursor string) error {
	if cursor == "" {
		return nil
	}
	if err := c.store.SaveCursor(ctx, c.filterKey()+" "+cursor); err != nil {
		return fmt.Errorf("save cursor: %w", err)
	}
	return nil
}

func (c *Consumer) filters() url.Values {
	q := url.Values{}
	for k, v := range map[string]string{
		"namespaces": c.cfg.Namespaces,
		"kinds":      c.cfg.Kinds,
		"reasons":    c.cfg.Reasons,
	} {
		if v = strings.TrimSpace(v); v != "" {
			q.Set(k, v)
		}
	}
	return q
}

func (c *Consumer) endpoint(q url.Values) string {
	return c.cfg.EverestAPIURL + "/v1/clusters/" + url.PathEscape(c.cfg.Cluster) + "/events?" + q.Encode()
}

type eventList struct {
	Items           []json.RawMessage `json:"items"`
	ResourceVersion string            `json:"resourceVersion"`
	Continue        string            `json:"continue"`
}

// list stores all the events and returns the resourceVersion to stream from.
func (c *Consumer) list(ctx context.Context) (string, error) {
	var resourceVersion, cont string
	total := 0
	for {
		q := c.filters()
		q.Set("limit", strconv.Itoa(listPageSize))
		if cont != "" {
			q.Set("continue", cont)
		}
		resp, err := c.get(ctx, c.endpoint(q), "application/json")
		if err != nil {
			return "", fmt.Errorf("list events: %w", err)
		}
		var page eventList
		err = json.NewDecoder(resp.Body).Decode(&page)
		resp.Body.Close()
		if err != nil {
			return "", fmt.Errorf("decode the event list: %w", err)
		}
		for _, raw := range page.Items {
			if err := c.record(ctx, raw); err != nil {
				return "", err
			}
		}
		total += len(page.Items)
		if resourceVersion == "" {
			resourceVersion = page.ResourceVersion
		}
		if page.Continue == "" {
			break
		}
		cont = page.Continue
	}
	if resourceVersion == "" {
		return "", errors.New("the event list has no resourceVersion")
	}
	log.Printf("listed %d events of cluster %q (resourceVersion %s)", total, c.cfg.Cluster, resourceVersion)
	return resourceVersion, nil
}

// watch stores the events streamed from cursor until the stream ends.
func (c *Consumer) watch(ctx context.Context, cursor string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	q := c.filters()
	q.Set("watch", "true")
	q.Set("since", cursor)
	resp, err := c.get(ctx, c.endpoint(q), "text/event-stream")
	if err != nil {
		return fmt.Errorf("watch events: %w", err)
	}
	defer resp.Body.Close()
	log.Printf("streaming the events of cluster %q from %s", c.cfg.Cluster, cursor)

	// Reconnect when the stream goes silent, e.g. behind a proxy that dropped the
	// connection without closing it.
	idle := time.AfterFunc(c.idleTimeout, cancel)
	defer idle.Stop()
	err = c.readSSE(ctx, resp.Body, func() { idle.Reset(c.idleTimeout) })
	if ctx.Err() != nil && !errors.Is(err, errCursorGone) {
		return fmt.Errorf("the event stream was idle for %s", c.idleTimeout)
	}
	return err
}

// get sends an authenticated GET and returns the response when it's 200 OK.
func (c *Consumer) get(ctx context.Context, endpoint, accept string) (*http.Response, error) {
	token, err := c.tokens.Token(ctx)
	if err != nil {
		return nil, fmt.Errorf("log in: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusOK {
		return resp, nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	switch resp.StatusCode {
	case http.StatusGone:
		return nil, errCursorGone
	case http.StatusUnauthorized:
		c.tokens.Invalidate()
	}
	return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
}

type sseMessage struct {
	id, event string
	data      strings.Builder
}

// readSSE handles the messages of an event stream until it ends. onLine is
// called for every line read, keepalive comments included.
func (c *Consumer) readSSE(ctx context.Context, body io.Reader, onLine func()) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var msg sseMessage
	for scanner.Scan() {
		onLine()
		line := scanner.Text()
		if line == "" {
			if err := c.handleMessage(ctx, &msg); err != nil {
				return err
			}
			msg = sseMessage{}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "id":
			msg.id = value
		case "event":
			msg.event = value
		case "data":
			if msg.data.Len() > 0 {
				msg.data.WriteByte('\n')
			}
			msg.data.WriteString(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return io.ErrUnexpectedEOF
}

func (c *Consumer) handleMessage(ctx context.Context, msg *sseMessage) error {
	switch {
	case msg.event == "" && msg.data.Len() == 0:
		return nil
	case msg.event == "bookmark":
		return c.saveCursor(ctx, msg.id)
	case msg.event == "error":
		var streamErr struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal([]byte(msg.data.String()), &streamErr)
		if streamErr.Code == http.StatusGone {
			return errCursorGone
		}
		return fmt.Errorf("the event stream failed (%d): %s", streamErr.Code, streamErr.Message)
	}
	raw := json.RawMessage(msg.data.String())
	if err := c.record(ctx, raw); err != nil {
		return err
	}
	cursor := msg.id
	if cursor == "" {
		var ev struct {
			Cursor string `json:"cursor"`
		}
		_ = json.Unmarshal(raw, &ev)
		cursor = ev.Cursor
	}
	return c.saveCursor(ctx, cursor)
}

// lifecycleEvent is the part of a LifecycleEvent stored in indexed columns.
// The JSON is also stored verbatim.
type lifecycleEvent struct {
	Name       string    `json:"name"`
	Cursor     string    `json:"cursor"`
	OccurredAt time.Time `json:"occurredAt"`
	Type       string    `json:"type"`
	Reason     string    `json:"reason"`
	Severity   string    `json:"severity"`
	Cluster    string    `json:"cluster"`
	Namespace  string    `json:"namespace"`
	Object     struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	} `json:"object"`
	Actor struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	} `json:"actor"`
	Message  string `json:"message"`
	Instance string `json:"instance"`
	Provider string `json:"provider"`
}

func (c *Consumer) record(ctx context.Context, raw json.RawMessage) error {
	var ev lifecycleEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		return fmt.Errorf("decode event: %w", err)
	}
	if ev.Name == "" {
		return fmt.Errorf("event without a name: %s", raw)
	}
	if ev.OccurredAt.IsZero() {
		ev.OccurredAt = time.Now().UTC()
	}
	if ev.Cluster == "" {
		ev.Cluster = c.cfg.Cluster
	}
	e := &store.Event{
		Name:         ev.Name,
		Cursor:       ev.Cursor,
		Type:         ev.Type,
		Reason:       ev.Reason,
		Severity:     ev.Severity,
		OccurredAt:   ev.OccurredAt,
		Cluster:      ev.Cluster,
		Namespace:    ev.Namespace,
		ResourceKind: ev.Object.Kind,
		ResourceName: ev.Object.Name,
		ActorType:    ev.Actor.Type,
		ActorID:      ev.Actor.ID,
		Message:      ev.Message,
		Instance:     ev.Instance,
		Provider:     ev.Provider,
		Envelope:     raw,
	}
	// Core versions before the events API reported the instance.
	if e.Instance == "" && e.ResourceKind == "Instance" {
		e.Instance = e.ResourceName
	}
	if err := c.store.Insert(ctx, e); err != nil {
		return fmt.Errorf("store event %s: %w", ev.Name, err)
	}
	return nil
}
