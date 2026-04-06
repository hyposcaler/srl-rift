#!/bin/bash
# Deploy rift-srl agent to all containerlab nodes.
# Usage: ./deploy.sh [--build]
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
LAB_DIR="$REPO_DIR/lab"
BINARY="$REPO_DIR/rift-srl"
LAB_NAME="rift"

NODES=("spine1" "spine2" "leaf1" "leaf2" "leaf3")

# Build if requested or binary doesn't exist.
if [[ "${1:-}" == "--build" ]] || [[ ! -f "$BINARY" ]]; then
    echo "Building rift-srl binary..."
    cd "$REPO_DIR"
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o rift-srl ./cmd/rift-srl/
    echo "Build complete: $BINARY"
fi

for node in "${NODES[@]}"; do
    container="clab-${LAB_NAME}-${node}"
    echo "Deploying to $container..."

    # Copy binary.
    docker cp "$BINARY" "$container:/usr/local/bin/rift-srl"

    # Copy app manager config.
    docker cp "$LAB_DIR/rift-srl.yml" "$container:/etc/opt/srlinux/appmgr/rift-srl.yml"

    # Copy YANG model.
    docker exec "$container" mkdir -p /opt/rift-srl/yang
    docker cp "$REPO_DIR/yang/rift-srl.yang" "$container:/opt/rift-srl/yang/rift-srl.yang"

    # Reload app manager to pick up new agent.
    docker exec "$container" sr_cli -- tools system app-management application app_mgr reload || true

    echo "  Deployed to $container"
done

echo "Deployment complete. Waiting for agents to start..."
sleep 5

for node in "${NODES[@]}"; do
    container="clab-${LAB_NAME}-${node}"
    echo ""
    echo "=== $node ==="
    docker exec "$container" sr_cli -- info from state system application rift-srl 2>/dev/null || echo "  Agent not yet visible"
done
