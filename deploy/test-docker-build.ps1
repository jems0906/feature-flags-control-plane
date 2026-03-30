# Local Docker Build Test Script
# Tests Dockerfile builds locally before pushing to GitHub

param(
    [switch]$SkipCleanup = $false
)

$ErrorActionPreference = "Stop"
$ProjectRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$Timestamp = Get-Date -Format "yyyyMMdd-HHmmss"

function Write-Step {
    param(
        [string]$Message
    )

    Write-Host $Message -ForegroundColor Yellow
}

function Build-Image {
    param(
        [string]$Label,
        [string]$Dockerfile,
        [string]$Tag
    )

    Write-Host "  Context: $ProjectRoot" -ForegroundColor Gray
    Write-Host "  Dockerfile: $Dockerfile" -ForegroundColor Gray

    Push-Location $ProjectRoot
    try {
        & docker build "--file" $Dockerfile "--tag" $Tag "--build-arg" "GOFLAGS=-v" "."
        if ($LASTEXITCODE -ne 0) {
            throw "docker build failed for $Label"
        }
    }
    finally {
        Pop-Location
    }

    Write-Host "[OK] $Label built successfully" -ForegroundColor Green
}

function Get-ImageSizeMb {
    param(
        [string]$Tag
    )

    $sizeText = & docker image inspect $Tag "--format={{.Size}}"
    if ($LASTEXITCODE -ne 0) {
        throw "docker image inspect failed for $Tag"
    }

    return [math]::Round(([double]$sizeText / 1MB), 2)
}

function Test-ContainerCreate {
    param(
        [string]$Label,
        [string]$Tag,
        [string]$EnvironmentName,
        [string]$EnvironmentValue
    )

    Write-Host "  Testing $Label..." -ForegroundColor Gray
    $containerId = & docker create "--read-only" "--cap-drop=ALL" "-e" "$EnvironmentName=$EnvironmentValue" $Tag

    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($containerId)) {
        throw "docker create failed for $Label"
    }

    & docker rm $containerId.Trim() | Out-Null
    Write-Host "  [OK] $Label container created successfully" -ForegroundColor Green
}

Write-Host "======================================" -ForegroundColor Cyan
Write-Host "Local Docker Build Test" -ForegroundColor Cyan
Write-Host "======================================" -ForegroundColor Cyan
Write-Host

Write-Step "[1/5] Checking Docker availability..."
$dockerVersion = & docker --version
if ($LASTEXITCODE -ne 0) {
    throw "Docker not available. Install Docker and try again."
}
Write-Host "[OK] Docker found: $dockerVersion" -ForegroundColor Green

$controlPlaneImage = "test-control-plane:$Timestamp"
$demoImage = "test-microservice-demo:$Timestamp"

Write-Step "`n[2/5] Building control-plane image..."
Build-Image -Label "control-plane" -Dockerfile "deploy/Dockerfile.control-plane" -Tag $controlPlaneImage

Write-Step "`n[3/5] Building microservice-demo image..."
Build-Image -Label "microservice-demo" -Dockerfile "deploy/Dockerfile.microservice-demo" -Tag $demoImage

Write-Step "`n[4/5] Inspecting built images..."
$controlPlaneSizeMb = Get-ImageSizeMb -Tag $controlPlaneImage
$demoSizeMb = Get-ImageSizeMb -Tag $demoImage
Write-Host "[OK] Image inspection successful" -ForegroundColor Green
Write-Host "  control-plane size: $controlPlaneSizeMb MB" -ForegroundColor Gray
Write-Host "  microservice-demo size: $demoSizeMb MB" -ForegroundColor Gray

Write-Step "`n[5/5] Testing image execution..."
Test-ContainerCreate -Label "control-plane" -Tag $controlPlaneImage -EnvironmentName "CONTROL_PLANE_AUTH_TOKEN" -EnvironmentValue "test-token"
Test-ContainerCreate -Label "microservice-demo" -Tag $demoImage -EnvironmentName "CONTROL_PLANE_URL" -EnvironmentValue "http://localhost:8080"

Write-Host "`n======================================" -ForegroundColor Cyan
Write-Host "[OK] All Docker builds successful" -ForegroundColor Green
Write-Host "======================================" -ForegroundColor Cyan
Write-Host
Write-Host "Images ready for manual testing:" -ForegroundColor Gray
Write-Host "  docker run --rm -p 8080:8080 -e CONTROL_PLANE_AUTH_TOKEN=test-token $controlPlaneImage" -ForegroundColor Gray
Write-Host "  docker run --rm -p 8081:8081 $demoImage" -ForegroundColor Gray
Write-Host

if (-not $SkipCleanup) {
    Write-Host "Cleaning up test images..." -ForegroundColor Yellow
    & docker rmi $controlPlaneImage $demoImage | Out-Null
    Write-Host "[OK] Test images removed" -ForegroundColor Green
}

if ($SkipCleanup) {
    Write-Host "Keeping test images (use: docker rmi $controlPlaneImage $demoImage)" -ForegroundColor Gray
}
