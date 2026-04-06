# rift-srl: RIFT Protocol Implementation for SR Linux via NDK

## How to Use This Repo

This file lives at `docs/plan.md` in the repo.

**Starting a new session (any milestone):**
```
Read CLAUDE.md. It tells you where to find everything.
```

CLAUDE.md points to ARCHITECTURE.md (current state, decisions made) and
this file (milestone definitions). Between those three files, a fresh
session has full context without any external prompt.

---

## 1. RIFT Protocol Summary

[RFC 9692 (April 2025)](docs/reference/rfc9692.txt). [YANG model in RFC 9719](docs/reference/rfc9719.txt). Read these directly for
implementation details.

- Hybrid link-state (northbound) / distance-vector (southbound) protocol
  for Clos / fat tree IP fabrics.
- **LIE:** UDP multicast neighbor discovery. 3-way adjacency FSM:
  OneWay -> TwoWay -> ThreeWay. Multicast 224.0.0.121 (IPv4) /
  ff02::a1f7 (IPv6), port 914, TTL=1.
- **TIE:** Link-state database entries. North TIEs flood northbound.
  South TIEs flood southbound.
- **TIDE:** Periodic full LSDB summary for synchronization.
- **TIRE:** Request/acknowledge individual TIEs.
- **TIE/TIDE/TIRE transport:** UDP unicast between adjacency endpoints.
- **Wire format:** Apache Thrift serialization inside a security envelope.
- **SPF:** Northbound SPF computes paths to spines. Southbound computation
  installs default routes.
- **Positive disaggregation:** On failure, spines advertise more-specific
  prefixes southbound to prevent blackholes.

## 2. SR Linux NDK

gRPC API for custom agents. Proto docs: https://ndk.srlinux.dev/ (v0.5.0).
Go bindings: github.com/nokia/srlinux-ndk-go.

Services used:
- **SdkMgrService** - Agent registration, notification subscriptions
- **SdkMgrRouteService** - `RouteAddOrUpdate`, `RouteDelete`. Routes
  keyed by (network_instance_name, ip_prefix).
- **SdkMgrNextHopGroupService** - `NextHopGroupAddOrUpdate`,
  `NextHopGroupDelete`. Keyed by (name, network_instance_name).
- **SdkMgrTelemetryService** - `TelemetryAddOrUpdate` for state into IDB.
- **SdkNotificationService** - Streaming interface state, config, BFD.

**NDK has no packet I/O API.** RIFT packets use Linux UDP sockets in the
correct network namespace. Two channels:
1. gRPC to NDK: route programming, interface events, config, state
2. Linux UDP sockets: RIFT packets (LIE/TIE/TIDE/TIRE)

## 3. Scope

### Implement
- LIE exchange and 3-way adjacency FSM
- North TIE origination and flooding (Node TIEs, Prefix TIEs)
- South TIE origination (default route prefix TIEs)
- TIDE/TIRE database synchronization
- Northbound SPF computation
- Southbound default route installation
- Positive disaggregation (non-transitive, single-plane)
- FIB programming via NDK Route/NextHopGroup services
- Custom YANG model for RIFT config and state
- Interface state tracking via NDK notifications
- BFD integration via NDK BFD notifications
- Containerlab topology for testing

### Do not implement
ZTP, negative disaggregation, multi-plane, multi-PoD, security envelope
auth, KV TIEs, BAD, label binding, L2L shortcuts, mobility, IPv6 prefixes,
east-west forwarding at non-leaf levels.

### Dead end policy
If Go hits a fundamental blocker (Thrift codegen failure, namespace access
impossible), stop and report. Do not attempt Python workarounds or hybrid
approaches.

## 4. Architecture

```
┌─────────────────────────────────────────────┐
│                 rift-srl agent               │
│                                              │
│  ┌──────────────┐    ┌───────────────────┐   │
│  │  LIE Engine  │    │  TIE Flood Engine │   │
│  │  (per-intf   │    │  (TIDE/TIRE sync, │   │
│  │   FSM + UDP  │    │   LSDB mgmt)      │   │
│  │   mcast sock)│    │                   │   │
│  └──────┬───────┘    └────────┬──────────┘   │
│         │                     │              │
│         ▼                     ▼              │
│  ┌──────────────────────────────────────┐    │
│  │         Topology / LSDB              │    │
│  └──────────────────┬───────────────────┘    │
│                     │                        │
│                     ▼                        │
│  ┌──────────────────────────────────────┐    │
│  │    SPF Engine + Route Computation    │    │
│  └──────────────────┬───────────────────┘    │
│                     │                        │
│                     ▼                        │
│  ┌──────────────────────────────────────┐    │
│  │      NDK Integration Layer           │    │
│  │  (Route/NHG programming, interface   │    │
│  │   notifications, config, telemetry)  │    │
│  └──────────────────┬───────────────────┘    │
│                     │ gRPC                   │
└─────────────────────┼────────────────────────┘
                      │
              ┌───────▼────────┐
              │   ndk_mgr      │
              │   (IDB/FIB)    │
              └────────────────┘
```

