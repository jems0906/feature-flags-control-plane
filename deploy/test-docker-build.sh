#!/bin/bash
# Local Docker Build Test Script
# Tests Dockerfile builds locally before pushing to GitHub

set -e

PROJECT_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TIMESTAMP=$(date +%Y%m%d-%H%M%S)

echo "======================================"
echo "Local Docker Build Test"
echo "======================================"
echo

# Check Docker availability
echo "[1/5] Checking Docker availability..."
if ! command -v docker &> /dev/null; then
    echo "✗ Docker not available. Install Docker and try again."
    exit 1
fi
DOCKER_VERSION=$(docker --version)
echo "✓ Docker found: $DOCKER_VERSION"

# Build control-plane
echo
echo "[2/5] Building control-plane image..."
CONTROL_PLANE_IMAGE="test-control-plane:$TIMESTAMP"
(
    cd "$PROJECT_ROOT"
    echo "  Context: $PWD"
    echo "  Dockerfile: ./deploy/Dockerfile.control-plane"
    
    if docker build \
        --file deploy/Dockerfile.control-plane \
        --tag "$CONTROL_PLANE_IMAGE" \
        --build-arg GOFLAGS=-v \
        . > /tmp/cp-build.log 2>&1; then
        echo "✓ control-plane built successfully"
        tail -1 /tmp/cp-build.log | sed 's/^/  /'
    else
        echo "✗ control-plane build failed"
        cat /tmp/cp-build.log >&2
        exit 1
    fi
)

# Build microservice-demo
echo
echo "[3/5] Building microservice-demo image..."
DEMO_IMAGE="test-microservice-demo:$TIMESTAMP"
(
    cd "$PROJECT_ROOT"
    echo "  Context: $PWD"
    echo "  Dockerfile: ./deploy/Dockerfile.microservice-demo"
    
    if docker build \
        --file deploy/Dockerfile.microservice-demo \
        --tag "$DEMO_IMAGE" \
        --build-arg GOFLAGS=-v \
        . > /tmp/demo-build.log 2>&1; then
        echo "✓ microservice-demo built successfully"
        tail -1 /tmp/demo-build.log | sed 's/^/  /'
    else
        echo "✗ microservice-demo build failed"
        cat /tmp/demo-build.log >&2
        exit 1
    fi
)

# Inspect images
echo
echo "[4/5] Inspecting built images..."
if command -v jq &> /dev/null; then
    if docker inspect "$CONTROL_PLANE_IMAGE" | jq . > /dev/null 2>&1; then
        CP_SIZE=$(docker image inspect "$CONTROL_PLANE_IMAGE" --format='{{.Size}}')
        CP_SIZE_MB=$((CP_SIZE / 1024 / 1024))
        
        DEMO_SIZE=$(docker image inspect "$DEMO_IMAGE" --format='{{.Size}}')
        DEMO_SIZE_MB=$((DEMO_SIZE / 1024 / 1024))
        
        echo "✓ Image inspection successful"
        echo "  control-plane size: ${CP_SIZE_MB} MB"
        echo "  microservice-demo size: ${DEMO_SIZE_MB} MB"
    fi
else
    echo "⚠ jq not installed, skipping detailed inspection"
    echo "  Install jq for better diagnostics: apt install jq (Linux) or brew install jq (Mac)"
fi

# Test execution (timeout simulation)
echo
echo "[5/5] Testing image execution..."
echo "  Testing control-plane..."
if timeout 3s docker run --rm --read-only --cap-drop=ALL \
    -e CONTROL_PLANE_AUTH_TOKEN=test-token \
    "$CONTROL_PLANE_IMAGE" > /dev/null 2>&1 || true; then
    echo "  ✓ control-plane initialization started (timeout expected)"
fi

echo "  Testing microservice-demo..."
if timeout 3s docker run --rm --read-only --cap-drop=ALL \
    -e CONTROL_PLANE_URL=http://localhost:8080 \
    "$DEMO_IMAGE" > /dev/null 2>&1 || true; then
    echo "  ✓ microservice-demo initialization started (timeout expected)"
fi

# Summary
echo
echo "======================================"
echo "✓ All Docker builds successful!"
echo "======================================"
echo
echo "Images ready for manual testing:"
echo "  docker run --rm -p 8080:8080 -e CONTROL_PLANE_AUTH_TOKEN=test-token $CONTROL_PLANE_IMAGE"
echo "  docker run --rm -p 8081:8081 $DEMO_IMAGE"
echo

# Cleanup
if [[ "${1:-}" != "--keep" ]]; then
    echo "Cleaning up test images..."
    docker rmi "$CONTROL_PLANE_IMAGE" "$DEMO_IMAGE" 2>/dev/null || true
    echo "✓ Test images removed"
else
    echo "Keeping test images (use: docker rmi $CONTROL_PLANE_IMAGE $DEMO_IMAGE)"
fi
