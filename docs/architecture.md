# Architecture

Kestrel separates workspace orchestration, high-density relational/indexed storage, and sharded LSM key-value persistence so each component stays inspectable while scaling to significantly larger datasets per gigabyte of storage.

## Request Paths

```text
Browser -> Vue Dashboard (/apps/dashboard) -> @kestrel/client -> FastAPI Control & High-Capacity Data Plane (/app) -> PostgreSQL 16 / SQLite WAL
                                                                          |
                                                                          +-> (optional STORAGE_ENGINE_ADDR) Orphan prefix purge on collection/project delete

SDK / CLI -> Go Data Gateway (/gateway) -> FastAPI /internal/v1/gateway/authorize
                      |
                      +-> 16-connection TCP pool -> Sharded Go LSM Storage Engine (/engine: KSS2 compressed SSTables + Bloom + LRU cache + WAL)
```

## Components

| Component | Path | Responsibility |
| --- | --- | --- |
| Control & data plane | `app/`, `alembic/` | Users, organizations, memberships, projects, collection schemas (up to 128 fields), transparent `KZ1` Zlib record compression, secondary B-tree field indexes (`record_field_indexes`), 64-bucket hash sharding, batch/NDJSON streaming ingest & export, O(1) keyset cursor pagination, storage telemetry, project-scoped API keys, per-client rate limiting, and Prometheus metrics. |
| Storage engine | `engine/` | Multi-shard Go LSM store (`ShardedStore`) with binary batch CRC32 WAL, directory `flock`, `KSS2` Zlib block-compressed SSTables with per-block CRC32 checksums, 10-bit/key Bloom filters, shared concurrent LRU block cache, and streaming k-way merge iterators/compaction. |
| Data gateway | `gateway/` | HTTP service with a 16-connection pooled TCP client, delegated authorization, single-record CRUD (up to 512 KiB), atomic `/batch` writes (up to 5,000 items / 16 MiB), prefix collection purge, manual compaction, and live engine telemetry (`/v1/stats/{project}/{collection}`). |
| TypeScript client | `packages/client/` | Zero-dependency control/data client supporting single & batch CRUD, B-tree indexed queries, streaming NDJSON import/export, online index management, compaction, and storage stats. |
| Dashboard | `apps/dashboard/` | Vue 3 workspace with live storage density metrics, 128-field schema & secondary index builder, bulk batch/NDJSON ingestion, server-side index-accelerated query bar, keyset cursor pagination, and explicit local demo adapter. |

## Why Kestrel Stores Far Denser Datasets

Traditional BaaS stacks (such as default Supabase JSONB tables with uncompressed row-by-row PostgREST writes and `OFFSET` pagination) encounter heavy TOAST/MVCC bloat, single-row round-trip bottlenecks, and linear deep-page latency as datasets grow. Kestrel addresses all four layers:

1. **Multi-layer Compression (`KZ1` + `KSS2`)**:
   - Control-plane records above 96 bytes are transparently compressed with `KZ1\x00` Zlib framing when smaller than raw UTF-8 JSON, tracking `raw_bytes` and `stored_bytes` per record.
   - Go LSM SSTables group sorted entries into 32 KiB blocks compressed with Zlib (`KSS2`), achieving ~35× disk compression on structured JSON datasets in `engine/scale_test.go`.
2. **Bloom Filters + LRU Block Cache + Multi-Shard Concurrency**:
   - Every SSTable embeds a double-hashed Kirsch-Mitzenmacher Bloom filter (~1% false-positive rate at 10 bits/key) and sparse binary-searchable block index, bypassing disk reads on point misses while caching hot decompressed blocks in a bounded LRU cache across `N` independent FNV-1a shards.
3. **Online Secondary B-Tree Indexes & Predicate Pushdown**:
   - Scalar fields (`string`, `integer`, `number`, `boolean`) are indexed into `record_field_indexes` with `eq`, `gt`, `gte`, `lt`, `lte`, `in`, and `prefix` pushdown, avoiding full-table decompression scans.
4. **High-Throughput Batch & Constant-Memory NDJSON Streaming**:
   - `POST .../batch` and `POST .../import` ingest up to 5,000 validated records per transaction (up to 16 MiB), `GET .../export` streams full collections as NDJSON in 500-row cursor chunks without buffering in RAM, and `after=<id>` cursors keep deep pagination O(1).

## Authentication & Middleware Stack

Two caller credentials reach the same authorization core:

1. **Session JWT** (15 minutes, `type=access`): issued by `/v1/auth/*`, held in browser memory.
2. **Project-scoped API key** (`klr_<prefix>_<secret>`): minted by project admins, stored only as a SHA-256 digest, capped `reader`/`writer`, pinned to one project, expiry-optional, and revocable at any time. Authority is always the intersection of the creator's *current* membership role, the key's project scope, and its cap — organization, project-admin, and key-admin routes refuse keys outright. The Go gateway's delegated `/access` check accepts both credential types without changes.

Every request walks the ASGI stack in this order (outermost first):

```text
SecurityHeadersMiddleware  -> headers on every response, including 413/429 rejections
  MetricsMiddleware        -> request counter + latency histogram per normalized path
    RateLimitMiddleware    -> per-client fixed windows; 429 + Retry-After when exceeded
      RequestBodyLimitMiddleware -> 128 KiB / 1 MiB / 16 MiB tiered streaming caps
        FastAPI application (auth -> membership -> role/cap -> handler)
```

## Current Boundaries

- The dashboard reads/writes the control-plane database (`Record` + `RecordFieldIndex`). The Go gateway reads/writes the sharded Go LSM engine (`engine/`). Deleting a collection or project in the control plane purges matching `<project_id>/<collection>/` keys in the Go engine when `STORAGE_ENGINE_ADDR` is configured, though live dual-write replication between PostgreSQL and the Go engine is intentionally left explicit rather than hidden behind distributed two-phase commit.
- Tokens stay in memory in the browser; refresh-token rotation, MFA, and distributed Raft consensus across multiple storage nodes remain future milestones.
