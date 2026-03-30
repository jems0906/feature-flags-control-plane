# Production Readiness Checklist

This document turns the current gap list into the minimum repo-specific work needed to move from demo-ready to production-candidate.

## Current Status

What is already in place:
- Multi-replica control plane with shared Redis-backed config in Kubernetes
- Feature flags, experiments, rate limiting, circuit breaking, metrics, and tracing
- Health probes and resource requests/limits in [deploy/k8s.yaml](../deploy/k8s.yaml)
- Passing control-plane tests and a clean demo-service build
- Bearer-token auth on write/admin routes with required Kubernetes secret wiring in manifest
- PVC-backed storage for Redis, Prometheus, and Grafana
- Versioned registry-style image references in [deploy/k8s.yaml](../deploy/k8s.yaml)
- HTTPS ingress resources for control-plane, demo, and Grafana hostnames
- Baseline Kubernetes hardening in manifest: service accounts, security contexts, PodDisruptionBudget, and NetworkPolicy
- Baseline Prometheus alert rules with in-cluster Alertmanager routing
- Secret-backed critical webhook receiver path for Alertmanager
- GitHub Actions workflow for build/test/push and keyless image signing
- Production external-ops overlay assets in [deploy/production](../deploy/production) for secret lifecycle, image admission, DNS/TLS automation, backup schedules, and on-call routing

What is still missing for production:
- Environment-specific rollout of [deploy/production](../deploy/production) with real org/repo/domain/secret-manager values
- Final change-management approval and staged promotion evidence (staging -> production)

Ingress/TLS status:
- Ingress resources with TLS are defined in [deploy/k8s.yaml](../deploy/k8s.yaml).
- A valid `flagplane-tls` certificate secret is still environment-managed.
- Additional ingress hardening (WAF/rate policies) is still recommended.

Authentication status:
- Bearer-token auth is enforced in Kubernetes deployments via `CONTROL_PLANE_AUTH_TOKEN` secret references.
- Secret wiring is required in [deploy/k8s.yaml](../deploy/k8s.yaml) for control-plane and demo workloads.
- Stronger identity options (for example mTLS or workload identity) are still recommended.

## Minimum Change Set

### 1. Protect The Control Plane (Implemented)

Target files:
- [control-plane/main.go](../control-plane/main.go)
- [deploy/k8s.yaml](../deploy/k8s.yaml)

Required changes:
- Add auth middleware in front of all mutating and admin endpoints. Done.
- Require a bearer token or mTLS-backed identity for control-plane writes. Bearer-token path done.
- Store auth material in Kubernetes `Secret`s, not plaintext env values. Done.

Definition of done:
- Unauthenticated requests to `POST`, `PUT`, and `DELETE` admin endpoints return `401` or `403`.
- Auth configuration is injected from Kubernetes secrets.

### 2. Add TLS And Controlled External Exposure (Implemented In Manifest)

Target files:
- [deploy/k8s.yaml](../deploy/k8s.yaml)
- optional new ingress manifest under [deploy](../deploy)

Required changes:
- Replace direct external exposure patterns with an ingress controller or managed load balancer. Done.
- Terminate TLS at ingress. Done.
- Keep Prometheus and Redis internal-only.
- Stop using raw `NodePort` for Grafana unless this remains explicitly dev-only. Done.

Definition of done:
- External traffic enters through HTTPS.
- Internal services are not directly exposed outside the cluster.

### 3. Make Stateful Components Durable (Completed In Manifest)

Target files:
- [deploy/k8s.yaml](../deploy/k8s.yaml)

Required changes:
- Replace `emptyDir` for Redis, Prometheus, and Grafana with `PersistentVolumeClaim`s. Done.
- Decide whether the control-plane `DATA_DIR` mount is still needed in cluster mode; if not, remove it.
- If Redis is production-critical, consider a managed Redis service or a hardened Redis StatefulSet.

Definition of done:
- Pod restarts do not wipe Redis data, dashboards, or metrics history.

### 4. Publish Real Container Images (Implemented With CI)

Target files:
- [deploy/k8s.yaml](../deploy/k8s.yaml)
- [README.md](../README.md)

