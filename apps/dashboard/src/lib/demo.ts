import type {
  AuthSession,
  BatchWriteResult,
  Collection,
  CollectionSchema,
  CompactionResult,
  DataRecord,
  IndexResult,
  Json,
  JsonObject,
  PageOptions,
  Project,
  ProjectStorageStats,
  RecordQuery,
  RecordQueryResult,
} from '@kestrel/client';
import { ApiError } from '@kestrel/client';

function estimateStoredBytes(rawBytes: number, text: string): number {
  if (rawBytes < 96) return rawBytes;
  // Measure character/bigram repetition deterministically to approximate Zlib framing without misleading numbers.
  const uniqueBigrams = new Set<string>();
  for (let i = 0; i < text.length - 1; i++) uniqueBigrams.add(text.slice(i, i + 2));
  const entropyFactor = Math.min(0.82, Math.max(0.24, (uniqueBigrams.size * 1.6) / Math.max(1, rawBytes)));
  return Math.max(24, Math.round(rawBytes * entropyFactor));
}

function matchesFilter(record: DataRecord, field: string, op: string, expected: Json): boolean {
  const actual: Json | undefined = field === 'id' ? record.id : record.data[field];
  if (actual === undefined) return false;
  if (op === 'eq') return actual === expected;
  if (op === 'neq') return actual !== expected;
  if (op === 'in') return Array.isArray(expected) && expected.includes(actual);
  if (op === 'contains') return typeof actual === 'string' && typeof expected === 'string' && actual.toLowerCase().includes(expected.toLowerCase());
  if (op === 'prefix') return typeof actual === 'string' && typeof expected === 'string' && actual.startsWith(expected);
  if (typeof actual === 'number' && typeof expected === 'number') {
    if (op === 'gt') return actual > expected;
    if (op === 'gte') return actual >= expected;
    if (op === 'lt') return actual < expected;
    if (op === 'lte') return actual <= expected;
  }
  if (typeof actual === 'string' && typeof expected === 'string') {
    if (op === 'gt') return actual > expected;
    if (op === 'gte') return actual >= expected;
    if (op === 'lt') return actual < expected;
    if (op === 'lte') return actual <= expected;
  }
  return false;
}

