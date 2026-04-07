# srl-rift

RIFT (RFC 9692) routing protocol implementation as an SR Linux NDK agent in Go.

Single-plane, configured levels, IPv4 only. Designed for 2-tier leaf-spine
Clos fabrics.

## Features

- LIE exchange and 3-way adjacency FSM (RFC 9692 Section 6.2)
- TIE origination and flooding: North/South Node and Prefix TIEs
- TIDE/TIRE database synchronization (RFC 9692 Section 6.3)
- Northbound and southbound SPF computation (RFC 9692 Section 6.4)
- ECMP across equal-cost next-hops
- Positive disaggregation on partial connectivity (RFC 9692 Section 6.5.1)
- FIB programming via NDK Route/NextHopGroup services
- YANG model for config and operational state
- Telemetry: adjacencies, LSDB, RIB, counters, disaggregation state

## Architecture

```
+-----------------------------------------+
|            rift-srl agent               |
|                                         |
|  +------------+   +-----------------+   |
|  | LIE Engine |   | TIE Flood Engine|   |
|  | (per-intf  |   | (TIDE/TIRE sync,|   |
|  |  FSM + UDP |   |  LSDB mgmt)    |   |
|  |  mcast)    |   |                 |   |
|  +-----+------+   +--------+--------+   |
|        |                   |             |
|        v                   v             |
|  +------------------------------------+  |
|  |        Topology / LSDB             |  |
|  +----------------+-------------------+  |
|                   |                      |
|                   v                      |
|  +------------------------------------+  |
|  |  SPF Engine + Route Computation    |  |
|  +----------------+-------------------+  |
|                   |                      |
|                   v                      |
|  +------------------------------------+  |
|  |     NDK Integration Layer          |  |
|  | (routes, NHGs, telemetry, config)  |  |
|  +----------------+-------------------+  |
|                   | gRPC                 |
+-------------------+---------------------+
                    |
            +-------v--------+
            |    ndk_mgr     |
            |   (IDB/FIB)   |
            +----------------+
```

Concurrency model:
- One goroutine per interface for LIE FSM and socket I/O
- One goroutine for NDK notification stream processing
- One goroutine for TIE flooding engine
- SPF triggered by LSDB changes, debounced (100ms hold-down)

## Prerequisites

