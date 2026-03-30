param(
  [string]$ValuesFile,

  [Parameter(Mandatory = $false)]
  [string]$RegistryOrg,

  [Parameter(Mandatory = $false)]
  [string]$Repository,

  [Parameter(Mandatory = $false)]
  [string]$RootDomain,

  [Parameter(Mandatory = $false)]
  [string]$AcmeEmail,

  [Parameter(Mandatory = $false)]
  [string]$KeyVaultName,

  [Parameter(Mandatory = $false)]
  [string]$PagerDutyRoutingKey
)

$ErrorActionPreference = "Stop"

if ($ValuesFile) {
  if (-not (Test-Path $ValuesFile)) {
    throw "Values file not found: $ValuesFile"
  }
  $vals = Import-PowerShellDataFile -Path $ValuesFile

  if (-not $RegistryOrg) { $RegistryOrg = $vals.RegistryOrg }
  if (-not $Repository) { $Repository = $vals.Repository }
  if (-not $RootDomain) { $RootDomain = $vals.RootDomain }
  if (-not $AcmeEmail) { $AcmeEmail = $vals.AcmeEmail }
  if (-not $KeyVaultName) { $KeyVaultName = $vals.KeyVaultName }
  if (-not $PagerDutyRoutingKey) { $PagerDutyRoutingKey = $vals.PagerDutyRoutingKey }
}

$required = @{
  RegistryOrg = $RegistryOrg
  Repository = $Repository
  RootDomain = $RootDomain
  AcmeEmail = $AcmeEmail
  KeyVaultName = $KeyVaultName
  PagerDutyRoutingKey = $PagerDutyRoutingKey
}

$missing = @()
foreach ($k in $required.Keys) {
  if ([string]::IsNullOrWhiteSpace([string]$required[$k])) {
    $missing += $k
  }
}
if ($missing.Count -gt 0) {
  throw "Missing required values: $($missing -join ', '). Provide parameters directly or via -ValuesFile."
}

$baseDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$files = @(
  (Join-Path $baseDir "kustomization.yaml"),
  (Join-Path $baseDir "kyverno-verify-images.yaml"),
  (Join-Path $baseDir "patch-production-overrides.yaml"),
  (Join-Path $baseDir "cert-manager-certificate.yaml"),
  (Join-Path $baseDir "external-secrets.yaml")
)

$apiHost = "api.$RootDomain"
$demoHost = "demo.$RootDomain"
$grafanaHost = "grafana.$RootDomain"

function Update-File {
  param(
    [string]$Path,
    [hashtable]$Replacements
  )

  $content = Get-Content -Raw -Path $Path
  foreach ($k in $Replacements.Keys) {
    $content = $content.Replace($k, $Replacements[$k])
  }
  Set-Content -Path $Path -Value $content -NoNewline
}

$replacements = @{
  "YOUR_ORG"                     = $RegistryOrg
  "YOUR_REPO"                    = $Repository
  "api.flagplane.example.com"    = $apiHost
  "demo.flagplane.example.com"   = $demoHost
  "grafana.flagplane.example.com"= $grafanaHost
  "api.flagplane.jems0906.dev"   = $apiHost
  "demo.flagplane.jems0906.dev"  = $demoHost
  "grafana.flagplane.jems0906.dev" = $grafanaHost
  "platform-ops@example.com"     = $AcmeEmail
  "ops@jems0906.dev"             = $AcmeEmail
  "YOUR-KEYVAULT-NAME"           = $KeyVaultName
  "flagplane-kv"                 = $KeyVaultName
  "YOUR_PAGERDUTY_ROUTING_KEY"   = $PagerDutyRoutingKey
  "<TODO: paste PagerDuty routing key here>" = $PagerDutyRoutingKey
}

foreach ($f in $files) {
  Update-File -Path $f -Replacements $replacements
  Write-Host "Updated $f" -ForegroundColor Green
}

Write-Host "Production values applied. Review with: kubectl kustomize deploy/production" -ForegroundColor Green