Required changes:
- Push images to a registry. Done.
- Replace `control-plane:latest` and `microservice-demo:latest` with immutable versioned tags. Done in manifest.
- Add image pull credentials only if the registry is private.
- Enforce signature verification/admission policy in cluster (recommended next hardening step).

Definition of done:
- A clean cluster can deploy without preloading local images.

### 5. Move Plaintext Configuration Into Secrets And ConfigMaps

Target files:
- [deploy/k8s.yaml](../deploy/k8s.yaml)

Required changes:
- Move the Grafana admin password out of the manifest.
- Store any future auth tokens, API keys, or signing secrets in Kubernetes `Secret`s.
- Keep non-sensitive configuration in `ConfigMap`s.

Definition of done:
- No plaintext credentials remain in source-controlled manifests.

### 6. Add Kubernetes Safety Controls

Target files:
- [deploy/k8s.yaml](../deploy/k8s.yaml)

Required changes:
- Add `securityContext` to containers and pods. Done.
- Add `PodDisruptionBudget` for the control plane. Done.
- Add `NetworkPolicy` so only required traffic flows are allowed. Done.
- Add `ServiceAccount`s if you later integrate cloud APIs.
- Add autoscaling if expected traffic justifies it.

Definition of done:
- Basic least-privilege and disruption controls are enforced.

### 7. Add Production Alerting

Target files:
- [deploy/k8s.yaml](../deploy/k8s.yaml)
- [deploy/grafana-dashboard.json](../deploy/grafana-dashboard.json)

Required changes:
- Add Prometheus alert rules for:
  - high 5xx rate
  - sustained throttling
  - circuit breaker open state
  - control-plane replica unavailability
- Add routing to Alertmanager or your external alerting stack. Baseline in-cluster routing is now configured.
- Add secret-backed receiver configuration and operations runbook. Baseline done.

Definition of done:
- Production-impacting conditions emit alerts without requiring dashboard inspection.

### 8. Clean Up SDK Packaging

Target files:
- [sdk/featureflags_sdk.py](../sdk/featureflags_sdk.py)
- optional dependency manifest under [sdk](../sdk)

Required changes:
- Document or package the Python SSE dependency explicitly.
- Decide whether Python/JS SDKs are production-supported artifacts or sample clients.

Definition of done:
- SDK installation steps are explicit and reproducible.

## Suggested Execution Order

1. Authentication, secrets, and ingress/TLS
2. Durable storage and real image publishing
3. Kubernetes hardening controls
4. Alerting and operational polish

## CI/CD Deployment Pipeline

Implemented pipeline:

- Workflow: `.github/workflows/publish-images.yml`
- Stages:
  - `test`: module tests for control-plane and microservice-demo
  - `build-scan-and-push`: image build, vulnerability scan (Trivy, CRITICAL), push to GHCR, keyless Cosign signing
  - `deploy`: optional manual deploy with rollout verification and smoke tests

Deployment trigger:

- Use `workflow_dispatch` with `deploy=true` to run cluster deployment.
- Required repository secrets for deploy stage:
  - `KUBE_CONFIG` (base64-encoded kubeconfig)
  - `CONTROL_PLANE_AUTH_TOKEN` (for authenticated smoke writes)

Deployment behavior:

- Applies `deploy/k8s.yaml`
- Pins running deployments to the newly published immutable SHA tag
- Waits for `kubectl rollout status` on both workloads
- Port-forwards both services and executes `deploy/smoke-test.sh`

## Rollback Strategy (Playbook)

Use immutable tags and keep at least one known-good prior release tag per service.

### 1) Capture current and previous image tags

```bash
kubectl -n flagplane get deploy control-plane -o=jsonpath='{.spec.template.spec.containers[0].image}'
kubectl -n flagplane get deploy microservice-demo -o=jsonpath='{.spec.template.spec.containers[0].image}'
```

Record:

- `CURRENT_CONTROL_IMAGE`
- `CURRENT_DEMO_IMAGE`
- `PREVIOUS_CONTROL_IMAGE`
- `PREVIOUS_DEMO_IMAGE`

### 2) Fast rollback via rollout history

