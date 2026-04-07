# ARCHITECTURE.md

## Current Status
**Active milestone:** None (all milestones complete)
**Last completed:** M6 (Polish)
**Blockers:** None

## Overview
rift-srl is a RIFT (RFC 9692) implementation as an SR Linux NDK agent in Go.
Single-plane, configured levels, IPv4 only.

See docs/PLAN.md for milestone definitions and gate criteria.
See CLAUDE.md for conventions and orientation.

## Lab
Containerlab with SR Linux nodes. Topology in lab/topology.clab.yml.
2 spines (level 1), 3 leaves (level 0), full mesh, 6 links.

## Key Implementation Challenges

### Multicast Reception on SR Linux

RIFT uses UDP multicast (224.0.0.121:914) for LIE neighbor discovery. SR Linux
splits each physical interface across two network namespaces: `srbase` holds the
parent interface (`e1-1`) with L2/link-local connectivity, while `srbase-default`
holds the subinterface (`e1-1.0`) with IPv4 addresses. These are connected by an
internal veth pair, and the `sr_xdp_lc_1` datapath bridges traffic between them
using L3 FIB lookups for unicast.

The problem: multicast has no FIB entries. Packets arriving on the wire reach the
parent interface in `srbase` and stop there. They never cross to `srbase-default`.
This was verified by eight different approaches (IGMP joins, ALLMULTI, CPM ACL
counters, tc mirred, etc.), all documented in the M0 decisions below. The root
cause is that `sr_xdp_lc_1` performs no multicast forwarding
(`/proc/net/ip_mr_cache` empty, `mc_forwarding=0`). This may be specific to the
containerized datapath; real hardware ASICs may behave differently.

**Solution:** Create multicast receive sockets in `srbase` (where multicast
actually arrives) using `runtime.LockOSThread()` + `netns.Set()`. Once a socket
fd is created, it remains bound to its creation namespace regardless of which
OS thread later uses it. Multicast send still goes from `srbase-default` (which
has real source IPs); the packet traverses the veth to the parent and goes on the
wire. The peer's recv socket in `srbase` picks it up with the real source IP.

See M0 decision "Socket Architecture" for the full socket model, and the
"Multicast investigation" section for reproduction steps.

### Namespace Switching in Go

The agent must operate in both `srbase` (multicast recv) and `srbase-default`
(unicast, address discovery, flood sockets) simultaneously. Linux namespace
switching via `setns(2)` is per-thread, but Go's goroutine scheduler freely
migrates goroutines across OS threads.

**Solution:** `runtime.LockOSThread()` pins the goroutine to a dedicated OS
thread before calling `netns.Set()`. The pattern for socket creation is:

1. Lock goroutine to OS thread
2. Save current namespace fd
3. Switch to target namespace (`srbase-default`)
4. Create and configure socket (bind, setsockopt)
5. Restore original namespace
6. Unlock OS thread

After creation, the socket fd works from any thread/namespace. This is used for:
multicast send sockets, flood (TIE unicast) sockets, interface address discovery,
and subinterface index lookups. The multicast recv socket needs no switch because
the agent process already runs in `srbase` (where app manager launches it).

An additional wrinkle: the agent runs as the unprivileged `srlinux` user. The
`srbase` namespace has `ip_unprivileged_port_start=0` (any port allowed), but
`srbase-default` uses the default of 1024. RIFT's TIE flood port 915 is below
this threshold. The deploy script sets
`sysctl net.ipv4.ip_unprivileged_port_start=0` in `srbase-default` before
starting the agent.

### Local Prefix Discovery

RIFT only runs LIE/adjacency on fabric-facing links. But leaf nodes must
advertise host-facing prefixes (e.g., `10.10.1.0/24`) northbound in their North
Prefix TIEs. RFC 9692 does not define configuration for non-RIFT interfaces; it
leaves local prefix discovery as an implementation detail.

**Solution:** At startup, enumerate all IPv4 interfaces in `srbase-default` via
Go's `net.Interfaces()` + `Addrs()`. This discovers loopbacks (`system0.0`,
mapped to /32), fabric link subnets, and host-facing subnets. Internal SR Linux
interfaces are filtered: `lo` (loopback flag), `gateway`, `mgmt0.0`, and any
127.x.x.x addresses.

