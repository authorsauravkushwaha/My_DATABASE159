export type Json = null | boolean | number | string | Json[] | { [key: string]: Json };
export type JsonObject = { [key: string]: Json };
export type FieldType = 'string' | 'integer' | 'number' | 'boolean' | 'object' | 'array' | 'null';
export interface CollectionSchema {
  type: 'object';
  properties: Record<string, { type: FieldType }>;
  required: string[];
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
export interface PageOptions { limit?: number; offset?: number; signal?: AbortSignal }
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

/** A small control-plane client. Tokens stay in memory; this package never persists credentials. */
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

  private async request<T>(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
    const headers: Record<string, string> = { Accept: 'application/json' };
    if (body !== undefined) headers['Content-Type'] = 'application/json';
    if (this.token) headers.Authorization = `Bearer ${this.token}`;
    const timeout = AbortSignal.timeout(this.timeoutMs);
    const combined = signal ? AbortSignal.any([signal, timeout]) : timeout;
    let response: Response;
    try {
      response = await this.fetcher(`${this.baseUrl}${path}`, {
        method, headers, credentials: 'omit', signal: combined,
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      });
    } catch (error) {
      if (combined.aborted) throw new Error(signal?.aborted ? 'Request cancelled' : 'Request timed out', { cause: error });
      throw new Error('Cannot reach the API. Check your connection and API proxy.', { cause: error });
    }
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
  setProjectApi(id: string, enabled: boolean) {
    return this.request<Project>('PATCH', `/v1/projects/${encodeURIComponent(id)}`, { api_enabled: enabled });
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
  private pageQuery(options: PageOptions) {
    const query = new URLSearchParams({ limit: String(options.limit ?? 50), offset: String(options.offset ?? 0) });
    return query.toString();
  }
  listRecords(projectId: string, collection: string, options: PageOptions = {}) {
    return this.request<DataRecord[]>('GET', `/v1/projects/${encodeURIComponent(projectId)}/data/${encodeURIComponent(collection)}?${this.pageQuery(options)}`, undefined, options.signal);
  }
  createRecord(projectId: string, collection: string, data: JsonObject) {
    return this.request<DataRecord>('POST', `/v1/projects/${encodeURIComponent(projectId)}/data/${encodeURIComponent(collection)}`, { data });
  }
  updateRecord(projectId: string, collection: string, id: string, data: JsonObject) {
    return this.request<DataRecord>('PATCH', `/v1/projects/${encodeURIComponent(projectId)}/data/${encodeURIComponent(collection)}/${encodeURIComponent(id)}`, { data });
  }
  deleteRecord(projectId: string, collection: string, id: string) {
    return this.request<void>('DELETE', `/v1/projects/${encodeURIComponent(projectId)}/data/${encodeURIComponent(collection)}/${encodeURIComponent(id)}`);
  }
}