/** Explicit, ephemeral demo. No network requests and no claim of server persistence. */
export class DemoApi {
  private projects: Project[] = [
    { id: 'demo-commerce', organization_id: 'demo-org', name: 'Commerce', slug: 'commerce', api_enabled: true, created_at: '2026-10-24T09:00:00Z' },
    { id: 'demo-internal', organization_id: 'demo-org', name: 'Internal tools', slug: 'internal-tools', api_enabled: true, created_at: '2026-10-28T09:00:00Z' },
  ];
  private collections: Collection[] = [
    { id: 'demo-products', project_id: 'demo-commerce', name: 'products', schema: { type: 'object', properties: { name: { type: 'string' }, price: { type: 'number' }, in_stock: { type: 'boolean' } }, required: ['name', 'price'], indexes: ['name', 'price'], additionalProperties: false } },
    { id: 'demo-customers', project_id: 'demo-commerce', name: 'customers', schema: { type: 'object', properties: { name: { type: 'string' }, email: { type: 'string' } }, required: ['name', 'email'], indexes: ['email'], additionalProperties: false } },
    { id: 'demo-tasks', project_id: 'demo-internal', name: 'tasks', schema: { type: 'object', properties: { title: { type: 'string' }, done: { type: 'boolean' } }, required: ['title'], additionalProperties: false } },
  ];
  private records: Record<string, DataRecord[]> = {
    'demo-products': [
      { id: 'prd-001', data: { name: 'Studio headphones', price: 129, in_stock: true }, created_at: '2026-10-28T10:00:00Z', updated_at: '2026-10-28T10:00:00Z' },
      { id: 'prd-002', data: { name: 'Mechanical keyboard', price: 89, in_stock: true }, created_at: '2026-10-28T11:00:00Z', updated_at: '2026-10-28T11:00:00Z' },
      { id: 'prd-003', data: { name: 'Desktop speaker', price: 199, in_stock: false }, created_at: '2026-10-29T09:00:00Z', updated_at: '2026-10-29T09:00:00Z' },
    ],
    'demo-customers': [], 'demo-tasks': [],
  };
  private findProject(id: string) {
    const project = this.projects.find((p) => p.id === id);
    if (!project) throw new ApiError(404, 'Project not found');
    return project;
  }
  private findCollection(project: string, name: string) {
    const p = this.findProject(project);
    if (!p.api_enabled) throw new ApiError(403, 'The project data API is disabled');
    const collection = this.collections.find((c) => c.project_id === project && c.name === name);
    if (!collection) throw new ApiError(404, 'Collection not found');
    return collection;
  }
  async login(_input: { email: string; password: string }): Promise<AuthSession> {
    return { access_token: 'demo-not-a-real-token', token_type: 'bearer', expires_in: 900, user_id: 'demo-user', organization_id: 'demo-org' };
  }
  async listProjects(_organizationId: string, signal?: AbortSignal) { void signal; return structuredClone(this.projects); }
  async createProject(_org: string, input: { name: string; slug: string }) {
    if (this.projects.some((p) => p.slug === input.slug)) throw new ApiError(409, 'This slug is already in use');
    const project: Project = { ...input, id: crypto.randomUUID(), organization_id: 'demo-org', api_enabled: true, created_at: new Date().toISOString() };
    this.projects.push(project);
    return structuredClone(project);
  }
  async getProjectStorage(project: string, signal?: AbortSignal): Promise<ProjectStorageStats> {
    void signal;
    this.findProject(project);
    const colls = this.collections.filter((c) => c.project_id === project);
    let totalRecords = 0;
    let totalCompressed = 0;
    let totalRaw = 0;
    let totalStored = 0;
    let totalIndexes = 0;
    const shardSet = new Set<number>();

    const collectionStats = colls.map((c) => {
      const rows = this.records[c.id] ?? [];
      let raw = 0;
      let stored = 0;
      let compressed = 0;
      for (const r of rows) {
        const serialized = JSON.stringify(r.data);
        const rb = new TextEncoder().encode(serialized).length;
        const sb = estimateStoredBytes(rb, serialized);
        raw += rb;
        stored += sb;
        if (sb < rb) compressed++;
        shardSet.add(r.id.charCodeAt(0) % 64);
      }
      const indexes = c.schema.indexes ?? [];
      totalRecords += rows.length;
      totalCompressed += compressed;
      totalRaw += raw;
      totalStored += stored;
      totalIndexes += indexes.length;
      const ratio = stored > 0 && raw > 0 ? Math.round((raw / stored) * 100) / 100 : 1;
      return {
        id: c.id,
        name: c.name,
        records: rows.length,
        compressed_records: compressed,
        raw_bytes: raw,
        stored_bytes: stored,
        saved_bytes: Math.max(0, raw - stored),
        compression_ratio: ratio,
        indexes: [...indexes],
      };
    });

    const overallRatio = totalStored > 0 && totalRaw > 0 ? Math.round((totalRaw / totalStored) * 100) / 100 : 1;
    return {
      project_id: project,
      total_collections: colls.length,
      total_records: totalRecords,
      compressed_records: totalCompressed,
      raw_bytes: totalRaw,
      stored_bytes: totalStored,
      saved_bytes: Math.max(0, totalRaw - totalStored),
      compression_ratio: overallRatio,
      indexed_fields_count: totalIndexes,
      virtual_shards: 64,
      active_shards: shardSet.size,
      collections: collectionStats,
      engine: null,
    };
  }
  async setProjectApi(id: string, enabled: boolean) {
    const project = this.findProject(id); project.api_enabled = enabled; return structuredClone(project);
  }
  async deleteProject(id: string) {
    this.findProject(id);
    this.projects = this.projects.filter((p) => p.id !== id);
    this.collections = this.collections.filter((c) => c.project_id !== id);
  }
  async listCollections(project: string, signal?: AbortSignal) {
    void signal;
    const p = this.findProject(project);
    if (!p.api_enabled) throw new ApiError(403, 'The project data API is disabled');
    return structuredClone(this.collections.filter((c) => c.project_id === project));
  }
  async createCollection(project: string, input: { name: string; schema: CollectionSchema }) {
    this.findProject(project);
    if (this.collections.some((c) => c.project_id === project && c.name === input.name)) throw new ApiError(409, 'Collection already exists');
    const collection: Collection = { ...input, id: crypto.randomUUID(), project_id: project };
    this.collections.push(collection); this.records[collection.id] = [];
    return structuredClone(collection);
  }
  async deleteCollection(project: string, name: string) {
    const p = this.findProject(project);
    const c = this.collections.find((item) => item.project_id === p.id && item.name === name);
    if (!c) throw new ApiError(404, 'Collection not found');
    this.collections = this.collections.filter((item) => item.id !== c.id); delete this.records[c.id];
  }
  async createIndex(project: string, name: string, field: string): Promise<IndexResult> {
    const c = this.findCollection(project, name);
    const prop = c.schema.properties[field];
    if (!prop) throw new ApiError(422, `Field '${field}' does not exist`);
    if (!['string', 'integer', 'number', 'boolean'].includes(prop.type)) throw new ApiError(422, 'Only scalar fields can be indexed');
    const current = c.schema.indexes ?? [];
    if (current.includes(field)) throw new ApiError(409, `Index on '${field}' already exists`);
    c.schema.indexes = [...current, field].sort();
    return { collection: c.name, field, indexed_entries: (this.records[c.id] ?? []).length, indexes: [...c.schema.indexes] };
  }
  async deleteIndex(project: string, name: string, field: string): Promise<void> {
    const c = this.findCollection(project, name);
    const current = c.schema.indexes ?? [];
    if (!current.includes(field)) throw new ApiError(404, `Index on '${field}' not found`);
    const next = current.filter((f) => f !== field);
    if (next.length) c.schema.indexes = next;
    else delete c.schema.indexes;
  }
  async compactCollection(project: string, name: string): Promise<CompactionResult> {
    const c = this.findCollection(project, name);
    const st = await this.getProjectStorage(project);
    const cs = st.collections.find((x) => x.id === c.id)!;
    return {
      collection: c.name,
      total_records: cs.records,
      recompressed_records: 0,
      raw_bytes: cs.raw_bytes,
      stored_bytes: cs.stored_bytes,
      saved_bytes: cs.saved_bytes,
      compression_ratio: cs.compression_ratio,
    };
  }
  async listRecords(project: string, name: string, options: PageOptions = {}) {
    const c = this.findCollection(project, name);
    const all = this.records[c.id] ?? [];
    if (options.after) {
      const idx = all.findIndex((r) => r.id === options.after);
      if (idx === -1) throw new ApiError(422, 'Invalid pagination cursor');
      return structuredClone(all.slice(idx + 1, idx + 1 + (options.limit ?? 50)));
    }
    return structuredClone(all.slice(options.offset ?? 0, (options.offset ?? 0) + (options.limit ?? 50)));
  }
  async queryRecords(project: string, name: string, query: RecordQuery = {}, signal?: AbortSignal): Promise<RecordQueryResult> {
    void signal;
    const c = this.findCollection(project, name);
    const filters = query.filters ?? [];
    for (const flt of filters) {
      if (flt.field !== 'id' && !(flt.field in c.schema.properties)) throw new ApiError(422, `Unknown filter field: ${flt.field}`);
    }
    const base = [...(this.records[c.id] ?? [])];
    if (query.sort_dir === 'desc') base.reverse();
    let startIdx = 0;
    if (query.after) {
      const found = base.findIndex((r) => r.id === query.after);
      if (found === -1) throw new ApiError(422, 'Invalid pagination cursor');
      startIdx = found + 1;
    }
    const limit = query.limit ?? 100;
    const matched: DataRecord[] = [];
    let scanned = 0;
    for (let i = startIdx; i < base.length; i++) {
      const rec = base[i]!;
      scanned++;
      if (filters.every((f) => matchesFilter(rec, f.field, f.op ?? 'eq', f.value))) {
        matched.push(structuredClone(rec));
        if (matched.length > limit) break;
      }
    }
    let nextCursor: string | null = null;
    if (matched.length > limit) {
      matched.length = limit;
      nextCursor = matched[matched.length - 1]!.id;
    }
    const indexedSet = new Set(c.schema.indexes ?? []);
    const usedIndexes = [...new Set(filters.map((f) => f.field).filter((f) => indexedSet.has(f)))];
    return {
      records: matched,
      next_cursor: nextCursor,
      returned: matched.length,
      scanned,
      index_used: usedIndexes.length > 0,
      indexed_fields_used: usedIndexes,
    };
  }
  private validate(c: Collection, data: JsonObject) {
    for (const field of c.schema.required) if (!(field in data)) throw new ApiError(422, `Missing required field: ${field}`);
    for (const [key, value] of Object.entries(data)) {
      const type = c.schema.properties[key]?.type;
      if (!type) throw new ApiError(422, `Unexpected field: ${key}`);
      const valid = type === 'null' ? value === null : type === 'array' ? Array.isArray(value) : type === 'integer' ? Number.isInteger(value) : type === 'number' ? typeof value === 'number' : type === 'object' ? value !== null && typeof value === 'object' && !Array.isArray(value) : typeof value === type;
      if (!valid) throw new ApiError(422, `${key} must be ${type}`);
    }
  }
  async createRecord(project: string, name: string, data: JsonObject) {
    const c = this.findCollection(project, name); this.validate(c, data);
    const now = new Date().toISOString();
    const record: DataRecord = { id: crypto.randomUUID(), data: structuredClone(data), created_at: now, updated_at: now };
    (this.records[c.id] ??= []).push(record); return structuredClone(record);
  }
  async batchCreateRecords(project: string, name: string, records: JsonObject[], signal?: AbortSignal): Promise<BatchWriteResult> {
    void signal;
    const c = this.findCollection(project, name);
    if (!records.length || records.length > 5000) throw new ApiError(422, 'Batch must contain between 1 and 5,000 records');
    for (const item of records) this.validate(c, item);
    const ids: string[] = [];
    let rawBytes = 0;
    let storedBytes = 0;
    const now = new Date().toISOString();
    const bucket = (this.records[c.id] ??= []);
    for (const item of records) {
      const id = crypto.randomUUID();
      ids.push(id);
      const serialized = JSON.stringify(item);
      const rb = new TextEncoder().encode(serialized).length;
      const sb = estimateStoredBytes(rb, serialized);
      rawBytes += rb;
      storedBytes += sb;
      bucket.push({ id, data: structuredClone(item), created_at: now, updated_at: now });
    }
    const ratio = storedBytes > 0 && rawBytes > 0 ? Math.round((rawBytes / storedBytes) * 100) / 100 : 1;
    return {
      inserted: ids.length,
      ids,
      raw_bytes: rawBytes,
      stored_bytes: storedBytes,
      saved_bytes: Math.max(0, rawBytes - storedBytes),
      compression_ratio: ratio,
    };
  }
  async importRecordsNdjson(project: string, name: string, ndjson: string, signal?: AbortSignal): Promise<BatchWriteResult> {
    const lines = ndjson.split(/\r?\n/).map((l) => l.trim()).filter(Boolean);
    const objects = lines.map((line) => {
      const parsed = JSON.parse(line) as Record<string, unknown>;
      return ('data' in parsed && parsed.data && typeof parsed.data === 'object' && !Array.isArray(parsed.data))
        ? parsed.data as JsonObject
        : parsed as JsonObject;
    });
    return this.batchCreateRecords(project, name, objects, signal);
  }
  async exportRecordsNdjson(project: string, name: string, signal?: AbortSignal): Promise<string> {
    void signal;
    const c = this.findCollection(project, name);
    return (this.records[c.id] ?? []).map((r) => JSON.stringify(r)).join('\n') + ((this.records[c.id]?.length ?? 0) ? '\n' : '');
  }
  async updateRecord(project: string, name: string, id: string, data: JsonObject) {
    const c = this.findCollection(project, name);
    const record = this.records[c.id]?.find((r) => r.id === id);
    if (!record) throw new ApiError(404, 'Record not found');
    const merged = { ...record.data, ...data }; this.validate(c, merged);
    record.data = structuredClone(merged); record.updated_at = new Date().toISOString(); return structuredClone(record);
  }
  async deleteRecord(project: string, name: string, id: string) {
    const c = this.findCollection(project, name); this.records[c.id] = (this.records[c.id] ?? []).filter((r) => r.id !== id);
  }
}

export type WorkspaceApi = Pick<
  DemoApi,
  | 'listProjects'
  | 'createProject'
  | 'getProjectStorage'
  | 'setProjectApi'
  | 'deleteProject'
  | 'listCollections'
  | 'createCollection'
  | 'deleteCollection'
  | 'createIndex'
  | 'deleteIndex'
  | 'compactCollection'
  | 'listRecords'
  | 'queryRecords'
  | 'createRecord'
  | 'batchCreateRecords'
  | 'importRecordsNdjson'
  | 'exportRecordsNdjson'
  | 'updateRecord'
  | 'deleteRecord'
>;
