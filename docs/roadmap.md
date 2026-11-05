# Roadmap

## Delivered in v0.1 – v0.3

- FastAPI control plane with organization, membership, project, collection, and record APIs.
- Reversible Alembic migrations (`0001` initial control plane, `0002` high-capacity storage, `0003` project API keys).
- Transparent `KZ1` Zlib record payload compression, online secondary B-tree field indexes, predicate-pushdown `/query`, O(1) keyset cursor pagination, atomic `/batch` writes (up to 5,000 records), and constant-memory streaming NDJSON `/import` & `/export`.
- Sharded Go LSM storage engine (`ShardedStore`) with `KSS2` block-compressed SSTables, per-block CRC32 checksums, Bloom filters, shared LRU block cache, directory `flock`, binary batch WAL, and streaming k-way compaction.
- Pooled Go HTTP data gateway supporting 512 KiB records, 5,000-op `/batch`, collection prefix purge, `/compact`, `/v1/stats`, and control-plane orphan cleanup (`STORAGE_ENGINE_ADDR`).
- Dependency-free `@kestrel/client` SDK and Vue 3 dashboard with storage density telemetry, secondary index management, bulk ingestion, server-side query builder, and API-key management.
- **Access lifecycle (part of milestone 2)**: project-scoped API keys with `reader`/`writer` caps, digest-only storage, expiry, immediate revocation, and membership-bound authority.
- **Operations (part of milestone 4)**: Prometheus `/metrics` with bounded label cardinality and per-client fixed-window rate limiting on authentication and global traffic.

## Next Milestones

1. **Multi-node replication**: Add WAL shipping and read-replica followers for the sharded Go LSM storage engine.
2. **Access lifecycle (remainder)**: Add refresh tokens with revocation, organization invitations, organization-level API keys, and audit events.
3. **PostgreSQL Row-Level Security & partitioning**: Pair `shard_key` hash partitioning with native PostgreSQL declarative partitions and optional RLS policies.
4. **Operations (remainder)**: Add OpenTelemetry trace export, automated backup snapshots to S3-compatible object storage, and point-in-time restore drills.
