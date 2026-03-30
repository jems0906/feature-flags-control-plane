$ErrorActionPreference = "Stop"

$ControlPlaneUrl = if ($env:CONTROL_PLANE_URL) { $env:CONTROL_PLANE_URL } else { "http://localhost:8080" }
$DemoUrl = if ($env:DEMO_URL) { $env:DEMO_URL } else { "http://localhost:8081" }
$AuthToken = $env:AUTH_TOKEN

function Log([string]$msg) {
  Write-Host "[smoke] $msg"
}

function Assert-True([bool]$condition, [string]$message) {
  if (-not $condition) {
    throw "[smoke] FAIL: $message"
  }
}

$headers = @{}
if ($AuthToken) {
  $headers["Authorization"] = "Bearer $AuthToken"
}

$flagName = "smoke-$([DateTimeOffset]::UtcNow.ToUnixTimeSeconds())"

try {
Log "Control plane health"
$cpHealth = Invoke-RestMethod -Method Get -Uri "$ControlPlaneUrl/health"
Assert-True ($cpHealth.status -eq "ok") "control plane /health did not return ok"

Log "Demo health"
$demoHealth = Invoke-RestMethod -Method Get -Uri "$DemoUrl/demo/health"
Assert-True ($demoHealth.status -eq "ok") "demo /demo/health did not return ok"

Log "Create smoke feature flag"
$createBody = @{
  Name = $flagName
  Enabled = $true
  Environment = "dev"
  TargetRules = @(@{ Type = "user"; Value = "smoke-user" })
} | ConvertTo-Json -Depth 5
Invoke-RestMethod -Method Post -Uri "$ControlPlaneUrl/flags" -Headers $headers -ContentType "application/json" -Body $createBody | Out-Null

Log "Evaluate smoke feature flag"
$evalBody = @{ UserID = "smoke-user"; Headers = @{} } | ConvertTo-Json
$evalResp = Invoke-RestMethod -Method Post -Uri "$ControlPlaneUrl/flags/$flagName/evaluate" -ContentType "application/json" -Body $evalBody
Assert-True ($evalResp.enabled -eq $true) "expected smoke flag evaluation enabled=true"

Log "Create smoke experiment"
$experimentBody = @{ Name = "button-color"; Variants = @("blue", "green") } | ConvertTo-Json
Invoke-RestMethod -Method Post -Uri "$ControlPlaneUrl/experiment" -Headers $headers -ContentType "application/json" -Body $experimentBody | Out-Null

Log "Experiment variant assignment"
$variantResp = Invoke-RestMethod -Method Get -Uri "$ControlPlaneUrl/experiment/button-color/variant?userId=smoke-user"
Assert-True ([string]::IsNullOrWhiteSpace($variantResp.variant) -eq $false) "missing variant in experiment response"

Log "Rate limit check"
$rateBody = @{ route = "/demo/action"; userId = "smoke-user" } | ConvertTo-Json
$rateResp = Invoke-RestMethod -Method Post -Uri "$ControlPlaneUrl/ratelimit/check" -ContentType "application/json" -Body $rateBody
Assert-True ($rateResp.allowed -eq $true) "expected rate limit allowed=true"

Log "Demo action"
$actionResp = Invoke-RestMethod -Method Post -Uri "$DemoUrl/demo/action?userId=smoke-user"
Assert-True ($actionResp.result -eq "action performed") "demo action call did not return expected body"

Log "Control plane metrics"
$cpMetrics = Invoke-WebRequest -Method Get -Uri "$ControlPlaneUrl/metrics" -UseBasicParsing
Assert-True ($cpMetrics.Content -match "control_plane_requests_total") "control-plane metric control_plane_requests_total missing"
Assert-True ($cpMetrics.Content -match "feature_flag_evaluations_total") "control-plane metric feature_flag_evaluations_total missing"

Log "Demo metrics"
$demoMetrics = Invoke-WebRequest -Method Get -Uri "$DemoUrl/metrics" -UseBasicParsing
Assert-True ($demoMetrics.Content -match "demo_action_ok") "demo metric demo_action_ok missing"

Log "Smoke test PASSED"
} finally {
  if ($flagName) {
    try {
      Invoke-RestMethod -Method Delete -Uri "$ControlPlaneUrl/flags/$flagName" -Headers $headers | Out-Null
      Log "Cleaned up temporary flag $flagName"
    } catch {
      Write-Host "[smoke] cleanup warning: unable to delete temporary flag $flagName"
    }
  }
}
