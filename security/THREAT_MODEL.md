# Threat Model

## Scope

This threat model covers the repository as shipped: the FastAPI control & data plane (`app/`), Alembic schema management (`alembic/`), the sharded Go LSM storage engine (`engine/`), the Go HTTP data gateway (`gateway/`), the `@kestrel/client` TypeScript package (`packages/client/`), and the Vue dashboard (`apps/dashboard/`).

## Assets

- User password hashes and JWT signing secret (`JWT_SECRET`).
- Project API key digests (`api_keys.key_hash`) and their reader/writer scope.
- Organization membership boundaries, project settings, collection schemas, secondary B-tree index entries, and compressed records.
- Sharded Go storage engine WAL segments, `KSS2` compressed SSTables, and directory locks on disk.
- Browser session state in the dashboard.

## Trust Boundaries

1. **Public caller to NGINX / FastAPI**: Untrusted input, credentials, JSON schemas, single records, batch arrays, and NDJSON streams.
2. **Browser dashboard to FastAPI**: Same-origin `/api/` proxy in Docker and Vite development; tokens are held in JavaScript memory only.
3. **Public caller to Go gateway**: Untrusted bearer token, collection path, single record, or batch payload; gateway delegates authorization to FastAPI before touching the storage engine.
4. **Go gateway / FastAPI cleanup client to Go storage engine**: Internal TCP wire protocol (`[u32 length <= 32 MiB][JSON frame]`); must never be exposed directly to untrusted networks.

## Mitigations Implemented

- Salted PBKDF2-HMAC-SHA256 (`600,000` iterations) password hashing with constant-time digest comparison.
- HS256 JWT validation requiring `sub`, `exp`, `iat`, and `type=access`, with non-placeholder secret enforcement in production.
- Project-scoped API keys (`klr_…`): random 176-bit secrets stored only as SHA-256 digests (pre-image resistant against database theft because the secret is uniformly random), constant-time digest comparison, immediate revocation via kept tombstones, optional expiry, a 50-active-key-per-project cap, and a public-prefix index that never stores or returns the secret twice. Effective authority is always intersected with the creator's *current* membership: revoking the membership or demoting the user instantly constrains every key they minted. Keys are pinned to one project (cross-project access returns `404`, preserving tenant non-enumeration) and are refused on organization, project-administration, and key-administration routes.
- Organization membership checks on every project, collection, index, storage-stats, query, export, and record route; `owner`/`admin` write-role checks on all mutating routes, with the same checks applied to API-key callers plus their reader/writer cap.
- Per-client fixed-window rate limiting in-process (`RateLimitMiddleware`): a strict `/v1/auth/*` window against credential stuffing, a generous global window against floods, `Retry-After` + `X-RateLimit-*` headers, memory-bounded bucket tracking (10,000 clients), and opt-in `X-Forwarded-For` keying only when `RATE_LIMIT_TRUST_PROXY` is set explicitly.
- Route-tiered streaming request body size enforcement (`RequestBodyLimitMiddleware`): 128 KiB default on auth/control-plane routes (preventing oversized PBKDF2/JSON abuse), 1 MiB on single-record writes, and 16 MiB on `/batch` and `/import` bulk data routes.
- Closed collection schemas (`additionalProperties: false`, max 128 fields, max 16 scalar secondary indexes), 512 KiB per-record serialized payload bound, and 5,000-record per-batch cap.
- Pure ASGI security-header middleware wrapping every response — including rate-limit (`429`) and body-limit (`413`) rejections — plus constant-memory NDJSON export streaming (`session.expunge()` per 500-row keyset cursor batch) to prevent OOM on large exports.
- Optional `METRICS_TOKEN` bearer gate for `GET /metrics`; metric label paths are normalized (UUIDs and collection names folded) and hard-capped at 200 patterns per process so scraping cannot be weaponized for memory growth.
- Per-frame CRC32 verification on both WAL segments and `KSS2` SSTable blocks, plus exclusive `flock` (`LOCK`) on every engine shard directory and per-connection TCP deadlines (`30s`) on the storage server.

## Residual Risks

- The in-process rate limiter and metrics registry are per worker process: multi-replica deployments need a shared edge limiter and per-instance scrape targets; the defaults are intentionally generous and IP-based, not account-aware.
- API keys are bearer credentials: they cannot be scoped to source IP or rotated automatically, and a leaked writer key grants data-plane writes until revoked or its creator loses membership. Prefer short expiry and one key per workload.
- Access tokens cannot be individually revoked before their 15-minute expiry unless `JWT_SECRET` is rotated.
- `GET /metrics` is unauthenticated unless `METRICS_TOKEN` is set; it exposes request counts and latency by normalized path, which can reveal traffic shape but never payloads or credentials.
- Encrypted disks, TLS termination, and scheduled off-host backups must be supplied by the deployment environment.
