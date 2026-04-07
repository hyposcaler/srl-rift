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

## Post-MVP Cleanup

These milestones address bugs, races, and correctness issues found during
review of the M0-M6 MVP. They are independent and can be executed in any
order, though C1 (correctness fixes) should land before C2/C3 since later
work touches the same files. Each is sized to fit comfortably in a single
Claude Code session.

Unlike M0-M6, these are not gated by RFC features. They are gated by:
- All existing unit tests pass
- `go test -race ./...` is clean
- New unit tests for the specific issues addressed
- Lab verification (`bash lab/scripts/verify.sh --full`) still passes 27/27

### C1: Correctness fixes

Pure bug fixes. No refactoring, no new features. Each item is a known
incorrect behavior identified in review.

**LSDB snapshot race**
`LSDB.Snapshot()` returns a map of `*LSDBEntry` pointers that alias live
data. `bumpOwnTIE` and `DecrementLifetimes` mutate those entries in place
while SPF reads them from another goroutine. Fix by deep-copying entries
in `Snapshot()` (copy the `LSDBEntry` struct and the `TIEPacket` it points
to, including the `Header` which is the field actually mutated). Document
that `Get()` and `ForEachSorted()` return live pointers and must only be
called from the flood engine goroutine; SPF must use `Snapshot()`.

**SystemID truncated to LinkID in TIE origination**
`originateNorthNodeTIE` and `originateSouthNodeTIE` both do
`RemoteID: encoding.LinkIDType(adj.Info.NeighborID)`, casting an int64
system ID to an int32 link ID and silently dropping the upper bits. The
remote link ID should be the peer's `local_id` from their LIE packet.
Plumb the peer's local ID into `lie.NeighborState` (already partially
there as `LinkID`), then into `tie.AdjacencyInfo` as a new field
`NeighborLinkID`, and use it when building `LinkIDPair`.

**Thrift decoder unbounded allocations**
`decodeTIDEPacket`, `decodeTIREPacket`, `decodeNodeTIEElement` (neighbors
map), `decodePrefixTIEElement`, `decodeNodeNeighborsTIEElement` (LinkIDs
set), `decodeKeyValueTIEElement`, and the address-family / miscabled-links
/ same-plane-tofs sets all read a length from the wire and immediately
`make([]T, n)`. A malicious or corrupt packet with `n = 2_000_000_000`
OOMs the agent. Add a sanity cap (10000 elements feels right for RIFT
collections) at every call site and return an error if exceeded. Add a
helper `(d *Decoder) readCollectionLen(max int32) (int32, error)` to
avoid copy-paste.

**TIDE chunk range tiling**
`sendTIDEsToAdj` produces TIDE chunks where the `start_range`/`end_range`
of consecutive chunks do not tile the TIEID space, leaving gaps between
`chunk[i-1].last` and `chunk[i].first`. RFC 9692 §4.2.3.3 requires
contiguous coverage so the receiver can correctly identify which TIEs
the sender does not have. Also fix the off-by-one that emits an empty
trailing TIDE when `len(headers)` is an exact multiple of
`MaxTIDEHeaders`. Replace the `for i := 0; i <= len/max; i++` loop with
`for start := 0; start < len(headers); start += MaxTIDEHeaders` and set
each chunk's `startRange` to the previous chunk's `endRange` (or
`MinTIEID` for the first), `endRange` to `chunk[last].TIEID` (or
`MaxTIEID` for the last).

**Leaf level acceptance too strict**
`FSM.levelAcceptable` rejects any non-leaf neighbor above level 1 when
this node is a leaf. RFC 9692 allows a leaf to peer with any non-leaf
regardless of level. Replace the leaf-specific branch with: a leaf
accepts any non-leaf neighbor; a non-leaf rejects neighbors with level
difference > 1 unless the neighbor is a leaf.

**Adjacency event channel drops**
`FSM.enterState` does a non-blocking send on `stateChangeCh` and logs a
warning on full. A dropped `ThreeWay -> OneWay` transition leaves a
zombie flood socket and a stale entry in the flood engine's adjacency
map. Change to a blocking send. The FSM has no real-time constraint
that justifies dropping state changes.

**Dead `runDisaggregation` interleaved with `syncRoutes` doc comment**
In `internal/agent/agent.go`, the `runDisaggregation` function is
textually interleaved with the doc comment of `syncRoutes` (the comment
ends mid-sentence, the function definition appears, then the comment
resumes). This compiles but is an editing accident. Move
`runDisaggregation` to its own location (or to `agent/disagg.go` if C2
runs first).

**Unused `math` import in `encoding/codec.go`**
Drop `var _ = math.MaxInt32` and remove the `math` import. Remove
`var _ = sort.Slice` since `sort` is used elsewhere in the file.

**Resolve before proceeding:** None. Each item has a clear fix.

