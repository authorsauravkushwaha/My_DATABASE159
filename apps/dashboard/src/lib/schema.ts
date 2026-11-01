import type { CollectionSchema, FieldType, JsonObject } from '@kestrel/client';

export interface Field {
  name: string;
  type: FieldType;
  required: boolean;
  indexed?: boolean;
}

export const fieldTypes: FieldType[] = ['string', 'integer', 'number', 'boolean', 'object', 'array', 'null'];
export const indexableTypes: ReadonlySet<FieldType> = new Set(['string', 'integer', 'number', 'boolean']);

export function buildSchema(fields: Field[]): CollectionSchema {
  if (!fields.length || fields.length > 128) throw new Error('Add between 1 and 128 fields.');
  const schema: CollectionSchema = { type: 'object', properties: {}, required: [], additionalProperties: false };
  const indexes: string[] = [];
  for (const field of fields) {
    const name = field.name.trim();
    if (!/^[a-zA-Z][a-zA-Z0-9_]{0,62}$/.test(name)) {
      throw new Error('Field names must start with a letter and contain only letters, numbers, or underscores.');
    }
    if (schema.properties[name]) throw new Error(`Duplicate field: ${name}`);
    schema.properties[name] = { type: field.type };
    if (field.required) schema.required.push(name);
    if (field.indexed && indexableTypes.has(field.type)) indexes.push(name);
  }
  if (indexes.length > 16) throw new Error('At most 16 secondary indexes are allowed per collection.');
  if (indexes.length) schema.indexes = [...indexes].sort();
  return schema;
}

export function sampleData(schema: CollectionSchema): JsonObject {
  const defaults = { string: 'Example text', integer: 1, number: 9.99, boolean: false, object: {}, array: [], null: null };
  return Object.fromEntries(Object.entries(schema.properties).map(([name, { type }]) => [name, defaults[type]]));
}

export function generateSampleBatch(schema: CollectionSchema, count: number): JsonObject[] {
  const safeCount = Math.max(1, Math.min(5000, Math.floor(count)));
  const entries = Object.entries(schema.properties);
  const categories = ['analytics-core', 'edge-gateway', 'storage-shard', 'billing-pipeline'];
  const out: JsonObject[] = [];
  for (let i = 1; i <= safeCount; i++) {
    const row: JsonObject = {};
    for (const [name, spec] of entries) {
      switch (spec.type) {
        case 'string':
          row[name] = name.includes('email')
            ? `user${i}@example.com`
            : `${categories[i % categories.length]}-${name}-record-${String(i).padStart(4, '0')}`;
          break;
        case 'integer':
          row[name] = (i * 13) % 10000;
          break;
        case 'number':
          row[name] = Math.round(((i * 17.35) % 999) * 100) / 100;
          break;
        case 'boolean':
          row[name] = i % 2 === 0;
          break;
        case 'object':
          row[name] = { shard: i % 64, batch: true };
          break;
        case 'array':
          row[name] = [categories[i % categories.length]!, `seq-${i}`];
          break;
        case 'null':
          row[name] = null;
          break;
      }
    }
    out.push(row);
  }
  return out;
}

export function parseObject(text: string): JsonObject {
  let value: unknown;
  try { value = JSON.parse(text); } catch { throw new Error('Enter valid JSON. Use double quotes around keys and strings.'); }
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('Record data must be a JSON object.');
  if (new TextEncoder().encode(JSON.stringify(value)).length > 524288) throw new Error('Record exceeds the 512 KiB limit.');
  return value as JsonObject;
}

export function parseBatchInput(text: string): JsonObject[] {
  const trimmed = text.trim();
  if (!trimmed) throw new Error('Provide a JSON array or newline-delimited JSON objects.');
  if (trimmed.startsWith('[')) {
    let parsed: unknown;
    try { parsed = JSON.parse(trimmed); } catch { throw new Error('Invalid JSON array.'); }
    if (!Array.isArray(parsed) || !parsed.length) throw new Error('Batch JSON array must contain at least one object.');
    if (parsed.length > 5000) throw new Error('Batch size must be 5,000 records or fewer per call.');
    return parsed.map((item, idx) => {
      if (!item || typeof item !== 'object' || Array.isArray(item)) throw new Error(`Item ${idx + 1} must be a JSON object.`);
      return item as JsonObject;
    });
  }
  const lines = trimmed.split(/\r?\n/).map((l) => l.trim()).filter(Boolean);
  if (!lines.length || lines.length > 5000) throw new Error('Provide between 1 and 5,000 NDJSON lines.');
  return lines.map((line, idx) => {
    let parsed: unknown;
    try { parsed = JSON.parse(line); } catch { throw new Error(`Invalid JSON on line ${idx + 1}.`); }
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error(`Line ${idx + 1} must be a JSON object.`);
    const obj = parsed as Record<string, unknown>;
    if ('data' in obj && obj.data && typeof obj.data === 'object' && !Array.isArray(obj.data) && Object.keys(obj).every((k) => ['id', 'data', 'created_at', 'updated_at'].includes(k))) {
      return obj.data as JsonObject;
    }
    return obj as JsonObject;
  });
}

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B';
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  let idx = 0;
  let value = bytes;
  while (value >= 1024 && idx < units.length - 1) {
    value /= 1024;
    idx++;
  }
  return `${value >= 100 || idx === 0 ? Math.round(value) : value.toFixed(1)} ${units[idx]}`;
}

export function downloadJson(filename: string, data: unknown) {
  const url = URL.createObjectURL(new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' }));
  const link = document.createElement('a'); link.href = url; link.download = filename; link.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

export function downloadText(filename: string, text: string, mime = 'application/x-ndjson') {
  const url = URL.createObjectURL(new Blob([text], { type: mime }));
  const link = document.createElement('a'); link.href = url; link.download = filename; link.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
