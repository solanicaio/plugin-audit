# OpenEverest Audit Plugin

A generic plugin (spec [`003-generic-plugins`](https://github.com/openeverest/specs/blob/main/specs/003-generic-plugins.md)) that subscribes to the OpenEverest event stream, persists every captured event, and exposes a UI to browse, filter and search the audit log.

Built for compliance and operational forensics: who did what, when, to which resource.

---

## Status — MVP (Phase 1)

| Area | Phase 1 (this release) | Phase 2 | Phase 3 | Phase 4 |
| --- | --- | --- | --- | --- |
| Storage | Embedded SQLite (PVC) or external PostgreSQL | — | Retention / vacuum policy | — |
| UI | Filter, search, JSON drawer | Saved views | MUI + theming | — |
| Operability | `/healthz`, single replica | — | Prom metrics, structured logs | — |
| Delivery | — | — | — | Webhook / Slack / S3 export |

The MVP is **deployable end-to-end**: a Helm chart installs the Plugin CR, the backend daemon consumes SSE events from the host and stores them in SQLite or PostgreSQL, and the React extension renders the audit log at `/audit` in the OpenEverest UI.

The audit log page shows the number of events over time, stacked by severity, above the event list. The time range is a preset (15 minutes to 30 days, or all) or a custom range; clicking a bar zooms into it. Events can be filtered by engine, kind, event reason, namespace, actor, severity, a database or object name, and free text. Each filter shows how many events have each value. The filters are kept in the page URL, so a view can be shared, and the matching events can be exported as CSV or JSON. The engine and the instance of backups and restores need an OpenEverest version whose events report them.

---

## Architecture

```
┌──────────────────┐    SSE /v1/events       ┌─────────────────────────────┐
│ OpenEverest host │ ──────────────────────▶ │ audit pod                   │
│ /v1/events       │                         │ ┌─────────────────────────┐ │
└──────────────────┘                         │ │ consumer (daemon)       │ │
        ▲                                    │ │  - cursor in meta table │ │
        │  /api/* (proxied, JWT injected)    │ └─────────┬───────────────┘ │
        │                                    │           ▼                 │
┌──────────────────┐                         │ ┌─────────────────────────┐ │
│ React extension  │ ──────────────────────▶ │ │ SQLite / PostgreSQL     │ │
│  /audit          │                         │ └─────────┬───────────────┘ │
└──────────────────┘                         │           ▲                 │
                                             │ ┌─────────┴───────────────┐ │
                                             │ │ REST: /api/events …     │ │
                                             │ └─────────────────────────┘ │
                                             └─────────────────────────────┘
```

Three plugin modes are enabled per spec 003 §10.4:
- **`daemon`** — long-running event consumer, single replica
- **`eventConsumer`** — subscribes to the configured event types
- **`requestHandler`** — exposes `/api/*` to the frontend bundle, proxied with the caller's JWT in `X-Everest-User`

---

## Quick start

### Install from a release

```bash
helm install audit oci://ghcr.io/openeverest/charts/audit \
  --version 0.1.0 \
  --namespace everest-system
```

### Install from source

```bash
# 1. Build the React bundle (writes backend/dist/main.js)
npm ci
npm run build

# 2. Build & push the container
docker build -t ghcr.io/<you>/plugin-audit:dev .
docker push ghcr.io/<you>/plugin-audit:dev

# 3. Install the chart
helm install audit charts/audit \
  --namespace everest-system \
  --set image.repository=ghcr.io/<you>/plugin-audit \
  --set image.tag=dev
```

The Plugin CR is created automatically; the OpenEverest controller will register the route and sidebar item at `/audit`.

### Store events in PostgreSQL

By default events are stored in SQLite on a PVC. To keep them in an existing PostgreSQL database instead, put its connection URI in a Secret and point the chart at it:

```bash
kubectl -n everest-system create secret generic audit-db \
  --from-literal=uri='postgres://audit:<password>@pg.example:5432/audit?sslmode=require'

helm install audit oci://ghcr.io/openeverest/charts/audit \
  --namespace everest-system \
  --set storage.driver=postgres \
  --set storage.postgres.uriSecretName=audit-db
```

The tables are created on startup, so the database user needs to be able to create tables in it. No PVC is created.

---

## Configuration

Key Helm values (see [`charts/audit/values.yaml`](charts/audit/values.yaml) for the full list):

| Value | Default | Description |
| --- | --- | --- |
| `plugin.eventTypes` | curated audit list | Event types to subscribe to; empty list = all |
| `plugin.namespaces` | `[]` | Optional namespace filter |
| `storage.driver` | `sqlite` | `sqlite` or `postgres` |
| `storage.postgres.uriSecretName` | `""` | Secret with the PostgreSQL connection URI under the key `uri` |
| `storage.persistence.enabled` | `true` | Provision a PVC for SQLite |
| `storage.persistence.size` | `5Gi` | PVC size |
| `storage.sqlite.path` | `/data/audit.db` | Database file path inside the pod |
| `everestAPIURL` | `""` | Override Everest API URL; auto-discovered when empty |
| `resources` | small | CPU/memory requests & limits |

Environment variables (also configurable directly on the Deployment):

| Var | Default | Purpose |
| --- | --- | --- |
| `PORT` | `8080` | HTTP listen port |
| `AUDIT_DB_DRIVER` | `sqlite` | `sqlite` or `postgres` |
| `AUDIT_DB_PATH` | `/data/audit.db` | SQLite path |
| `AUDIT_DB_URI` | — | PostgreSQL connection URI |
| `AUDIT_EVENT_TYPES` | (chart-driven) | CSV of event types |
| `AUDIT_NAMESPACES` | (chart-driven) | CSV of namespaces |
| `EVEREST_API_URL` | auto | Override Everest API URL |
| `EVEREST_TOKEN_PATH` | `/var/run/secrets/everest/token` | Mounted plugin service token |

---

## REST API (host-proxied)

All paths below are reached as `/api/...` from the host (the host strips that prefix when forwarding). Audit data is served on POST only, because the host proxies plugin GETs without authentication:

| Method | Path | Description |
| --- | --- | --- |
| POST | `/api/events/query` | List events, newest first. Body: the filter (below), `limit` (≤500) and `before` (the `next` token of the previous page). |
| POST | `/api/events/get` | Single event with its full envelope. Body: `{"id": 1}`. |
| POST | `/api/facets` | Values of kinds, reasons, namespaces, actors and providers, with event counts. Body: the filter; each facet ignores its own selection. |
| POST | `/api/stats` | Counts by dimension for a filter, plus the last event time. |
| POST | `/api/histogram` | Events over time, by severity. Body: the filter. The bucket size (`step`, seconds) is picked for at most 120 buckets. |
| POST | `/api/export` | Download the matching events (newest 10,000) as CSV or JSON. Body: the filter and `format` (`csv` or `json`). |
| GET | `/healthz` | Liveness/readiness. |

The filter is a JSON object with the optional fields `providers`, `kinds`, `reasons`, `namespaces`, `actors` (lists), `severities` (`Normal`, `Warning`), `name` (object or instance name contains), `search` (any text), and `since` / `until` (RFC 3339).

---

## Local development

```bash
# Frontend (Vite lib mode → backend/dist/main.js)
npm ci
npm run dev          # rebuilds on change

# Backend
cd backend
touch dist/main.js dist/icon.png   # placeholders for go:embed
go run .
```

Point the running server at a real Everest API with `EVEREST_API_URL` and provide a token file via `EVEREST_TOKEN_PATH`.

---

## Roadmap

- **Phase 2** — retention policy, schema migration tool.
- **Phase 3** — structured logs, Prometheus metrics, saved filter views.
- **Phase 4** — outbound delivery (webhook, Slack, S3) and alerting rules.

---

## License

[Apache-2.0](LICENSE)
