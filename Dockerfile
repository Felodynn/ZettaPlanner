# syntax=docker/dockerfile:1
#
# One image, the whole product:
#
#   nginx  127.0.0.1:8091   the SPA from /srv/web, and /api, /health, /docs
#                           proxied to the Go binary beside it
#   Go     127.0.0.1:8095   the API, bound to loopback — only nginx reaches it
#
# The SPA calls a relative `/api` (src/api/client.ts sets `baseUrl: ''`), so
# there is exactly one origin and no CORS in a deployment — which is what D2
# says and what ZP_DEV_ORIGIN exists to avoid needing. nginx does here what the
# Vite dev proxy does in development.
#
# Postgres is not in this image. It is a container of its own with a volume,
# because the database is the product and it must outlive every rebuild.

# ---------- 1. Backend -------------------------------------------------------
# CGO_ENABLED=0 gives a static binary, so the runtime image needs no Go.
FROM golang:1.26-alpine AS backend
WORKDIR /src
# Dependencies first: cached until go.mod/go.sum actually change.
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
      -o /out/zettaplanner ./cmd/zettaplanner
# `openapi` needs neither a database nor a model server, by design — so the
# document the client is generated from comes out of the binary that will
# actually serve it, in the same build. A client generated from a stale
# document is the copy nobody is looking at.
RUN /out/zettaplanner openapi > /out/openapi.json

# ---------- 2. Web app -------------------------------------------------------
FROM node:22-alpine AS web
WORKDIR /build
# Same trick: lockfile first, so editing a component does not reinstall npm.
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY frontend/ ./
COPY --from=backend /out/openapi.json /openapi.json
# `npm run build` is `api:generate && tsc -b && vite build`, so a type error or
# a drifted API client fails the image build rather than shipping a broken
# bundle. ZP_OPENAPI_FILE points the generator at the document emitted above
# instead of making it build Go inside the Node stage.
ENV ZP_OPENAPI_FILE=/openapi.json
RUN npm run build

# ---------- 3. Runtime -------------------------------------------------------
FROM nginx:1.27-alpine

# git      — the one-way per-project projection (D10) shells out to it
# poppler  — pdftoppm, how an ingested PDF becomes page images; without it
#            images still ingest and PDFs fail with a message naming this
# tini     — reaps the two children properly; the entrypoint is a shell script
RUN apk add --no-cache git poppler-utils ca-certificates tzdata tini

COPY --from=backend /out/zettaplanner /usr/local/bin/zettaplanner
COPY --from=web     /build/dist       /srv/web

COPY deploy/nginx.conf    /etc/nginx/nginx.conf
COPY deploy/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

# Where state that must survive a rebuild lives. Every one of these is a volume
# in docker-compose.yml; the mkdir is here so the paths exist when it is not.
RUN mkdir -p /var/lib/zettaplanner/state \
             /var/lib/zettaplanner/uploads \
             /var/lib/zettaplanner/git

ENV ZP_ADDR=127.0.0.1:8095 \
    ZP_MODELS_PATH=/var/lib/zettaplanner/state/models.yaml \
    ZP_UPLOADS_DIR=/var/lib/zettaplanner/uploads \
    ZP_GIT_DIR=/var/lib/zettaplanner/git \
    ZP_LOG_FORMAT=json

EXPOSE 8091

# Both halves have to answer. A container serving pages with a dead API is not
# healthy, and the start period is generous because `serve` validates every
# configured model against a live endpoint before it listens (D20).
HEALTHCHECK --interval=30s --timeout=10s --start-period=180s --retries=3 \
  CMD wget -qO /dev/null http://127.0.0.1:8091/healthz \
   && wget -qO /dev/null http://127.0.0.1:8091/health || exit 1

STOPSIGNAL SIGTERM
ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/entrypoint.sh"]
