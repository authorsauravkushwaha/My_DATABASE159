# Kestrel

Kestrel is an open-source-friendly database platform MVP: a tenant-aware control plane and JSON document API built on PostgreSQL. It is intentionally small, runnable, and explicit about what is not implemented. It is not yet a production-ready replacement for Supabase or Firebase.

## What works today

- Email/password registration and login, PBKDF2 password hashing, signed short-lived bearer tokens.
- Registration creates an organization and owner membership.
- Organization-scoped project create/list/read/delete with membership checks.
- Project-scoped collections with a validated JSON Schema subset and record CRUD, pagination, and per-project isolation.
- PostgreSQL is the configured deployment database; SQLite is supported for fast local development and automated tests.
- Health endpoint, Docker Compose deployment, API docs, test suite, CI, and an initial security/architecture/roadmap documentation set.

There is no email verification, password reset, OAuth, MFA, SQL editor, generated SQL tables, realtime, object storage, functions, billing, backups, or admin dashboard yet. JSON collection records are stored in PostgreSQL JSON columns; this is not a general-purpose SQL database proxy. The API does not accept or execute user-provided SQL.

## Run locally

Requirements: Python 3.11+ and PostgreSQL 15+ (or Docker Compose).

```bash
cp .env.example .env
# Set a unique JWT_SECRET of at least 32 characters in .env before use.
docker compose up --build
```

The API is at <http://localhost:8000>; OpenAPI docs at <http://localhost:8000/docs>. Compose starts PostgreSQL and the API. The API's database URL and signing key are read from environment variables. Do not reuse development secrets in production.

For local Python development, create a virtual environment, install `requirements.txt`, set `DATABASE_URL` and `JWT_SECRET`, then run:

```bash
uvicorn app.main:app --reload
```

Set `AUTO_CREATE_SCHEMA=true` only for development. Production schema changes should use reviewed migrations; migration automation is not yet wired into the runtime. `/health` is a liveness endpoint, not a complete dependency/readiness check.

## Quick API flow

1. `POST /v1/auth/register` with `email`, `password` (12+ characters), and `organization_name`.
2. Use the returned bearer token with `POST /v1/organizations/{organization_id}/projects`.
3. Create a collection with `POST /v1/projects/{project_id}/collections`, e.g. `{"name":"notes","schema":{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}}`.
4. Create and list records using `/v1/projects/{project_id}/data/{collection_name}`.

The token is a bearer credential. Keep it out of logs, browser local storage where possible, and source control. API authentication currently has no refresh tokens or revocation list.

## Tests

```bash
python -m pip install -r requirements-dev.txt
pytest
```

The tests use isolated SQLite databases and exercise registration, authentication, validation, CRUD, and cross-organization denial. PostgreSQL-specific behavior, load, failover, and recovery have not been validated. See [docs/roadmap.md](docs/roadmap.md) for milestones and [docs/architecture.md](docs/architecture.md) for system boundaries.

## Project documents

- [Architecture and stack](docs/architecture.md)
- [Threat model and security notes](security/THREAT_MODEL.md)
- [Implementation roadmap](docs/roadmap.md)
- [Deployment and operations](docs/deployment.md)
- [Contributing](CONTRIBUTING.md)
- [Security reporting](SECURITY.md)

## License

MIT; see [LICENSE](LICENSE).
