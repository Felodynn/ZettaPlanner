# Running ZettaPlanner on zettaplanner.felodynn.com

**Status: live.** <https://zettaplanner.felodynn.com> is serving from this
machine right now.

```
  user                        Cloudflare edge          this machine
  ────                        ───────────────          ────────────
  https://zettaplanner            tunnel        cloudflared ──► 127.0.0.1:8091  nginx
        .felodynn.com  ─────────► (outbound                       ├─ /        /srv/web   the SPA
                                   only, no                       └─ /api     127.0.0.1:8095  Go
                                   open ports)                                     │
                                                                                   ├─► 127.0.0.1:5433  Postgres 18
                                                                                   │      + pgvector, in a volume
                                                                                   └─► 127.0.0.1:11434 Ollama
                                                                                          on the host
```

Three containers, one image built from this repo:

| Container | What it is |
|---|---|
| `zettaplanner-app` | nginx + the Go binary. One image, built from `Dockerfile` |
| `zettaplanner-db` | `pgvector/pgvector:pg18`, data in the `zp-db` volume |
| `zettaplanner-tunnel` | `cloudflared`, running the `zettaplanner` tunnel |

## Running it

```bash
cd /home/smtai/ZettaPlanner
docker compose up -d          # everything, including the public URL
docker compose ps             # app and db should say (healthy)
```

All three are `restart: unless-stopped`, and `docker` is enabled at boot — so
the deployment comes back by itself after a reboot with nothing to run by hand.
**This machine has to stay powered on and online**: a tunnel only serves while
its origin is running. If the box sleeps the URL goes down and returns by itself
when it wakes.

### After changing code

```bash
docker compose build app && docker compose up -d app
```

The tunnel keeps running throughout; the URL blips while the app container
swaps. Migrations run on every start and are idempotent, so a schema change
ships with the image and needs no separate step.

### Checking on it

```bash
docker compose ps
docker compose logs -f app
docker compose logs zettaplanner-tunnel | grep "Registered tunnel"   # x4
cloudflared tunnel info zettaplanner

curl -sI https://zettaplanner.felodynn.com | head -1
curl -s  https://zettaplanner.felodynn.com/health | jq
```

`/health` is the honest one: it reports the schema version and probes the
database and every configured model, so it fails when the product is unusable
rather than merely when nginx is dead.

### Stopping

```bash
docker compose down           # URL goes down, DNS record stays, volumes stay
```

Bring it back with `up -d`; nothing needs reconfiguring.

---

## Why it is built this way

Four constraints shaped this. They are worth recording because they will come up
again.

### 1. The app container uses host networking

Not a stylistic choice. The model registry reaches the planner and vision models
through the **Ollama daemon on this host**, which listens on `127.0.0.1:11434`
and nothing else. A bridged container cannot see that address. The alternatives
were to reconfigure Ollama to listen on the docker bridge (root on the host's
systemd unit) or to widen it to `0.0.0.0` (a model server on the LAN). Host
networking costs neither, and it is already how `open-webui` on this box reaches
the same daemon.

The price is that nginx binds a **host** port rather than a container one. It
binds `127.0.0.1:8091` — loopback only — so the product is no more exposed to
the LAN than a bridge network would leave it. The tunnel, running on the same
host, is the only way in.

A consequence worth knowing: `models.yaml` is used **verbatim**, endpoints and
all. `http://localhost:11434` inside the container means the same daemon it
means when you run the binary directly. Nothing is rewritten.

### 2. Postgres is a container with a volume, not the host's

Port 5432 on this machine is a different Postgres serving other things. This one
is `zettaplanner-db`, published on `127.0.0.1:5433` so you can reach it from the
machine for `psql` or a backup, and from nowhere else.

It is the **pgvector** image rather than plain `postgres` because the first
migration *asserts* the extension and refuses to create it — a migration runner
that needs superuser is a deployment where the application role has it, which is
a far larger problem than a setup step. The image's init hook is where a
superuser legitimately exists, so `CREATE EXTENSION vector` runs there, once, on
an empty volume (`configs: pgvector-init` in `docker-compose.yml`).

One trap, already hit: from Postgres 18 these images want the volume at
`/var/lib/postgresql`, **not** `/var/lib/postgresql/data`. They refuse to start
against the old path so that a later `pg_upgrade --link` is not crossing a mount
boundary.

### 3. One origin, no CORS

The SPA sets `baseUrl: ''` and calls a relative `/api`, so nginx serving both
the bundle and the API means the browser never learns two processes are
involved. That is what D2 says the deployment should be and what `ZP_DEV_ORIGIN`
exists to avoid needing — it is empty here, deliberately.

