# Deployment and local operations

## Local Compose

1. Install Docker Engine and Compose v2.
2. Copy `.env.example` to `.env`; generate independent high-entropy values for `JWT_SECRET` and `POSTGRES_PASSWORD` (for example, `python -c 'import secrets; print(secrets.token_urlsafe(48))'`).
3. Run `docker compose up --build` and visit `http://localhost:8000/docs`.
4. Stop with `docker compose down`. `docker compose down -v` also destroys the local database volume; use only when you intend to erase local data.

PostgreSQL is not published to the host. The compose API port is published for local use. The example intentionally enables `AUTO_CREATE_SCHEMA` only for local development.

## Production is not turnkey yet

This repository does not include a production deployment workflow, TLS reverse proxy, managed secret store, migration runner, encrypted backup service, monitoring stack, HA, or automated restore. Do not expose this compose configuration directly to the internet. A first production deployment should use a private PostgreSQL endpoint, managed secrets, TLS ingress, firewall/network rules, non-development schema migrations, backups stored separately, monitoring, restricted database credentials, and tested restore/rollback procedures. The repo has no operational evidence for RPO/RTO or production performance targets yet.

## Python development

```bash
python -m venv .venv
. .venv/bin/activate
pip install -r requirements-dev.txt
export DATABASE_URL='postgresql+psycopg://user:password@localhost:5432/kestrel'
export JWT_SECRET="$(python -c 'import secrets; print(secrets.token_urlsafe(48))')"
export AUTO_CREATE_SCHEMA=true
uvicorn app.main:app --reload
```

For quick tests, pytest uses an isolated in-memory SQLite database and does not need PostgreSQL. A PostgreSQL integration test job remains an explicit roadmap item. Keep production and development credentials/data separate.

## Health and observability

`GET /health` reports process liveness only. It does not test database readiness or dependency health. Structured logs, OpenTelemetry, metrics, alerting, query statistics, audit log storage, and dashboards have not been implemented; do not infer service health from missing telemetry.
