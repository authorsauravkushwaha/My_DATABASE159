# Changelog

All notable changes to this project will be documented in this file.

## [0.3.0] - 2026-11-04

### Added

- **Project-scoped API keys (`klr_…`)**: Mint `reader`/`writer` credentials from `POST /v1/projects/{id}/api-keys` (reversible Alembic migration `0003_project_api_keys`). Only a SHA-256 digest is stored; the full secret is returned exactly once. Keys are pinned to a single project, can never exceed their creator's live membership role, refuse project/organization/key administration, support optional expiry (1–3650 days), are revoked idempotently with kept tombstones, and are capped at 50 active keys per project. The Go gateway's delegated `/access` authorization accepts them transparently.
- **In-app fixed-window rate limiting** (`RateLimitMiddleware`): a strict per-IP window on `/v1/auth/*` (20/min default) plus a generous global window (1200/min default), `X-RateLimit-*`/`Retry-After` headers, `RATE_LIMIT_*` env tuning, optional `RATE_LIMIT_TRUST_PROXY` for proxied deployments, and `/health` + `/ready` exemption.
- **Prometheus metrics**: dependency-free `GET /metrics` exposing `kestrel_http_requests_total`, `kestrel_http_request_duration_seconds` histograms, and `kestrel_build_info{version}`, with UUID/collection-name path normalization and a hard cardinality cap (overflow folds into `other`). Optional `METRICS_TOKEN` bearer gating.
- **API-key management in the dashboard** (Settings → API keys): create with name/role/expiry, one-time secret dialog with copy button, list with role/status/last-used, and confirm-to-revoke — plus matching `listApiKeys`/`createApiKey`/`revokeApiKey` methods in `@kestrel/client` and the demo adapter.
- **OpenAPI polish**: MIT license metadata, per-tag descriptions for `/docs`, and version label on metrics.
- **Repository hygiene**: GitHub issue/PR templates, Dependabot configuration (npm, pip, GitHub Actions, Docker), README badges and architecture diagram.

### Fixed

- Security headers now wrap request-body-limit rejections: the middleware stack order previously emitted `413` responses from `RequestBodyLimitMiddleware` without `X-Content-Type-Options`, `X-Frame-Options`, `Referrer-Policy`, and `Cache-Control`.
- README gateway health URL corrected from `:8080` to the compose-published `:8100`.

## [0.2.0] - 2026-11-01

### Added

- **High-density record compression (`KZ1`)**: Transparent Zlib payload compression in the Python data plane with per-record `raw_bytes`, `stored_bytes`, `is_compressed`, and 64-bucket `shard_key` tracking (`alembic` migration `0002_high_capacity_storage`).
- **Online secondary B-tree field indexes & query engine**: Create/drop scalar field indexes (`POST/DELETE .../collections/{name}/indexes`), automatic index maintenance on single & batch writes, and `POST .../data/{name}/query` with SQL subquery pushdown (`eq`, `gt`, `gte`, `lt`, `lte`, `in`, `prefix`), field projection, and O(1) keyset cursor pagination.
- **High-throughput bulk ingestion & constant-memory streaming**: `POST .../batch` (up to 5,000 records / 16 MiB), `POST .../import` (`application/x-ndjson`), `GET .../export` (streaming NDJSON in constant memory), `POST .../compact`, and `GET /v1/projects/{id}/storage`.
- **Upgraded sharded Go LSM storage engine (`engine/`)**: `KSS2` Zlib block-compressed SSTables (~35× compression on structured JSON benchmarks) with per-block CRC32 checksums, sparse binary-searchable block indexes, 10-bit/key Bloom filters, shared concurrent LRU block cache, directory `flock`, binary batch WAL commits, streaming k-way merge iterators/compaction, and `ShardedStore` (`--shards`, `--memtable-mb`, `--cache-mb`).
- **Upgraded Go HTTP gateway (`gateway/`)**: 16-connection TCP pool with stale-socket reconnect, 512 KiB single-record limit, 5,000-op `/batch` endpoint, `DELETE` collection prefix purge, `/compact`, `/v1/stats/{project}/{collection}`, and automatic control-plane orphan cleanup via `STORAGE_ENGINE_ADDR`.
- **SDK & Vue dashboard upgrades**: Extended `@kestrel/client` and `apps/dashboard` with live storage efficiency telemetry, 128-field schemas, secondary B-tree index toggles, bulk batch/NDJSON ingestion, synthetic dataset generator, and server-side indexed query bar.

## [0.1.0] - 2026-10-30

### Added

- FastAPI control plane for user registration, login, organizations, projects, collections, and schema-validated records.
- Reversible initial Alembic migration and migration parity test.
- Go LSM storage engine with CRC-framed WAL, torn-tail recovery, SSTable flush, startup recovery, and compaction.
- Go HTTP data gateway with FastAPI-delegated project, role, and collection authorization.
- `@kestrel/client` TypeScript package and Vue 3 + Vite dashboard with explicit local demo mode.