### Concurrency model
- One goroutine per interface for LIE FSM + socket I/O
- One goroutine for NDK notification stream processing
- One goroutine for TIE flooding engine (processes flood queue)
- SPF triggered by LSDB changes, debounced (100ms hold-down)

### Isolation rules
- `internal/ndk/` owns NDK gRPC. Protocol logic must not import it.
- `internal/transport/` owns sockets. Protocol logic uses Go channels.
- `internal/tie/lsdb.go` is the single LSDB. SPF reads, flooding writes.
  Protected by sync.RWMutex.
- LIE FSMs are per-interface, independent, emit state via channels.

### Network namespace access
Use `github.com/vishvananda/netns` with `runtime.LockOSThread()` +
`netns.Set()`. Bind to interfaces via `SO_BINDTODEVICE`.

## 5. Repository Structure

```
rift-srl/
├── CLAUDE.md
├── AGENTS.md
├── ARCHITECTURE.md              # Living doc, updated after each milestone
├── README.md
├── go.mod
├── go.sum
├── docs/
│   └── plan.md                  # This file
├── cmd/
│   └── rift-srl/
│       └── main.go
├── internal/
│   ├── agent/
│   │   └── agent.go
│   ├── config/
│   │   └── config.go
│   ├── lie/
│   │   ├── fsm.go
│   │   ├── packet.go
│   │   └── interface.go
│   ├── tie/
│   │   ├── types.go
│   │   ├── lsdb.go
│   │   ├── flood.go
│   │   └── sync.go
│   ├── spf/
│   │   ├── northbound.go
│   │   └── southbound.go
│   ├── ndk/
│   │   ├── client.go
│   │   ├── routes.go
│   │   ├── notifications.go
│   │   └── telemetry.go
│   ├── transport/
│   │   ├── udp.go
│   │   ├── multicast.go
│   │   └── netns.go
│   └── encoding/
│       ├── thrift.go
│       └── envelope.go
├── yang/
│   └── rift-srl.yang
├── lab/
│   ├── topology.clab.yml
│   ├── configs/
│   │   ├── leaf1.cfg
│   │   ├── leaf2.cfg
│   │   ├── leaf3.cfg
│   │   ├── spine1.cfg
│   │   └── spine2.cfg
│   └── scripts/
│       ├── deploy.sh
│       └── verify.sh
├── proto/
│   └── rift/
│       ├── common.thrift
│       └── encoding.thrift
└── test/
    ├── lie_fsm_test.go
    ├── lsdb_test.go
    ├── spf_test.go
    └── integration/
        └── adjacency_test.go
```

## 6. Lab Topology

2-tier leaf-spine, single plane. 2 spines, 3 leaves, full mesh (6 links).

```
        ┌──────────┐     ┌──────────┐
        │ spine1   │     │ spine2   │
        │ level=1  │     │ level=1  │
        └─┬──┬──┬──┘     └──┬───┬─┬─┘
          │  │  │           │   │ │
     ┌────┘  │  └──────┐  ┌─┘   │ └────┐
     │       │         │  │     │      │
     │    ┌──┘    ┌────┘  │    ┌┘      │
     │    │       │       │    │       │
   ┌─┴────┴──┐  ┌─┴───────┴┐  ┌┴───────┴─┐
   │ leaf1   │  │  leaf2   │  │  leaf3   │
   │ level=0 │  │  level=0 │  │  level=0 │
   └─────────┘  └──────────┘  └──────────┘
```

## 7. Milestones

Execute sequentially. Do not start a milestone until the previous gate passes.
After each gate passes, update ARCHITECTURE.md.

### M0: Scaffolding and build pipeline
- Initialize Go module and repo structure per Section 5
- Set up Thrift codegen for RIFT schema (common.thrift, encoding.thrift
  from RFC 9692 Section 7). If codegen fails, stop and report.
- Verify NDK Go bindings compile and link
- Create containerlab topology file with SR Linux nodes
- Create minimal YANG model (enough for `admin-state enable`)
- Build minimal NDK agent: register, receive interface notifications, log.
  Deploy to SR Linux and verify.
