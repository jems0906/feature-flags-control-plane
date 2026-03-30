# Production Go-Live Sign-Off

Use this document to record final approvals after staging validation and before production cutover.

## Change Summary

- Release version:
- Change request ID:
- Planned go-live window:
- Rollback owner:

## Environment Values Applied

- Production values file used:
- set-prod-values execution timestamp:
- Rendered manifest review completed by:

## Required Controls Verification

- [ ] `deploy/production/preflight-prod.ps1` passed
- [ ] TLS certificate ready (`flagplane-tls`)
- [ ] Required secrets present (`platform-secrets`, `alerting-secrets`, `alertmanager-config`)
- [ ] Image signature verification policy active
- [ ] Backup schedules present (`flagplane-hourly`, `flagplane-daily`)

## Staging Promotion Evidence

- Evidence folder:
- Staging image tags recorded:
- Staging smoke test result:
- Staging rollback drill result:

## Production Deployment Evidence

- Evidence folder:
- Production image tags recorded:
- Production smoke test result:
- Production health checks result:

## Approvals

- SRE approver:
- Security approver:
- Service owner approver:
- Product/Business approver (if required):

## Final Go / No-Go

- Decision: GO / NO-GO
- Decision timestamp:
- Notes:
