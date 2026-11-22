# OpenEverest Audit Plugin

A generic plugin (spec [`003-generic-plugins`](https://github.com/openeverest/specs/blob/main/specs/003-generic-plugins.md)) that subscribes to the OpenEverest event stream, persists every captured event, and exposes a UI to browse, filter and search the audit log.

Built for compliance and operational forensics: who did what, when, to which resource.

---

## Status — MVP (Phase 1)

| Area | Phase 1 (this release) | Phase 2 | Phase 3 | Phase 4 |
| --- | --- | --- | --- | --- |
| Storage | Embedded SQLite (PVC) | External PostgreSQL | Retention / vacuum policy | — |
| UI | Filter, search, JSON drawer | Saved views | MUI + theming | — |
| Operability | `/healthz`, single replica | — | Prom metrics, structured logs | — |
| Delivery | — | — | — | Webhook / Slack / S3 export |

The MVP is **deployable end-to-end**: a Helm chart installs the Plugin CR, the backend daemon consumes SSE events from the host and stores them in SQLite, and the React extension renders the audit log at `/audit` in the OpenEverest UI.

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
│ React extension  │ ──────────────────────▶ │ │ SQLite (PVC: /data)     │ │
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

### Authentication

There are no credentials to set up. The Plugin CR declares the permissions the daemon needs (`read-events` on the recorded kinds and `read` on `platform-events`), and OpenEverest issues the plugin a service token with exactly those permissions. The token lives in the `<release>-token` Secret, is mounted at `/var/run/secrets/everest/token` and is renewed automatically.

OpenEverest versions that don't issue plugin service tokens need a user to log in as instead. Create a Secret with `username` and `password` keys and set `auth.credentialsSecretName`; see [`values.yaml`](charts/audit/values.yaml) for the permissions that user needs.

---

## Configuration

Key Helm values (see [`charts/audit/values.yaml`](charts/audit/values.yaml) for the full list):

| Value | Default | Description |
| --- | --- | --- |
| `plugin.eventTypes` | curated audit list | Event types to subscribe to; empty list = all |
| `plugin.namespaces` | `[]` | Optional namespace filter |
| `storage.persistence.enabled` | `true` | Provision a PVC for SQLite |
| `storage.persistence.size` | `5Gi` | PVC size |
| `storage.sqlite.path` | `/data/audit.db` | Database file path inside the pod |
| `everestAPIURL` | `""` | Override Everest API URL; auto-discovered when empty |
| `auth.credentialsSecretName` | `""` | Secret with `username` and `password`, used only when OpenEverest issues no service token |
| `resources` | small | CPU/memory requests & limits |

Environment variables (also configurable directly on the Deployment):

| Var | Default | Purpose |
| --- | --- | --- |
| `PORT` | `8080` | HTTP listen port |
| `AUDIT_DB_PATH` | `/data/audit.db` | SQLite path |
| `AUDIT_EVENT_TYPES` | (chart-driven) | CSV of event types |
| `AUDIT_NAMESPACES` | (chart-driven) | CSV of namespaces |
| `EVEREST_API_URL` | auto | Override Everest API URL |
| `EVEREST_TOKEN_PATH` | `/var/run/secrets/everest/token` | Mounted plugin service token |
| `EVEREST_SERVICE_USER`, `EVEREST_SERVICE_PASSWORD` | | Fallback login when there is no service token |

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

- **Phase 2** — pluggable storage with PostgreSQL backend, retention policy, schema migration tool.
- **Phase 3** — structured logs, Prometheus metrics, saved filter views.
- **Phase 4** — outbound delivery (webhook, Slack, S3) and alerting rules.

---

## License

[Apache-2.0](LICENSE)
