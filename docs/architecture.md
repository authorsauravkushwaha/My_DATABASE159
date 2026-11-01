# Kestrel architecture

## Design principle

Keep boundaries understandable, use mature persistence for the control plane, and make experimental pieces explicit. The repository retains Python and Go rather than rewriting storage in TypeScript to match a language-popularity chart.

```text
Browser
  │ same-origin /api/
  ▼
Vue dashboard ─────────────► FastAPI control plane ─► PostgreSQL
                             │ auth + membership
                             │ schemas + JSON CRUD
                             │ Alembic migrations
                             ▲
                             │ caller-token access check
Go gateway ──────────────────┘
  │ private TCP
  ▼
Go LSM engine ─► WAL + SSTables on disk
```

## Control plane

`app/` contains FastAPI routes, SQLAlchemy models, Pydantic validation, and security helpers. PostgreSQL is the deployment database; SQLite is a development/test convenience.

Assets include users, organizations, memberships, projects, collection definitions, and JSON records. Protected operations resolve the user from a signed, expiring bearer token. Project access requires organization membership. Mutating collection/record operations require owner, admin, or editor membership; viewers can read. Project administration requires owner/admin.

There is no client SQL execution. Schema property names are validated and never interpolated into SQL. Records are constrained to a closed, flat schema subset and 64 KiB. Request bodies are capped at 128 KiB, including chunked requests. Pagination is capped at 100 by the API; the dashboard uses 25.

## Storage plane

`engine/` is an experimental LSM key-value store. Writes append a CRC-protected WAL frame and fsync before updating memory. Flushes create an SSTable and fsync its directory before truncating the WAL. Recovery replays complete WAL frames, removes an incomplete tail, and rejects corrupt complete frames. Read I/O failures are surfaced rather than treated as missing records. Compaction merges runs and the memtable.

`gateway/` exposes record CRUD and bounded, cursor-based collection listing. It forwards the caller's bearer token to the control plane for **both authentication and authorization** on every request. It validates returned project/collection identity and enforces the collection schema before writing. The former public raw-prefix scan is removed. Storage and authorization network calls have deadlines; HTTP has server timeouts and graceful shutdown.

Keys are `p:<hex(project)>:c:<hex(collection)>:r:<hex(record)>`. Namespacing prevents accidental key collisions; it is **not** authorization. Membership checks provide the authorization boundary.

### Important separation

Dashboard/SDK records use collection **names** and PostgreSQL. Gateway records use collection **UUIDs** and the Go engine. There is no synchronization or distributed transaction between them. Control-plane deletion does not clean up gateway data. The gateway fails closed when authorization is unavailable, but orphan cleanup and a unified data plane remain future work.

## Workspace and SDK

- `apps/dashboard/`: Vue 3, Vue Router, TypeScript, custom CSS, native dialog elements, and build-time MDX compilation through `@mdx-js/rollup` and `@mdx-js/vue`.
- `packages/client/`: typed native-fetch client with request deadlines, cancellation, encoded path segments, and structured HTTP errors.
- `e2e/`: JavaScript Playwright tests for demo CRUD, real API persistence after re-login, and mobile/MDX navigation.
- `scripts/`: Shell setup/verification and JavaScript manifest/lockfile consistency checks.

Browser tokens and demo data stay in memory. Reloading signs out. The demo implements a separate adapter and is visibly labeled; it is not presented as server persistence. Dashboard JSON export covers the current page and is not a backup.

## Deployment boundaries

Compose runs PostgreSQL, a one-shot Alembic migration, the API, a non-root Nginx dashboard, the engine, and the gateway. Database and engine ports stay private. Nginx and Vite provide same-origin API proxies. Nginx supplies a restrictive CSP; API responses include no-store and anti-sniffing headers.

TLS ingress, secret management, edge rate/body limits, database roles/RLS, observability, backups, and restore exercises are deployment responsibilities still outside this MVP. `/health` checks process liveness; `/ready` checks control-plane database connectivity, not every dependency.

## Deliberate limitations

No SQL editor, relational table provisioning, realtime, object storage, functions, billing, automatic backups, or HA. No claim of independent security review, production load capacity, or measured RPO/RTO. See the [roadmap](roadmap.md) and [threat model](../security/THREAT_MODEL.md).
