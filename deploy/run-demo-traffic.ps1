param(
  [string]$ControlPlaneUrl = "http://127.0.0.1:18080",
  [string]$DemoUrl = "http://127.0.0.1:18081",
  [string]$AuthToken = "local-dev-token",
  [int]$Users = 80,
  [int]$Burst = 20,
  [int]$PostWaitSeconds = 15
)

$ErrorActionPreference = "Stop"

function Log([string]$msg) {
  Write-Host "[demo-traffic] $msg" -ForegroundColor Cyan
}

function Invoke-PostJson([string]$url, [object]$body, [bool]$includeAuth = $true) {
  $headers = @{}
  if ($includeAuth -and $AuthToken) {
    $headers["Authorization"] = "Bearer $AuthToken"
  }
  Invoke-RestMethod -Method Post -Uri $url -Headers $headers -ContentType "application/json" -Body ($body | ConvertTo-Json -Depth 8) | Out-Null
}

function Invoke-Post([string]$url) {
  try {
    Invoke-WebRequest -UseBasicParsing -Method Post -Uri $url | Out-Null
  }
  catch {
    # Expected for this script when intentionally generating failures/throttling.
  }
}

function Set-BaselineConfig {
  Log "Configuring flag, experiment, rate limit, and circuit breaker"
  Invoke-PostJson "$ControlPlaneUrl/flags" @{
    Name = "dark-mode"
    Enabled = $true
    Environment = "dev"
    TargetRules = @(@{ Type = "percentage"; Rollout = 60 })
  }

  Invoke-PostJson "$ControlPlaneUrl/experiment" @{
    Name = "button-color"
    Variants = @("blue", "green")
  }

  Invoke-PostJson "$ControlPlaneUrl/ratelimit" @{
    Route = "/demo/action"
    Limit = 5
  }

  Invoke-PostJson "$ControlPlaneUrl/circuitbreaker" @{
    Route = "/demo/action"
    State = "closed"
    ErrorThreshold = 0.5
    LatencyThresholdMs = 250
  }
}

function Invoke-MixedTraffic {
  Log "Generating mixed traffic for $Users users"
  for ($i = 1; $i -le $Users; $i++) {
    $user = "user$i"

    Invoke-WebRequest -UseBasicParsing "$DemoUrl/demo/hello?userId=$user" | Out-Null

    $variantResp = Invoke-RestMethod -Method Get -Uri "$DemoUrl/demo/experiment?userId=$user"
    if ($variantResp.variant) {
      $headers = @{ Authorization = "Bearer $AuthToken" }
      Invoke-WebRequest -UseBasicParsing -Method Post -Headers $headers -Uri "$ControlPlaneUrl/experiment/button-color/convert?variant=$($variantResp.variant)" | Out-Null
    }

    if ($i % 4 -eq 0) {
      Invoke-Post "$DemoUrl/demo/action?userId=$user&fail=true"
    }
    elseif ($i % 3 -eq 0) {
      Invoke-Post "$DemoUrl/demo/action?userId=$user&sleepMs=350"
    }
    else {
      Invoke-Post "$DemoUrl/demo/action?userId=$user"
    }
  }
}

function Invoke-BurstTraffic {
  Log "Generating burst traffic ($Burst calls) to trigger throttling"
  for ($i = 1; $i -le $Burst; $i++) {
    Invoke-Post "$DemoUrl/demo/action?userId=burst-user"
  }
}

function Get-CircuitState {
  $stateResp = Invoke-RestMethod -Method Get -Uri "$ControlPlaneUrl/circuitbreaker?route=%2Fdemo%2Faction"
  Log "Circuit breaker state: $($stateResp.state)"
}

Log "Starting demo traffic run"
Set-BaselineConfig
Invoke-MixedTraffic
Invoke-BurstTraffic
Log "Waiting $PostWaitSeconds seconds for scrape/aggregation windows"
Start-Sleep -Seconds $PostWaitSeconds
Get-CircuitState
Log "Demo traffic run complete"