**Tradeoff:** This couples prefix origination to Linux namespace state. Any
interface that happens to exist in `srbase-default` gets advertised. For the lab
topology this is correct, but a production implementation would want config-driven
prefix selection (explicit prefix list or passive interface flag).

See M4 decision "Local Prefix Discovery via Namespace Enumeration" for details.

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

### Decisions Log

#### M1: Agent Runs in srbase Namespace
**Choice:** Agent process runs in `srbase` namespace, not `srbase-default`
**Why:** SR Linux app manager launches agents in the `srbase` namespace.
The M0 architecture document assumed `srbase-default`, but testing confirmed
the actual namespace is `srbase`. All socket operations and interface address
discovery explicitly switch namespaces as needed:
- Multicast recv socket: created in `srbase` (current ns, no switch needed)
- Multicast send socket: created in `srbase-default` via `unix.Setns()`
- Interface address discovery: performed in `srbase-default` via `unix.Setns()`
- Subinterface index lookup: performed in `srbase-default` via `unix.Setns()`

#### M1: NDK Config Delivery Model
**Choice:** Accumulate config across multiple notifications per commit batch
**Why:** NDK delivers RIFT config as multiple separate notifications:
1. `.rift` path with JSON: `{"admin-state":"enable","system-id":"1","level":1}`
2. `.rift.interface{.name=="ethernet-1/X"}` per interface (empty data, name in path)
3. `.commit.end` signals end of batch

Field names use YANG hyphenated naming (`admin-state`, `system-id`).
`system-id` is delivered as a JSON string, not a number.
Interface names are extracted from `JsPathWithKeys` or `Keys` fields.

#### M1: NDK Server Must Be Explicitly Enabled
**Choice:** Add `set / system ndk-server admin-state enable` to startup configs
**Why:** SR Linux v26.3.1 does not enable the NDK server by default. Without it,
the `sr_sdk_service_manager:50053` unix socket does not exist and the agent
blocks on `grpc.Dial()` with `WithBlock()`. The deploy script now applies this
config along with RIFT-specific config after the app manager reload.

#### M1: Initial Interface Notifications Have Zeroed Data
**Choice:** Ignore interface events where admin_up=false, oper_up=false, mtu=0
**Why:** NDK sends initial interface notifications with all fields zeroed before
delivering real state. Acting on these would incorrectly stop interfaces that
were just started. Real admin-down or oper-down events always have non-zero MTU.

### Gate Results
- All 6 lab adjacencies reach ThreeWay within ~5 seconds of agent start
- Adjacency state visible via `info from state rift` on all 5 nodes
- Each adjacency shows: state, neighbor-system-id, neighbor-level, neighbor-address
- Interface disable (admin-state disable) causes adjacency drop to one-way
- Interface re-enable causes adjacency recovery to three-way
- LIE FSM unit tests: 17 tests passing (14 table-driven + 3 focused)
- Transport unit tests: 4 tests passing (interface name mapping)
- Existing codec tests: 4 tests passing (unchanged)
- SR Linux version: v26.3.1, NDK proto v0.5.0

## M2: TIE Flooding

### Decisions Log

#### M2: Unprivileged Port Binding in srbase-default
**Choice:** Set `net.ipv4.ip_unprivileged_port_start=0` in srbase-default
**Why:** The agent runs as user `srlinux` (not root). The srbase namespace has
`ip_unprivileged_port_start=0` (any port allowed), but srbase-default has the
default value of 1024. TIE flood port 915 requires binding below 1024. The
deploy script sets the sysctl before starting the agent.
**Alternatives considered:** Creating the socket in srbase (would not work for
unicast routing), using CAP_NET_BIND_SERVICE (not available to srlinux user)

