# Secrets Lifecycle Runbook

This runbook defines rotation cadence, owners, and execution steps for production secrets used by Flagplane.

## Secret Inventory

- `platform-secrets/controlPlaneAuthToken`
- `platform-secrets/grafanaAdminPassword`
- `alerting-secrets/webhook-url`
- `alertmanager-config` (PagerDuty routing key in config content)

## Ownership

- Primary owner: Platform SRE
- Secondary owner: Service owner on-call
- Security approver: Security Engineering

## Rotation Cadence

- `controlPlaneAuthToken`: every 30 days
- `grafanaAdminPassword`: every 30 days
- `webhook-url`: every 90 days or provider-triggered
- PagerDuty routing key: every 90 days or incident-triggered

## Rotation Procedure

1. Write new secret values to external secret manager.
2. Wait for ExternalSecret sync (`refreshInterval: 1h` in `deploy/production/external-secrets.yaml`).
3. Restart dependent workloads:

```bash
kubectl rollout restart deployment/control-plane -n flagplane
kubectl rollout restart deployment/microservice-demo -n flagplane
kubectl rollout restart deployment/grafana -n flagplane
kubectl rollout restart deployment/alertmanager -n flagplane
```

4. Validate health and authentication:

```bash
kubectl get pods -n flagplane
kubectl port-forward svc/control-plane -n flagplane 8080:8080
curl -s http://localhost:8080/health
```

5. Close rotation ticket with timestamp and approver.

## Emergency Rotation

Execute immediate rotation when compromise is suspected.

1. Rotate all secrets in manager.
2. Force refresh ExternalSecrets by annotating:

```bash
kubectl annotate externalsecret platform-secrets -n flagplane force-sync="$(date +%s)"
kubectl annotate externalsecret alerting-secrets -n flagplane force-sync="$(date +%s)"
```

3. Restart deployments and validate traffic/alerting.
4. Audit access logs and preserve evidence.

## Audit Evidence

Capture for each rotation:

- Secret version IDs from manager
- Deployment rollout completion output
- Health check output
- Alertmanager test notification evidence
- Approval record
