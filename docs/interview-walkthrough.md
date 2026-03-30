# Interview Walkthrough

This is a concise script to present the project in a senior backend interview.

## 1) Problem And Scope

I built a distributed feature-control platform for microservices that combines:

- Feature flags with targeting and percentage rollout
- A/B experiments with deterministic assignment and conversion tracking
- Route/user/tenant-aware rate limiting
- Circuit breaking driven by error and latency signals
- Distributed configuration and hot reload
- Observability with metrics, dashboards, and alerting foundations

## 2) Architecture Summary

Main components:

- Control plane service (Go): authoritative config and decision APIs
- Demo microservice (Go): integrates control APIs and enforces runtime policies
- SDK (Go + sample JS/Python clients): flag fetch/eval, experiment calls, and updates
- Redis-backed shared config in Kubernetes, local JSON persistence fallback in dev
- Prometheus and Grafana for metrics and dashboarding

## 3) Critical Design Decisions

- Deterministic assignment: FNV-style hashing gives stable experiment and rollout behavior for each user
- Fail-open tradeoffs in client paths: keeps serving traffic when control plane is transiently unavailable
- Auth boundary: write/admin endpoints protected with bearer token in runtime configs
- Two-mode config store: no external dependency for local dev, shared backend for multi-replica cluster mode

## 4) Reliability And Safety

- Rate limiter: token bucket per route/user/tenant key
- Circuit breaker states: closed, open, half-open with state metrics
- Health probes and resource limits in Kubernetes manifests
- Stateful components use PVC-backed storage in cluster manifests

## 5) Observability Story

What to demonstrate live:

- /metrics from both services
- Feature evaluation and experiment exposure counters
- Throttle and circuit breaker state changes
- Dashboard import from deploy/grafana-dashboard.json
- Smoke test and runbook-guided operations

## 6) Traffic Capture And Replay

I added demo-level traffic capture and replay endpoints to support controlled reliability testing:

- Capture recent demo requests
- Replay selected request samples against live endpoint
- Summarize replay outcomes and status distribution

This is useful for pre-release confidence checks and incident reproduction in lower environments.

## 7) Production Readiness Status

Current repo is deployable for demos and local clusters.
Production candidate requires environment rollout completion:

- Real org/domain/secret values
- Staging to production promotion evidence
- Final operational approvals

Reference: docs/production-readiness.md

## 8) Senior-Level Discussion Prompts

Use these prompts if asked for deeper tradeoffs:

- How to evolve from bearer token auth to mTLS/workload identity
- How to move from Redis single-instance to managed HA Redis
- How to make replay safer with sampling, masking, and idempotency guards
- How to add SLOs and error budget policies to deployment gates
- How to design backward-compatible API versioning for SDK consumers
