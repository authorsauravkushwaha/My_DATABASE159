# Deployment and operations

## Local Compose

```bash
bash scripts/setup.sh
docker compose up --build
```

Open `http://localhost:3000`. The API is bound to `127.0.0.1:8000`; the gateway to `127.0.0.1:8100`. PostgreSQL and the raw engine have no published host ports. The dashboard container runs Nginx as a non-root user on port 8080.

The `migrate` service runs `alembic upgrade head` and must finish successfully before the API starts. `AUTO_CREATE_SCHEMA` is false. `setup.sh` installs local development dependencies and generates `.env` with independent random secrets. If you only need containers, you may instead copy `.env.example` and fill both values with unique high-entropy secrets.

Do not use example/empty secrets. Do not expose this development topology directly to the internet. The dashboard's CSP allows same-origin API requests; it does not require permissive CORS.

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

## Health and shutdown

- API `/health`: process liveness only.
- API `/ready`: executes `SELECT 1`; does not verify migrations or Go dependencies.
- Gateway `/health`: process liveness only, not authorization/storage readiness.
- Dashboard container: checks its static entry page.
- Gateway handles SIGTERM with a bounded graceful shutdown.

The engine's TCP protocol is private and unauthenticated. Never publish port 9100. Run exactly one engine process per data directory; multi-process ownership locking is not implemented.

## Backups and destructive operations

No automated backup service is included. Use PostgreSQL-native backup/PITR tooling and separate encrypted storage. Back up the Go data directory only with a consistent snapshot or after a clean engine shutdown. Test restores regularly.

Dashboard JSON export is **one page**, not a backup. Deleting a project/collection cascades only control-plane records. Go-engine records are separate and are not automatically removed. Plan orphan cleanup before unified storage is implemented.

`docker compose down` preserves named volumes. `docker compose down -v` destroys them.

## Production gates—not included yet

Before public production use, provide TLS ingress, managed secrets and rotation, least-privilege DB roles, PostgreSQL RLS, edge rate/concurrency/body limits, identity recovery/MFA/session revocation, audit logs, metrics/alerting, quotas, encrypted backups, restore drills, migration rollback exercises, dependency/container scanning, and independent security review.

The Go engine remains experimental: no full SSTable checksum validation, multi-process lock, replication, or measured crash/load guarantees. No production RPO/RTO, availability, or performance claim is made.
