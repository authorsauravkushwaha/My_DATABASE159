import { describe, expect, it, vi } from 'vitest';
import { ApiError, KestrelClient } from '../src/index.ts';

function response(body: unknown, status = 200) {
  return new Response(status === 204 ? null : JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}
const schema = { type: 'object' as const, properties: { title: { type: 'string' as const } }, required: ['title'], additionalProperties: false as const };

describe('KestrelClient', () => {
  it('uses same-origin API and never stores tokens', async () => {
    const fetch = vi.fn(async () => response([]));
    const client = new KestrelClient({ baseUrl: '/api/', fetch, token: 'secret' });
    await client.listProjects('org');
    expect(fetch).toHaveBeenCalledWith('/api/v1/organizations/org/projects', expect.objectContaining({ credentials: 'omit', headers: expect.objectContaining({ Authorization: 'Bearer secret' }) }));
    client.clearToken();
    await client.listProjects('org');
    expect(fetch.mock.calls[1]?.[1]?.headers).not.toHaveProperty('Authorization');
  });
  it('defaults to a relative API path and supports explicit absolute origins', async () => {
    const fetch = vi.fn(async () => response({ status: 'ok', service: 'kestrel' }));
    await new KestrelClient({ fetch }).health();
    expect(fetch.mock.calls[0]?.[0]).toBe('/api/health');
    await new KestrelClient({ fetch, baseUrl: 'https://api.example.com/control/' }).health();
    expect(fetch.mock.calls[1]?.[0]).toBe('https://api.example.com/control/health');
  });
  it('encodes every path segment and pagination', async () => {
    const fetch = vi.fn(async () => response([]));
    await new KestrelClient({ baseUrl: '/api/', fetch }).listRecords('p/x', 'notes ?#', { limit: 10, offset: 20 });
    expect(fetch.mock.calls[0]?.[0]).toBe('/api/v1/projects/p%2Fx/data/notes%20%3F%23?limit=10&offset=20');
  });
  it('creates validated collections and records', async () => {
    const fetch = vi.fn(async () => response({ id: 'r1', data: { title: 'Hello' } }, 201));
    const client = new KestrelClient({ fetch });
    await client.createCollection('p1', { name: 'notes', schema });
    expect(JSON.parse(String(fetch.mock.calls[0]?.[1]?.body))).toEqual({ name: 'notes', schema });
    await client.createRecord('p1', 'notes', { title: 'Hello' });
    expect(fetch.mock.calls[1]?.[1]?.method).toBe('POST');
    expect(JSON.parse(String(fetch.mock.calls[1]?.[1]?.body))).toEqual({ data: { title: 'Hello' } });
  });
  it('surfaces validation detail without leaking request headers', async () => {
    const fetch = vi.fn(async () => response({ detail: 'Missing required fields: title' }, 422));
    await expect(new KestrelClient({ fetch, token: 'secret' }).createRecord('p', 'c', {})).rejects.toEqual(new ApiError(422, 'Missing required fields: title', { detail: 'Missing required fields: title' }));
  });
  it('formats FastAPI validation arrays', async () => {
    const fetch = vi.fn(async () => response({ detail: [{ msg: 'Invalid email' }] }, 422));
    await expect(new KestrelClient({ fetch }).login({ email: 'bad', password: 'x' })).rejects.toThrow('Invalid email');
  });
  it('handles delete without parsing a body', async () => {
    const fetch = vi.fn(async () => response(null, 204));
    await expect(new KestrelClient({ fetch }).deleteRecord('p', 'c', 'r')).resolves.toBeUndefined();
  });
  it('reports proxy failures and timeouts distinctly', async () => {
    await expect(new KestrelClient({ fetch: vi.fn(async () => response({ error: 'Bad gateway' }, 502)) }).health()).rejects.toThrow('Bad gateway');
    const fetch = vi.fn((_url: string | URL | Request, init?: RequestInit) => new Promise<Response>((_resolve, reject) => {
      init?.signal?.addEventListener('abort', () => reject(new Error('aborted')));
    }));
    await expect(new KestrelClient({ fetch, timeoutMs: 10 }).health()).rejects.toThrow('Request timed out');
  });
  it('rejects insecure and credential-bearing base URLs', () => {
    expect(() => new KestrelClient({ baseUrl: 'file:///tmp/test' })).toThrow('HTTP(S)');
    expect(() => new KestrelClient({ baseUrl: 'https://user:pass@example.com' })).toThrow('credentials');
  });
  it('supports batch ingestion, indexed queries, storage telemetry, and NDJSON export/import', async () => {
    const fetch = vi.fn(async (url: string) => {
      if (url.endsWith('/export')) {
        return new Response('{"id":"r1","data":{"title":"One"}}\n', { status: 200, headers: { 'Content-Type': 'application/x-ndjson' } });
      }
      return response({ inserted: 2, ids: ['r1', 'r2'], raw_bytes: 200, stored_bytes: 80, saved_bytes: 120, compression_ratio: 2.5 }, 201);
    });
    const client = new KestrelClient({ fetch, token: 'tok' });
    const batchRes = await client.batchCreateRecords('p1', 'notes', [{ title: 'One' }, { title: 'Two' }]);
    expect(batchRes.inserted).toBe(2);
    expect(fetch.mock.calls[0]?.[0]).toBe('/api/v1/projects/p1/data/notes/batch');

    await client.importRecordsNdjson('p1', 'notes', '{"title":"One"}\n{"title":"Two"}');
    expect(fetch.mock.calls[1]?.[0]).toBe('/api/v1/projects/p1/data/notes/import');

    const ndjson = await client.exportRecordsNdjson('p1', 'notes');
    expect(ndjson).toContain('"title":"One"');

    await client.queryRecords('p1', 'notes', { filters: [{ field: 'title', op: 'prefix', value: 'O' }], limit: 50, after: 'r1' });
    expect(fetch.mock.calls[3]?.[0]).toBe('/api/v1/projects/p1/data/notes/query');
  });
});
