# CLAUDE.md

## Project
rift-srl: RIFT (RFC 9692) routing protocol as an SR Linux NDK agent in Go.

## Starting a New Session
1. Read this file (conventions, rules, what not to do).
2. Read `docs/ARCHITECTURE.md` for current project state, what has been built,
   key decisions already made, and the current milestone.
3. Read the current milestone section in `docs/PLAN.md` for what to do next.
Do not ask the user what to work on if ARCHITECTURE.md has a current
milestone listed. Just continue from where it left off.

## Build & Test
- `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o rift-srl ./cmd/rift-srl/`
- `go test ./...`
- Deploy to lab: `bash lab/scripts/deploy.sh --build`

## Code Conventions
- No em-dashes. Use commas, parentheses, or separate sentences.
- Table-driven tests.
- `slog` for structured logging.
- `fmt.Errorf("operation: %w", err)` for error wrapping.
- Pass `context.Context` through long-running operations and gRPC calls.
- Goroutine lifecycle via `errgroup` or explicit shutdown channels. No leaks.
- Use RIFT terminology from RFC 9692 in identifiers (LIE, TIE, TIDE, TIRE,
  LSDB, ThreeWay). Do not invent synonyms.

## Architecture Rules
- `internal/ndk/` owns NDK interaction. Protocol logic must not import it.
- `internal/transport/` owns socket I/O. Protocol logic uses Go channels.
- `internal/tie/lsdb.go` is the single LSDB. SPF reads, flooding writes.
- LIE FSMs are per-interface, independent, emit state via channels.

## Do NOT
- Use Python or uv.
- Implement ZTP, negative disaggregation, multi-plane, security envelope
  auth, KV TIEs, BAD, label binding, L2L shortcuts, mobility, IPv6 prefixes,
  or east-west forwarding at non-leaf levels.
- Use third-party routing libraries. Implement SPF directly.
- Use localhost/127.0.0.1 for protocol packets.
- Skip lab verification. If containerlab is running, test on it.
