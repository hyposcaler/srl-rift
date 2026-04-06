#!/bin/bash
# Verify rift-srl agent is running on all containerlab nodes.
set -euo pipefail

LAB_NAME="rift"
NODES=("spine1" "spine2" "leaf1" "leaf2" "leaf3")

echo "Checking rift-srl agent status on all nodes..."
echo ""

all_ok=true
for node in "${NODES[@]}"; do
    container="clab-${LAB_NAME}-${node}"
    echo "=== $node ==="

    # Check agent state.
    docker exec "$container" sr_cli -- info from state system application rift-srl 2>/dev/null || {
        echo "  FAIL: Agent not found"
        all_ok=false
        continue
    }
    echo ""
done

if $all_ok; then
    echo "All agents running."
else
    echo "Some agents failed. Check output above."
    exit 1
fi
