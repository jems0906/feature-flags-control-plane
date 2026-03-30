param(
  [Parameter(Mandatory = $true)]
  [string]$ValuesFile,

  [ValidateSet("staging", "production")]
  [string]$Environment = "staging",

  [switch]$Apply,
  [switch]$CaptureEvidence
)

$ErrorActionPreference = "Stop"

function Invoke-Kubectl {
  param(
    [Parameter(Mandatory = $true)]
    [scriptblock]$Command,

    [Parameter(Mandatory = $true)]
    [string]$Action,

    [switch]$ThrowOnFailure,
    [int]$MaxAttempts = 5,
    [int]$DelaySeconds = 3
  )

  for ($attempt = 1; $attempt -le $MaxAttempts; $attempt++) {
    $output = & $Command 2>&1
    $exitCode = $LASTEXITCODE
    if ($exitCode -eq 0) {
      return [pscustomobject]@{ ExitCode = 0; Output = $output }
    }

    $text = ($output | Out-String)
    $isTransient = $text -match "TLS handshake timeout|Unable to connect to the server|client connection lost|i/o timeout|connection reset|EOF|net/http"
    if ($isTransient -and $attempt -lt $MaxAttempts) {
      Write-Host "[go-live] $Action transient error (attempt $attempt/$MaxAttempts). Retrying in $DelaySeconds s..." -ForegroundColor Yellow
      Start-Sleep -Seconds $DelaySeconds
      continue
    }

    if ($ThrowOnFailure) {
      throw "$Action failed: $text"
    }

    return [pscustomobject]@{ ExitCode = $exitCode; Output = $output }
  }
}

if (-not (Test-Path $ValuesFile)) {
  throw "Values file not found: $ValuesFile"
}

$vals = Import-PowerShellDataFile -Path $ValuesFile
$namespace = if ($Environment -eq "production") { $vals.ProductionNamespace } else { $vals.StagingNamespace }
if ([string]::IsNullOrWhiteSpace($namespace)) {
  $namespace = if ($Environment -eq "production") { "flagplane" } else { "flagplane-staging" }
}

Write-Host "[go-live] Applying production values from file" -ForegroundColor Cyan
& "$PSScriptRoot/set-prod-values.ps1" -ValuesFile $ValuesFile

Write-Host "[go-live] Rendering manifests" -ForegroundColor Cyan
$renderResult = Invoke-Kubectl -Action "kustomize render" -ThrowOnFailure -Command { kubectl kustomize --load-restrictor=LoadRestrictionsNone deploy/production }
$rendered = $renderResult.Output
$renderedText = ($rendered | Out-String)

if ($namespace -ne "flagplane") {
  $renderedText = [regex]::Replace($renderedText, '(?m)^(\s*namespace:\s*)flagplane\s*$', "`$1$namespace")
  $renderedText = $renderedText.Replace('namespace="flagplane"', "namespace=`"$namespace`"")
}

if ($Apply) {
  $namespaceCheck = Invoke-Kubectl -Action "check namespace $namespace" -Command { kubectl get namespace $namespace }
  if ($namespaceCheck.ExitCode -ne 0) {
    Write-Host "[go-live] Namespace $namespace does not exist. Creating it." -ForegroundColor Yellow
    Invoke-Kubectl -Action "create namespace $namespace" -ThrowOnFailure -Command { kubectl create namespace $namespace } | Out-Null
  }

  $requiredResources = @(
    @{ Group = "cert-manager.io"; Name = "certificates.cert-manager.io" },
    @{ Group = "cert-manager.io"; Name = "clusterissuers.cert-manager.io" },
    @{ Group = "external-secrets.io"; Name = "clustersecretstores.external-secrets.io" },
    @{ Group = "external-secrets.io"; Name = "externalsecrets.external-secrets.io" },
    @{ Group = "kyverno.io"; Name = "clusterpolicies.kyverno.io" },
    @{ Group = "velero.io"; Name = "schedules.velero.io" }
  )

  $missingResources = @()
  foreach ($req in $requiredResources) {
    $resourceResult = Invoke-Kubectl -Action "discover API resources for $($req.Group)" -Command { kubectl api-resources --api-group $req.Group -o name }
    $names = $resourceResult.Output
    if ($resourceResult.ExitCode -ne 0 -or -not ($names -contains $req.Name)) {
      $missingResources += "$($req.Group)/$($req.Name)"
    }
  }
  if ($missingResources.Count -gt 0) {
    throw "Missing required CRD resources in cluster: $($missingResources -join ', '). Install required operators before applying production overlay."
  }

  Write-Host "[go-live] Applying overlay to namespace: $namespace" -ForegroundColor Cyan
  $tempManifest = Join-Path $env:TEMP "go-live-rendered-$Environment.yaml"
  $renderedText | Set-Content -Encoding UTF8 $tempManifest
  Invoke-Kubectl -Action "apply overlay manifests" -ThrowOnFailure -Command { kubectl apply --server-side --force-conflicts --validate=false -f $tempManifest } | Out-Null

  Write-Host "[go-live] Waiting for rollout" -ForegroundColor Cyan
  Invoke-Kubectl -Action "rollout status control-plane" -ThrowOnFailure -Command { kubectl -n $namespace rollout status deployment/control-plane --timeout=180s } | Out-Null
  Invoke-Kubectl -Action "rollout status microservice-demo" -ThrowOnFailure -Command { kubectl -n $namespace rollout status deployment/microservice-demo --timeout=180s } | Out-Null
}
else {
  Write-Host "[go-live] Dry run only (render + validation complete). Use -Apply to deploy." -ForegroundColor Yellow
}

if ($CaptureEvidence) {
  Write-Host "[go-live] Capturing promotion evidence" -ForegroundColor Cyan
  & "$PSScriptRoot/collect-promotion-evidence.ps1" -Namespace $namespace
}

Write-Host "[go-live] Completed for environment: $Environment" -ForegroundColor Green
