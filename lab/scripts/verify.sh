#!/bin/bash
# Verify rift-srl agent: status, adjacencies, routes, and connectivity.
# Usage: verify.sh [--full]
#   --full  also runs disaggregation failure/recovery test
set -euo pipefail

LAB_NAME="rift"
SPINES=("spine1" "spine2")
LEAVES=("leaf1" "leaf2" "leaf3")
ALL_NODES=("${SPINES[@]}" "${LEAVES[@]}")
PASS=0
FAIL=0

pass() { echo "  PASS: $1"; PASS=$((PASS + 1)); }
fail() { echo "  FAIL: $1"; FAIL=$((FAIL + 1)); }

cli() {
    local node="$1"; shift
    docker exec "clab-${LAB_NAME}-${node}" sr_cli -- "$@" 2>/dev/null
}

# ---------- Agent status ----------
echo "=== Agent Status ==="
for node in "${ALL_NODES[@]}"; do
    output=$(cli "$node" "info from state system app-management application rift-srl state" 2>&1) || true
    if echo "$output" | grep -q "state running"; then
        pass "$node agent running"
    else
        fail "$node agent not running"
    fi
done
echo ""

# ---------- Adjacencies ----------
echo "=== Adjacencies ==="
for node in "${SPINES[@]}"; do
    count=$(cli "$node" "info from state rift interface *" | grep -c "state three-way" || true)
    if [ "$count" -eq 3 ]; then
        pass "$node: $count/3 adjacencies three-way"
    else
        fail "$node: $count/3 adjacencies three-way"
    fi
done

for node in "${LEAVES[@]}"; do
    count=$(cli "$node" "info from state rift interface *" | grep -c "state three-way" || true)
    if [ "$count" -eq 2 ]; then
        pass "$node: $count/2 adjacencies three-way"
    else
        fail "$node: $count/2 adjacencies three-way"
    fi
done
echo ""

# ---------- LSDB ----------
echo "=== LSDB ==="
for node in "${ALL_NODES[@]}"; do
    lsdb=$(cli "$node" "info from state rift lsdb-summary" 2>&1) || true
    if echo "$lsdb" | grep -q "node\|prefix"; then
        pass "$node: LSDB populated"
    else
        fail "$node: LSDB empty or missing"
    fi
done
echo ""

# ---------- Routes ----------
echo "=== Routes ==="
for node in "${LEAVES[@]}"; do
    routes=$(cli "$node" "info from state network-instance default route-table ipv4-unicast" 2>&1) || true
    if echo "$routes" | grep "0.0.0.0/0" | grep -q "rift-srl"; then
        pass "$node: default route via rift-srl"
    else
        fail "$node: default route missing"
    fi
done

for node in "${SPINES[@]}"; do
    routes=$(cli "$node" "info from state network-instance default route-table ipv4-unicast" 2>&1) || true
    if echo "$routes" | grep -q "rift-srl"; then
        pass "$node: rift-srl routes present"
    else
        fail "$node: rift-srl routes missing"
    fi
done
echo ""

# ---------- Ping ----------
echo "=== Connectivity ==="
for pair in "host1:10.10.2.10:host2" "host1:10.10.3.10:host3" "host2:10.10.3.10:host3"; do
    IFS=: read -r src dst desc <<< "$pair"
    if docker exec "clab-${LAB_NAME}-${src}" ping -c 3 -W 2 "$dst" >/dev/null 2>&1; then
        pass "$src -> $desc ($dst)"
    else
        fail "$src -> $desc ($dst)"
    fi
done
echo ""

# ---------- Disaggregation (--full only) ----------
if [[ "${1:-}" == "--full" ]]; then
    echo "=== Disaggregation Test ==="

    echo "  Disabling spine1 ethernet-1/3 (link to leaf3)..."
    docker exec "clab-${LAB_NAME}-spine1" bash -c \
        "printf 'enter candidate\nset interface ethernet-1/3 admin-state disable\ncommit now\n' | sr_cli" >/dev/null

    echo "  Waiting 15s for convergence..."
    sleep 15

    # Check spine2 has disaggregation active.
    disagg=$(cli spine2 "info from state rift disaggregation-summary" 2>&1) || true
    if echo "$disagg" | grep -qv "none"; then
        pass "spine2 disaggregation active"
    else
        fail "spine2 disaggregation not active"
    fi

    # Ping host1 -> host3 should still work via spine2.
    if docker exec "clab-${LAB_NAME}-host1" ping -c 3 -W 2 10.10.3.10 >/dev/null 2>&1; then
        pass "host1 -> host3 during failure"
    else
        fail "host1 -> host3 during failure"
    fi

    echo "  Re-enabling spine1 ethernet-1/3..."
    docker exec "clab-${LAB_NAME}-spine1" bash -c \
        "printf 'enter candidate\nset interface ethernet-1/3 admin-state enable\ncommit now\n' | sr_cli" >/dev/null

    echo "  Waiting 15s for recovery..."
    sleep 15

    # Check disaggregation withdrawn.
    disagg=$(cli spine2 "info from state rift disaggregation-summary" 2>&1) || true
    if echo "$disagg" | grep -q "none"; then
        pass "spine2 disaggregation withdrawn"
    else
        fail "spine2 disaggregation not withdrawn"
    fi

    # Ping should still work.
    if docker exec "clab-${LAB_NAME}-host1" ping -c 3 -W 2 10.10.3.10 >/dev/null 2>&1; then
        pass "host1 -> host3 after recovery"
    else
        fail "host1 -> host3 after recovery"
    fi
    echo ""
fi

# ---------- Summary ----------
TOTAL=$((PASS + FAIL))
echo "=== Summary: $PASS/$TOTAL passed ==="
if [ "$FAIL" -gt 0 ]; then
    echo "FAILED: $FAIL checks failed"
    exit 1
fi
echo "ALL CHECKS PASSED"
