# Roadmap

## Delivered in v0.1 – v0.2

- FastAPI control plane with organization, membership, project, collection, and record APIs.
- Reversible Alembic migrations (`0001` initial control plane + `0002` high-capacity storage, `shard_key`, compression telemetry, and `record_field_indexes`).
- Transparent `KZ1` Zlib record payload compression, online secondary B-tree field indexes, predicate-pushdown `/query`, O(1) keyset cursor pagination, atomic `/batch` writes (up to 5,000 records), and constant-memory streaming NDJSON `/import` & `/export`.
- Sharded Go LSM storage engine (`ShardedStore`) with `KSS2` block-compressed SSTables, per-block CRC32 checksums, Bloom filters, shared LRU block cache, directory `flock`, binary batch WAL, and streaming k-way compaction.
- Pooled Go HTTP data gateway supporting 512 KiB records, 5,000-op `/batch`, collection prefix purge, `/compact`, `/v1/stats`, and control-plane orphan cleanup (`STORAGE_ENGINE_ADDR`).
- Dependency-free `@kestrel/client` SDK and Vue 3 dashboard with storage density telemetry, secondary index management, bulk ingestion, and server-side query builder.

## Next Milestones

1. **Multi-node replication**: Add WAL shipping and read-replica followers for the sharded Go LSM storage engine.
2. **Access lifecycle**: Add refresh tokens with revocation, organization invitations, API keys with scoped permissions, and audit events.
3. **PostgreSQL Row-Level Security & partitioning**: Pair `shard_key` hash partitioning with native PostgreSQL declarative partitions and optional RLS policies.
4. **Operations**: Add Prometheus/OpenTelemetry metrics exporters, automated backup snapshots to S3-compatible object storage, and point-in-time restore drills.
