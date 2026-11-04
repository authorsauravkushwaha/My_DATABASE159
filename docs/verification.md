# Release verification

## Verified locally during implementation

Verification date: **2026-11-04** (v0.3.0). Local environment: Python 3.11, Node 22 / npm 10, no Go toolchain, no Docker daemon, no PostgreSQL server, no Chromium download access.

| Check | Result |
|---|---|
| Python suite (API, hardening, high-capacity, migrations round-trip, API keys, observability) | **38 passed**, 3 skipped (Go toolchain ×2, PostgreSQL ×1) |
| Ruff lint (`app`, `tests`, `alembic`) | Passed |
| Alembic `upgrade head` → `check` → `downgrade base` → `upgrade head` (SQLite) | Passed; `alembic check` reports no drift vs models |
| ESLint (root workspace), `vue-tsc --noEmit`, client `tsc --noEmit` | Passed |
| SDK tests (`@kestrel/client`) | 11 passed |
| Dashboard Vitest (schema, demo incl. API-key lifecycle, modal) | 10 passed |
| Production builds (client + dashboard Vite/MDX) | Passed |
| `npm ci` + `scripts/check-workspace.mjs` manifest/lockfile consistency | Passed |
| `npm audit` / `pip-audit -r requirements.txt` | 0 known vulnerabilities at verification time |
| `bash -n scripts/*.sh` | Passed |
| Live smoke against `uvicorn` + migrated SQLite | Passed: register/login, project + collection, mint `writer`/`reader` keys, writer writes (201), reader writes denied (403), reader indexed query (200), key-scoped list/revoke (204 → immediate 401), expired key → 401, wrong-`type` JWT → 401, `X-RateLimit-*` headers present on API routes and absent on `/health`, `/metrics` renders `kestrel_build_info{version="0.3.0"}` + normalized paths (`/v1/projects/{id}/api-keys`), oversized body → 413 **with** security headers |

Middleware stack order was verified programmatically: `SecurityHeaders → Metrics → RateLimit → RequestBodyLimit → router`.

## Configured but not executed here

- GitHub Actions: PostgreSQL 16 integration/migration job, Go `engine`/`gateway` `gofmt`/`vet`/`-race` suites, ShellCheck, gitleaks, hosted Playwright browser tests.
- Docker image builds and full Compose startup (no Docker daemon in the workspace).
- Local Chromium/Playwright run (binary download blocked in this sandbox; `scripts/check.sh` skips gracefully).
- TLS ingress and production deployment smoke tests.

These are configured CI/deployment gates, not claimed local results. Run them before release. Dependency audit results are time-sensitive and are not a guarantee of security.

## Still required before production

PostgreSQL concurrency/isolation coverage, identity hardening beyond session + API keys, distributed rate limiting, RLS, audit logging, backup restore drills, storage failure/load tests, migration adoption/rollback exercises, operational alerting, and independent security review. See the roadmap and threat model.
