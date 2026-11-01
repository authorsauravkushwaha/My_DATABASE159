#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
command -v python3 >/dev/null || { echo 'Python 3.11+ is required.' >&2; exit 1; }
command -v node >/dev/null || { echo 'Node 22.12+ is required.' >&2; exit 1; }
command -v npm >/dev/null || { echo 'npm 10+ is required.' >&2; exit 1; }
python3 - <<'PY'
from pathlib import Path
import secrets
import sys
if sys.version_info < (3, 11):
    raise SystemExit('Python 3.11+ is required')
path = Path('.env')
if path.exists():
    print('Keeping existing .env; no secrets were overwritten.')
else:
    path.write_text('# Local development only. Never commit this file.\n'
                    f'POSTGRES_PASSWORD={secrets.token_urlsafe(36)}\n'
                    f'JWT_SECRET={secrets.token_urlsafe(48)}\n')
    path.chmod(0o600)
    print('Created .env with unique random secrets (mode 0600).')
PY
if [[ ! -d .venv ]]; then python3 -m venv .venv; fi
.venv/bin/python -m pip install --disable-pip-version-check -r requirements-dev.txt
npm ci
printf '\nSetup complete. Run docker compose up --build, or bash scripts/dev.sh for SQLite development.\n'
