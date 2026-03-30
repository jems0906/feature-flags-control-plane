# Alerting Runbook

This runbook describes how to activate and validate the in-cluster Prometheus + Alertmanager alert path.

For production escalation routing (Slack + PagerDuty) use the production overlay documented in [docs/external-ops-controls.md](external-ops-controls.md).

## Scope

Configured components in [deploy/k8s.yaml](../deploy/k8s.yaml):
- Prometheus alert rules (`prometheus-config`)
- Alertmanager deployment and service (`alertmanager:9093`)
- Critical-severity route to a webhook receiver via secret-backed `url_file`

## Prerequisites

1. Kubernetes cluster is running the `flagplane` namespace stack.
2. `kube-state-metrics` is installed if you want the replica-availability rule to evaluate correctly.
3. A reachable webhook endpoint for critical alerts.

## Configure Webhook Receiver

Create/update the webhook secret:

```bash
kubectl create secret generic alerting-secrets \
  -n flagplane \
  --from-literal=webhook-url='https://your-alert-endpoint.example/webhook' \
  --dry-run=client -o yaml | kubectl apply -f -
```

Restart Alertmanager to ensure new secret data is picked up:

```bash
kubectl rollout restart deployment/alertmanager -n flagplane
kubectl rollout status deployment/alertmanager -n flagplane
```

## Validate Rule Evaluation

Check Prometheus rule health:

```bash
kubectl port-forward svc/prometheus -n flagplane 9090:9090
```

Then open:
- `http://localhost:9090/rules`
- `http://localhost:9090/alerts`

## Trigger A Real Alert

The `CircuitBreakerOpen` rule is easiest to trigger from the demo:

```bash
kubectl port-forward svc/microservice-demo -n flagplane 8081:8081
for i in 1 2 3 4 5; do curl -s -o /dev/null -X POST "http://localhost:8081/demo/action?userId=alice&fail=true"; done
```

Then verify:
1. Prometheus shows `CircuitBreakerOpen` as firing.
2. Alertmanager `/` UI shows received alert.
3. Webhook endpoint receives a notification payload.

## Common Failure Modes

1. No critical notifications sent:
- Confirm `alerting-secrets` has `webhook-url` key.
- Confirm Alertmanager pod can resolve/reach webhook host.

2. Replica alert never fires:
- Ensure kube-state-metrics is installed and exposing `kube_deployment_status_replicas_available`.

3. Alertmanager config errors on startup:
- Check pod logs: `kubectl logs deploy/alertmanager -n flagplane`.
- Validate the rendered secret file at `/etc/alertmanager/secrets/webhook-url`.

## Operational Notes

- Current routing sends only `severity="critical"` alerts to webhook.
- Non-critical alerts are routed to the default null receiver.
- Production overlay replaces webhook-only baseline with explicit warning/critical routes using `alertmanager-config` secret in [deploy/production/external-secrets.yaml](../deploy/production/external-secrets.yaml).