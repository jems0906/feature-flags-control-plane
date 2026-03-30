# Release Checklist

Use this checklist before creating a release tag.

## 1) Source And Tests

- [ ] Control-plane tests pass
- [ ] Demo tests pass
- [ ] SDK module builds
- [ ] go vet passes for all Go modules
- [ ] gofmt clean in all Go modules

Suggested commands:

```powershell
Set-Location control-plane; go test ./... -v; go vet ./...; gofmt -w .
Set-Location ../microservice-demo; go test ./... -v; go vet ./...; gofmt -w .
Set-Location ../sdk; go test ./... -v; go vet ./...; gofmt -w .
```

## 2) Local Runtime Validation

- [ ] Control-plane health endpoint responds
- [ ] Demo health endpoint responds
- [ ] One-command local smoke test passes

Suggested commands:

```powershell
Invoke-WebRequest -UseBasicParsing http://localhost:8080/health
Invoke-WebRequest -UseBasicParsing http://localhost:8081/demo/health
powershell -ExecutionPolicy Bypass -File deploy/smoke-test.ps1
```

## 3) Security And Config

- [ ] No plaintext production credentials committed
- [ ] Kubernetes secrets planned for auth and Grafana admin password
- [ ] TLS secret plan documented for target cluster
- [ ] Image tags are immutable (no latest-only deploys)

## 4) Container And Deploy Assets

- [ ] Control-plane image builds from deploy/Dockerfile.control-plane
- [ ] Demo image builds from deploy/Dockerfile.microservice-demo
- [ ] deploy/k8s.yaml references the intended image tags
- [ ] Required pre-deploy secrets and DNS entries are prepared

## 5) Observability And Operations

- [ ] /metrics endpoints available for both services
- [ ] Grafana dashboard file present and reviewed
- [ ] Alert rules and runbook links verified
- [ ] Rollback commands reviewed in docs/production-readiness.md

## 6) Docs And Handoff

- [ ] docs/quickstart.md validated against current API behavior
- [ ] docs/api.md aligned with endpoint contracts
- [ ] Known limitations and environment assumptions documented
- [ ] Release notes summarize key changes

## 7) Tag And Publish

- [ ] Create version tag (example: v1.1.0)
- [ ] Push tag
- [ ] Verify CI publish workflow succeeded
- [ ] Record image digests in release notes

Suggested commands:

```powershell
git tag v1.1.0
git push origin v1.1.0
```
