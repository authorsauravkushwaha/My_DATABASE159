# Threat Model

## Scope

This threat model covers the repository as shipped: the FastAPI control & data plane (`app/`), Alembic schema management (`alembic/`), the sharded Go LSM storage engine (`engine/`), the Go HTTP data gateway (`gateway/`), the `@kestrel/client` TypeScript package (`packages/client/`), and the Vue dashboard (`apps/dashboard/`).

## Assets

- User password hashes and JWT signing secret (`JWT_SECRET`).
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
- Organization membership checks on every project, collection, index, storage-stats, query, export, and record route; `owner`/`admin` write-role checks on all mutating routes.
- Route-tiered streaming request body size enforcement (`RequestBodyLimitMiddleware`): 128 KiB default on auth/control-plane routes (preventing oversized PBKDF2/JSON abuse), 1 MiB on single-record writes, and 16 MiB on `/batch` and `/import` bulk data routes.
- Closed collection schemas (`additionalProperties: false`, max 128 fields, max 16 scalar secondary indexes), 512 KiB per-record serialized payload bound, and 5,000-record per-batch cap.
- Pure ASGI security-header middleware + constant-memory NDJSON export streaming (`session.expunge()` per 500-row keyset cursor batch) to prevent OOM on large exports.
- Per-frame CRC32 verification on both WAL segments and `KSS2` SSTable blocks, plus exclusive `flock` (`LOCK`) on every engine shard directory and per-connection TCP deadlines (`30s`) on the storage server.

## Residual Risks

- Rate limiting is not enforced in-application; deploy behind an edge proxy with per-IP and per-account rate limits on `/v1/auth/*` and bulk `/batch` + `/import` routes.
- Access tokens cannot be individually revoked before their 15-minute expiry unless `JWT_SECRET` is rotated.
- Encrypted disks, TLS termination, and scheduled off-host backups must be supplied by the deployment environment.
