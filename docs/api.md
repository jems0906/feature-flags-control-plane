# API Reference — Feature Flag & Traffic Control Platform

Base URL (local dev): `http://localhost:8080`

All request/response bodies are JSON (`Content-Type: application/json`).
All endpoints return HTTP `200` on success, `400` for bad input, `404` when a resource does not exist, and `500` for internal errors.

When `CONTROL_PLANE_AUTH_TOKEN` is configured, write/admin endpoints require:

```http
Authorization: Bearer <token>
```

---

## Health

### `GET /health`

Returns service health status.

**Response**
```json
{ "status": "ok" }
```

---

## Feature Flags

### `POST /flags`

Auth required when `CONTROL_PLANE_AUTH_TOKEN` is set.

Create or update a feature flag.

**Request body**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Unique flag identifier |
| `enabled` | bool | yes | Global on/off switch |
| `environment` | string | yes | e.g. `production`, `staging` |
| `target_rules` | array | no | Targeting rules (see below) |

**TargetRule fields**

| Field | Type | Description |
|-------|------|-------------|
| `type` | string | `"user"`, `"tenant"`, `"header"`, `"percentage"` |
| `value` | string | Match value (user ID, tenant ID, header `key=value`, or ignored for percentage) |
| `rollout` | float | 0–100 percentage (only used when `type = "percentage"`) |

**Example**
```bash
curl -X POST http://localhost:8080/flags \
  -H "Content-Type: application/json" \
  -d '{
    "name": "dark-mode",
    "enabled": true,
    "environment": "production",
    "target_rules": [
      { "type": "tenant", "value": "acme-corp" },
      { "type": "percentage", "rollout": 25 }
    ]
  }'
```

---

### `GET /flags`

Use `GET /flags/all?env={env}` to list flags. Omit `env` to return every environment.

**Response** — array of flag objects.

---

### `GET /flags/all?env={env}`

List all feature flags.

**Response** — array of flag objects.

---

### `GET /flags/{name}`

Get a single flag by name.

Returns `404` when the flag does not exist.

**Response**
```json
{
  "name": "dark-mode",
  "enabled": true,
  "environment": "production",
  "target_rules": [...]
}
```

---

### `PUT /flags/{name}`

Auth required when `CONTROL_PLANE_AUTH_TOKEN` is set.

Update an existing flag. Full replacement — include all fields.

---

### `DELETE /flags/{name}`

Auth required when `CONTROL_PLANE_AUTH_TOKEN` is set.

Delete a flag.

---

### `POST /flags/{name}/evaluate`

Evaluate a flag for a specific user context.

**Request body**

| Field | Type | Description |
|-------|------|-------------|
| `UserID` | string | End-user identifier |
| `TenantID` | string | Tenant identifier |
| `Headers` | object | Map of HTTP header key → value |

**Response**
```json
{ "enabled": true }
```

**Example**
```bash
curl -X POST http://localhost:8080/flags/dark-mode/evaluate \
  -d '{ "UserID": "u123", "TenantID": "acme-corp", "Headers": {} }'
```

---

### `GET /flags/stream?env={env}`

Server-Sent Events — streams real-time flag change notifications for the specified environment. Use `env=all` to subscribe to every environment.

**Events**
```
event: update
data: [{"Name":"dark-mode","Enabled":true,"Environment":"production",...}]
```

Connect with `EventSource` in the browser or any SSE client.

---

## A/B Experiments

### `POST /experiment`

Auth required when `CONTROL_PLANE_AUTH_TOKEN` is set.

Create or update an experiment.

**Request body**

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Unique experiment identifier |
| `variants` | array of string | Variant names (e.g. `["control","treatment"]`) |

---

### `GET /experiment/{name}/variant?userId={uid}`

Get the assigned variant for a user. Assignment is deterministic and stable for the same `(userId, experiment_name)` pair.

**Response**
```json
{ "variant": "treatment" }
```

---

### `POST /experiment/{name}/convert?variant={variant}`

Auth required when `CONTROL_PLANE_AUTH_TOKEN` is set.

Record a conversion event for an experiment variant.

**Response**
```json
{ "recorded": "ok", "experiment": "button-color", "variant": "green" }
```

---

## Rate Limiting

### `POST /ratelimit`

Auth required when `CONTROL_PLANE_AUTH_TOKEN` is set.

Create or update a rate limit rule.

**Request body**

| Field | Type | Description |
|-------|------|-------------|
| `route` | string | Route identifier |
| `userId` | string | User scope (empty = all users) |
| `tenantId` | string | Tenant scope (empty = all tenants) |
| `limit` | int | Requests per second |

**Example**
```bash
curl -X POST http://localhost:8080/ratelimit \
  -d '{ "route": "/api/search", "userId": "", "tenantId": "", "limit": 100 }'
```

---

### `GET /ratelimit?route={route}&userId={userId}&tenantId={tenantId}`

Check the effective rate limit config for a route and optional user or tenant scope.