**Gate:**
- New tests: LSDB snapshot test that mutates an entry through `bumpOwnTIE`
  while a snapshot is held and verifies the snapshot is unchanged
- New tests: TIDE chunking test that asserts contiguous range coverage
  for header counts of 0, 1, MaxTIDEHeaders-1, MaxTIDEHeaders,
  MaxTIDEHeaders+1, 2*MaxTIDEHeaders
- New tests: decoder bounds tests that feed crafted packets with
  oversized lengths and verify clean error returns (not panics, not OOM)
- New tests: leaf-to-level-2 LIE acceptance
- All existing tests pass
- `go test -race ./...` clean
- Lab verification 27/27 with `--full`

### C2: Agent refactor and concurrency cleanup

Restructuring without behavior changes. The goal is making the locking
model explicit and shrinking `agent.go` to something a new reader can
understand in one sitting.

**Split `agent.go`**
Currently ~700 lines with eight responsibilities. Split into:
- `agent/agent.go` — struct, `New`, `Run`, `Close`, `eventLoop`,
  interface lifecycle (`startInterfaceLocked`, `stopInterfaceLocked`,
  `startConfiguredInterfaces`, `handleInterfaceEvent`, keepalive)
- `agent/config.go` — `processConfigEvent`, `waitForConfig`,
  `handleConfigChange`
- `agent/routes.go` — `syncRoutes`, `withdrawAllRoutes`, `nhgName`,
  `nhAddrsEqual`, `programmedRoute`
- `agent/telemetry.go` — `updateAdjacencyTelemetry`, `updateLSDBTelemetry`,
  `updateRIBTelemetry`, `updateStateTelemetry`, `formatLSDBSummary`,
  `lsdbTIESummary`, `interfaceTelemetry`, `adjacencyTelemetry`,
  `disaggSummaryString`, `ribSummaryString`
- `agent/disagg.go` — `runDisaggregation`
- `agent/flood.go` — `floodSendLoop`, `floodRecvRelay`,
  `handleAdjacencyEvent`, `discoverLocalPrefixes`

Target: no file in `internal/agent/` over 300 lines.

**Lock discipline cleanup**
The current code half-uses `a.mu` and half-relies on the
single-goroutine invariant of `eventLoop`. Pick the single-goroutine
model and commit to it: document at the top of `agent.go` that all
methods on `*Agent` except `Close` and the channel-receiving callbacks
must only be called from the goroutine running `eventLoop`. Drop `a.mu`
where it is unnecessary. Keep it only where genuinely concurrent access
exists (the per-interface map accessed by `Close` from a different
goroutine).

Specifically: `handleAdjacencyEvent` currently locks/unlocks `a.mu`
around an interface map lookup, then operates on `iface.transport` and
`iface.floodCancel` after releasing the lock. This is a TOCTOU. After
the lock model is committed, hold the lock for the whole handler or
drop the lock entirely if the goroutine model justifies it.

**Unify `ReceivedPacket` and `ReceivedFloodPkt`**
`internal/transport.ReceivedPacket` carries `SrcAddr netip.Addr`.
`internal/tie.ReceivedFloodPkt` carries `SrcAddr string`. They exist
because the flood engine wants the source as a string but the transport
returns netip.Addr. The agent's `floodRecvRelay` goroutine exists solely
to convert between them. Use `netip.Addr` everywhere, delete
`tie.ReceivedFloodPkt`, have the flood engine consume
`transport.ReceivedPacket` directly, and delete `floodRecvRelay`.

This does mean `internal/tie` would import `internal/transport`, which
breaks the layering rule in CLAUDE.md. Alternative: define a minimal
shared type in a new `internal/wire` package that both import. Pick
whichever is less disruptive; the layering rule exists to keep NDK out
of protocol logic, not to prevent transport types from being shared.

**Extract `inNamespace` helper in transport**
`internal/transport/udp.go` has six functions
(`getIfIndexInNS`, `createMcastRecvSocket`, `createMcastSendSocket`,
`createUnicastSocket`, `DiscoverInterfaceAddr`,
`DiscoverInterfacePrefix`, `DiscoverAllPrefixes`) that each repeat the
same `LockOSThread` / save current ns / open target ns / `Setns` /
defer restore pattern. Extract:

```go
func inNamespace(nsPath string, fn func() error) error
```

and rewrite the seven callers. Each shrinks by ~20 lines and the
error handling becomes consistent. The two socket-creation functions
need slight care because the fd they create must outlive the namespace
switch — that already works since the fd is namespace-bound at creation,
but the helper signature should make this obvious.

**Resolve before proceeding:**
1. Do we move `tie.ReceivedFloodPkt` types to a shared package, or
   accept the layering exception? Decide before starting.

