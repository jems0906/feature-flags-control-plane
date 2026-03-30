# Backup, Restore, and Retention Runbook

This runbook defines backup schedules, retention policy, and restore validation for Flagplane stateful components.

## Protected Data

- Redis PVC: `redis-data`
- Prometheus PVC: `prometheus-data`
- Grafana PVC: `grafana-data`

## Backup Policy

Velero schedules are defined in `deploy/production/velero-schedules.yaml`.

- Hourly snapshots: retained for 7 days
- Daily snapshots: retained for 30 days

Validation criteria for policy correctness:

- Velero schedules exist and are `Enabled`
- Schedule TTL values match policy (`168h` hourly, `720h` daily)
- New backups are created on schedule with status `Completed`

Apply schedules:

```bash
kubectl apply -f deploy/production/velero-schedules.yaml
```

## Verify Backup Health

```bash
kubectl get schedules -n velero
kubectl get backups -n velero
kubectl get schedules -n velero -o yaml | grep -E "name:|ttl:|schedule:"
```

Success criteria:

- Latest hourly backup status is `Completed`
- Latest daily backup status is `Completed`
- No repeated backup warnings/errors over 24h
- Schedule TTLs match retention policy (`168h` and `720h`)

## Restore Drill (Monthly)

1. Select a recent backup:

```bash
kubectl get backups -n velero
```

2. Create restore job:

```bash
velero restore create --from-backup <backup-name>
```

3. Validate restored resources:

```bash
kubectl get pods -n flagplane
kubectl get pvc -n flagplane
```

4. Functional checks:

- Control plane responds at `/health`
- Redis reconnect succeeds from control-plane pod
- Grafana dashboards and data sources are present
- Prometheus targets are up
- Smoke test script passes against restored workloads (`deploy/smoke-test.sh` or `deploy/smoke-test.ps1`)

Suggested restore validation command set:

```bash
kubectl -n flagplane port-forward svc/control-plane 18080:8080 &
kubectl -n flagplane port-forward svc/microservice-demo 18081:8081 &
CONTROL_PLANE_URL=http://127.0.0.1:18080 DEMO_URL=http://127.0.0.1:18081 bash deploy/smoke-test.sh
```

Record these evidence items in the incident/change ticket:

- Backup name used
- Restore job name and completion status
- `kubectl get pods -n flagplane` output snapshot
- Smoke test pass output

## RPO and RTO Baseline

- RPO: up to 1 hour
- RTO: up to 2 hours

## Incident Checklist

- Declare incident and freeze conflicting deploys
- Restore from latest valid backup
- Validate app health and alert flow
- Post-incident review with backup integrity notes

## Documentation Validation Status

Runbook validation completed:

- Commands are aligned with repository assets (`deploy/production/velero-schedules.yaml`, smoke scripts in `deploy/`)
- Retention policy now has explicit, testable acceptance criteria
- Restore drill now requires functional smoke verification, not only pod/PVC presence