- Go 1.21+
- [containerlab](https://containerlab.dev/)
- Docker
- SR Linux container image (`ghcr.io/nokia/srlinux:26.3.1`)

## Build

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o rift-srl ./cmd/rift-srl/
```

## Test

```bash
go test ./...
```

## Lab Topology

2-tier leaf-spine with 2 spines (level 1), 3 leaves (level 0), full mesh
(6 fabric links), and 3 Linux hosts.

```
        +----------+     +----------+
        | spine1   |     | spine2   |
        | level=1  |     | level=1  |
        +-+--+--+--+     +--+---+-+-+
          |  |  |           |   | |
     +----+  |  +------+  +-+   | +----+
     |       |         |  |     |      |
     |    +--+    +----+  |    ++      |
     |    |       |       |    |       |
   +-+----+--+  +-+-------+-+  ++------+-+
   | leaf1   |  |  leaf2   |  |  leaf3   |
   | level=0 |  |  level=0 |  |  level=0 |
   +----+----+  +----+-----+  +----+-----+
        |             |             |
   +----+----+  +-----+----+  +----+-----+
   |  host1  |  |  host2   |  |  host3   |
   | .1.10   |  | .2.10    |  | .3.10    |
   +---------+  +----------+  +----------+
```

## Demo Walkthrough

Deploy and verify a working RIFT fabric from scratch:

```bash
# 1. Build the agent binary
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o rift-srl ./cmd/rift-srl/

# 2. Deploy the containerlab topology
sudo containerlab deploy -t lab/topology.clab.yml

# 3. Deploy the agent to all nodes
bash lab/scripts/deploy.sh --build

# 4. Verify (agent status, adjacencies, routes, ping)
bash lab/scripts/verify.sh

# 5. Check adjacencies on a leaf
docker exec clab-rift-leaf1 sr_cli "info from state rift interface *"

# 6. Check RIFT routes on a leaf (default route via both spines)
docker exec clab-rift-leaf1 sr_cli "info from state rift rib-summary"

# 7. Ping between hosts
docker exec clab-rift-host1 ping -c 3 10.10.2.10
docker exec clab-rift-host1 ping -c 3 10.10.3.10

# 8. Test disaggregation: disable spine1-leaf3 link
docker exec -i clab-rift-spine1 sr_cli <<SREOF
enter candidate
set interface ethernet-1/3 admin-state disable
commit now
SREOF

# Wait ~10s, then verify leaf3 is still reachable via spine2
docker exec clab-rift-host1 ping -c 3 10.10.3.10

# Check disaggregation state on spine2
docker exec clab-rift-spine2 sr_cli "info from state rift disaggregation-summary"

# 9. Restore the link
docker exec -i clab-rift-spine1 sr_cli <<SREOF
enter candidate
set interface ethernet-1/3 admin-state enable
commit now
SREOF

# 10. Tear down
sudo containerlab destroy -t lab/topology.clab.yml
```

Makefile in the repo has a number of targets for each of these steps, see: `make help`

## Documentation and process notes.

This code was largely generated via Claude Code. The CLAUDE.md includes conventions for the repo. The [docs/PLAN.md](docs/PLAN.md) is the overall plan with milestones that I created through a series of conversations with Claude. The [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) was a doc set aside for Claude to keep track of the work it did over multiple sessions.

The basic workflow for this effort was: start a Claude session, enter plan mode via /plan, tell Claude we are ready for Mn where n is the milestone we were set to work on. Let Claude create a plan, then execute.

Overall this process worked, I found that we used ~200k tokens per Milestone.

## Questions

The code works, which if I am being honest I didn't really expect. I am still working through how it works. There are some gaps and features not implemented, but I think it does stand as an MVP.

The config used to configure it doesn't really fit well with network-instances. I didn't really think that through.

For prefix discovery for interfaces without RIFT, it just enumerates all IPv4 interfaces in srbase-default at startup via net.Interfaces() + Addrs(). This catches loopbacks (system0.0 mapped to /32), fabric link subnets, and host-facing subnets. Filter out noise: lo (loopback flag), gateway (SR Linux internal), mgmt0.0 (management), and 127.x.x.x addresses. A better choice would likely be to use an export list similar to what Junos does.

There are some namespace shenanigans that I don't fully understand in the code.

To make multicast work for the LIEs, create the multicast receive socket in srbase using runtime.LockOSThread() + netns.Set(). Once created, the file descriptor works from any namespace. The agent process runs in srbase (where app manager launches it), so the recv socket is native. Multicast send goes from srbase-default (which has real IPs), traverses the veth to the parent, and goes on the wire with a real source IP. The peer's recv socket in srbase picks it up.

Sub-interfaces also created some heartburn. The workaround involves runtime.LockOSThread(), which pins the goroutine to an OS thread before switching namespace. For sockets, the switch only needs to happen at creation time. Once a socket fd exists, it stays bound to the namespace where it was created regardless of which thread uses it later. So the pattern is: lock thread, switch ns, create socket, switch back, unlock. The agent creates multicast send sockets and flood (unicast) sockets in srbase-default this way. Also, binding below port 1024 required sysctl net.ipv4.ip_unprivileged_port_start=0 in srbase-default since the agent runs as srlinux (unprivileged), and only srbase had that sysctl set by default.

I'm explicitly not saying these solutions make sense. Bear in mind, this project was literally just outside of my capabilities on a good day. This is just what me and Claude came up with. Read the ARCH doc for more details.


## YANG Model

Config path: `/rift`

| Path | Type | Description |
|------|------|-------------|
| `admin-state` | enum | enable/disable |
| `system-id` | uint64 | RIFT system identifier |
| `level` | uint8 | Node level (0=leaf) |
| `interface[name]` | list | RIFT-enabled interfaces |

State paths (read-only):

| Path | Type | Description |
|------|------|-------------|
| `oper-state` | enum | Agent operational state |
| `interface[name]/adjacency/*` | container | Per-interface adjacency state |
| `lsdb-summary` | string | Link-state database contents |
| `rib-summary` | string | Computed routes |
| `spf-runs` | uint64 | SPF computation count |
| `adjacency-count` | uint32 | Three-way adjacency count |
| `lsdb-tie-count` | uint32 | TIE count in LSDB |
| `route-count` | uint32 | Route count |
| `disaggregation-summary` | string | Active disaggregation prefixes |

## Project Structure

```
rift-srl/
  cmd/rift-srl/         Entry point
  internal/
    agent/              Top-level agent, event loop, telemetry
    config/             YANG config parsing
    encoding/           Thrift binary protocol codec
    lie/                LIE FSM, adjacency formation
    ndk/                NDK gRPC client (routes, NHGs, telemetry)
    spf/                SPF computation, disaggregation
    tie/                LSDB, TIE flooding, TIDE/TIRE sync
    transport/          UDP sockets, namespace management
  yang/                 YANG model
  lab/
    topology.clab.yml   Containerlab topology
    configs/            SR Linux startup configs
    scripts/            Deploy and verify scripts
  proto/                RIFT Thrift schema reference
```

## References

- [RFC 9692](https://www.rfc-editor.org/rfc/rfc9692) - RIFT protocol specification
- [RFC 9719](https://www.rfc-editor.org/rfc/rfc9719) - RIFT YANG model
- [NDK documentation](https://ndk.srlinux.dev/) - SR Linux NDK proto docs (v0.5.0)
- [srlinux-ndk-go](https://github.com/nokia/srlinux-ndk-go) - Go bindings
