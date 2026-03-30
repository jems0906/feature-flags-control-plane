param(
  [string]$Namespace = "flagplane",
  [string]$OutputRoot = "docs/evidence",
  [string]$ControlPlaneUrl = "",
  [string]$DemoUrl = ""
)

$ErrorActionPreference = "Stop"

$timestamp = Get-Date -Format "yyyyMMdd-HHmmss"
$targetDir = Join-Path $OutputRoot "$($Namespace)-$timestamp"
New-Item -ItemType Directory -Path $targetDir -Force | Out-Null

function Save-CommandOutput {
  param(
    [string]$FileName,
    [scriptblock]$Command
  )
  $path = Join-Path $targetDir $FileName
  try {
    $output = & $Command | Out-String
    $output | Set-Content -Path $path
  }
  catch {
    ("ERROR: " + $_.Exception.Message) | Set-Content -Path $path
    throw
  }
}

Save-CommandOutput -FileName "01-cluster-info.txt" -Command { kubectl version --client; kubectl config current-context }
Save-CommandOutput -FileName "02-deployments.txt" -Command { kubectl -n $Namespace get deploy -o wide }
Save-CommandOutput -FileName "03-rollout-status.txt" -Command {
  kubectl -n $Namespace rollout status deployment/control-plane --timeout=180s
  kubectl -n $Namespace rollout status deployment/microservice-demo --timeout=180s
}
Save-CommandOutput -FileName "04-images.txt" -Command {
  kubectl -n $Namespace get deploy control-plane -o=jsonpath='{.spec.template.spec.containers[0].image}'; ''
  kubectl -n $Namespace get deploy microservice-demo -o=jsonpath='{.spec.template.spec.containers[0].image}'; ''
}
Save-CommandOutput -FileName "05-cert-and-ingress.txt" -Command {
  kubectl -n $Namespace get certificate flagplane-tls -o wide
  kubectl -n $Namespace get ingress -o wide
}
Save-CommandOutput -FileName "06-secrets-presence.txt" -Command {
  kubectl -n $Namespace get secret platform-secrets
  kubectl -n $Namespace get secret alerting-secrets
  kubectl -n $Namespace get secret alertmanager-config
}
Save-CommandOutput -FileName "07-pods.txt" -Command { kubectl -n $Namespace get pods -o wide }

if ($ControlPlaneUrl -or $DemoUrl) {
  $cp = if ($ControlPlaneUrl) { $ControlPlaneUrl } else { "http://localhost:8080" }
  $demo = if ($DemoUrl) { $DemoUrl } else { "http://localhost:8081" }
  Save-CommandOutput -FileName "08-runtime-health.txt" -Command {
    Invoke-WebRequest -UseBasicParsing "$cp/health" -TimeoutSec 10
    Invoke-WebRequest -UseBasicParsing "$demo/demo/health" -TimeoutSec 10
  }
}

$summary = @(
  "Promotion evidence generated: $targetDir"
  "Generated at: $(Get-Date -Format o)"
  "Namespace: $Namespace"
)
$summary | Set-Content -Path (Join-Path $targetDir "00-summary.txt")

Write-Host "Promotion evidence written to $targetDir" -ForegroundColor Green