#### M2: LSDB Telemetry via Summary Leaf
**Choice:** Push LSDB as a formatted string to a single `lsdb-summary` leaf
**Why:** NDK telemetry does not support pushing to YANG list entries with
composite enumeration keys. A YANG `list tie` with `key "direction originator
tie-type tie-nr"` could not receive telemetry updates via the NDK
`TelemetryAddOrUpdate` API. Using a single leaf with a formatted summary string
provides visibility without YANG complexity.
**Alternatives considered:** Per-TIE list entries (NDK path format incompatible
with enum keys)

#### M2: TIE Acceptance Scope Check
**Choice:** Nodes only accept TIEs they should hold per flooding scope
**Why:** Without scope filtering on TIE acceptance, TIDE synchronization caused
incorrect TIE distribution. Spines would include leaf North TIE headers in
southbound TIDEs (correct per RFC for synchronization), but other leaves would
then request those TIEs (incorrect, leaves should not hold other leaves' North
TIEs). Adding `shouldAcceptTIE()` prevents requesting or installing TIEs that
would not normally be flooded to the receiving node.

### Gate Results
- Spines hold North Node/Prefix TIEs from all 3 leaves
- Leaves hold South Node/Prefix TIEs from both spines
- Self-originated TIEs present on all nodes (Node + Prefix, North + South as appropriate)
- South Node TIE reflection working (spine2's South Node TIE visible on spine1)
- Leaves do NOT hold other leaves' North TIEs (scope filtering correct)
- LSDB visible via `info from state rift` on all nodes
- TIE sequence numbers increment on adjacency changes
- LSDB unit tests: 18 tests passing (TIEID ordering, header comparison, LSDB ops, scope rules)
- All existing tests: 43 tests passing (encoding 4, LIE FSM 17, transport 4, TIE 18)
- SR Linux version: v26.3.1, NDK proto v0.5.0

## M3: SPF

### Decisions Log

#### M3: No Separate Graph Data Structure
**Choice:** Run Dijkstra directly on the LSDB snapshot map
**Why:** The LSDB already indexes TIEs by TIEID (direction, originator, type, nr).
SPF can look up any node's South Node TIE or North Node TIE with a simple map
lookup. Building a separate adjacency graph would duplicate information and
require synchronization. The `Snapshot()` method provides a consistent copy
under a single read lock.

#### M3: Backlink Verification via Opposite-Direction Node TIEs
**Choice:** S-SPF checks North Node TIEs for backlinks, N-SPF checks South Node TIEs
**Why:** RFC 9692 Section 6.4 requires verifying that a neighbor acknowledges the
link before including it in the SPF tree. For southbound SPF (traversing South
Node TIEs downward), the backlink is found in the neighbor's North Node TIE.
For northbound SPF, the backlink is in the spine's South Node TIE. This
prevents unidirectional links from being used in routing.

#### M3: 100ms LSDB Change Debounce
**Choice:** SPF runs 100ms after the last LSDB change notification
**Why:** Multiple TIEs often arrive in rapid succession (adjacency changes trigger
re-origination of multiple TIEs). Running SPF after each individual TIE would
waste CPU. The flood engine sends non-blocking notifications on LSDBChangeCh
whenever a TIE is installed, updated, or expires. The agent event loop resets
a 100ms timer on each notification, so SPF runs once after the burst settles.

#### M3: Combined State Telemetry Push
**Choice:** Push `lsdb-summary` and `rib-summary` in a single `TelemetryAddOrUpdate` call
**Why:** NDK `TelemetryAddOrUpdate` replaces the entire JSON at the given path.
Pushing LSDB and RIB separately to `.rift` caused whichever ran second to
overwrite the first. Combining both fields into one struct and pushing once
ensures both leaves are always populated.

### Gate Results
- Each leaf computes default route `0.0.0.0/0` via both spines (2 ECMP next-hops, metric 1)
- Each spine computes routes to all 3 leaf loopbacks (`10.0.1.1/32`, `10.0.1.2/32`, `10.0.1.3/32`, metric 2)
- Spines also compute routes to leaf link subnets (6 additional /31 routes)
- S-SPF reports 3 reachable nodes, 9 routes per spine
- N-SPF reports "no validated northbound neighbors" for spines (correct, no superspines)
- RIB contents logged with prefix, metric, route type, and next-hop system IDs
- RIB visible via `info from state rift rib-summary` on all nodes
- SPF unit tests: 28 tests passing (helpers 22, northbound 8, southbound 10)
- All existing tests: 71 tests passing (encoding 4, LIE FSM 17, transport 4, TIE 18, SPF 28)
- SR Linux version: v26.3.1, NDK proto v0.5.0

## M4: Route Programming

### Decisions Log

#### M4: NHG Naming Convention
**Choice:** NHG names follow `rift_<prefix>_<length>_sdk` pattern (e.g. `rift_0.0.0.0_0_sdk`)
**Why:** NDK requires next-hop group names to end with `_sdk` or `_SDK` suffix. Using one
NHG per prefix with a deterministic name is simple, debuggable, and sufficient for this
scale. The name encodes the prefix so operators can identify what each NHG serves.

#### M4: Full Sync via SyncStart/SyncEnd
**Choice:** Push ALL routes between SyncStart/SyncEnd on every SPF run
**Why:** NDK SyncEnd deletes any routes not added during the sync window. An incremental
approach (only pushing deltas) caused previously-installed routes to be removed by SyncEnd.
The correct pattern is: SyncStart, push the complete RIB, SyncEnd. SyncEnd automatically
removes stale routes. NHGs are only created/updated when their next-hops actually change,
avoiding unnecessary churn.

#### M4: Local Prefix Discovery via Namespace Enumeration
**Choice:** Auto-discover all IPv4 prefixes in `srbase-default` namespace for North
Prefix TIE origination, rather than requiring explicit config per host-facing interface
**Why:** RFC 9692 does not define configuration for non-RIFT interfaces. RIFT only runs
(LIE/adjacency) on fabric-facing links. Host-facing prefixes are simply locally attached
routes that a leaf advertises northbound in its North Prefix TIE. The RFC leaves local
prefix discovery as an implementation detail. Enumerating all IPv4 interfaces in the
namespace is pragmatic but not ideal: it couples prefix origination to Linux namespace
state and picks up any interface that happens to exist (internal SR Linux interfaces like
`gateway` and `mgmt0.0` must be filtered explicitly). A cleaner long-term approach would
be config-driven (explicit prefix list or passive interface flag), but this works for now.
**Filtered:** `lo` (loopback flag), `gateway`, `mgmt0.0`, and any 127.x.x.x addresses

#### M4: Deploy Script Agent Restart
**Choice:** Explicitly restart the rift-srl agent before app_mgr reload
**Why:** `app_mgr reload` only restarts agents when the YAML or YANG model changes. When
only the binary is updated, the running agent continues with the old code. Adding an
explicit `tools system app-management application rift-srl restart` ensures the new binary
is always loaded on deploy.

### Gate Results
- Leaf FIBs: default route 0.0.0.0/0 via both spines (2 ECMP next-hops, metric 1, preference 250)
- Spine FIBs: /32 routes to all 3 leaf loopbacks (10.0.1.1, 10.0.1.2, 10.0.1.3, metric 2)
- Spine FIBs: 6 additional /31 link subnet routes + 3 host subnet /24 routes
- Ping between leaf loopbacks: 0% packet loss, TTL=63 (traversing spines)
- Ping between hosts (host1 10.10.1.10 to host2 10.10.2.10, host3 10.10.3.10): 0% loss, TTL=61
- Route owner shown as `rift-srl` in SR Linux route table
- NHGs resolve correctly with `resolved true`
- Routes automatically update on LSDB changes (SPF debounce + full sync)
- All existing tests: 71 tests passing (encoding 4, LIE FSM 17, transport 4, TIE 18, SPF 28)
- SR Linux version: v26.3.1, NDK proto v0.5.0

## M5: Disaggregation

### Decisions Log

#### M5: Overload Bit on Leaf Node TIEs
**Choice:** Leaves unconditionally set `Flags.Overload = true` on their North Node TIE
**Why:** RFC 9692 Section 6.8.2 states leaf nodes SHOULD set the overload attribute
on all originated Node TIEs. Overloaded nodes are terminal-only in S-SPF: their
prefixes are reachable but they are not transited. This prevents spines from
attempting to route through leaves to reach other leaves.

#### M5: Disaggregation Computation in SPF Package
**Choice:** `internal/spf/disagg.go` computes positive disaggregation after S-SPF
**Why:** The disaggregation algorithm (RFC 9692 Section 6.5.1) requires the S-SPF
result (reachable prefixes with next-hops) plus reflected South Node TIEs from
same-level peers. Placing it in the `spf` package keeps route computation logic
together. The agent mediates between `spf` and `tie` packages: it runs the
computation, then sends the result to the flood engine via `DisaggUpdateCh` for
TIE origination.

#### M5: Disaggregation TIE Withdrawal via Empty TIE
**Choice:** Withdraw by originating an empty positive disagg TIE with bumped sequence number
**Why:** Simply removing the TIE from the local LSDB causes TIDE convergence to
re-request it from peers who still have the old version, creating an oscillation.
Originating an empty TIE with a higher sequence number propagates the withdrawal
through normal TIDE/TIRE synchronization. Peers receive the empty version and
remove the disaggregated routes from their RIBs.

#### M5: Spine2 Disaggregates (Not Spine1)
**Choice:** The spine that still has connectivity to the affected leaf disaggregates
**Why:** Per RFC 9692 Section 6.5.1, a node disaggregates prefixes reachable via
its south neighbors that other same-level peers cannot reach. When spine1 loses
its link to leaf3, spine2 (which still has the link) detects that spine1's
reflected South Node TIE no longer lists leaf3. Spine2 then originates a South
Positive Disaggregation Prefix TIE containing leaf3's prefixes, attracting traffic
from leaves that would otherwise blackhole via spine1.

### Gate Results
- Disable spine1-leaf3 link: spine2 originates South Positive Disaggregation Prefix TIE
- leaf1 and leaf2 install leaf3's /32 and /24 via spine2 only (more-specific wins over default)
- Default route 0.0.0.0/0 still has ECMP via both spines
- Ping from host1 to host3: 0% packet loss throughout
- Re-enable spine1-leaf3 link: spine2 withdraws disaggregation TIE (empty version with bumped seq)
- leaf1 and leaf2 revert to default route only
- Ping continues working throughout
- Overload bit set on all leaf North Node TIEs
- S-SPF respects overload bit (terminal-only, no transit)
- All tests: 79 tests passing (encoding 4, LIE FSM 17, transport 4, TIE 18, SPF 36)
- SR Linux version: v26.3.1, NDK proto v0.5.0

## M6: Polish

### Decisions Log

#### M6: Telemetry Scalars Over YANG Lists
**Choice:** New state leaves are scalar counters and string summaries, not YANG list entries
**Why:** The M2 decision established that NDK telemetry cannot push to YANG list entries
with composite enumeration keys. Scalar leaves (`spf-runs`, `adjacency-count`,
`lsdb-tie-count`, `route-count`) and string summaries (`disaggregation-summary`) work
reliably with the NDK `TelemetryAddOrUpdate` API, following the same pattern as the
existing `lsdb-summary` and `rib-summary` leaves.

### Gate Results
- YANG model: 6 config leaves, 12 state leaves (oper-state, per-interface adjacency, lsdb-summary, rib-summary, spf-runs, adjacency-count, lsdb-tie-count, route-count, disaggregation-summary)
- Full telemetry: counters and disaggregation state pushed to IDB alongside existing summaries
- README.md: architecture, build, lab setup, demo walkthrough, YANG reference
- verify.sh: 27 checks (agent status, adjacencies, LSDB, routes, host-to-host ping, disaggregation failure/recovery)
- All unit tests: 26 test functions, 122 subtests passing (encoding 4, LIE FSM 5+subtests, transport 1, TIE 7+subtests, SPF 9+subtests)
- Lab verification: 23/23 base checks + 4/4 disaggregation checks = 27/27 ALL PASSED
- Reproducible demo from clone to working fabric confirmed
- SR Linux version: v26.3.1, NDK proto v0.5.0
