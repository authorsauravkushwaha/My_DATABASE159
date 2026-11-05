export type Json = null | boolean | number | string | Json[] | { [key: string]: Json };
export type JsonObject = { [key: string]: Json };
export type FieldType = 'string' | 'integer' | 'number' | 'boolean' | 'object' | 'array' | 'null';
export type FilterOperator = 'eq' | 'neq' | 'gt' | 'gte' | 'lt' | 'lte' | 'in' | 'contains' | 'prefix';

export interface CollectionSchema {
  type: 'object';
  properties: Record<string, { type: FieldType }>;
  required: string[];
  indexes?: string[];
  additionalProperties: false;
}
export interface AuthSession {
  access_token: string;
  token_type: 'bearer';
  expires_in: number;
  user_id: string;
  organization_id: string | null;
}
export interface Project {
  id: string;
  organization_id: string;
  name: string;
  slug: string;
  api_enabled: boolean;
  created_at: string;
}
export interface Collection {
  id: string;
  project_id: string;
  name: string;
  schema: CollectionSchema;
}
export interface DataRecord {
  id: string;
  data: JsonObject;
  created_at: string;
  updated_at: string;
}
export interface PageOptions {
  limit?: number | undefined;
  offset?: number | undefined;
  after?: string | undefined;
  signal?: AbortSignal | undefined;
}
export interface QueryFilter {
  field: string;
  op?: FilterOperator | undefined;
  value: Json;
}
export interface RecordQuery {
  filters?: QueryFilter[] | undefined;
  sort_dir?: 'asc' | 'desc' | undefined;
  select?: string[] | undefined;
  limit?: number | undefined;
  after?: string | undefined;
}
export interface RecordQueryResult {
  records: DataRecord[];
  next_cursor: string | null;
  returned: number;
  scanned: number;
  index_used: boolean;
  indexed_fields_used: string[];
}
export interface BatchWriteResult {
  inserted: number;
  ids: string[];
  raw_bytes: number;
  stored_bytes: number;
  saved_bytes: number;
  compression_ratio: number;
}
export interface IndexResult {
  collection: string;
  field: string;
  indexed_entries: number;
  indexes: string[];
}
export interface CompactionResult {
  collection: string;
  total_records: number;
  recompressed_records: number;
  raw_bytes: number;
  stored_bytes: number;
  saved_bytes: number;
  compression_ratio: number;
}
export interface CollectionStorageStats {
  id: string;
  name: string;
  records: number;
  compressed_records: number;
  raw_bytes: number;
  stored_bytes: number;
  saved_bytes: number;
  compression_ratio: number;
  indexes: string[];
}
export interface EngineStorageStats {
  shards?: number;
  sstables?: number;
  memtable_bytes?: number;
  wal_bytes?: number;
  logical_bytes?: number;
  disk_bytes?: number;
  compression_ratio?: number;
  estimated_keys?: number;
}
export type ApiKeyRole = 'reader' | 'writer';
export interface ApiKeySummary {
  id: string;
  project_id: string;
  name: string;
  role: ApiKeyRole;
  key_prefix: string;
  created_by: string;
  created_at: string;
  expires_at: string | null;
  last_used_at: string | null;
  revoked_at: string | null;
}
export interface CreatedApiKey extends ApiKeySummary {
  /** Full secret. Shown once at creation; only a SHA-256 digest is stored server-side. */
  api_key: string;
}
export interface ApiKeyCreateInput {
  name: string;
  role?: ApiKeyRole;
  expires_in_days?: number | null;
}
export interface ProjectStorageStats {
  project_id: string;
  total_collections: number;
  total_records: number;
  compressed_records: number;
  raw_bytes: number;
  stored_bytes: number;
  saved_bytes: number;
  compression_ratio: number;
  indexed_fields_count: number;
  virtual_shards: number;
  active_shards: number;
  collections: CollectionStorageStats[];
  engine?: EngineStorageStats | null;
}
export interface ClientOptions {
  baseUrl?: string;
  token?: string;
  fetch?: typeof globalThis.fetch;
  timeoutMs?: number;
}

/** An HTTP failure. The message contains server detail, never the bearer token. */
export class ApiError extends Error {
  constructor(public readonly status: number, message: string, public readonly details?: Json) {
    super(message);
    this.name = 'ApiError';
  }
}

function detailMessage(body: Json | undefined, status: number): string {
  if (body && typeof body === 'object' && !Array.isArray(body)) {
    const detail = body.detail ?? body.error;
    if (typeof detail === 'string') return detail;
    if (Array.isArray(detail)) {
      const messages = detail.map((item) =>
        item && typeof item === 'object' && !Array.isArray(item) && typeof item.msg === 'string'
          ? item.msg : 'Invalid request payload',
      );
      if (messages.length) return messages.join('; ');
    }
  }
  return `Request failed (${status})`;
}

