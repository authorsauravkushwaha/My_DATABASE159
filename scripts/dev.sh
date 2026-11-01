#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
PYTHON="$ROOT/.venv/bin/python"
[[ -x "$PYTHON" ]] || { echo 'Run bash scripts/setup.sh first.' >&2; exit 1; }
[[ -d node_modules ]] || { echo 'Run bash scripts/setup.sh first.' >&2; exit 1; }
mkdir -p .kestrel
chmod 700 .kestrel
if [[ ! -f .kestrel/dev-secret ]]; then
  (umask 077; "$PYTHON" -c 'import secrets; print(secrets.token_urlsafe(48))' > .kestrel/dev-secret)
fi
export JWT_SECRET="${JWT_SECRET:-$(cat .kestrel/dev-secret)}"
export DATABASE_URL="${DATABASE_URL:-sqlite:///$ROOT/.kestrel/dev.db}"
export AUTO_CREATE_SCHEMA=false
"$PYTHON" -m alembic upgrade head
"$PYTHON" -m uvicorn app.main:app --host 0.0.0.0 --port 8000 &
API_PID=$!
trap 'kill -TERM "$API_PID" 2>/dev/null || true; wait "$API_PID" 2>/dev/null || true' EXIT INT TERM
npm run dev
