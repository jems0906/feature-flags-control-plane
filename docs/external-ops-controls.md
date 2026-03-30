# External Ops Controls Completion Guide

This guide completes the production external controls for this repository by using the manifests in `deploy/production`.

## What This Covers

- Real registry/image path enforcement via Kustomize image overrides
- Secret lifecycle integration with External Secrets Operator
- Image signature admission enforcement with Kyverno verifyImages
- DNS + TLS automation with ExternalDNS annotations + cert-manager
- Backup/restore and retention policy with Velero schedules
- Real on-call escalation via Alertmanager secret-based config (Slack + PagerDuty)

Supporting runbooks:

- [Secrets lifecycle](secrets-lifecycle-runbook.md)
- [DNS/TLS automation](dns-tls-automation.md)
- [Backup/restore retention](backup-restore-retention-runbook.md)

## Prerequisites

- Kubernetes cluster with ingress-nginx installed
- cert-manager installed
- External Secrets Operator installed
- Kyverno installed
- Velero installed and configured with backup storage
- DNS zone delegated for your production hostnames

## 0) Inject Production Values

Use the helper script to replace all placeholder values in `deploy/production`:

```powershell
powershell -ExecutionPolicy Bypass -File deploy/production/set-prod-values.ps1 \
  -RegistryOrg <org> \
  -Repository <repo> \
  -RootDomain <your-domain.example.com> \
  -AcmeEmail <platform-ops@your-domain.example.com> \
  -KeyVaultName <your-keyvault-name> \
  -PagerDutyRoutingKey <routing-key>
```

Or use a values file:

```powershell
powershell -ExecutionPolicy Bypass -File deploy/production/go-live.ps1 -ValuesFile deploy/production/prod-values.psd1 -Environment staging
```

After running, render and review:

```bash
kubectl kustomize --load-restrictor=LoadRestrictionsNone deploy/production
```

## 1) Set Real Registry/Image Paths

Edit `deploy/production/kustomization.yaml`:

- Replace `ghcr.io/YOUR_ORG/control-plane`
- Replace `ghcr.io/YOUR_ORG/microservice-demo`
- Set immutable production tags

Then build and inspect rendered images:

```bash
kubectl kustomize --load-restrictor=LoadRestrictionsNone deploy/production | grep image:
```

## 2) Configure Secrets Lifecycle

Edit `deploy/production/external-secrets.yaml`:

- Set Key Vault URL or your provider equivalent
- Set remote secret paths for:
  - `flagplane/controlPlaneAuthToken`
  - `flagplane/grafanaAdminPassword`
  - `flagplane/alertingWebhookUrl`

Apply:

```bash
kubectl apply -f deploy/production/external-secrets.yaml
```

Rotation policy baseline:

- `controlPlaneAuthToken`: every 30 days
- `grafanaAdminPassword`: every 30 days
- `alertingWebhookUrl`: every 90 days or on vendor key rotation

Ownership baseline:

- Platform SRE owns store access policy and incident response
- Service owner owns alert routing semantics and endpoint consumers

## 3) Enforce Signed Images In Cluster

Edit `deploy/production/kyverno-verify-images.yaml`:

- Replace `YOUR_ORG`
- Replace `YOUR_REPO`

Apply:

```bash
kubectl apply -f deploy/production/kyverno-verify-images.yaml
```

Validate enforcement:

```bash
kubectl get clusterpolicy verify-flagplane-images
```

## 4) Automate DNS + TLS

Edit these production hostnames in `deploy/production/patch-production-overrides.yaml` and `deploy/production/cert-manager-certificate.yaml`:

- `api.flagplane.example.com`
- `demo.flagplane.example.com`
- `grafana.flagplane.example.com`

Set a real email in `deploy/production/cert-manager-certificate.yaml` for ACME registration.

Apply:

```bash
kubectl apply -f deploy/production/cert-manager-certificate.yaml
```

## 5) Backup/Restore + Retention

Apply Velero schedule baselines:

```bash
kubectl apply -f deploy/production/velero-schedules.yaml
```

Defined retention:

- hourly backups retained for 7 days
- daily backups retained for 30 days

Run a restore drill monthly and verify Redis, Prometheus, and Grafana state recovery.

## 6) Wire Real On-Call Escalation

Edit `deploy/production/external-secrets.yaml` under `alertmanager-config`:

- Set `routing_key` for PagerDuty receiver
- Set Slack channel destination
- Keep webhook URL file from managed secret

Apply:

```bash
kubectl apply -f deploy/production/external-secrets.yaml
```

This replaces baseline single-webhook routing with explicit warning/critical routing.

## 7) Apply Full Production Overlay

```bash
kubectl apply -k deploy/production
```

## 8) Run Production Preflight Gate

```powershell
powershell -ExecutionPolicy Bypass -File deploy/production/preflight-prod.ps1
```

Cut over only when all checks pass.

Capture staged promotion evidence:

```powershell
powershell -ExecutionPolicy Bypass -File deploy/production/collect-promotion-evidence.ps1 -Namespace flagplane-staging
powershell -ExecutionPolicy Bypass -File deploy/production/collect-promotion-evidence.ps1 -Namespace flagplane
```

## Recommended Change Control

- Raise a change request with rendered manifest diff: `kubectl kustomize deploy/production`
- Approve with SRE + security sign-off
- Execute in staging first, then production
- Capture post-deploy validation screenshots and command output
