import type { CollectionSchema, FieldType, JsonObject } from '@kestrel/client';
export interface Field { name: string; type: FieldType; required: boolean }
export const fieldTypes: FieldType[] = ['string', 'integer', 'number', 'boolean', 'object', 'array', 'null'];
export function buildSchema(fields: Field[]): CollectionSchema {
  if (!fields.length || fields.length > 32) throw new Error('Add between 1 and 32 fields.');
  const schema: CollectionSchema = { type: 'object', properties: {}, required: [], additionalProperties: false };
  for (const field of fields) {
    const name = field.name.trim();
    if (!/^[a-zA-Z][a-zA-Z0-9_]{0,62}$/.test(name)) throw new Error('Field names must start with a letter and contain only letters, numbers, or underscores.');
    if (schema.properties[name]) throw new Error(`Duplicate field: ${name}`);
    schema.properties[name] = { type: field.type };
    if (field.required) schema.required.push(name);
  }
  return schema;
}
export function sampleData(schema: CollectionSchema): JsonObject {
  const defaults = { string: 'Example text', integer: 1, number: 9.99, boolean: false, object: {}, array: [], null: null };
  return Object.fromEntries(Object.entries(schema.properties).map(([name, { type }]) => [name, defaults[type]]));
}
export function parseObject(text: string): JsonObject {
  let value: unknown;
  try { value = JSON.parse(text); } catch { throw new Error('Enter valid JSON. Use double quotes around keys and strings.'); }
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('Record data must be a JSON object.');
  if (new TextEncoder().encode(JSON.stringify(value)).length > 65536) throw new Error('Record exceeds the 64 KiB limit.');
  return value as JsonObject;
}
export function downloadJson(filename: string, data: unknown) {
  const url = URL.createObjectURL(new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' }));
  const link = document.createElement('a'); link.href = url; link.download = filename; link.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