Resolution order:
- exact `route + userId + tenantId`
- `route + userId`
- `route + tenantId`
- route-wide default

---

### `POST /ratelimit/check`

Check whether a request is allowed by the rate limiter.

**Request body**
```json
{ "route": "/api/search", "userId": "u123", "tenantId": "acme" }
```

**Response — allowed**
```json
{ "allowed": true }
```

**Response — throttled**
```json
{ "allowed": false }
```

---

## Circuit Breakers

### `POST /circuitbreaker`

Auth required when `CONTROL_PLANE_AUTH_TOKEN` is set.

Create or configure a circuit breaker for a route.

Provide the route either in the query string or JSON body.

**Request body**
```json
{
  "Route": "/api/search",
  "State": "closed",
  "ErrorThreshold": 0.5,
  "LatencyThresholdMs": 250
}
```

**Example (force open)**
```bash
curl -X POST "http://localhost:8080/circuitbreaker?route=/api/search&state=open"
```

---

### `POST /circuitbreaker/check`

Ask the control plane whether a request is currently allowed through the breaker.

This is the runtime admission endpoint clients should call before executing protected work. It enforces the single-probe rule while the breaker is `half-open`.

**Request body**
```json
{
  "route": "/api/search"
}
```

**Response**
```json
{
  "route": "/api/search",
  "allowed": true,
  "state": "half-open"
}
```

---

### `POST /circuitbreaker/report`

Auth required when `CONTROL_PLANE_AUTH_TOKEN` is set.

Report a request outcome to the circuit breaker so it can trip on error rate or latency.

**Request body**
```json
{
  "route": "/api/search",
  "success": false,
  "latencyMs": 380
}
```

---

### `GET /circuitbreaker?route={route}`

Get the current circuit breaker state.

**Response**
```json
{
  "route": "/api/search",
  "state": "closed",
  "errorThreshold": 0.5,
  "latencyMs": 0,
  "latencyThresholdMs": 250
}
```

States:
- `closed` — normal operation, all requests pass through
- `open` — circuit tripped; requests return `503` immediately
- `half-open` — one probe request allowed through to test recovery

---

## Configuration Store

A generic key-value store for arbitrary distributed configuration.

### `POST /config`

Auth required when `CONTROL_PLANE_AUTH_TOKEN` is set.

Set a configuration value.

**Request body**
```json
{ "key": "payments.timeout_ms", "value": "3000" }
```

---

### `GET /config/{key}`

Get a configuration value.

**Response**
```json
{ "key": "payments.timeout_ms", "value": "3000" }
```

---

## Observability

### `GET /metrics`

Prometheus text-format metrics endpoint. Scrape with Prometheus.

**Metrics exposed**

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `http_requests_total` | Counter | `route`, `method`, `status` | Total HTTP requests |
| `http_errors_total` | Counter | `route` | Total error responses |
| `http_request_duration_seconds` | Histogram | `route` | Request latency |
| `feature_flag_evaluations_total` | Counter | `flag`, `result` | Flag evaluation outcomes |
| `experiment_variant_exposures_total` | Counter | `experiment`, `variant` | Variant assignments |
| `experiment_conversion_events_total` | Counter | `experiment`, `variant` | Conversion events |
| `rate_limit_throttled_total` | Counter | `route` | Throttled request count |
| `circuit_breaker_state` | Gauge | `route` | 0=closed, 0.5=half-open, 1=open |

---

## Demo Traffic Capture And Replay

Base URL (demo service): `http://localhost:8081`

### `GET /demo/traffic/captured?limit={n}`

Return recent captured demo requests (excluding replay/admin endpoints).

**Query params**

| Field | Type | Description |
|-------|------|-------------|
| `limit` | int | Number of records to return (default 50, max 200) |

**Response**
```json
{
  "totalCaptured": 37,
  "returned": 20,
  "items": [
    {
      "id": 18,
      "timestamp": "2026-03-28T10:11:12.123Z",
      "method": "POST",
      "path": "/demo/action",
      "query": "userId=alice",
      "headers": {"Content-Type":"application/json"},
      "body": ""
    }
  ]
}
```

### `POST /demo/traffic/replay`

Replay captured requests back into the live demo service.

**Request body**

| Field | Type | Description |
|-------|------|-------------|
| `path` | string | Endpoint path to replay (default `/demo/action`) |
| `method` | string | HTTP method to replay (default `POST`) |
| `limit` | int | Max captured requests to replay (default 20, max 500) |
| `delayMs` | int | Delay between replay requests in milliseconds |

**Example**
```bash
curl -X POST http://localhost:8081/demo/traffic/replay \
  -H "Content-Type: application/json" \
  -d '{"path":"/demo/action","method":"POST","limit":10,"delayMs":25}'
```

**Response**
```json
{
  "path": "/demo/action",
  "method": "POST",
  "attempted": 10,
  "successful": 10,
  "failed": 0,
  "statusCounts": {
    "200": 8,
    "429": 2
  }
}
```
