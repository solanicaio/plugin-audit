# ──────────────────────────────────────────────────────────────────
# Stage 1 — Build the frontend bundle that gets embedded into the Go
# binary. Vite is configured (vite.config.ts) to emit to backend/dist/main.js.
# The bundle is platform-independent, so it is built once on the build host.
# ──────────────────────────────────────────────────────────────────
FROM --platform=$BUILDPLATFORM node:22-alpine AS frontend-builder

WORKDIR /app

COPY package.json package-lock.json ./
RUN npm ci

COPY tsconfig.json vite.config.ts ./
COPY src/ ./src/

RUN npm run build

# ──────────────────────────────────────────────────────────────────
# Stage 2 — Build the Go backend, embedding the frontend bundle and icon.
# Runs on the build host's platform and cross-compiles for the target
# platform, so multi-arch builds need no QEMU emulation.
# ──────────────────────────────────────────────────────────────────
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS backend-builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /app

COPY backend/go.mod backend/go.sum* ./
RUN go mod download

COPY backend/ ./

# Provide the assets required by //go:embed directives in backend/main.go.
COPY src/icon.png ./dist/icon.png
COPY --from=frontend-builder /app/backend/dist/main.js ./dist/main.js

# We use modernc.org/sqlite (pure Go), so CGO can stay disabled.
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o server .

# ──────────────────────────────────────────────────────────────────
# Stage 3 — Minimal runtime image.
# No RUN steps here: they would execute on the target platform and
# require emulation. The CA bundle is copied from the builder instead.
# ──────────────────────────────────────────────────────────────────
FROM alpine:3.22

COPY --from=backend-builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=backend-builder /app/server /usr/local/bin/server

EXPOSE 8080

ENTRYPOINT ["server"]
