# Contributing

Thanks for helping build Kestrel. The project is currently an early MVP; please open an issue before a major architectural change.

## Development checks

- Python 3.11+, Node 22.12+, npm 10+, and Go 1.25+ for the full stack. Run `bash scripts/setup.sh` for Python/npm dependencies.
- Run `bash scripts/check.sh` before submitting changes. Run `npm run test:e2e` for browser-facing changes (install Chromium first).
- Add tests for new endpoints, authorization boundaries, validation, and failure cases.
- Keep all persistence tenant-aware; test that another organization cannot retrieve, modify, or infer a resource.
- Never put credentials, real customer data, or generated artifacts in source control.
- Document limitations honestly. Do not add UI controls that only simulate server behavior.

## Pull requests

Describe the threat model impact, migration/backwards-compatibility impact, and verification performed. Schema changes need reviewed Alembic migrations and upgrade/downgrade tests. Pin direct npm dependencies exactly and update the lockfile. Never commit `.env`, `.kestrel`, generated builds, real credentials, or customer data. A passing unit suite is not proof of production readiness; report untested PostgreSQL-specific behavior.
