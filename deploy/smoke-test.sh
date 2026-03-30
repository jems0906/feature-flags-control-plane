#!/usr/bin/env bash
set -euo pipefail

CONTROL_PLANE_URL="${CONTROL_PLANE_URL:-http://localhost:8080}"
DEMO_URL="${DEMO_URL:-http://localhost:8081}"
AUTH_TOKEN="${AUTH_TOKEN:-}"

log() {
  echo "[smoke] $*"
}

fail() {
  echo "[smoke] FAIL: $*" >&2
  exit 1
}

json_contains_true() {
  local body="$1"
  local key="$2"
  echo "$body" | tr -d '[:space:]' | grep -q "\"$key\":true"
}

header_args=()
if [[ -n "$AUTH_TOKEN" ]]; then
  header_args=(-H "Authorization: Bearer $AUTH_TOKEN")
fi

flag_name="smoke-$(date +%s)"

cleanup() {
  if [[ -n "${flag_name:-}" ]]; then
    curl -fsS -X DELETE "${header_args[@]}" "$CONTROL_PLANE_URL/flags/$flag_name" >/dev/null 2>&1 || true
    echo "[smoke] Cleaned up temporary flag $flag_name"
  fi
}
trap cleanup EXIT

log "Control plane health"
cp_health="$(curl -fsS "$CONTROL_PLANE_URL/health")"
[[ "$cp_health" == *"ok"* ]] || fail "control plane /health did not return ok"

log "Demo health"
demo_health="$(curl -fsS "$DEMO_URL/demo/health")"
[[ "$demo_health" == *"ok"* ]] || fail "demo /demo/health did not return ok"

log "Create smoke feature flag"
curl -fsS -X POST "${header_args[@]}" -H "Content-Type: application/json" \
  -d "{\"Name\":\"$flag_name\",\"Enabled\":true,\"Environment\":\"dev\",\"TargetRules\":[{\"Type\":\"user\",\"Value\":\"smoke-user\"}]}" \
  "$CONTROL_PLANE_URL/flags" >/dev/null

log "Evaluate smoke feature flag"
flag_eval="$(curl -fsS -X POST -H "Content-Type: application/json" -d '{"UserID":"smoke-user"}' "$CONTROL_PLANE_URL/flags/$flag_name/evaluate")"
json_contains_true "$flag_eval" "enabled" || fail "expected smoke flag evaluation enabled=true"

log "Create smoke experiment"
curl -fsS -X POST "${header_args[@]}" -H "Content-Type: application/json" \
  -d '{"Name":"button-color","Variants":["blue","green"]}' \
  "$CONTROL_PLANE_URL/experiment" >/dev/null

log "Experiment variant assignment"
variant_resp="$(curl -fsS "$CONTROL_PLANE_URL/experiment/button-color/variant?userId=smoke-user")"
[[ "$variant_resp" == *"variant"* ]] || fail "missing variant in experiment response"

log "Rate limit check"
rate_resp="$(curl -fsS -X POST -H "Content-Type: application/json" -d '{"route":"/demo/action","userId":"smoke-user"}' "$CONTROL_PLANE_URL/ratelimit/check")"
json_contains_true "$rate_resp" "allowed" || fail "expected rate limit allowed=true"

log "Demo action"
action_resp="$(curl -fsS -X POST "$DEMO_URL/demo/action?userId=smoke-user")"
[[ "$action_resp" == *"action performed"* ]] || fail "demo action call did not return expected body"

log "Control plane metrics"
cp_metrics="$(curl -fsS "$CONTROL_PLANE_URL/metrics")"
[[ "$cp_metrics" == *"control_plane_requests_total"* ]] || fail "control-plane metric control_plane_requests_total missing"
[[ "$cp_metrics" == *"feature_flag_evaluations_total"* ]] || fail "control-plane metric feature_flag_evaluations_total missing"

log "Demo metrics"
demo_metrics="$(curl -fsS "$DEMO_URL/metrics")"
[[ "$demo_metrics" == *"demo_action_ok"* ]] || fail "demo metric demo_action_ok missing"

log "Smoke test PASSED"