**Gate:**
- All existing tests pass
- `go test -race ./...` clean
- No file in `internal/agent/` exceeds 300 lines
- `internal/agent/agent.go` has a documented goroutine model at the top
- `floodRecvRelay` no longer exists
- Lab verification 27/27 with `--full`

### C3: Decoder fuzzing and agent test coverage

Test infrastructure for the parts that currently have none. The MVP has
good coverage for `encoding`, `lie`, `tie`, and `spf`, but nothing for
`agent`, `config`, `ndk`, or most of `transport`.

**Thrift decoder fuzz target**
Add `internal/encoding/fuzz_test.go` with a Go native fuzz target:

```go
func FuzzDecodeProtocolPacket(f *testing.F) {
    // seed corpus from the round-trip tests
    f.Fuzz(func(t *testing.T, data []byte) {
        dec := NewDecoder(bytes.NewReader(data))
        _, _ = dec.DecodeProtocolPacket()  // must not panic or OOM
    })
}
```

Seed the corpus with the bytes produced by `TestLIEPacketRoundTrip`,
`TestNodeTIERoundTrip`, `TestPrefixTIERoundTrip`. The fuzz target
relies on the bounds caps from C1 to not OOM, which is part of why C1
should land first.

Add a similar fuzz target for `DecodeEnvelope`.

These are not run in `go test ./...` by default (they run in a separate
mode), but a 60-second fuzz run should be part of the gate.

**Config package tests**
`internal/config/config.go` has zero test coverage. Add table-driven
tests for:
- `ParseRiftData` with valid JSON, missing fields, malformed JSON,
  invalid system-id strings, out-of-range levels
- `ExtractInterfaceName` with valid paths, malformed paths, paths from
  other YANG modules
- `Config.Valid` with various combinations of system-id and interface
  presence
- `Config.HasInterface` for present and absent interfaces

**Fake NDK client and agent event tests**
The hardest missing coverage is `internal/agent`. The blocker is that
`Agent` directly holds `*ndk.Client`. Introduce an interface in
`internal/agent` that the real client satisfies, and write a fake that:
- Records all `UpdateTelemetry` calls
- Records all `RouteSyncStart` / `AddOrUpdateRoutes` / `RouteSyncEnd` /
  `DeleteRoutes` / NHG calls
- Lets the test inject `ConfigEvent` and `InterfaceEvent` into the
  notification channels via the existing `NotificationHandler`

Then write tests for:
- Config arrives in the documented multi-event sequence
  (`.rift` -> `.rift.interface` -> `.commit.end`) and `Agent.cfg` ends
  up correct
- Initial interface notification with all-zero data is ignored
- Adjacency `ThreeWay` event triggers a flood socket open and an adj
  change to the flood engine
- Adjacency drop triggers flood socket close
- `syncRoutes` with an empty new RIB and a non-empty programmed RIB
  issues delete calls for the stale routes
- `runDisaggregation` produces the right `DisaggUpdateCh` payload for
  a single-link-failure scenario (this can reuse the test fixtures from
  `disagg_test.go`)

These tests do not need a real NDK or a real lab. They run as plain
`go test`.

**Resolve before proceeding:**
1. Interface name for the NDK client abstraction: `ndkClient` in
   `internal/agent`, or a public interface in `internal/ndk`? Suggest
   the former (consumer-defined interface, Go idiom).
2. How long should the fuzz target run in the gate? Suggest 60s for
   each target as the default; longer in a CI run.

**Gate:**
- All existing tests pass
- `go test -race ./...` clean
- `internal/config` has unit tests with at least 80% line coverage
- `internal/agent` has unit tests covering config flow, adjacency
  lifecycle, route sync, and disaggregation orchestration
- `go test -fuzz=FuzzDecodeProtocolPacket -fuzztime=60s
  ./internal/encoding/` runs without panic, OOM, or crash
- `go test -fuzz=FuzzDecodeEnvelope -fuzztime=60s
  ./internal/encoding/` runs without panic, OOM, or crash
- Lab verification 27/27 with `--full`

## 8. Key References

| Resource | Use |
|----------|-----|
| [RFC 9692](docs/reference/rfc9692.txt) | Primary spec. LIE FSM Sec 6.2.1, flooding 6.3, SPF 6.4, disaggregation 6.5, Thrift schema Sec 7 |
| [RFC 9719](docs/reference/rfc9719.txt) | YANG model reference for rift-srl.yang |
| https://ndk.srlinux.dev/ | NDK proto docs |
| https://learn.srlinux.dev/ndk/guide/dev/go/with-bond/ | NDK Go dev with Bond |
| https://github.com/nokia/srlinux-ndk-go | Go bindings |
| https://github.com/nokia/srlinux-ndk-protobufs | Proto files (v0.5.0) |
| https://github.com/brunorijsman/rift-python | Reference impl for cross-checking |

