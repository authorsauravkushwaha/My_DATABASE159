# Implementation roadmap

A milestone is complete only after its acceptance tests pass. The current code implements Milestone 0 and the scoped slice described below; it does not implement later checklist items.

## Milestone 0 — foundation and secure vertical slice (current)

- [x] Inspect the empty repository; establish a distinct name, architecture, threat model, roadmap, and deployment notes.
- [x] FastAPI + PostgreSQL-ready persistence, registration/login, organizations, owner role, projects, schema-validated JSON collections and CRUD.
- [x] Project/organization membership enforcement and tests for cross-tenant denial.
- [x] Compose local stack, non-root API image, CI lint/test/dependency audit.
- [ ] Replace development `create_all` with versioned Alembic migrations and production migration workflow.
- [ ] Add PostgreSQL-backed CI integration tests and deployment smoke test.

## Milestone 1 — harden identity and tenant boundary

- Email verification, password-reset with single-use expiring tokens, generic auth responses, login/registration rate limits and abuse monitoring.
- Refresh-token rotation/revocation, device/session management, MFA, OAuth/OIDC; secure cookie strategy for first-party dashboard.
- Organization invitations, membership management, auditable role changes; explicit organization/project permission matrix.
- PostgreSQL RLS policies and tests, distinct control-plane/data-plane credentials, API key lifecycle and secret redaction.
- Threat-model review and external security review before public production use.

## Milestone 2 — real database projects and generated APIs

### Data plane MVP (implemented in this release)

- LSM-tree storage engine in Go: write-ahead log, sorted SSTable runs, background flush, compaction that drops obsolete tombstones, crash recovery by WAL replay, served over a length-prefixed JSON TCP protocol.
- Go HTTP data-plane gateway: authenticated record CRUD (GET/PUT/DELETE), collection listing, and prefix scan; every key is namespaced per project and collection so cross-tenant reads are impossible at the storage layer.
- Docker Compose: `storage` and `gateway` services run beside PostgreSQL and the control-plane API.
- CI: Go build, vet, and unit tests on every push and pull request.

Remaining milestone work:

- Provision isolated PostgreSQL database/schema and least-privilege credentials per project; connection pooling and quotas.
- Migration DSL/CLI, schema introspection, relational tables, constraints, indexes, transactions, SQL editor with query timeout/cancel, safe export.
- REST endpoints from schema with OpenAPI, filtering/sorting/pagination and RLS; disable-public-API control. API contract tests.
- GraphQL only after authorization model parity and resource-cost limits.

## Milestone 3 — operational correctness

- Versioned migrations/rollbacks, structured logs, OpenTelemetry traces/metrics, readiness checks, alerting, audit log retention.
- Encrypted scheduled backups separate from database infrastructure; integrity checks and documented, regularly exercised restore; measured RPO/RTO.
- Quotas and usage metering based on actual measured work; no fabricated metrics or payment states.
- PostgreSQL load/soak tests, migration-under-load tests, failure injection and incident response runbooks.

## Milestone 4 — managed services

- Realtime authorization and event delivery via logical replication/outbox; bounded queues, reconnect, backpressure, replay contract.
- S3-compatible storage with private-by-default buckets, signed URL expiry, quotas, malware scanning integration.
- Function deployment to isolated worker/sandbox with CPU/memory/time/concurrency limits; never evaluate user code in API process.
- JavaScript/TypeScript and Python SDKs plus CLI, each tested against actual APIs.

## Milestone 5 — scale and enterprise capabilities

- Read replicas, regional placement, failover, sharded control/data plane only when benchmarks show need.
- KMS-backed encryption/key rotation, SSO/SCIM, compliance evidence, policy controls, data residency and auditable administrative access.
- Billing/payment provider integration only after metering correctness, server-side signed webhook validation and idempotency.
- Vector search using PostgreSQL extensions, permission-preserving semantic retrieval, AI SQL proposal reviewed and run under user's actual DB role.

## Go/no-go gates

Do not claim production readiness until PostgreSQL integration tests, security review, tenant isolation testing, backup restore drills, load/failure testing, operational alerting, migration rollback, incident procedures, and TLS/secrets deployment have been demonstrated. Maintain explicit limits and measured benchmarks for each release.
