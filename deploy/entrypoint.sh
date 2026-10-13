#!/bin/sh
# Two processes in one container, which needs a little care: if either dies the
# container must die too, so Docker's restart policy brings back a whole working
# deployment rather than leaving half of one running.
set -eu

say() { echo "[entrypoint] $*"; }

ZP_ADDR="${ZP_ADDR:-127.0.0.1:8095}"
export ZP_ADDR

# ── the model registry ───────────────────────────────────────────────────────
#
# models.yaml is machine-scoped and never committed (CONTRACT §8), so it is
# bind-mounted read-only from the host. It is copied rather than used in place
# because the directory holding it is also where the probe cache is written —
# `serve` proves every model's context window at boot (D20) and caches the
# answer next to the registry. A read-only mount would make that a silent
# re-probe on every restart.
MODELS_SRC="${ZP_MODELS_SRC:-/etc/zettaplanner/models.yaml}"
if [ ! -f "$MODELS_SRC" ]; then
  say "no model registry at $MODELS_SRC" >&2
  say "mount ~/.zettaplanner/models.yaml there — the API refuses to start without one" >&2
  exit 1
fi
mkdir -p "$(dirname "$ZP_MODELS_PATH")"
cp "$MODELS_SRC" "$ZP_MODELS_PATH"
say "model registry copied from $MODELS_SRC"

# ── schema ───────────────────────────────────────────────────────────────────
#
# Idempotent: `migrate up` on a current database applies nothing. Retried
# because compose's healthcheck says Postgres is accepting connections, which
# is a moment or two before it is actually ready to serve the first one after a
# cold start on a new volume.
i=0
until zettaplanner migrate up; do
  i=$((i + 1))
  if [ "$i" -ge 30 ]; then
    say "the database did not become usable in time" >&2
    exit 1
  fi
  say "waiting for the database (attempt $i)"
  sleep 2
done
say "schema is current"

# ── the API ──────────────────────────────────────────────────────────────────
say "starting the API on ${ZP_ADDR}"
zettaplanner serve &
BACKEND_PID=$!

# nginx proxies to it, so wait for it to answer before opening the door. `serve`
# validates every configured model against a live endpoint before it listens,
# and a cold model load is minutes rather than seconds — hence 300 x 1s rather
# than the handful a plain HTTP server would need.
i=0
until wget -qO /dev/null "http://${ZP_ADDR}/health" 2>/dev/null; do
  i=$((i + 1))
  if [ "$i" -ge 300 ]; then
    say "the API did not come up in time — check the model registry and the Ollama daemon" >&2
    kill "$BACKEND_PID" 2>/dev/null || true
    exit 1
  fi
  # The API exiting during startup validation is the common failure, and it
  # deserves the real error rather than five minutes of silence.
  if ! kill -0 "$BACKEND_PID" 2>/dev/null; then
    say "the API exited during startup — its error is above" >&2
    exit 1
  fi
  sleep 1
done
say "the API is up"

nginx -g 'daemon off;' &
NGINX_PID=$!
say "nginx is up on 127.0.0.1:8091"

# Forward shutdown to both children so `docker stop` is clean rather than a kill
# after the grace period — the API drains in-flight turns on SIGTERM.
terminate() {
  say "shutting down"
  kill -TERM "$BACKEND_PID" "$NGINX_PID" 2>/dev/null || true
  wait "$BACKEND_PID" 2>/dev/null || true
  wait "$NGINX_PID" 2>/dev/null || true
  exit 0
}
trap terminate TERM INT

# busybox sh has no `wait -n`, so poll.
while kill -0 "$BACKEND_PID" 2>/dev/null && kill -0 "$NGINX_PID" 2>/dev/null; do
  sleep 2
done

say "a process exited — stopping the container" >&2
kill -TERM "$BACKEND_PID" "$NGINX_PID" 2>/dev/null || true
exit 1
