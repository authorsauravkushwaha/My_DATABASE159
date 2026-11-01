# Security threat model

This is an initial engineering model, not an audit or compliance certification. Scope: browser dashboard, TypeScript client, Python control plane, PostgreSQL/SQLite metadata and records, Go gateway/engine, and local deployment configuration.

## Trust boundaries

1. Browser/client → same-origin dashboard proxy → control plane.
2. Client → gateway → control-plane authorization → private engine.
3. Services → database and persistent volumes.
4. Operator/CI → source, dependencies, migrations, and signing/database secrets.

A project ID, collection ID, key prefix, or valid token signature is not itself permission to access data.

## Main risks and current treatment

| Threat | Implemented mitigation | Material remaining gap |
|---|---|---|
| Cross-tenant IDOR | Membership lookup; project/collection predicates; gateway delegates every access check using the caller token; regression tests | No PostgreSQL RLS or independent per-project DB roles; application bugs remain possible |
| Privilege escalation | Owner/admin project management; owner/admin/editor data writes; viewer read-only; gateway requests write grants | No invitation/membership-management UI, audit trail, MFA, or formal external review |
| Credential attacks | PBKDF2-HMAC-SHA256, random salt, 600,000 iterations; generic login failure; minimum password length | No application/edge rate limit, breached-password check, verified email, recovery, or MFA |
| Token theft/replay | HS256, required claims, 15-minute expiry; dashboard memory-only token; clear on sign-out/expiry | No revocation/refresh rotation/device binding; XSS can steal in-memory tokens; reload requires sign-in |
| SQL injection | Parameterized SQLAlchemy operations; no client SQL; constrained names | Future migration/dynamic-query code needs review; DB runtime privileges are not minimized yet |
| Malicious JSON | Closed flat schemas, type/required checks on both write paths, 64 KiB records, bounded pages | Control-plane bodies are bounded to 128 KiB, including streamed requests; edge concurrency/rate limits are still needed; nested object/array cost is not separately bounded; engine scans merge the full prefix before applying the result limit, so storage memory/cost quotas remain future work |
| Gateway bypass | No public raw scan; control-plane identity/membership/schema check before storage; fail-closed timeouts; key identity verification | Shared control-plane availability dependency; separate stores and orphan cleanup unresolved |
| Storage corruption/crash | WAL CRC and fsync; incomplete-tail trimming; corrupt complete-frame rejection; SSTable directory sync before WAL truncation; I/O error propagation | No SSTable checksums, multi-process ownership lock, replication, or demonstrated storage-device failure recovery |
| Browser XSS | Vue text escaping; no user HTML/MDX evaluation; compiled trusted MDX; restrictive Nginx CSP; no remote fonts/scripts | CSP includes `style-src 'unsafe-inline'`; dependency compromise and development-server exposure remain risks |
| Secret leakage | `.env` and preview secrets ignored; random setup; no gateway signing-secret copy; no credentials in SDK storage | No managed secret store/rotation; deployment logs and backups need operator review |
| Supply chain | Exact direct npm versions and lockfile; `npm ci`; npm/pip audits; CI tests | Actions use major tags; no signed provenance/SBOM or independent dependency review |

## Authorization contract

The gateway calls:

`GET /v1/projects/{project}/collections/{collection_uuid}/access?write=true|false`

It forwards the original bearer token. The control plane authenticates the user, checks organization membership, project API state, collection ownership, and requested write role. The gateway validates the returned IDs and schema, then accesses only the corresponding namespaced key. Authorization failures are not converted into successful empty results. Service/network failures return 503.

The endpoint is not a secret bypass: without an authorized caller token it reveals no collection access. Network isolation and edge limits are still recommended.

## Browser session decision

The dashboard intentionally does not put bearer tokens in localStorage or sessionStorage. This reduces persistent token exposure but is not XSS protection and does not survive a reload. A future first-party cookie/session design needs CSRF defenses, secure cookie attributes, rotation, and revocation; it must not be represented as already implemented.

The interactive demo is a separate in-memory adapter. It sends no record CRUD to the API and displays a persistent demo banner. Its sample records are not real service data or usage metrics.

## Deployment assumptions

- TLS and trusted ingress are required outside localhost.
- PostgreSQL and engine networking stay private; the engine protocol has no authentication.
- Unique random secrets, least-privilege DB roles, edge request/rate limits, monitoring, and backups are operator responsibilities.
- Development Compose is not a hardened production topology.
- `/ready` is a database connectivity check, not a security or full-dependency attestation.
- Control-plane and gateway records are separate. Deleting one does not erase the other.

## Go/no-go

Do not claim production readiness until PostgreSQL integration and isolation tests, identity hardening, rate limits, RLS/role review, backup restore drills, crash/load/failure tests, migration rollback exercises, alerting, TLS/secrets deployment, and independent review are demonstrated.

Report vulnerabilities using [SECURITY.md](../SECURITY.md). Never include real credentials or customer data in a public report.
