# @kestrel/client

Dependency-free, typed TypeScript access to the Kestrel **control plane**. This workspace package is not published to npm yet. It uses native fetch and requires Node 22+ or a modern browser with `AbortSignal.any` and `AbortSignal.timeout`.

```ts
import { KestrelClient, ApiError } from '@kestrel/client';

const client = new KestrelClient({ baseUrl: '/api/', timeoutMs: 15000 });
const session = await client.login({ email, password });
client.setToken(session.access_token);

try {
  const records = await client.listRecords(projectId, 'notes', { limit: 25 });
  console.log(records);
} catch (error) {
  if (error instanceof ApiError && error.status === 401) {
    client.clearToken();
    // Prompt for sign-in; there is no silent refresh mechanism.
  }
  throw error;
}
```

The default base URL is the relative `/api/` path, including in Node. Pass an absolute origin explicitly for a directly exposed API.

## API

- `register`, `login`, `health`, `ready`
- `listProjects`, `createProject`, `getProject`, `setProjectApi`, `deleteProject`
- `listCollections`, `createCollection`, `deleteCollection`
- `listRecords`, `createRecord`, `updateRecord`, `deleteRecord`
- `setToken`, `clearToken`

Record updates merge fields on the server. Collection schemas are a closed, flat JSON Schema subset. Unknown fields are rejected. `listRecords` returns one page, not an auto-followed iterator.

## Security and transport

- Use a same-origin reverse proxy in browsers. The dashboard proxies `/api/` to FastAPI.
- In Node, use an explicit HTTPS base URL outside local development.
- Credentials are never written to storage by this package. Keep server secrets in environment variables or a secret manager.
- Requests omit cookies and send bearer authorization only when a token is set.
- Path segments are encoded; redirects are not used for credential forwarding.
- HTTP failures become `ApiError(status, message, details)`. Network and timeout failures are regular errors with the original cause.
- Pass an `AbortSignal` to cancellable reads to abort in-flight work. Mutations are not automatically retried.
- Tokens expire after 15 minutes. Refresh rotation and revocation are not implemented.

This client does **not** access the separate Go gateway or execute SQL.

## Build and test

```bash
npm run build -w @kestrel/client
npm run test -w @kestrel/client
```

The package emits ESM and TypeScript declarations to `dist/`. Generated output is ignored by Git.