Cloudflare terminates TLS and forwards plain http, so the backend runs with
`ZP_SECURE_COOKIES=true` and `ZP_TRUST_PROXY=true` and reads
`X-Forwarded-Proto`. Verified over the public URL: the session cookie comes back
`HttpOnly; Secure; SameSite=Lax`.

### 4. A tunnel of its own

Created with the cloudflared CLI against the felodynn.com zone, not through the
Zero Trust dashboard — same product, no payment method required, and the routing
lives in `deploy/cloudflared/config.yml` in git rather than in a web UI.

It is a **separate** tunnel from `felodynn-demo`, not a second hostname on it.
The two deployments then start, stop, break and get revoked independently;
sharing one would mean a demo restart takes the product down with it.

| Thing | Value |
|---|---|
| Tunnel name | `zettaplanner` |
| Tunnel ID | `e5943c39-250a-4cf7-9f0d-d00e9a557a4f` |
| Zone certificate | `~/.cloudflared/cert.pem` |
| Tunnel credentials | `~/.cloudflared/e5943c39-….json` — **private key, never commit it** |
| Ingress routing | [`deploy/cloudflared/config.yml`](deploy/cloudflared/config.yml) |
| DNS | One proxied CNAME, created by `cloudflared tunnel route dns` |

The commands that produced it, for the record:

```bash
cloudflared tunnel create zettaplanner
cloudflared tunnel route dns zettaplanner zettaplanner.felodynn.com
```

---

## State, and what losing it costs

Four volumes. Only one of them is precious.

| Volume | Holds | If you lose it |
|---|---|---|
| `zp-db` | **The entire product.** Every project, plan, diagram, thread, proposal, audit row | Everything is gone |
| `zp-uploads` | Ingest source images, referenced **by path** from the database | Ingest rows survive and point at files that are not there |
| `zp-git` | The one-way per-project git projections (D10) | Disposable — `zettaplanner git resync` rebuilds them |
| `zp-state` | `models.yaml` copy and the model probe cache | Disposable — re-probed on next boot, a slower start |

Backing up the one that matters:

```bash
docker exec zettaplanner-db pg_dump -U zettaplanner -Fc zettaplanner > zp-$(date -I).dump
docker run --rm -v zettaplanner_zp-uploads:/src -v "$PWD:/out" alpine \
  tar czf /out/zp-uploads-$(date -I).tar.gz -C /src .
```

Restoring:

```bash
docker compose stop app
docker exec -i zettaplanner-db pg_restore -U zettaplanner -d zettaplanner --clean --if-exists < zp-DATE.dump
docker compose start app
```

`data/` in the backend working tree is the **development** copy of uploads and
git projections. The container never touches it; it is excluded from the build
context.

---

## Configuration

`.env` is gitignored and holds one secret. `.env.example` is its committed
shape.

| Variable | What it does |
|---|---|
| `ZP_DB_PASSWORD` | The `zettaplanner` Postgres role. **Baked into the data directory** on first start — changing it later means `ALTER ROLE` too, not just this file |
| `ZP_LOG_LEVEL` | `debug \| info \| warn \| error`, default `info` |
| `ZP_MODELS_FILE` | The registry to mount, default `~/.zettaplanner/models.yaml` |
| `ZP_TUNNEL_CREDENTIALS_FILE` | The tunnel's private key, default this machine's |

Everything else is set in `docker-compose.yml` or the image, where it belongs:
addresses, paths, and the three flags that describe the deployment shape
(`ZP_SECURE_COOKIES`, `ZP_TRUST_PROXY`, `ZP_DEV_ORIGIN=""`).

**Changing models.yaml** takes a restart, not a rebuild — the entrypoint copies
it in at every start:

```bash
docker compose restart app
```

It is copied rather than used in place because the directory holding it is also
where the probe cache is written: `serve` proves every model's context window at
boot (D20) and caches the answer beside the registry, and a read-only mount
would make that a silent re-probe on every restart.

---

## Accounts

The first admin was bootstrapped during deployment. Its one-time password is in
`.bootstrap-admin` (mode 600, gitignored). **Change it and delete that file.**

Making another account, later:

```bash
# Only works while no account exists — it is a bootstrap, not a back door.
printf 'the-password' | docker compose exec -T app \
  zettaplanner admin create-user -email you@example.com -name "You" -role admin -password-stdin
```

After bootstrap, users are created through the API by an admin
(`POST /api/v1/admin/users`), which is also how a password is changed
(`PATCH /api/v1/admin/users/{id}`).

---

## Verified working

Checked end to end over the public URL, not just locally:

- The SPA loads, and deep links (`/projects`, and the rest of the client-side
  routes) return the app rather than a 404.
