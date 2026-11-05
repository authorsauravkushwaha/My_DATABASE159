## Summary

<!-- What changed and why. Link related issues, e.g. "Closes #12". -->

## Threat model impact

<!-- New credentials, trust boundaries, tenant isolation, or say "none". -->

## Migration / compatibility impact

<!-- Schema or API changes, migration numbering, or say "none". -->

## Verification performed

<!-- e.g. bash scripts/check.sh results, new tests, manual steps. A passing unit suite is not proof of production readiness — report what was NOT tested. -->

## Checklist

- [ ] `bash scripts/check.sh` passes locally (or CI is green for untouched areas)
- [ ] New endpoints/authorization boundaries have tests, including cross-tenant denial
- [ ] Docs/changelog updated when behavior or configuration changed
- [ ] No credentials, `.env`, generated builds, or customer data committed
