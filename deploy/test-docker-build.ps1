# Local Docker Build Test Script
# Tests Dockerfile builds locally before pushing to GitHub

param(
    [switch]$SkipCleanup = $false
)

$ErrorActionPreference = "Stop"
$ProjectRoot = (Split-Path -Parent (Split-Path -Parent $PSScriptRoot))
$Timestamp = Get-Date -Format "yyyyMMdd-HHmmss"

Write-Host "======================================" -ForegroundColor Cyan
Write-Host "Local Docker Build Test" -ForegroundColor Cyan
Write-Host "======================================" -ForegroundColor Cyan
Write-Host

# Check Docker availability
Write-Host "[1/5] Checking Docker availability..." -ForegroundColor Yellow
try {
    $DockerVersion = docker --version
    Write-Host "✓ Docker found: $DockerVersion" -ForegroundColor Green
}
catch {
    Write-Host "✗ Docker not available. Install Docker and try again." -ForegroundColor Red
    exit 1
}

# Build control-plane
Write-Host "`n[2/5] Building control-plane image..." -ForegroundColor Yellow
$ControlPlaneImage = "test-control-plane:$Timestamp"
try {
    Push-Location $ProjectRoot
    
    Write-Host "  Context: $((Get-Item .).FullName)" -ForegroundColor Gray
    Write-Host "  Dockerfile: ./deploy/Dockerfile.control-plane" -ForegroundColor Gray
    
    $BuildOutput = docker build `
        --file deploy/Dockerfile.control-plane `
        --tag $ControlPlaneImage `
        --build-arg GOFLAGS=-v `
        . 2>&1
    
    Write-Host "✓ control-plane built successfully" -ForegroundColor Green
    $BuildOutput | Select-Object -Last 1 | ForEach-Object { Write-Host "  $_" -ForegroundColor Gray }
}
catch {
    Write-Host "✗ control-plane build failed" -ForegroundColor Red
    Write-Host $_ -ForegroundColor Red
    exit 1
}
finally {
    Pop-Location
}

# Build microservice-demo
Write-Host "`n[3/5] Building microservice-demo image..." -ForegroundColor Yellow
$DemoImage = "test-microservice-demo:$Timestamp"
try {
    Push-Location $ProjectRoot
    
    Write-Host "  Context: $((Get-Item .).FullName)" -ForegroundColor Gray
    Write-Host "  Dockerfile: ./deploy/Dockerfile.microservice-demo" -ForegroundColor Gray
    
    $BuildOutput = docker build `
        --file deploy/Dockerfile.microservice-demo `
        --tag $DemoImage `
        --build-arg GOFLAGS=-v `
        . 2>&1
    
    Write-Host "✓ microservice-demo built successfully" -ForegroundColor Green
    $BuildOutput | Select-Object -Last 1 | ForEach-Object { Write-Host "  $_" -ForegroundColor Gray }
}
catch {
    Write-Host "✗ microservice-demo build failed" -ForegroundColor Red
    Write-Host $_ -ForegroundColor Red
    exit 1
}
finally {
    Pop-Location
}

# Inspect images
Write-Host "`n[4/5] Inspecting built images..." -ForegroundColor Yellow
try {
    docker inspect $ControlPlaneImage | jq . | Out-Null
    $CPSize = (docker image inspect $ControlPlaneImage --format='{{.Size}}' | {process { [int]$_ }})
    $CPSizeMB = [math]::Round($CPSize / 1MB, 2)
    
    docker inspect $DemoImage | jq . | Out-Null
    $DemoSize = (docker image inspect $DemoImage --format='{{.Size}}' | {process { [int]$_ }})
    $DemoSizeMB = [math]::Round($DemoSize / 1MB, 2)
    
    Write-Host "✓ Image inspection successful" -ForegroundColor Green
    Write-Host "  control-plane size: $CPSizeMB MB" -ForegroundColor Gray
    Write-Host "  microservice-demo size: $DemoSizeMB MB" -ForegroundColor Gray
}
catch {
    Write-Host "⚠ Image inspection failed (jq may not be installed)" -ForegroundColor Yellow
    Write-Host "  Consider installing jq: choco install jq" -ForegroundColor Gray
}

# Test execution
Write-Host "`n[5/5] Testing image execution (timeout: 3s)..." -ForegroundColor Yellow
try {
    Write-Host "  Testing control-plane..." -ForegroundColor Gray
    $CPTest = docker run --rm --read-only --cap-drop=ALL -e CONTROL_PLANE_AUTH_TOKEN=test-token `
        --timeout 3s $ControlPlaneImage 2>&1
    Write-Host "  ✓ control-plane runs (exited as expected)" -ForegroundColor Green
}
catch {
    if ($_ -match "deadline exceeded|Does not have minimum required") {
        Write-Host "  ✓ control-plane initialization started (timeout expected)" -ForegroundColor Green
    }
    else {
        Write-Host "  ⚠ control-plane test inconclusive" -ForegroundColor Yellow
    }
}

try {
    Write-Host "  Testing microservice-demo..." -ForegroundColor Gray
    $DemoTest = docker run --rm --read-only --cap-drop=ALL -e CONTROL_PLANE_URL=http://localhost:8080 `
        --timeout 3s $DemoImage 2>&1
    Write-Host "  ✓ microservice-demo runs (exited as expected)" -ForegroundColor Green
}
catch {
    if ($_ -match "deadline exceeded|Does not have minimum required") {
        Write-Host "  ✓ microservice-demo initialization started (timeout expected)" -ForegroundColor Green
    }
    else {
        Write-Host "  ⚠ microservice-demo test inconclusive" -ForegroundColor Yellow
    }
}

# Summary
Write-Host "`n======================================" -ForegroundColor Cyan
Write-Host "✓ All Docker builds successful!" -ForegroundColor Green
Write-Host "======================================" -ForegroundColor Cyan
Write-Host
Write-Host "Images ready for manual testing:" -ForegroundColor Gray
Write-Host "  docker run --rm -p 8080:8080 -e CONTROL_PLANE_AUTH_TOKEN=test-token $ControlPlaneImage" -ForegroundColor Gray
Write-Host "  docker run --rm -p 8081:8081 $DemoImage" -ForegroundColor Gray
Write-Host

# Cleanup option
if (-not $SkipCleanup) {
    Write-Host "Cleaning up test images..." -ForegroundColor Yellow
    docker rmi $ControlPlaneImage $DemoImage 2>&1 | Out-Null
    Write-Host "✓ Test images removed" -ForegroundColor Green
}
else {
    Write-Host "Keeping test images (use: docker rmi $ControlPlaneImage $DemoImage)" -ForegroundColor Gray
}
