# Quickstart Guide — Feature Flag & Traffic Control Platform

Get the full platform running locally in under 5 minutes.

---

## Prerequisites

- Go 1.22+ (`go version`)
- Docker & Docker Compose (optional, for containerised stack)
- `curl` or any HTTP client

---

## 1. Run the Control Plane

```bash
cd control-plane
go run .
# Listening on :8080
```

The control plane creates a `./data/` directory and persists all state there as JSON.

### Verify

```bash
curl http://localhost:8080/health
# {"status":"ok"}
```

---

## 2. Create a Feature Flag

```bash
curl -X POST http://localhost:8080/flags \
  -H "Content-Type: application/json" \
  -d '{
    "name": "dark-mode",
    "enabled": true,
    "environment": "production",
    "target_rules": [
      { "type": "percentage", "rollout": 50 }
    ]
  }'
```

### Evaluate the flag for a user

```bash
curl -X POST http://localhost:8080/flags/dark-mode/evaluate \
  -H "Content-Type: application/json" \
  -d '{ "user_id": "alice", "tenant_id": "", "headers": {} }'
# {"enabled":true}
```

Evaluation is deterministic — the same user_id always gets the same result.

---

## 3. Create an A/B Experiment

```bash
curl -X POST http://localhost:8080/experiments \
  -H "Content-Type: application/json" \
  -d '{ "name": "button-color", "variants": ["blue", "green", "orange"] }'
```

### Assign a variant

```bash
curl "http://localhost:8080/experiment/button-color/variant?user_id=alice"
# {"variant":"green"}
```

### Record a conversion

```bash
curl -X POST http://localhost:8080/experiment/button-color/conversion \
  -d '{ "variant": "green" }'
```

---

## 4. Configure Rate Limiting

```bash
curl -X POST http://localhost:8080/ratelimit \
  -H "Content-Type: application/json" \
  -d '{ "route": "/demo/action", "user_id": "", "tenant_id": "", "limit": 20 }'
```

### Check if a request is allowed

```bash
curl "http://localhost:8080/ratelimit/%2Fdemo%2Faction/check?user_id=alice&tenant_id="
# {"allowed":true}
```

---

## 5. Manage Circuit Breakers

```bash
curl -X POST "http://localhost:8080/circuitbreaker?route=/demo/action&state=open"
curl "http://localhost:8080/circuitbreaker/%2Fdemo%2Faction"
# {"route":"/demo/action","state":"open","error_threshold":0.5,"latency_ms":0}
curl -X POST "http://localhost:8080/circuitbreaker?route=/demo/action&state=closed"
```

---

## 6. Store Arbitrary Config

```bash
curl -X POST http://localhost:8080/config \
  -d '{ "key": "payments.timeout_ms", "value": "3000" }'
curl http://localhost:8080/config/payments.timeout_ms
# {"key":"payments.timeout_ms","value":"3000"}
```

---

## 7. Real-Time Flag Updates via SSE

Open a terminal and subscribe to changes:

```bash
curl -N "http://localhost:8080/flags/stream?env=production"
```

In another terminal, update the flag:

```bash
curl -X PUT http://localhost:8080/flags/dark-mode \
  -d '{ "name": "dark-mode", "enabled": false, "environment": "production" }'
```

The SSE stream immediately emits the new flag state.

---

## 8. Run the Microservice Demo

```bash
cd microservice-demo
go run .
# Listening on :8081
```

The demo seeds flags, experiments, and rate limits on startup, then exposes:

| Endpoint | Description |
|----------|-------------|
| GET /demo/hello?user_id=alice | Greeting with dark-mode flag |
| GET /demo/experiment?user_id=alice | Returns assigned button-color variant |
| POST /demo/action?user_id=alice | Rate-limited and circuit-broken action |
| GET /metrics | Prometheus metrics |

---

## 9. Observe Metrics

```bash
curl http://localhost:8080/metrics
curl http://localhost:8081/metrics
```

---

## 10. Run Unit Tests

```bash
cd control-plane
go test ./... -v
```

---

## 11. Deploy to Kubernetes

```bash
docker build -f deploy/Dockerfile.control-plane   -t control-plane:latest   .
docker build -f deploy/Dockerfile.microservice-demo -t microservice-demo:latest .
kubectl apply -f deploy/k8s.yaml
```

Access Grafana at http://<node-ip>:30300 (admin/admin).
Import deploy/grafana-dashboard.json for the pre-built dashboard.