# Distributed Feature Flag & Traffic Control Platform

## Overview
A production-grade platform providing feature flags, A/B testing, rate limiting, and circuit breaking for microservices, with centralized configuration and full observability. The control plane is written in Go 1.22, uses a local JSON store for single-node development, and switches to Redis for shared multi-replica config in Kubernetes.

### Structure
```
control-plane/   Go 1.22 REST API server (flags, experiments, rate limits, circuit breakers, config, SSE)
sdk/             Go client library (polling, SSE hot-reload, typed helpers)
microservice-demo/ Sample service demonstrating full platform integration
deploy/          Dockerfiles, Kubernetes manifests, Prometheus config, Grafana dashboard
docs/            API reference and quickstart guide
```

## Requirements
- **Go 1.22+** — `go version`
- **Docker + kubectl** (optional, for Kubernetes deploy)
- No external services required for local development
- Redis is provisioned by `deploy/k8s.yaml` for multi-replica shared state
- A container registry for production deployment (example: GHCR, ACR, ECR)
- An ingress controller in the cluster (example: ingress-nginx)
- A CNI plugin that enforces `NetworkPolicy`

## Quick Start

PowerShell:

```powershell
# Terminal 1 - control plane
cd control-plane
$env:CONTROL_PLANE_AUTH_TOKEN="local-dev-token"
go run .

# Terminal 2 - demo microservice
cd microservice-demo
$env:CONTROL_PLANE_AUTH_TOKEN="local-dev-token"
go run .
```

One-command local verification:

```powershell
$env:AUTH_TOKEN="local-dev-token"
powershell -ExecutionPolicy Bypass -File deploy/smoke-test.ps1
```

Expected local endpoints:
- control plane landing page: http://localhost:8080/
- demo landing page: http://localhost:8081/
- control plane health: http://localhost:8080/health
- demo health: http://localhost:8081/demo/health

See [docs/quickstart.md](docs/quickstart.md) for the full walkthrough.

## Features
- **Feature Flags** — CRUD, per-user/tenant/header/percentage targeting rules, environments
- **A/B Experiments** — deterministic FNV-hash variant bucketing, conversion tracking
- **Rate Limiting** — thread-safe token bucket with route, user, and tenant-scoped overrides that update live
- **Circuit Breaker** — 3-state machine with explicit runtime admission checks and single-probe half-open recovery
- **Traffic Capture + Replay (Demo)** — capture live demo requests and replay them against `/demo/action` for reliability testing
- **Distributed Config** — local file store for dev, Redis-backed shared config for multi-replica control planes
- **Replica Sync + SSE Hot Reload** — control-plane replicas poll the shared store and broadcast full flag payloads over `GET /flags/stream?env=production`
- **Prometheus Metrics** — custom text-format exporter, scrape-compatible, no client library needed
- **Tracing** — OTel-compatible span logger; swap `Tracer` variable for a real SDK in production

## Deploy to Kubernetes
Pre-deploy fail-fast checks:
- `platform-secrets` exists in namespace `flagplane` and contains both keys: `controlPlaneAuthToken` and `grafanaAdminPassword`.
- `alerting-secrets` exists in namespace `flagplane` and contains key: `webhook-url`.
- TLS secret `flagplane-tls` exists in namespace `flagplane`.
- Control-plane and demo image references in `deploy/k8s.yaml` are not placeholders (`ghcr.io/your-org/...`) unless that is your real registry path.
- DNS/hosts map `api.flagplane.local`, `demo.flagplane.local`, and `grafana.flagplane.local` to your ingress controller.

```bash
kubectl create namespace flagplane --dry-run=client -o yaml | kubectl apply -f -

kubectl create secret generic platform-secrets \
  -n flagplane \
  --from-literal=controlPlaneAuthToken='<strong-random-token>' \
  --from-literal=grafanaAdminPassword='<strong-random-password>' \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl create secret tls flagplane-tls \
  -n flagplane \
  --cert=/path/to/tls.crt \
  --key=/path/to/tls.key \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl create secret generic alerting-secrets \
  -n flagplane \
  --from-literal=webhook-url='https://your-alert-endpoint.example/webhook' \
  --dry-run=client -o yaml | kubectl apply -f -

docker build -f deploy/Dockerfile.control-plane -t ghcr.io/your-org/control-plane:v1.0.0 .
docker build -f deploy/Dockerfile.microservice-demo -t ghcr.io/your-org/microservice-demo:v1.0.0 .
docker push ghcr.io/your-org/control-plane:v1.0.0
docker push ghcr.io/your-org/microservice-demo:v1.0.0

# Optional: update image names in deploy/k8s.yaml if you use a different registry/org/tag.
kubectl apply -f deploy/k8s.yaml
# Add host mappings for api.flagplane.local, demo.flagplane.local, and grafana.flagplane.local.
# Then access:
# https://api.flagplane.local
# https://demo.flagplane.local
# https://grafana.flagplane.local
# Import deploy/grafana-dashboard.json
```

The Kubernetes deployment runs two control-plane replicas against a shared Redis service and uses PVC-backed storage for Redis, Prometheus, and Grafana.

The manifest also includes baseline hardening: dedicated service accounts, pod/container security contexts, a control-plane PodDisruptionBudget, and namespace-scoped network policies.

Baseline alerting is included: Prometheus rule evaluation plus Alertmanager wiring. Critical alerts are routed through a secret-backed webhook URL (`alerting-secrets` / `webhook-url`).

Alerting operations guide: [docs/alerting-runbook.md](docs/alerting-runbook.md)

In Kubernetes, `CONTROL_PLANE_AUTH_TOKEN` is required by the manifest and write/admin control-plane endpoints require `Authorization: Bearer <token>`.

## Production Readiness

This repository is deployable for local clusters and demos.

For the minimum changes needed to reach production-candidate status, see [docs/production-readiness.md](docs/production-readiness.md).

Release and handoff helpers:
- [docs/release-checklist.md](docs/release-checklist.md)
- [docs/interview-walkthrough.md](docs/interview-walkthrough.md)

For production external operations controls (registry overrides, secret lifecycle, admission policy, DNS/TLS automation, backup schedules, and on-call routing), use [docs/external-ops-controls.md](docs/external-ops-controls.md) and apply the overlay in `deploy/production`.

Use `deploy/production/set-prod-values.ps1` to replace production placeholder values before applying the overlay.

## CI Image Publishing

Automated image build/test/push is defined in [.github/workflows/publish-images.yml](.github/workflows/publish-images.yml).

Workflow behavior:
- Runs module tests for `control-plane` and `microservice-demo`.
- Runs SDK regression tests for Go, Python, and JavaScript clients.
- Builds and pushes both images to GHCR on `main`, tags, or manual dispatch.
- Signs pushed images with keyless Cosign using GitHub OIDC.

Published image naming:
- `ghcr.io/<owner>/control-plane:<tag-or-sha>`
- `ghcr.io/<owner>/microservice-demo:<tag-or-sha>`

## Run Tests
```bash
cd control-plane && go test ./... -v
cd ../microservice-demo && go test ./... -v
cd ../sdk && go test ./... -v
pip install requests sseclient-py
python featureflags_sdk_test.py
npm install
node featureflags_sdk.test.js
```