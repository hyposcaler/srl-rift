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

# RIFT config per node: system-id, level, interfaces.
declare -A NODE_SYSID=([spine1]=1 [spine2]=2 [leaf1]=101 [leaf2]=102 [leaf3]=103)
declare -A NODE_LEVEL=([spine1]=1 [spine2]=1 [leaf1]=0 [leaf2]=0 [leaf3]=0)
declare -A NODE_IFACES=(
    [spine1]="ethernet-1/1 ethernet-1/2 ethernet-1/3"
    [spine2]="ethernet-1/1 ethernet-1/2 ethernet-1/3"
    [leaf1]="ethernet-1/1 ethernet-1/2"
    [leaf2]="ethernet-1/1 ethernet-1/2"
    [leaf3]="ethernet-1/1 ethernet-1/2"
)

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

    # Allow unprivileged port binding in srbase-default (for RIFT TIE port 915).
    docker exec "$container" ip netns exec srbase-default sysctl -w net.ipv4.ip_unprivileged_port_start=0 || true

    # Reload app manager to pick up new agent.
    docker exec "$container" sr_cli -- tools system app-management application app_mgr reload || true

    echo "  Deployed to $container"
done

echo "Waiting for agents to start..."
sleep 5

# Enable NDK server and apply RIFT config on each node.
for node in "${NODES[@]}"; do
    container="clab-${LAB_NAME}-${node}"
    sysid="${NODE_SYSID[$node]}"
    level="${NODE_LEVEL[$node]}"
    ifaces="${NODE_IFACES[$node]}"

    echo "Configuring $node (system-id=$sysid, level=$level)..."

    # Build config commands.
    cmds="enter candidate\nset / system ndk-server admin-state enable\nset / rift system-id $sysid\nset / rift level $level"
    for iface in $ifaces; do
        cmds="$cmds\nset / rift interface $iface"
    done
    cmds="$cmds\ncommit now"

    docker exec "$container" bash -c "printf '$cmds\n' | sr_cli" || true
done

echo ""
echo "Waiting for adjacencies to form..."
sleep 5

for node in "${NODES[@]}"; do
    container="clab-${LAB_NAME}-${node}"
    echo ""
    echo "=== $node ==="
    docker exec "$container" sr_cli -- "info from state system app-management application rift-srl state" 2>/dev/null || echo "  Agent not visible"
    docker exec "$container" sr_cli -- "info from state rift" 2>/dev/null || echo "  No RIFT state"
done
