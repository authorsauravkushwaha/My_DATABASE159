#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
PYTHON="$ROOT/.venv/bin/python"
[[ -x "$PYTHON" ]] || { echo 'Run bash scripts/setup.sh first.' >&2; exit 1; }
for tool in node npm go; do command -v "$tool" >/dev/null || { echo "$tool is required for the full check." >&2; exit 1; }; done
node scripts/check-workspace.mjs
npm ci
npm run lint
npm run typecheck
npm run test
npm run build
npm audit
if [[ -n "${CHROMIUM_EXECUTABLE_PATH:-}" ]] || compgen -G "$HOME/.cache/ms-playwright/chromium-*/chrome-linux*/chrome" >/dev/null; then
  npm run test:e2e
else
  echo 'Browser tests not run: install Chromium with npx playwright install chromium.'
fi
"$PYTHON" -m ruff check app tests alembic
"$PYTHON" -m pytest -q
"$PYTHON" -m pip_audit -r requirements.txt
for module in engine gateway; do
  pushd "$module" >/dev/null
  if command -v gofmt >/dev/null; then
    test -z "$(gofmt -l .)" || { echo 'Go formatting required.' >&2; exit 1; }
  else
    formatted="$(go fmt ./...)"
    [[ -z "$formatted" ]] || { echo "Go formatting required: $formatted" >&2; exit 1; }
  fi
  go vet ./...
  go test -race ./...
  popd >/dev/null
done
if command -v shellcheck >/dev/null; then shellcheck scripts/*.sh; fi
printf '\nAll available checks passed. PostgreSQL integration and deployment smoke tests are separate CI gates.\n'
