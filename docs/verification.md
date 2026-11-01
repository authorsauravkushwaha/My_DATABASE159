# Release verification

## Verified locally during implementation

Verification date: **2026-11-01**. The final `bash scripts/check.sh` run completed successfully with an external Chromium configured.

| Check | Result |
|---|---|
| TypeScript client and Vue strict type checking | Passed |
| ESLint and production Vite/MDX build | Passed |
| SDK unit tests | 9 passed |
| Dashboard schema/demo/dialog tests | 8 passed |
| Python suite (includes the integration test below) | 14 passed with Go available; PostgreSQL test skipped locally |
| Go engine and gateway `go vet` and `go test -race` | Passed |
| Real Python → gateway → engine integration | Passed: CRUD, cursor pagination, engine restart persistence, cross-tenant denial, schema rejection, API toggle, raw-scan denial |
| Playwright demo CRUD, real API persistence, mobile/MDX | 3 passed |
| npm dependency audit | 0 reported vulnerabilities at verification time |
| Python dependency audit | No known vulnerabilities at verification time |
| ShellCheck, shell syntax, and workspace/lockfile consistency | Passed |

Browser checks used Chromium against the running Vite dashboard and FastAPI/SQLite control plane. Screenshots were inspected at desktop sizes; the mobile test checks navigation and horizontal overflow.

## Configured but not executed here

- GitHub Actions PostgreSQL 16 integration/migration job.
- Docker image builds and full Compose startup (no Docker daemon in the workspace).
- GitHub secret scanning and hosted browser-test job.
- TLS ingress and production deployment smoke tests.

These are configured CI/deployment gates, not claimed local results. Run them before release. Dependency audit results are time-sensitive and are not a guarantee of security.

## Still required before production

PostgreSQL concurrency/isolation coverage, identity hardening, rate limits, RLS, audit logging, backup restore drills, storage failure/load tests, migration adoption/rollback exercises, operational alerting, and independent security review. See the roadmap and threat model.
