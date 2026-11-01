# Kestrel

Kestrel is an open-source-friendly database platform: a tenant-aware control plane
and JSON document API built on PostgreSQL, plus a persistent LSM-tree storage
plane (write-ahead log, SSTable runs, compaction) implemented in Go and exposed
through a Go data-plane gateway. It is intentionally small, runnable, and
explicit about what is not implemented. It is not yet a production-ready
replacement for Supabase or Firebase.

## What works today

- Email/password registration and login, PBKDF2 password hashing, signed short-lived bearer tokens.
- Registration creates an organization and owner membership.
- Organization-scoped project create/list/read/delete with membership checks.
- Project-scoped collections with a validated JSON Schema subset and record CRUD, pagination, and per-project isolation.
- PostgreSQL is the configured deployment database; SQLite is supported for fast local development and automated tests.
- **Storage plane:** a Go LSM-tree key-value engine (WAL + SSTable runs + compaction) with crash recovery, served over a length-prefixed JSON protocol, plus a Go HTTP gateway that authenticates with the same JWT secret as the control plane and namespaces every record key per project/collection.
- Health endpoints, Docker Compose deployment (control plane + storage plane), API docs, test suite, CI, and an initial security/architecture/roadmap documentation set.

There is no email verification, password reset, OAuth, MFA, SQL editor, generated SQL tables, realtime, object storage, functions, billing, backups, or admin dashboard yet. JSON collection records are stored in PostgreSQL JSON columns by the control plane; the storage plane is a standalone data plane awaiting record-CRUD delegation from the control plane. The API does not accept or execute user-provided SQL.

## Architecture

```text
Control plane (Python / FastAPI + PostgreSQL)
  - auth, organizations, projects, collection definitions
  - JWT authentication and organization/project membership enforcement

Data plane (Go)
  storage engine            gateway
  LSM-tree KV (WAL, SSTable,      HTTP/JSON record API:
  compaction, crash recovery)     GET/PUT/DELETE/scan per project/collection
          |                              |
          +------------ TCP protocol ----+
                              |
                         persistent runs on disk
```

The control plane authorizes who may touch which project; the gateway authenticates
every request and scopes every stored key to `project:{id}:collection:{id}:record:{id}`,
so cross-tenant reads are impossible at the storage layer regardless of the caller.

## Run locally

Requirements: Python 3.11+ and Docker Compose (the Go services build from source
in Compose; a Go 1.22+ toolchain is needed only to build them outside Compose).

```bash
cp .env.example .env
# Set a unique JWT_SECRET of at least 32 characters in .env before use.
docker compose up --build
```

Services:

| Service | Port | Purpose |
|---------|------|---------|
| db (PostgreSQL 16) | 5432 | Control-plane metadata (auth, orgs, projects, collections) |
| api (FastAPI) | 8000 | Control plane: auth, projects, collections, OpenAPI docs at /docs |
| storage (Go LSM engine) | 9100 | Persistent key-value store, WAL + SSTable runs |
| gateway (Go data plane) | 8100 | Authenticated HTTP record API |

The API is at <http://localhost:8000>; the data plane gateway is at
<http://localhost:8100>.

For local Python development, create a virtual environment, install
`requirements.txt`, set `DATABASE_URL` and `JWT_SECRET`, then run:

```bash
uvicorn app.main:app --reload
```

Set `AUTO_CREATE_SCHEMA=true` only for development. Production schema changes
should use reviewed migrations; migration automation is not yet wired into the
runtime. `/health` is a liveness endpoint, not a complete dependency/readiness check.

## Data plane API (gateway)

All endpoints require `Authorization: Bearer <token>` with a token issued by the
control plane (`/v1/auth/register` or `/v1/auth/login`).

- `PUT /v1/data/{project_id}/{collection_id}/{record_id}` body `{"data": {...}}` → `201 {"id": ...}`
- `GET /v1/data/{project_id}/{collection_id}/{record_id}` → `200 {"id": ..., "data": {...}}` or `404`
- `DELETE /v1/data/{project_id}/{collection_id}/{record_id}` → `204`
- `GET /v1/data/{project_id}/{collection_id}` → list of records in the collection
- `POST /v1/scan` body `{"prefix": "...", "limit": 50}` → raw key scan of the storage engine
- `GET /health` → `{"status":"ok","service":"kestrel-gateway"}`

Record keys are namespaced internally as
`p:<hex(project)>:c:<hex(collection)>:r:<hex(record)>`, so a scan of
`p:<hex>:c:<hex>:r:` returns exactly one collection's records.

## Quick API flow (control plane)

1. `POST /v1/auth/register` with `email`, `password` (12+ characters), and `organization_name`.
2. Use the returned bearer token with `POST /v1/organizations/{organization_id}/projects`.
3. Create a collection with `POST /v1/projects/{project_id}/collections`, e.g.
   `{"name":"notes","schema":{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}}`.
4. Create and list records using `/v1/projects/{project_id}/data/{collection_name}`
   (control plane) or the data plane gateway above.

The token is a bearer credential. Keep it out of logs, browser local storage where
possible, and source control. API authentication currently has no refresh tokens or revocation list.

## Tests

```bash
python -m pip install -r requirements-dev.txt
pytest
```

The tests use isolated SQLite databases and exercise registration, authentication,
validation, CRUD, and cross-organization denial. The Go storage engine has its own
test suite (`go test ./...` in `engine/`); it runs in CI on every push.

PostgreSQL-specific behavior, load, failover, and recovery have not been validated.
See [docs/roadmap.md](docs/roadmap.md) for milestones and [docs/architecture.md](docs/architecture.md) for system boundaries.

## Project documents

- [Architecture and stack](docs/architecture.md)
- [Threat model and security notes](security/THREAT_MODEL.md)
- [Implementation roadmap](docs/roadmap.md)
- [Deployment and operations](docs/deployment.md)
- [Contributing](CONTRIBUTING.md)
- [Security reporting](SECURITY.md)

## License

MIT; see [LICENSE](LICENSE).
