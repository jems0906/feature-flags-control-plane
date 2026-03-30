param(
  [string]$Namespace = "flagplane"
)

$ErrorActionPreference = "Stop"
$failed = $false

try {
  kubectl version --client | Out-Null
  Write-Host "[PASS] kubectl connectivity" -ForegroundColor Green
} catch {
  $failed = $true
  Write-Host "[FAIL] kubectl connectivity :: $($_.Exception.Message)" -ForegroundColor Red
}

try {
  $rendered = kubectl kustomize --load-restrictor=LoadRestrictionsNone deploy/production
  $placeholderPattern = "YOUR_ORG|YOUR_REPO|YOUR-KEYVAULT-NAME|YOUR_PAGERDUTY_ROUTING_KEY|flagplane\.example\.com|platform-ops@example\.com|your-org|your-keyvault-name|<TODO: paste PagerDuty routing key here>"
  if ($rendered -match $placeholderPattern) {
    throw "rendered production manifests still contain placeholders"
  }
  Write-Host "[PASS] no unresolved production placeholders" -ForegroundColor Green
} catch {
  $failed = $true
  Write-Host "[FAIL] no unresolved production placeholders :: $($_.Exception.Message)" -ForegroundColor Red
}

try {
  kubectl get namespace $Namespace | Out-Null
  Write-Host "[PASS] namespace exists" -ForegroundColor Green
} catch {
  $failed = $true
  Write-Host "[FAIL] namespace exists :: $($_.Exception.Message)" -ForegroundColor Red
}

try {
  kubectl get secret platform-secrets -n $Namespace | Out-Null
  kubectl get secret alerting-secrets -n $Namespace | Out-Null
  Write-Host "[PASS] required secrets exist" -ForegroundColor Green
} catch {
  $failed = $true
  Write-Host "[FAIL] required secrets exist :: $($_.Exception.Message)" -ForegroundColor Red
}

try {
  $certificate = kubectl get certificate flagplane-tls -n $Namespace -o json | ConvertFrom-Json
  if ($LASTEXITCODE -ne 0 -or $null -eq $certificate) {
    throw "could not fetch certificate flagplane-tls"
  }
  $readyCondition = $certificate.status.conditions | Where-Object { $_.type -eq "Ready" } | Select-Object -First 1
  if ($null -eq $readyCondition -or $readyCondition.status -ne "True") {
    throw "certificate flagplane-tls not Ready"
  }
  Write-Host "[PASS] certificate ready" -ForegroundColor Green
} catch {
  $failed = $true
  Write-Host "[FAIL] certificate ready :: $($_.Exception.Message)" -ForegroundColor Red
}

try {
  $clusterPolicies = kubectl api-resources --api-group kyverno.io -o name 2>$null
  if ($LASTEXITCODE -ne 0 -or -not ($clusterPolicies -contains "clusterpolicies.kyverno.io")) {
    throw "Kyverno clusterpolicies CRD not installed"
  }
  kubectl get clusterpolicy verify-flagplane-images | Out-Null
  if ($LASTEXITCODE -ne 0) {
    throw "clusterpolicy verify-flagplane-images missing"
  }
  Write-Host "[PASS] image signature policy present" -ForegroundColor Green
} catch {
  $failed = $true
  Write-Host "[FAIL] image signature policy present :: $($_.Exception.Message)" -ForegroundColor Red
}

try {
  $schedules = kubectl api-resources --api-group velero.io -o name 2>$null
  if ($LASTEXITCODE -ne 0 -or -not ($schedules -contains "schedules.velero.io")) {
    throw "Velero schedules CRD not installed"
  }
  kubectl get schedule -n velero flagplane-hourly | Out-Null
  if ($LASTEXITCODE -ne 0) {
    throw "schedule flagplane-hourly missing"
  }
  kubectl get schedule -n velero flagplane-daily | Out-Null
  if ($LASTEXITCODE -ne 0) {
    throw "schedule flagplane-daily missing"
  }
  Write-Host "[PASS] backup schedules present" -ForegroundColor Green
} catch {
  $failed = $true
  Write-Host "[FAIL] backup schedules present :: $($_.Exception.Message)" -ForegroundColor Red
}

try {
  kubectl get secret alertmanager-config -n $Namespace | Out-Null
  Write-Host "[PASS] alertmanager on-call config present" -ForegroundColor Green
} catch {
  $failed = $true
  Write-Host "[FAIL] alertmanager on-call config present :: $($_.Exception.Message)" -ForegroundColor Red
}

if ($failed) {
  Write-Host "Preflight failed. Resolve failed checks before production cutover." -ForegroundColor Red
  exit 1
}

Write-Host "All production external-ops controls passed." -ForegroundColor Green
