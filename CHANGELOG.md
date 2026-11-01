# Changelog

## 0.2.1 — published website

- Added a public marketing and documentation site served by GitHub Pages from the Vue workspace.
- Added a home page covering what works, the three-plane architecture, the typed client, and known limits.
- Added `VITE_BASE` and `VITE_STATIC_DEMO` so one build serves a project page without a control plane, while local and self-hosted behavior stays unchanged.
- Added `scripts/build-site.sh`, a Pages workflow, and a `npm run site` local preview.
- The static build states that sign-in needs the API and that the demo workspace is in-memory.

## 0.2.0 — workspace and hardening

- Added Vue/TypeScript dashboard, responsive CSS, compiled MDX guides, and a clearly labeled in-memory demo.
- Added dependency-free TypeScript control-plane client and JavaScript Playwright tests.
- Added Alembic baseline and one-shot Compose migrations; disabled Compose schema auto-creation.
- Added project/collection deletion, viewer write denial, and database readiness checks.
- Replaced gateway token-only access with control-plane membership/schema authorization; removed public raw scans.
- Added gateway request/schema bounds, control-plane streamed body limits, network/server deadlines, cursor listing, and regression tests.
- Added a real Python → Go integration test covering authorization, pagination, and record/deletion persistence across engine restarts.
- Hardened WAL tail recovery, closed-store handling, SSTable I/O error propagation, and directory durability ordering.
- Added Shell setup/check scripts, JavaScript workspace validation, exact direct npm pins, ShellCheck, and expanded CI.
- Fixed the SDK default relative base URL and added a regression test.
- Documented separate record stores, migration adoption, browser-session trade-offs, and production gaps.

No production-readiness, independent audit, benchmark, or managed-backup claim is made.
