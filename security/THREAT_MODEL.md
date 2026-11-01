# Security threat model (initial)

## Scope and assets

Assets: account emails/password verifiers, bearer tokens, organization/project metadata, collection schemas, JSON records, database credentials, signing keys, and service availability. Trust boundaries: internet client to API; API to PostgreSQL; future deployment operator to host/secret manager. This is a lightweight initial model, not a security audit or compliance assessment.

## Threat actors and main risks

| Threat | Current mitigations | Remaining risk / required work |
|---|---|---|
| Cross-tenant IDOR | authenticated membership lookup; project and collection predicates in data queries; tests return 404 for unauthorized tenant | App-layer bug could expose data. No PostgreSQL RLS, independent DB credentials, fuzz/property tests, or formal review yet. |
| Credential stuffing/brute force | PBKDF2-HMAC-SHA256, random salt, 600,000 iterations; generic login failure | No rate limiting, MFA, verification, breached-password check, or account recovery. Add edge and application limits before public exposure. |
| Token theft/replay | HS256 signature, 15-minute expiry, key length requirement | No refresh rotation/revocation, device-bound session, sender constraint, or secure browser session; secret rotation invalidates all tokens. |
| SQL injection | SQLAlchemy parameterized queries; no client SQL execution; collection/property names constrained | Database role isolation and query time/resource limits absent. Audit future dynamic SQL and migration code. |
| Malicious JSON / resource exhaustion | schema field limits, property type checks, record limit 64 KiB, page size max 100 | No streaming/request-body limit, rate/concurrency quotas, DB statement timeout or DDoS service. Enforce limits at proxy and application. |
| Secret leakage | no real credentials committed; `.env` ignored; env-driven signing key; no database port published in compose | Dev compose is not a hardened production topology; logs/CI/deploy secrets still need review and secret scanning. |
| Database compromise | PostgreSQL password supplied by environment; API container runs non-root | Single DB trust domain, no encryption/KMS/rotation, network segmentation, external backups or least-privilege runtime role. |
| Stored XSS / unsafe rendering | API returns JSON; no dashboard yet | Any future UI must encode output, use CSP, CSRF defenses and secure cookies. |
| Dependency/supply-chain compromise | CI runs dependency audit and lint/tests | CI actions use major tags, no signed build provenance/container scanning/SBOM. Pin actions and add scanners before release. |

## Security behavior and assumptions

- Tokens should be sent only over TLS in non-local environments; TLS termination and certificate management are deployment responsibilities and are not included.
- `JWT_SECRET` must be generated randomly and stored outside Git. Do not use `.env.example` values. Database secret must also be unique.
- The API `AUTO_CREATE_SCHEMA` option is for development only. Production requires reviewed migrations and a restricted runtime DB user.
- Collections use JSON storage and are not a secure boundary for sensitive column-level access; the MVP currently has no per-record or per-field policy system.
- No claim of being fully secure is made. Before production, conduct independent review, penetration testing, dependency/container scanning, restore and incident exercises.

## Reporting

Please report suspected vulnerabilities privately using the process in [../SECURITY.md](../SECURITY.md). Avoid publishing exploit details before a fix is available.
