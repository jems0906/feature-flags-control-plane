param(
  [string]$Namespace = "flagplane",
  [string]$ControlPlaneImage = "ghcr.io/jems0906/control-plane:v1.0.0",
  [string]$MicroserviceDemoImage = "ghcr.io/jems0906/microservice-demo:v1.0.0",
  [switch]$UseOverlay
)

$ErrorActionPreference = "Stop"

function Step([string]$msg) {
  Write-Host "[deploy] $msg" -ForegroundColor Cyan
}

function Test-CommandAvailable([string]$name) {
  if (-not (Get-Command $name -ErrorAction SilentlyContinue)) {
    throw "required command not found: $name"
  }
}

function Test-KubeContext {
  $ctx = kubectl config current-context 2>$null
  if (-not $ctx) {
    throw "kubectl current-context is not set. Configure kubeconfig and retry."
  }
  Step "using kube context: $ctx"
}

function Test-SecretExists([string]$name, [string]$namespace, [switch]$Optional) {
  kubectl -n $namespace get secret $name | Out-Null
  if ($LASTEXITCODE -ne 0) {
    if ($Optional) {
      Step "warning: optional secret missing: $name"
      return
    }
    throw "required secret missing: $name"
  }
}

function Wait-Rollout([string]$deployName, [string]$namespace) {
  kubectl -n $namespace rollout status deployment/$deployName --timeout=240s | Out-Null
  if ($LASTEXITCODE -ne 0) {
    throw "rollout failed for deployment/$deployName"
  }
}

try {
  Test-CommandAvailable "kubectl"
  Test-CommandAvailable "powershell"
  Test-KubeContext

  Step "ensuring namespace exists"
  $nsName = kubectl get namespace $Namespace --ignore-not-found -o name
  if (-not $nsName) {
    kubectl create namespace $Namespace | Out-Null
  }

  Step "checking required secrets"
  Test-SecretExists -name "platform-secrets" -namespace $Namespace
  Test-SecretExists -name "alerting-secrets" -namespace $Namespace
  Test-SecretExists -name "flagplane-tls" -namespace $Namespace -Optional

  if ($UseOverlay) {
    Step "applying production overlay"
    kubectl apply -k deploy/production | Out-Null
  } else {
    Step "applying base manifest"
    kubectl apply -f deploy/k8s.yaml | Out-Null
  }

  Step "pinning deployment images"
  kubectl -n $Namespace set image deployment/control-plane control-plane=$ControlPlaneImage | Out-Null
  kubectl -n $Namespace set image deployment/microservice-demo microservice-demo=$MicroserviceDemoImage | Out-Null

  Step "waiting for rollouts"
  Wait-Rollout -deployName "control-plane" -namespace $Namespace
  Wait-Rollout -deployName "microservice-demo" -namespace $Namespace

  Step "starting temporary port-forwards"
  $pfControl = Start-Process kubectl -ArgumentList "-n $Namespace port-forward svc/control-plane 18080:8080" -WindowStyle Hidden -PassThru
  $pfDemo = Start-Process kubectl -ArgumentList "-n $Namespace port-forward svc/microservice-demo 18081:8081" -WindowStyle Hidden -PassThru
  Start-Sleep -Seconds 8

  try {
    Step "running smoke tests"
    $env:CONTROL_PLANE_URL = "http://127.0.0.1:18080"
    $env:DEMO_URL = "http://127.0.0.1:18081"

    $token = kubectl -n $Namespace get secret platform-secrets -o jsonpath='{.data.controlPlaneAuthToken}'
    if ($token) {
      $env:AUTH_TOKEN = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($token))
    }

    powershell -NoProfile -ExecutionPolicy Bypass -File deploy/smoke-test.ps1
    if ($LASTEXITCODE -ne 0) {
      throw "smoke tests failed"
    }
  }
  finally {
    Step "stopping temporary port-forwards"
    if ($pfControl -and -not $pfControl.HasExited) { Stop-Process -Id $pfControl.Id -Force }
    if ($pfDemo -and -not $pfDemo.HasExited) { Stop-Process -Id $pfDemo.Id -Force }
  }

  Step "deployment and verification completed"
}
catch {
  Write-Host "[deploy] FAILED: $($_.Exception.Message)" -ForegroundColor Red
  exit 1
}
