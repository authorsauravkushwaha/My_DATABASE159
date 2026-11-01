# @kestrel/client

Dependency-free, typed TypeScript access to the Kestrel **control and high-capacity data plane**. This workspace package uses native `fetch` and requires Node 22+ or a modern browser with `AbortSignal.any` and `AbortSignal.timeout`.

```ts
import { KestrelClient, ApiError } from '@kestrel/client';

const client = new KestrelClient({ baseUrl: '/api/', timeoutMs: 15000 });
const session = await client.login({ email, password });
client.setToken(session.access_token);

await client.batchCreateRecords(projectId, 'notes', [
  { title: 'First note' },
  { title: 'Second note' },
]);

const page = await client.queryRecords(projectId, 'notes', {
  filters: [{ field: 'title', op: 'prefix', value: 'First' }],
  limit: 100,
});
```

## API

- `register`, `login`, `health`, `ready`
- `listProjects`, `createProject`, `getProject`, `getProjectStorage`, `setProjectApi`, `deleteProject`
- `listCollections`, `createCollection`, `deleteCollection`, `createIndex`, `deleteIndex`, `compactCollection`
- `listRecords`, `queryRecords`, `createRecord`, `batchCreateRecords`, `importRecordsNdjson`, `exportRecordsNdjson`, `updateRecord`, `deleteRecord`
- `setToken`, `clearToken`

Record updates merge fields on the server. Collection schemas are a closed, flat JSON Schema subset (up to 128 fields and 16 secondary B-tree indexes). Unknown fields are rejected. `listRecords` and `queryRecords` support O(1) keyset cursor pagination via `after`.

## Build and test

```bash
npm run build -w @kestrel/client
npm run test -w @kestrel/client
```
