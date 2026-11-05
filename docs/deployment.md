# Deployment and operations

## Local Compose

```bash
bash scripts/setup.sh
docker compose up --build
```

Open `http://localhost:3000`. The API is bound to `127.0.0.1:8000`; the gateway to `127.0.0.1:8100`. PostgreSQL and the raw engine have no published host ports. The dashboard container runs Nginx as a non-root user on port 8080.

The `migrate` service runs `alembic upgrade head` and must finish successfully before the API starts. `AUTO_CREATE_SCHEMA` is false. `setup.sh` installs local development dependencies and generates `.env` with independent random secrets. If you only need containers, you may instead copy `.env.example` and fill both values with unique high-entropy secrets.

Do not use example/empty secrets. Do not expose this development topology directly to the internet. The dashboard's CSP allows same-origin API requests; it does not require permissive CORS.

## Static GitHub Pages site

`.github/workflows/pages.yml` publishes the Vue workspace as a static site on every push
to `main`. It is a demonstration and documentation site, not a deployment of the API.

```bash
VITE_BASE=/My_DATABASE159/ VITE_STATIC_DEMO=true npm run build -w @kestrel/dashboard
# or, with the base path handled for you:
bash scripts/build-site.sh /My_DATABASE159/
```

- `VITE_BASE` is the asset path for a project page; use `/` for a user or organization page.
- `VITE_STATIC_DEMO=true` opens the marketing page at `/#/home` and replaces the sign-in
  form with demo entry, because a static host has no control plane to authenticate against.
- The output is `apps/dashboard/dist`: one `index.html`, hashed assets, `favicon.svg`, and
  `.nojekyll`. Routing is hash-based, so deep links need no rewrite rules.
- The site makes **no API requests**. The workspace runs in its in-memory demo mode and
  resets on refresh; nothing is stored in the browser.

To publish, enable **Settings → Pages → Source: GitHub Actions** once. The workflow's
`deploy` job uses the `github-pages` environment and needs `pages: write` and
`id-token: write`, which are already declared. Preview the same artifact locally with
`npm run site` (builds for `/` and serves `apps/dashboard/dist`).

Do not treat this site as evidence of production readiness: it has no account system, no
persistence, and no data of its own.

## Database migrations

Always back up and test a migration on a copy before applying it to real data.

```bash
export DATABASE_URL='postgresql+psycopg://user:password@private-db:5432/kestrel'
.venv/bin/alembic upgrade head
.venv/bin/alembic check
```

For model changes, generate a reviewed migration:

```bash
.venv/bin/alembic revision --autogenerate -m 'describe the change'
# Review every operation, especially drops, type changes, and constraints.
.venv/bin/alembic upgrade head
```

CI tests upgrade → model comparison → downgrade → upgrade on SQLite and runs a PostgreSQL vertical-slice test after migrations. A downgrade can destroy data; it is not an automatic production rollback strategy.

### Existing pre-migration database

The initial repository used `create_all` without an Alembic version table. **Do not run the initial migration blindly over those existing tables.** Back up the database, compare its actual schema and constraints with revision `0001`, reconcile differences manually, and only then stamp the verified baseline:

```bash
# ONLY after verifying that the existing schema exactly matches revision 0001.
.venv/bin/alembic stamp 0001
.venv/bin/alembic upgrade head
```

Stamping records a version; it does not repair a schema. Keep `AUTO_CREATE_SCHEMA=false` outside tests.

## Development without Docker

```bash
bash scripts/setup.sh
bash scripts/dev.sh
```

This uses SQLite under `.kestrel/`, applies migrations, starts FastAPI on port 8000, and Vite on port 5173. It does not start the separate Go services. The live demo adapter needs no backend at all.

## Browser tests

```bash
npx playwright install chromium
npm run test:e2e
```

Playwright starts the API and dashboard if needed. `CHROMIUM_EXECUTABLE_PATH` can point to an existing compatible Chromium binary. Tests create disposable accounts/records in `.kestrel/e2e.db`; never point the test server at production. CI installs Chromium and uploads failure traces.

## Rate limiting and metrics

The API enforces two per-client fixed windows in process memory (no external dependencies):

| Variable | Default | Meaning |
| --- | --- | --- |
| `RATE_LIMIT_MAX_REQUESTS` / `RATE_LIMIT_WINDOW_SECONDS` | `1200` / `60` | Global per-client window for all non-exempt routes. |
| `RATE_LIMIT_AUTH_MAX_REQUESTS` / `RATE_LIMIT_AUTH_WINDOW_SECONDS` | `20` / `60` | Strict window on `/v1/auth/*` against credential stuffing. |
| `RATE_LIMIT_DISABLED` | `false` | Disable enforcement and headers entirely. |
| `RATE_LIMIT_TRUST_PROXY` | `false` | Key on the first `X-Forwarded-For` hop instead of the peer address. Enable **only** behind a proxy that overwrites that header. |

Successful responses carry `X-RateLimit-Limit` / `X-RateLimit-Remaining`; rejections are `429` with `Retry-After`. `/health` and `/ready` are exempt so probes never starve. The limiter is per process and IP-based: multi-replica deployments should keep a shared limiter at the edge (the defaults are deliberately generous to avoid false positives behind NAT).

`GET /metrics` serves Prometheus text format (`kestrel_http_requests_total`, `kestrel_http_request_duration_seconds`, `kestrel_build_info`) with UUID and collection-name path normalization plus a 200-pattern cardinality cap per process. Set `METRICS_TOKEN` (32+ characters) to require `Authorization: Bearer <token>` for scrapes; otherwise the endpoint is open like `/health`. Scrape each worker/replica separately — the registry is intentionally in-process.

## Health and shutdown

- API `/health`: process liveness only.
- API `/ready`: executes `SELECT 1`; does not verify migrations or Go dependencies.
- API `/metrics`: Prometheus registry render; counts every response including rejections.
- Gateway `/health`: process liveness only, not authorization/storage readiness.
- Dashboard container: checks its static entry page.
- Gateway handles SIGTERM with a bounded graceful shutdown.

The engine's TCP protocol is private and unauthenticated. Never publish port 9100. Run exactly one engine process per data directory; multi-process ownership locking is not implemented.

## Backups and destructive operations

No automated backup service is included. Use PostgreSQL-native backup/PITR tooling and separate encrypted storage. Back up the Go data directory only with a consistent snapshot or after a clean engine shutdown. Test restores regularly.

Dashboard JSON export is **one page**, not a backup. Deleting a project/collection cascades only control-plane records. Go-engine records are separate and are not automatically removed. Plan orphan cleanup before unified storage is implemented.

`docker compose down` preserves named volumes. `docker compose down -v` destroys them.

## Production gates—not included yet

Before public production use, provide TLS ingress, managed secrets and rotation, least-privilege DB roles, PostgreSQL RLS, distributed edge rate/concurrency limits on top of the in-process defaults, identity recovery/MFA/session revocation, audit logs, alerting on `/metrics`, quotas, encrypted backups, restore drills, migration rollback exercises, dependency/container scanning, and independent security review.

The Go engine remains experimental: no full SSTable checksum validation, multi-process lock, replication, or measured crash/load guarantees. No production RPO/RTO, availability, or performance claim is made.