```bash
kubectl -n flagplane rollout undo deployment/control-plane
kubectl -n flagplane rollout undo deployment/microservice-demo
kubectl -n flagplane rollout status deployment/control-plane --timeout=180s
kubectl -n flagplane rollout status deployment/microservice-demo --timeout=180s
```

### 3) Explicit rollback to known-good image tags

```bash
kubectl -n flagplane set image deployment/control-plane control-plane=<PREVIOUS_CONTROL_IMAGE>
kubectl -n flagplane set image deployment/microservice-demo microservice-demo=<PREVIOUS_DEMO_IMAGE>
kubectl -n flagplane rollout status deployment/control-plane --timeout=180s
kubectl -n flagplane rollout status deployment/microservice-demo --timeout=180s
```

### 4) Post-rollback validation

```bash
kubectl -n flagplane get pods
kubectl -n flagplane get events --sort-by=.lastTimestamp | tail -n 20
```

Run smoke tests:

- Local/port-forwarded: `pwsh deploy/smoke-test.ps1` or `bash deploy/smoke-test.sh`
- Required checks: `/health`, `/demo/health`, feature-flag evaluation, rate-limit check, `/metrics`

## Minimum Production Candidate Bar

Do not call this production-ready until all of the following are true:
- Control-plane writes are authenticated
- External traffic uses TLS
- Stateful services use durable storage
- Images are registry-hosted and version-pinned
- Plaintext credentials are removed from manifests
- At least basic alerting exists for service failure and breaker/throttling conditions

## External Ops Control Bundle

The repository now includes an implementation bundle for external production controls:

- Overlay and policy manifests: [deploy/production](../deploy/production)
- Rollout guide: [docs/external-ops-controls.md](external-ops-controls.md)
- Secret rotation ownership/runbook: [docs/secrets-lifecycle-runbook.md](secrets-lifecycle-runbook.md)
- DNS and certificate automation guide: [docs/dns-tls-automation.md](dns-tls-automation.md)
- Backup/restore and retention runbook: [docs/backup-restore-retention-runbook.md](backup-restore-retention-runbook.md)

Status:

- Repo implementation: complete
- Environment rollout: pending cluster-specific values and approvals

## Operational Go-Live Workflow

Use these repository scripts to complete the remaining production steps:

1. Create a real values file from template:

```powershell
Copy-Item deploy/production/prod-values.example.psd1 deploy/production/prod-values.psd1
```

2. Populate org/domain/secret-manager values in `deploy/production/prod-values.psd1`.

3. Dry-run render and validate values:

```powershell
powershell -ExecutionPolicy Bypass -File deploy/production/go-live.ps1 -ValuesFile deploy/production/prod-values.psd1 -Environment staging
```

This command performs a kustomize render using `--load-restrictor=LoadRestrictionsNone` and fails fast if rendering is invalid.

4. Apply in staging and capture evidence:

```powershell
powershell -ExecutionPolicy Bypass -File deploy/production/go-live.ps1 -ValuesFile deploy/production/prod-values.psd1 -Environment staging -Apply -CaptureEvidence
```

5. Run preflight gate before production cutover:

```powershell
powershell -ExecutionPolicy Bypass -File deploy/production/preflight-prod.ps1
```

6. Apply in production and capture evidence:

```powershell
powershell -ExecutionPolicy Bypass -File deploy/production/go-live.ps1 -ValuesFile deploy/production/prod-values.psd1 -Environment production -Apply -CaptureEvidence
```

7. Complete final approvals in `docs/production-signoff.md`.

## Live Validation Snapshot (2026-03-27)

Runtime smoke and stress checks were executed against local running services.

Observed circuit-breaker behavior for route `/demo/action`:
- After a burst of failing (`fail=true`) and slow (`sleepMs=450`) requests, breaker state reported `open`.
- Timed probe sampled breaker state once per second for 13 samples (`t=0` through `t=12`) and remained `open` for the full observed window.
- A subsequent healthy action request returned with `cbState=closed` and low latency, showing recovery to closed on healthy traffic.

Operational note:
- The measured data confirms trip and recovery behavior in a live run.
- Keep alerting thresholds and recovery expectations aligned with this behavior profile during production tuning.