/** A dependency-free control-plane and high-capacity data client. Tokens stay in memory. */
export class KestrelClient {
  private readonly baseUrl: string;
  private readonly fetcher: typeof globalThis.fetch;
  private readonly timeoutMs: number;
  private token: string | undefined;

  constructor(options: ClientOptions = {}) {
    const url = new URL(options.baseUrl ?? '/api/', globalThis.location?.href ?? 'http://localhost');
    if (!['http:', 'https:'].includes(url.protocol)) throw new Error('Only HTTP(S) API URLs are supported');
    if (url.username || url.password || url.search || url.hash) throw new Error('API URL must not contain credentials, query, or fragment');
    // Preserve relative paths: resolving them for validation must not turn the
    // default into an absolute localhost URL in non-browser environments.
    this.baseUrl = !options.baseUrl || options.baseUrl.startsWith('/')
      ? url.pathname.replace(/\/+$/, '')
      : url.href.replace(/\/+$/, '');
    this.fetcher = options.fetch ?? globalThis.fetch.bind(globalThis);
    this.timeoutMs = options.timeoutMs ?? 15_000;
    if (!Number.isFinite(this.timeoutMs) || this.timeoutMs <= 0) throw new Error('timeoutMs must be positive');
    this.token = options.token;
  }

  setToken(token: string | undefined): void { this.token = token; }
  clearToken(): void { this.token = undefined; }

  private async rawRequest(
    method: string,
    path: string,
    options: {
      body?: string | undefined;
      contentType?: string | undefined;
      accept?: string | undefined;
      signal?: AbortSignal | undefined;
    } = {},
  ): Promise<Response> {
    const headers: Record<string, string> = { Accept: options.accept ?? 'application/json' };
    if (options.contentType) headers['Content-Type'] = options.contentType;
    if (this.token) headers.Authorization = `Bearer ${this.token}`;
    const timeout = AbortSignal.timeout(this.timeoutMs);
    const combined = options.signal ? AbortSignal.any([options.signal, timeout]) : timeout;
    try {
      return await this.fetcher(`${this.baseUrl}${path}`, {
        method,
        headers,
        credentials: 'omit',
        signal: combined,
        ...(options.body === undefined ? {} : { body: options.body }),
      });
    } catch (error) {
      if (combined.aborted) throw new Error(options.signal?.aborted ? 'Request cancelled' : 'Request timed out', { cause: error });
      throw new Error('Cannot reach the API. Check your connection and API proxy.', { cause: error });
    }
  }

  private async request<T>(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
    const response = await this.rawRequest(method, path, {
      ...(body === undefined ? {} : { body: JSON.stringify(body), contentType: 'application/json' }),
      signal,
    });
    if (response.status === 204) return undefined as T;
    let result: Json | undefined;
    try { result = await response.json() as Json; } catch { /* Proxies may return an empty or HTML error. */ }
    if (!response.ok) throw new ApiError(response.status, detailMessage(result, response.status), result);
    if (result === undefined) throw new ApiError(response.status, 'The API returned an invalid JSON response');
    return result as T;
  }

