# ARCHITECTURE.md

## Current Status
**Active milestone:** M1 (LIE)
**Last completed:** M0 (Scaffolding)
**Blockers:** None

## Overview
rift-srl is a RIFT (RFC 9692) implementation as an SR Linux NDK agent in Go.
Single-plane, configured levels, IPv4 only.

See docs/PLAN.md for milestone definitions and gate criteria.
See CLAUDE.md for conventions and orientation.

## Lab
Containerlab with SR Linux nodes. Topology in lab/topology.clab.yml.
2 spines (level 1), 3 leaves (level 0), full mesh, 6 links.

## Decisions Log

### M0: Thrift Codegen vs Hand-Written Types
**Choice:** Hand-written Go types with manual Thrift binary protocol codec
**Why:** Apache Thrift codegen was not installed (no sudo) and the known issue
with `map<union_type, struct>` in PrefixTIEElement (IPPrefixType is a union
used as a map key, which Go doesn't support natively) makes codegen unreliable.
Hand-writing is safe because the schema is frozen at major version 8 and the
total surface area is ~25 types.
**Alternatives considered:** Apache Thrift codegen (blocked by missing compiler
and union-as-map-key issue)

### M0: Bond vs Raw NDK gRPC
**Choice:** Raw NDK gRPC (github.com/nokia/srlinux-ndk-go v0.5.0)
**Why:** The "Bond" framework is actually just the raw NDK Go protobuf bindings.
There is no higher-level abstraction layer. The v0.5.0 package provides gRPC
client stubs generated from the NDK proto definitions.
**Alternatives considered:** None — Bond IS the raw gRPC client.

### M0: NDK Registration Requires agent_name Metadata
**Choice:** All gRPC calls to NDK include `agent_name` metadata header
**Why:** SR Linux v26.3.1 NDK SDK manager requires `agent_name` gRPC metadata
to identify which application is making the call. Without it, AgentRegister
returns SDK_MGR_STATUS_FAILED with empty error string. Also requires
`grpc.WithBlock()` on dial to ensure connection is established before first RPC.
**Alternatives considered:** Process name matching (doesn't work in v26.3.1)

### M0: Socket Architecture — Two Namespaces
**Choice:** Multicast receive in `srbase`, everything else in `srbase-default`

SR Linux creates two network namespaces per network instance:

| Namespace | Interfaces | IPv4 | Purpose |
|-----------|-----------|------|---------|
| `srbase` | parent: `e1-1`, `e1-2`, `e1-3` | none (IPv6 link-local only) | L2, multicast |
| `srbase-default` | subinterface: `e1-1.0`, `e1-2.0`, `e1-3.0` | yes (e.g. 10.1.1.0/31) | L3 unicast |

Connected by internal veth pair:
`e1-1` (srbase) <--veth--> `e1-1-0` (srbase) <--veth--> `e1-1.0` (srbase-default)

**Unicast** traverses this path (confirmed by tcpdump on all three interfaces).
**Multicast does NOT.** The SR Linux datapath (`sr_xdp_lc_1`) forwards unicast
via FIB lookup but has no multicast forwarding entries. Multicast packets stop
at the parent interface in `srbase`. This was verified exhaustively — see
"Multicast investigation" below.

**Agent socket model:**

| Socket | Namespace | Interface | Bind address | Purpose |
|--------|-----------|-----------|-------------|---------|
| LIE mcast recv | `srbase` | `e1-1` (parent) | `0.0.0.0:914` + `IP_ADD_MEMBERSHIP` | Receive LIE from peers |
| LIE mcast send | `srbase-default` | `e1-1.0` (sub) | subinterface IP:914 + `IP_MULTICAST_IF` | Send LIE with real src IP |
| TIE unicast | `srbase-default` | `e1-1.0` (sub) | subinterface IP:915 | Send/recv TIE/TIDE/TIRE |

The multicast receive socket is created once per interface at startup using
`runtime.LockOSThread()` + `netns.Set(srbase)`. After creation the fd works
from any namespace. The agent process otherwise runs entirely in `srbase-default`.

**Why this works:**
- Multicast send from `srbase-default` leaves with a real source IP (e.g.
  10.1.1.0), traverses the veth to the parent, goes on the wire
- Multicast arrives at the peer's parent interface in `srbase`, where the
  recv socket (created in `srbase`) picks it up with the peer's real source IP
- TIE unicast to the peer's IP goes through `srbase-default` L3 forwarding

#### Multicast investigation

Attempts to receive multicast in `srbase-default` (all failed):

1. **No join:** Multicast sent to 224.0.0.121:914, tcpdump shows packets on
   `e1-1` but zero on `e1-1-0` or `e1-1.0`.

2. **With IGMP join on subinterface:** `IP_ADD_MEMBERSHIP` on `e1-1.0` for
   224.0.0.121 (verified via `ip maddr show` and `/proc/net/igmp`). Still
   zero packets on `e1-1-0` or `e1-1.0`.

3. **Sourced from srbase-default:** Sent from spine1 `srbase-default` bound
   to 10.1.1.0 with `IP_MULTICAST_IF`. Packets arrive at peer's `e1-1` with
   real source IP. Still zero on `e1-1-0` or `e1-1.0`.

4. **CPM ACL counters:** Entry 900 (accept UDP 914) match count unchanged
   after multicast — packets never reach CPM. Not a CPM issue.

5. **SR Linux IGMP enabled:** `set protocols igmp admin-state enable` on
   interface. `group-count 0` — SR Linux IGMP doesn't see kernel socket joins.
   Static IGMP join blocked: 224.0.0.121 is in reserved 224.0.0.0/24 range.

6. **ALLMULTI flag:** Set on `e1-1-0` and `e1-1.0`. No effect.

7. **Duplicate IPs on parent:** Added same IP to parent `e1-1` in `srbase`.
   Caused ARP poisoning and broke SR Linux forwarding permanently on that link.

8. **tc mirred:** `tc filter add dev e1-1 parent ffff: protocol ip u32
   match ip dst 224.0.0.121/32 action mirred egress mirror dev e1-1-0`.
   This WORKS but is a hack — injects a Linux tc rule to work around the
   datapath. Not needed with the recv-in-srbase approach.

**Root cause:** The `sr_xdp_lc_1` datapath process handles all forwarding
between parent and subinterface namespaces. It does L3 FIB lookup for unicast.
For multicast, there are no FIB entries (`/proc/net/ip_mr_cache` empty) and
no multicast routing (`ip_forward=0`, `mc_forwarding=0`). The datapath drops
multicast before it reaches the internal veth. This may be specific to the
containerized/simulated datapath; real hardware ASICs may behave differently.

**To reproduce:**
```bash
# Deploy lab
containerlab deploy -t lab/topology.clab.yml

# Receiver on leaf1 e1-1.0 in srbase-default (with join):
docker exec clab-rift-leaf1 bash -c "ip netns exec srbase-default python3 -c \"
import socket, struct
sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM, socket.IPPROTO_UDP)
sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
sock.bind(('224.0.0.121', 914))
mreq = struct.pack('4s4s', socket.inet_aton('224.0.0.121'), socket.inet_aton('10.1.1.1'))
sock.setsockopt(socket.IPPROTO_IP, socket.IP_ADD_MEMBERSHIP, mreq)
sock.settimeout(5)
try:
    data, addr = sock.recvfrom(1024)
    print(f'RECEIVED from {addr}')
except socket.timeout:
    print('TIMEOUT - multicast not received on subinterface')
\""

# Sender on spine1 from srbase-default (real source IP):
docker exec clab-rift-spine1 bash -c "ip netns exec srbase-default python3 -c \"
import socket, struct
sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM, socket.IPPROTO_UDP)
sock.setsockopt(socket.IPPROTO_IP, socket.IP_MULTICAST_TTL, 1)
sock.bind(('10.1.1.0', 914))
ifidx = socket.if_nametoindex('e1-1.0')
mreqn = struct.pack('4s4sI', socket.inet_aton('0.0.0.0'), socket.inet_aton('10.1.1.0'), ifidx)
sock.setsockopt(socket.IPPROTO_IP, socket.IP_MULTICAST_IF, mreqn)
sock.sendto(b'TEST', ('224.0.0.121', 914))
print('Sent with src=10.1.1.0')
\""

# tcpdump confirms: parent gets it, subinterface does not
docker exec clab-rift-leaf1 ip netns exec srbase \
  tcpdump -c 1 -i e1-1 'dst 224.0.0.121' -n          # captures
docker exec clab-rift-leaf1 ip netns exec srbase-default \
  tcpdump -c 1 -i e1-1.0 'dst 224.0.0.121' -n         # times out
```

### M0: CPM ACL Required for RIFT UDP Unicast
**Choice:** Add CPM ACL entries for UDP ports 914 (LIE) and 915 (TIE flood)
**Why:** SR Linux's default CPM ACL (entry 1000: "Drop all else") drops all
unmatched traffic to the control plane. Without explicit allow rules for RIFT
ports, unicast UDP packets arriving on subinterfaces in `srbase-default` are
silently dropped. Verified by sending unicast UDP to 10.1.1.1:915 before and
after adding the ACL: before = timeout, after = received. ICMP ping works
without ACL changes because ICMP is explicitly allowed by default entries
10/20/40/50.

CPM ACL entries added to all startup configs (entries 900, 901):
```
set / acl acl-filter cpm type ipv4 entry 900 match ipv4 protocol udp
set / acl acl-filter cpm type ipv4 entry 900 match transport destination-port operator eq
set / acl acl-filter cpm type ipv4 entry 900 match transport destination-port value 914
set / acl acl-filter cpm type ipv4 entry 900 action accept
set / acl acl-filter cpm type ipv4 entry 901 match ipv4 protocol udp
set / acl acl-filter cpm type ipv4 entry 901 match transport destination-port operator eq
set / acl acl-filter cpm type ipv4 entry 901 match transport destination-port value 915
set / acl acl-filter cpm type ipv4 entry 901 action accept
```

### M0: NDK Notification Two-Step Subscription
**Choice:** Create stream first (OPERATION_CREATE, no type), then add subscription
(OPERATION_ADD_SUBSCRIPTION with interface type)
**Why:** Single-step CREATE with subscription type returns SUCCESS but silently
does not register the subscription (NotificationQuery shows 0 subscriptions).
Two-step approach produces working notifications.
**Alternatives considered:** Single-step CREATE (subscription not registered)

## M0: Scaffolding

### Resolved Questions

**1. Thrift library:** Hand-written Go types using Thrift binary protocol format
directly (no external Thrift library dependency). Encoder/Decoder in
`internal/encoding/codec.go`. Round-trip tests pass for LIE, Node TIE, and
Prefix TIE packets.

**2. Network namespace:** Agent runs in `srbase-default`. Multicast receive
sockets created in `srbase` via `runtime.LockOSThread()` + `netns.Set()` at
startup (fd works from any namespace after creation). See "Socket Architecture"
decision above for full details.

**3. Interface name mapping:** `ethernet-1/X` → `e1-X` (parent in `srbase`)
and `e1-X.0` (subinterface in `srbase-default`). Simple string substitution.

**4. UDP transport:**
  - LIE send: IPv4 multicast to 224.0.0.121:914 from `srbase-default`, bound to
    subinterface IP, `IP_MULTICAST_IF` set. Packet leaves with real source IP.
  - LIE recv: In `srbase` on parent interface. `IP_ADD_MEMBERSHIP` with
    `ip_mreqn` (ifindex). `SO_BINDTODEVICE` to parent.
  - TIE/TIDE/TIRE: IPv4 unicast in `srbase-default`. Peer IP learned from LIE
    source address. Requires CPM ACL entries 900/901 for UDP ports 914/915.

**5. Bond vs raw gRPC:** The NDK Go package IS the raw gRPC bindings. No
higher-level Bond wrapper exists in v0.5.0.

**6. Agent packaging:** Static Go binary at `/usr/local/bin/rift-srl`, app manager
YAML at `/etc/opt/srlinux/appmgr/rift-srl.yml`, YANG at
`/opt/rift-srl/yang/rift-srl.yang`. Deployed via `docker cp` and app manager
restart. See `lab/scripts/deploy.sh`.

### Gate Results
- Agent deploys and registers on all 5 SR Linux nodes
- Agent state `running` visible via `info from state system app-management application rift-srl`
- Interface notification subscription active (stream_id=1)
- Thrift types for ProtocolPacket/LIE/TIE/TIDE/TIRE implemented and tested
- Round-trip serialization tests pass (4 tests)
- UDP multicast verified between nodes on fabric interfaces
- YANG model `rift-srl` loaded successfully (visible in CLI)
- SR Linux version: v26.3.1, NDK proto v0.5.0

## M1: LIE
<!-- filled in after M1 gate passes -->

## M2: TIE Flooding
<!-- filled in after M2 gate passes -->

## M3: SPF
<!-- filled in after M3 gate passes -->

## M4: Route Programming
<!-- filled in after M4 gate passes -->

## M5: Disaggregation
<!-- filled in after M5 gate passes -->

## M6: Polish
<!-- filled in after M6 gate passes -->
