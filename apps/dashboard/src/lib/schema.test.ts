import { describe, expect, it } from 'vitest';
import { buildSchema, parseObject, sampleData } from './schema';
import { DemoApi } from './demo';

describe('schema builder', () => {
  it('builds a closed, typed schema', () => {
    expect(buildSchema([{ name: 'title', type: 'string', required: true }, { name: 'count', type: 'integer', required: false }])).toEqual({ type: 'object', properties: { title: { type: 'string' }, count: { type: 'integer' } }, required: ['title'], additionalProperties: false });
  });
  it('rejects duplicate and unsafe field names', () => {
    expect(() => buildSchema([{ name: 'x', type: 'string', required: false }, { name: 'x', type: 'number', required: false }])).toThrow('Duplicate');
    expect(() => buildSchema([{ name: 'x;drop', type: 'string', required: false }])).toThrow('Field names');
    expect(() => buildSchema([])).toThrow('between 1 and 32');
  });
  it('creates editable defaults for every supported type', () => {
    const schema = buildSchema(['string', 'integer', 'number', 'boolean', 'object', 'array', 'null'].map((type, i) => ({ name: `f${i}`, type: type as 'string', required: false })));
    expect(Object.values(sampleData(schema))).toEqual(['Example text', 1, 9.99, false, {}, [], null]);
  });
  it('rejects invalid JSON, arrays, and oversized records', () => {
    expect(() => parseObject('{bad}')).toThrow('valid JSON');
    expect(() => parseObject('[]')).toThrow('JSON object');
    expect(() => parseObject(JSON.stringify({ x: 'a'.repeat(65536) }))).toThrow('64 KiB');
    expect(parseObject('{"x":1}')).toEqual({ x: 1 });
  });
});

describe('explicit demo adapter', () => {
  it('supports real in-memory CRUD without network', async () => {
    const api = new DemoApi();
    const project = await api.createProject('demo-org', { name: 'Test', slug: 'test' });
    const collection = await api.createCollection(project.id, { name: 'notes', schema: buildSchema([{ name: 'title', type: 'string', required: true }]) });
    const record = await api.createRecord(project.id, collection.name, { title: 'Hello' });
    expect((await api.listRecords(project.id, 'notes'))[0]?.data.title).toBe('Hello');
    await api.updateRecord(project.id, 'notes', record.id, { title: 'Updated' });
    expect((await api.listRecords(project.id, 'notes'))[0]?.data.title).toBe('Updated');
    await api.deleteRecord(project.id, 'notes', record.id);
    expect(await api.listRecords(project.id, 'notes')).toEqual([]);
  });
  it('validates data and isolates projects', async () => {
    const api = new DemoApi();
    await expect(api.createRecord('demo-commerce', 'products', { name: 'x' })).rejects.toThrow('price');
    await expect(api.listCollections('other-org-project')).rejects.toThrow('not found');
    await api.setProjectApi('demo-commerce', false);
    await expect(api.listCollections('demo-commerce')).rejects.toThrow('disabled');
    expect((await api.listCollections('demo-internal')).length).toBe(1);
  });
  it('returns copies so callers cannot mutate storage directly', async () => {
    const api = new DemoApi(); const projects = await api.listProjects('demo-org'); projects[0]!.name = 'changed';
    expect((await api.listProjects('demo-org'))[0]?.name).toBe('Commerce');
  });
});
