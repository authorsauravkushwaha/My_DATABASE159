# Contributing

Thanks for helping build Kestrel. The project is currently an early MVP; please open an issue before a major architectural change.

## Development checks

- Python 3.11+; install `requirements-dev.txt` in a virtual environment.
- Run `ruff check app tests` and `pytest` before submitting changes.
- Add tests for new endpoints, authorization boundaries, validation, and failure cases.
- Keep all persistence tenant-aware; test that another organization cannot retrieve, modify, or infer a resource.
- Never put credentials, real customer data, or generated artifacts in source control.
- Document limitations honestly. Do not add UI controls that only simulate server behavior.

## Pull requests

Describe the threat model impact, migration/backwards-compatibility impact, and verification performed. Schema changes need migration plans before production use. A passing unit suite is not proof of production readiness; report untested PostgreSQL-specific behavior.
