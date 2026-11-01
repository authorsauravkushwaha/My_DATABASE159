import type { AuthSession, Collection, CollectionSchema, DataRecord, JsonObject, PageOptions, Project } from '@kestrel/client';
import { ApiError } from '@kestrel/client';

/** Explicit, ephemeral demo. No network requests and no claim of server persistence. */
export class DemoApi {
  private projects: Project[] = [
    { id: 'demo-commerce', organization_id: 'demo-org', name: 'Commerce', slug: 'commerce', api_enabled: true, created_at: '2026-10-24T09:00:00Z' },
    { id: 'demo-internal', organization_id: 'demo-org', name: 'Internal tools', slug: 'internal-tools', api_enabled: true, created_at: '2026-10-28T09:00:00Z' },
  ];
  private collections: Collection[] = [
    { id: 'demo-products', project_id: 'demo-commerce', name: 'products', schema: { type: 'object', properties: { name: { type: 'string' }, price: { type: 'number' }, in_stock: { type: 'boolean' } }, required: ['name', 'price'], additionalProperties: false } },
    { id: 'demo-customers', project_id: 'demo-commerce', name: 'customers', schema: { type: 'object', properties: { name: { type: 'string' }, email: { type: 'string' } }, required: ['name', 'email'], additionalProperties: false } },
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
    this.findProject(project);
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
  async setProjectApi(id: string, enabled: boolean) {
    const project = this.findProject(id); project.api_enabled = enabled; return structuredClone(project);
  }
  async deleteProject(id: string) {
    this.findProject(id);
    this.projects = this.projects.filter((p) => p.id !== id);
    this.collections = this.collections.filter((c) => c.project_id !== id);
  }
  async listCollections(project: string, signal?: AbortSignal) { void signal;
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
    const c = this.findCollection(project, name);
    this.collections = this.collections.filter((item) => item.id !== c.id); delete this.records[c.id];
  }
  async listRecords(project: string, name: string, options: PageOptions = {}) {
    const c = this.findCollection(project, name);
    return structuredClone((this.records[c.id] ?? []).slice(options.offset ?? 0, (options.offset ?? 0) + (options.limit ?? 50)));
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
export type WorkspaceApi = Pick<DemoApi, 'listProjects' | 'createProject' | 'setProjectApi' | 'deleteProject' | 'listCollections' | 'createCollection' | 'deleteCollection' | 'listRecords' | 'createRecord' | 'updateRecord' | 'deleteRecord'>;