  register(input: { email: string; password: string; organization_name: string }) {
    return this.request<AuthSession>('POST', '/v1/auth/register', input);
  }
  login(input: { email: string; password: string }) {
    return this.request<AuthSession>('POST', '/v1/auth/login', input);
  }
  health(signal?: AbortSignal) {
    return this.request<{ status: string; service: string }>('GET', '/health', undefined, signal);
  }
  ready(signal?: AbortSignal) {
    return this.request<{ status: string; database: string }>('GET', '/ready', undefined, signal);
  }
  listProjects(organizationId: string, signal?: AbortSignal) {
    return this.request<Project[]>('GET', `/v1/organizations/${encodeURIComponent(organizationId)}/projects`, undefined, signal);
  }
  createProject(organizationId: string, input: { name: string; slug: string }) {
    return this.request<Project>('POST', `/v1/organizations/${encodeURIComponent(organizationId)}/projects`, input);
  }
  getProject(id: string, signal?: AbortSignal) {
    return this.request<Project>('GET', `/v1/projects/${encodeURIComponent(id)}`, undefined, signal);
  }
  getProjectStorage(id: string, signal?: AbortSignal) {
    return this.request<ProjectStorageStats>('GET', `/v1/projects/${encodeURIComponent(id)}/storage`, undefined, signal);
  }
  setProjectApi(id: string, enabled: boolean) {
    return this.request<Project>('PATCH', `/v1/projects/${encodeURIComponent(id)}`, { api_enabled: enabled });
  }
  listApiKeys(projectId: string, signal?: AbortSignal) {
    return this.request<ApiKeySummary[]>('GET', `/v1/projects/${encodeURIComponent(projectId)}/api-keys`, undefined, signal);
  }
  createApiKey(projectId: string, input: ApiKeyCreateInput) {
    return this.request<CreatedApiKey>('POST', `/v1/projects/${encodeURIComponent(projectId)}/api-keys`, input);
  }
  revokeApiKey(projectId: string, keyId: string) {
    return this.request<void>('DELETE', `/v1/projects/${encodeURIComponent(projectId)}/api-keys/${encodeURIComponent(keyId)}`);
  }
  deleteProject(id: string) {
    return this.request<void>('DELETE', `/v1/projects/${encodeURIComponent(id)}`);
  }
  listCollections(projectId: string, signal?: AbortSignal) {
    return this.request<Collection[]>('GET', `/v1/projects/${encodeURIComponent(projectId)}/collections`, undefined, signal);
  }
  createCollection(projectId: string, input: { name: string; schema: CollectionSchema }) {
    return this.request<Collection>('POST', `/v1/projects/${encodeURIComponent(projectId)}/collections`, input);
  }
  deleteCollection(projectId: string, name: string) {
    return this.request<void>('DELETE', `/v1/projects/${encodeURIComponent(projectId)}/collections/${encodeURIComponent(name)}`);
  }
  createIndex(projectId: string, collection: string, field: string) {
    return this.request<IndexResult>(
      'POST',
      `/v1/projects/${encodeURIComponent(projectId)}/collections/${encodeURIComponent(collection)}/indexes`,
      { field },
    );
  }
  deleteIndex(projectId: string, collection: string, field: string) {
    return this.request<void>(
      'DELETE',
      `/v1/projects/${encodeURIComponent(projectId)}/collections/${encodeURIComponent(collection)}/indexes/${encodeURIComponent(field)}`,
    );
  }
  compactCollection(projectId: string, collection: string) {
    return this.request<CompactionResult>(
      'POST',
      `/v1/projects/${encodeURIComponent(projectId)}/collections/${encodeURIComponent(collection)}/compact`,
    );
  }
  private pageQuery(options: PageOptions) {
    const query = new URLSearchParams({ limit: String(options.limit ?? 50), offset: String(options.offset ?? 0) });
    if (options.after) query.set('after', options.after);
    return query.toString();
  }
  listRecords(projectId: string, collection: string, options: PageOptions = {}) {
    return this.request<DataRecord[]>(
      'GET',
      `/v1/projects/${encodeURIComponent(projectId)}/data/${encodeURIComponent(collection)}?${this.pageQuery(options)}`,
      undefined,
      options.signal,
    );
  }
  queryRecords(projectId: string, collection: string, query: RecordQuery = {}, signal?: AbortSignal) {
    return this.request<RecordQueryResult>(
      'POST',
      `/v1/projects/${encodeURIComponent(projectId)}/data/${encodeURIComponent(collection)}/query`,
      query,
      signal,
    );
  }
  createRecord(projectId: string, collection: string, data: JsonObject) {
    return this.request<DataRecord>('POST', `/v1/projects/${encodeURIComponent(projectId)}/data/${encodeURIComponent(collection)}`, { data });
  }
  batchCreateRecords(projectId: string, collection: string, records: JsonObject[], signal?: AbortSignal) {
    return this.request<BatchWriteResult>(
      'POST',
      `/v1/projects/${encodeURIComponent(projectId)}/data/${encodeURIComponent(collection)}/batch`,
      { records },
      signal,
    );
  }
  async importRecordsNdjson(projectId: string, collection: string, ndjson: string, signal?: AbortSignal) {
    const response = await this.rawRequest(
      'POST',
      `/v1/projects/${encodeURIComponent(projectId)}/data/${encodeURIComponent(collection)}/import`,
      { body: ndjson, contentType: 'application/x-ndjson', signal },
    );
    let result: Json | undefined;
    try { result = await response.json() as Json; } catch { /* ignore */ }
    if (!response.ok) throw new ApiError(response.status, detailMessage(result, response.status), result);
    if (result === undefined) throw new ApiError(response.status, 'The API returned an invalid JSON response');
    return result as unknown as BatchWriteResult;
  }
  async exportRecordsNdjson(projectId: string, collection: string, signal?: AbortSignal): Promise<string> {
    const response = await this.rawRequest(
      'GET',
      `/v1/projects/${encodeURIComponent(projectId)}/data/${encodeURIComponent(collection)}/export`,
      { accept: 'application/x-ndjson', signal },
    );
    if (!response.ok) {
      let result: Json | undefined;
      try { result = await response.json() as Json; } catch { /* ignore */ }
      throw new ApiError(response.status, detailMessage(result, response.status), result);
    }
    return response.text();
  }
  updateRecord(projectId: string, collection: string, id: string, data: JsonObject) {
    return this.request<DataRecord>('PATCH', `/v1/projects/${encodeURIComponent(projectId)}/data/${encodeURIComponent(collection)}/${encodeURIComponent(id)}`, { data });
  }
  deleteRecord(projectId: string, collection: string, id: string) {
    return this.request<void>('DELETE', `/v1/projects/${encodeURIComponent(projectId)}/data/${encodeURIComponent(collection)}/${encodeURIComponent(id)}`);
  }
}
