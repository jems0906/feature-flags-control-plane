param(
  [switch]$Remove,
  [switch]$DryRun
)

$ErrorActionPreference = "Stop"

$hostsPath = Join-Path $env:SystemRoot "System32\drivers\etc\hosts"
$managedBegin = "# BEGIN FLAGPLANE LOCAL HOSTS"
$managedEnd = "# END FLAGPLANE LOCAL HOSTS"

$entries = @(
  "127.0.0.1 api.flagplane.local",
  "127.0.0.1 demo.flagplane.local",
  "127.0.0.1 grafana.flagplane.local"
)

function Write-Step([string]$message) {
  Write-Host "[hosts] $message" -ForegroundColor Cyan
}

function Test-IsAdministrator {
  $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
  $principal = New-Object Security.Principal.WindowsPrincipal($identity)
  return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

if (-not (Test-IsAdministrator)) {
  Write-Host "[hosts] This script must run as Administrator." -ForegroundColor Yellow
  Write-Host "[hosts] Re-run in an elevated terminal:" -ForegroundColor Yellow
  Write-Host "powershell -NoProfile -ExecutionPolicy Bypass -File deploy/setup-local-hosts.ps1" -ForegroundColor Yellow
  exit 1
}

if (-not (Test-Path $hostsPath)) {
  throw "hosts file not found at $hostsPath"
}

$content = Get-Content -Path $hostsPath -Raw

$pattern = [regex]::Escape($managedBegin) + "[\\s\\S]*?" + [regex]::Escape($managedEnd) + "\\r?\\n?"
$contentWithoutManagedBlock = [regex]::Replace($content, $pattern, "")

if ($Remove) {
  if ($DryRun) {
    Write-Step "Dry-run remove: would remove managed Flagplane hosts block"
    exit 0
  }
  Set-Content -Path $hostsPath -Value $contentWithoutManagedBlock -NoNewline
  Write-Step "Removed Flagplane local host mappings"
  exit 0
}

$newBlock = ($managedBegin + "`r`n" + ($entries -join "`r`n") + "`r`n" + $managedEnd + "`r`n")
$newContent = $contentWithoutManagedBlock.TrimEnd() + "`r`n`r`n" + $newBlock

if ($DryRun) {
  Write-Step "Dry-run add: would write the following managed block"
  Write-Output $newBlock
  exit 0
}

Set-Content -Path $hostsPath -Value $newContent -NoNewline
Write-Step "Applied Flagplane host mappings"