- HTTPS with Cloudflare's certificate, HTTP/2.
- The session cookie comes back `HttpOnly; Secure; SameSite=Lax` — the backend
  reads `X-Forwarded-Proto` and nginx sets it.
- Login, `/auth/me`, project create and project patch all round-trip.
- `/health` reports the database and all four models `ok`, and the planner's
  context window proven by probe — so the container really is reaching the
  host's Ollama daemon.
- **SSE genuinely streams.** `: connected` arrives immediately and `: keepalive`
  25 seconds later on a still-open connection, through Cloudflare. A buffering
  proxy anywhere in that path would deliver both at once when the stream closed,
  which is exactly the stall the thread's reconnect logic exists to survive.
- `docker compose restart` brings all three back healthy and serving, which is
  the same path a reboot takes.

---

## What is exposed

Being explicit, because this is a real product with real planning data in it:

- **Anyone on the internet can reach the login page.** There is no network-level
  gate — the tunnel is public and Cloudflare Access needs the Zero Trust
  dashboard, which needs a card on file. The only thing between a stranger and
  the data is a password.
- There is **no rate limiting and no lockout** on `POST /api/v1/auth/login`. A
  slow credential-stuffing run against a known email is not currently slowed
  down by anything.
- `/docs` and `/openapi.json` are public. They describe the API and expose no
  data, but they do tell a reader exactly what the surface is.
- The tunnel exposes **only** `zettaplanner.felodynn.com`. Anything else
  arriving at it gets a flat 404 from the catch-all in `config.yml`, and nothing
  else on this machine — Plane, ShareLaTeX, the Felodynn demo, Apache — is
  reachable through it.
- `X-Robots-Tag: noindex, nofollow` is set, so the URL should stay out of search
  results.

If you want it tighter, in rough order of effort:

1. **Close `/docs`** — delete its three `location =` lines in
   `deploy/nginx.conf`, rebuild.
2. **Rate-limit login** — an nginx `limit_req_zone` on `/api/v1/auth/login` is a
   few lines and buys most of the protection a lockout would.
3. **Gate it by email** — Cloudflare Access. This is the one thing that does
   need the Zero Trust dashboard, and therefore a payment method.

---

## If something is wrong

| Symptom | Cause | Fix |
|---|---|---|
| Cloudflare **Error 1033** | `cloudflared` not running | `docker compose up -d cloudflared`, then check its logs |
| **502** through the tunnel | app container down or unhealthy | `docker compose ps`, then `docker compose logs app` |
| App restarts in a loop, logs mention a model | Ollama is down, or a model in `models.yaml` is unreachable | `systemctl status ollama`; `serve` validates every model before it listens, on purpose. The loop recovers by itself once the daemon is back |
| App restarts, logs mention the database | Postgres not ready yet | Self-correcting — the entrypoint retries `migrate up` for a minute |
| Pages load, every API call fails | the Go process died inside the container | The container should have exited and restarted; look for `[entrypoint]` lines |
| `permission denied` reading `creds.json` | the image runs as uid 65532; the key is 0400 owned by you | Already handled — compose sets `user: "1000:1000"`. Match that to the new owner if you move hosts |
| Thread stuck **Reconnecting…** | stream interrupted, or session expired | Reload. Sessions survive a restart (they are in Postgres), so this should be rare |
| `cloudflared` warns it is outdated | that is the **host** CLI, 2026.2.0 | Harmless: the container runs `cloudflare/cloudflared:latest`. The host binary is only used for management commands |
| PDF ingest fails, images work | `pdftoppm` missing | It is in the image (`poppler-utils`). If this appears, the image is stale — rebuild |

---

## Moving to another machine

The deployment is tied to this box by two things: the tunnel credentials and the
host's Ollama daemon.

```bash
# copy the repo and the volumes, then:
cloudflared tunnel login
cloudflared tunnel create zettaplanner-2
cloudflared tunnel route dns zettaplanner-2 zettaplanner.felodynn.com  # repoints the CNAME
```

Then update `tunnel:` in `deploy/cloudflared/config.yml`, set
`ZP_TUNNEL_CREDENTIALS_FILE` in `.env`, adjust `user:` in `docker-compose.yml`
if the uid differs, make sure Ollama is running on the new host with the models
`models.yaml` names, and `docker compose up -d --build`.

Do **not** copy the credentials file between machines. Creating a second tunnel
takes ten seconds and keeps the old one revocable on its own.

### Taking it down permanently

```bash
docker compose down -v          # -v deletes the volumes. That is the database.
cloudflared tunnel delete zettaplanner
```

Then remove the CNAME under **DNS → Records** in the Cloudflare dashboard.
Deleting the tunnel revokes its credentials, so a leaked credentials file
becomes useless.