- Write CLAUDE.md per Section 8
- Create initial ARCHITECTURE.md
- **Resolve before proceeding:**
  1. Which Go Thrift library works with RIFT's schema? Test
     `github.com/apache/thrift` first.
  2. How does an NDK agent discover which Linux network namespace
     corresponds to a given SR Linux interface? Options: parse
     `/var/run/netns/`, NDK network instance notifications, SR Linux
     internal state files.
  3. Map SR Linux interface names (`ethernet-1/1`) to Linux interface
     names in the namespace (`e1-1`) for socket binding.
  4. Verify UDP multicast (224.0.0.121) works from an NDK agent process
     on SR Linux interfaces.
  5. Evaluate Bond vs raw NDK gRPC client.
  6. Define agent packaging for containerlab deployment.
- **Gate:** Agent deploys, registers with NDK, logs interface events.
  Thrift codegen produces usable Go types. UDP multicast works on
  fabric interfaces. All findings documented in ARCHITECTURE.md.

### M1: LIE exchange and adjacency formation
- UDP multicast socket management (join/leave per interface)
- Network namespace discovery and socket binding
- LIE packet construction and parsing via Thrift
- LIE FSM per RFC 9692 Sec 6.2.1 (follow FSM table mechanically)
- Wire NDK interface notifications to LIE engine
- LIE validity checks (level, system ID, MTU)
- Holdtime expiry
- YANG config for level, system_id, enabled interfaces
- **Gate:** All 6 lab adjacencies reach ThreeWay. Adjacency state visible
  via `info from state`. Interface disable causes adjacency drop.

### M2: TIE origination and flooding
- LSDB data structures (TIE storage, indexed by TIE-ID)
- Self-originated TIE generation:
  - North Node TIE (adjacencies, capabilities)
  - North Prefix TIE (loopback / locally attached prefixes)
  - South Node TIE (adjacencies, capabilities)
  - South Prefix TIE (default route; populated after M3)
- Flooding rules per RFC 9692 Sec 6.3.3 and 6.3.4:
  - North TIEs flood northbound only
  - South TIEs flood southbound only
  - South Node TIEs reflect back north
- TIDE generation (periodic full LSDB summary)
- TIRE processing (request missing TIEs, ack received TIEs)
- TIE lifetime management (expiry, re-origination)
- Sequence number handling
- **Gate:** Spines hold North Node/Prefix TIEs from all leaves. Leaves
  hold South Node TIEs from spines. LSDB visible via telemetry.

### M3: SPF and route computation
- Northbound SPF using South Node TIEs
- Southbound reachability using North Node/Prefix TIEs
- RIB from SPF output (prefix -> set of next-hops with metrics)
- ECMP: multiple equal-cost next-hops per prefix
- **Gate:** Each leaf has default route via both spines. Each spine has
  routes to all leaf loopbacks. RIB contents logged.

### M4: NDK route programming
- NextHopGroup creation via NDK
- Route installation via RouteAddOrUpdate
- Route withdrawal via RouteDelete on topology changes
- Incremental RIB-to-FIB sync (deltas only)
- Wire SPF output to NDK route programming
- SyncStart/SyncEnd for bulk updates
- **Gate:** Leaf FIBs: default route to both spines. Spine FIBs: /32 to
  all leaf loopbacks. Ping between leaf loopbacks succeeds.

### M5: Convergence and disaggregation
- SPF re-run on LSDB changes (triggered, debounced)
- Route withdrawal on adjacency loss
- Positive disaggregation per RFC 9692 Sec 6.5.1
- Test: single link failure, spine failure, link recovery
- Overload bit support
- **Gate:** Bring down spine1-leaf3 link. Spine1 disaggregates leaf3's
  prefix southbound. leaf1/leaf2 route via spine2 only. Restore link,
  disaggregation withdraws. Ping works throughout.

### M6: Polish and documentation
- Complete YANG model for all config and state
- Full telemetry: adjacencies, LSDB, routes, disaggregation, counters
- README: architecture, build, lab setup, demo walkthrough
- Lab verification scripts
- Unit tests for LIE FSM, LSDB, SPF
- **Gate:** Passing tests, reproducible demo from clone to working fabric.

## 8. Key References

| Resource | Use |
|----------|-----|
|[RFC 9692](docs/reference/rfc9692.txt) | Primary spec. LIE FSM Sec 6.2.1, flooding 6.3, SPF 6.4, disaggregation 6.5, Thrift schema Sec 7 |
| [RFC 9719](docs/reference/rfc9719.txt) | YANG model reference for rift-srl.yang |
| https://ndk.srlinux.dev/ | NDK proto docs |
| https://learn.srlinux.dev/ndk/guide/dev/go/with-bond/ | NDK Go dev with Bond |
| https://github.com/nokia/srlinux-ndk-go | Go bindings |
| https://github.com/nokia/srlinux-ndk-protobufs | Proto files (v0.5.0) |
| https://github.com/brunorijsman/rift-python | Reference impl for cross-checking |

