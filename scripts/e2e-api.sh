#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
PYTHON="$ROOT/.venv/bin/python"
[[ -x "$PYTHON" ]] || PYTHON=python
mkdir -p .kestrel
export DATABASE_URL="sqlite:///$ROOT/.kestrel/e2e.db"
export JWT_SECRET="e2e-only-secret-not-for-deployment-at-least-32-characters"
export AUTO_CREATE_SCHEMA=false
"$PYTHON" -m alembic upgrade head
exec "$PYTHON" -m uvicorn app.main:app --host 0.0.0.0 --port 8000
