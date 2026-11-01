# Kestrel architecture (MVP)

## Assessment and design decision

The repository was an empty shell: the initial commit contained only a 17-byte README and no source, tests, deployment files, or existing interfaces. The first milestone therefore establishes a working and testable vertical slice rather than pretending to deliver an entire hyperscale backend.

**Stack:** Python 3.11+, FastAPI, SQLAlchemy 2, PostgreSQL 16, psycopg 3, PyJWT, and pytest. Docker Compose is the single-node local deployment. SQLite is supported only as a test/local convenience. PostgreSQL remains the intended primary platform datastore. These choices favor mature components, fast iteration, and a low-operational-complexity starting point.

## MVP request/data flow

```text
Developer client
    | HTTPS at deployment edge (TLS termination not included in this repo)
    v
FastAPI API (auth + request validation + membership checks)
    | SQLAlchemy parameterized queries
    v
PostgreSQL (users, organizations, memberships, projects, collection definitions, JSON records)
```

The implemented API surface is authentication, organization/project metadata, collection definitions, and a constrained JSON record API. Records are not dynamically created SQL tables. Client-provided SQL is never executed. Access to every organization/project endpoint is resolved using the authenticated user and membership; record queries additionally constrain both project and collection IDs. Unauthenticated and cross-tenant requests are denied.

## Full intended repository structure

```text
app/                       # MVP API and persistence layer
  db.py, main.py, models.py, schemas.py, security.py
tests/                     # MVP API and tenant-isolation tests
docs/                      # architecture, roadmap, deployment
security/                  # threat model and security process
.github/workflows/          # CI (lint, test, dependency audit)
Dockerfile
docker-compose.yml
requirements*.txt
README.md
CONTRIBUTING.md
SECURITY.md
LICENSE
```

As capabilities arrive, expand into a modular monorepo: `apps/dashboard`, `services/{gateway,auth,projects,database,realtime,storage,functions,workers}`, `packages/{sdk-js,sdk-python,shared-types,cli}`, `database/{migrations,seeds}`, `infrastructure/{terraform,monitoring}`, `security/{policies,security-tests}`, and `docs`. Do not create empty services solely to imply they exist. Keep service boundaries logical until independent scaling or deployment has a measured benefit.

## Service boundaries and evolution

1. **Control plane (now):** account, organization, membership, project and collection metadata. It currently shares one API process and one PostgreSQL instance.
2. **Data plane (next):** provision project databases/schemas with isolated database roles, private networking and pooling; generated APIs should delegate enforcement to PostgreSQL RLS as a second authorization boundary.
3. **Identity:** email verification, recovery, MFA/passkeys, OAuth/OIDC, session revocation and organization invitations. A distinct auth service only when operationally justified.
4. **Data APIs:** schema-aware REST/OpenAPI generation. GraphQL and SQL editor follow only after tenant enforcement, resource limits, auditing, and query controls are proven.
5. **Event/storage/compute:** PostgreSQL logical decoding/outbox plus a broker for realtime; S3-compatible object storage; isolated function workers (never in the API process).
6. **Operations:** migration runner, pooled connections, encrypted backups, restore drills, OpenTelemetry, quotas, billing and infrastructure automation.

## Security principles

- deny by default; identify a principal before project access; return 404 for inaccessible organization/project resources to reduce enumeration;
- use parameterized ORM operations; JSON property names are validated and never interpolated into SQL;
- short-lived signed access tokens, high-iteration salted password hashes, minimum password length, no secrets in source;
- production should use managed secret storage, TLS, separate least-privilege service/database roles, network isolation, rate limiting and audit trails;
- do not treat this MVP's application-level filters as a substitute for PostgreSQL RLS or isolated project credentials.

Threats and remaining gaps are detailed in [../security/THREAT_MODEL.md](../security/THREAT_MODEL.md). The system is not represented as production-ready or independently audited.
