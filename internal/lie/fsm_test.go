package lie

import (
	"log/slog"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/hyposcaler/srl-rift/internal/encoding"
)

// mockTransport captures sent LIE packets for testing.
type mockTransport struct {
	sent     []*encoding.ProtocolPacket
	localID  encoding.LinkIDType
	localAddr netip.Addr
}

func (m *mockTransport) SendLIE(pkt *encoding.ProtocolPacket) error {
	m.sent = append(m.sent, pkt)
	return nil
}

func (m *mockTransport) LocalID() encoding.LinkIDType {
	return m.localID
}

func (m *mockTransport) LocalAddr() netip.Addr {
	return m.localAddr
}

func newTestFSM(systemID encoding.SystemIDType, level encoding.LevelType) (*FSM, *mockTransport, chan AdjacencyEvent) {
	mt := &mockTransport{
		localID:   42,
		localAddr: netip.MustParseAddr("10.1.1.1"),
	}
	adjCh := make(chan AdjacencyEvent, 64)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	fsm := NewFSM(systemID, level, "test-node", "ethernet-1/1", mt, adjCh, logger)
	return fsm, mt, adjCh
}

func makeLIEPacket(sender encoding.SystemIDType, level encoding.LevelType, localID encoding.LinkIDType, neighbor *encoding.Neighbor) *encoding.ProtocolPacket {
	lvl := level
	return &encoding.ProtocolPacket{
		Header: encoding.PacketHeader{
			MajorVersion: encoding.ProtocolMajorVersion,
			MinorVersion: encoding.ProtocolMinorVersion,
			Sender:       sender,
			Level:        &lvl,
		},
		Content: encoding.PacketContent{
			LIE: &encoding.LIEPacket{
				Name:      "peer",
				LocalID:   localID,
				FloodPort: encoding.DefaultTIEUDPFloodPort,
				Holdtime:  encoding.DefaultLIEHoldtime,
				NodeCapabilities: encoding.NodeCapabilities{
					ProtocolMinorVersion: encoding.ProtocolMinorVersion,
				},
				Neighbor: neighbor,
			},
		},
	}
}

func drainAdjEvents(ch chan AdjacencyEvent) []AdjacencyEvent {
	var events []AdjacencyEvent
	for {
		select {
		case ev := <-ch:
			events = append(events, ev)
		default:
			return events
		}
	}
}

func TestFSMTransitions(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(f *FSM, mt *mockTransport)
		action    func(f *FSM)
		wantState State
	}{
		{
			name:  "initial state is OneWay",
			setup: func(f *FSM, mt *mockTransport) {},
			action: func(f *FSM) {},
			wantState: OneWay,
		},
		{
			name:  "OneWay to TwoWay on valid LIE with new neighbor",
			setup: func(f *FSM, mt *mockTransport) {},
			action: func(f *FSM) {
				pkt := makeLIEPacket(100, 1, 99, nil)
				f.HandlePacket(pkt, netip.MustParseAddr("10.1.1.0"))
			},
			wantState: TwoWay,
		},
		{
			name: "TwoWay to ThreeWay on valid reflection",
			setup: func(f *FSM, mt *mockTransport) {
				// First get to TwoWay.
				pkt := makeLIEPacket(100, 1, 99, nil)
				f.HandlePacket(pkt, netip.MustParseAddr("10.1.1.0"))
			},
			action: func(f *FSM) {
				// Now send LIE with valid reflection.
				pkt := makeLIEPacket(100, 1, 99, &encoding.Neighbor{
					Originator: 1, // our system ID
					RemoteID:   42, // our local ID
				})
				f.HandlePacket(pkt, netip.MustParseAddr("10.1.1.0"))
			},
			wantState: ThreeWay,
		},
		{
			name: "ThreeWay to TwoWay on dropped reflection",
			setup: func(f *FSM, mt *mockTransport) {
				// Get to ThreeWay.
				pkt := makeLIEPacket(100, 1, 99, nil)
				f.HandlePacket(pkt, netip.MustParseAddr("10.1.1.0"))
				pkt = makeLIEPacket(100, 1, 99, &encoding.Neighbor{
					Originator: 1,
					RemoteID:   42,
				})
				f.HandlePacket(pkt, netip.MustParseAddr("10.1.1.0"))
			},
			action: func(f *FSM) {
				// LIE without neighbor element.
				pkt := makeLIEPacket(100, 1, 99, nil)
				f.HandlePacket(pkt, netip.MustParseAddr("10.1.1.0"))
			},
			wantState: TwoWay,
		},
		{
			name: "ThreeWay to OneWay on holdtime expiry",
			setup: func(f *FSM, mt *mockTransport) {
				pkt := makeLIEPacket(100, 1, 99, nil)
				f.HandlePacket(pkt, netip.MustParseAddr("10.1.1.0"))
				pkt = makeLIEPacket(100, 1, 99, &encoding.Neighbor{
					Originator: 1,
					RemoteID:   42,
				})
				f.HandlePacket(pkt, netip.MustParseAddr("10.1.1.0"))
			},
			action: func(f *FSM) {
				// Simulate holdtime expiry by backdating lastLIERcvd.
				f.lastLIERcvd = time.Now().Add(-10 * time.Second)
				f.neighborHoldtime = 3 * time.Second
				f.Tick()
			},
			wantState: OneWay,
		},
		{
			name:  "OneWay stays OneWay on unacceptable header (no level)",
			setup: func(f *FSM, mt *mockTransport) {},
			action: func(f *FSM) {
				pkt := &encoding.ProtocolPacket{
					Header: encoding.PacketHeader{
						MajorVersion: encoding.ProtocolMajorVersion,
						Sender:       100,
						Level:        nil, // undefined level
					},
					Content: encoding.PacketContent{
						LIE: &encoding.LIEPacket{
							LocalID:   99,
							FloodPort: encoding.DefaultTIEUDPFloodPort,
							Holdtime:  encoding.DefaultLIEHoldtime,
							NodeCapabilities: encoding.NodeCapabilities{
								ProtocolMinorVersion: encoding.ProtocolMinorVersion,
							},
						},
					},
				}
				f.HandlePacket(pkt, netip.MustParseAddr("10.1.1.0"))
			},
			wantState: OneWay,
		},
		{
			name:  "OneWay stays OneWay on own system ID",
			setup: func(f *FSM, mt *mockTransport) {},
			action: func(f *FSM) {
				pkt := makeLIEPacket(1, 1, 99, nil) // sender == our system ID
				f.HandlePacket(pkt, netip.MustParseAddr("10.1.1.0"))
			},
			wantState: OneWay,
		},
		{
			name:  "OneWay stays OneWay on MTU mismatch",
			setup: func(f *FSM, mt *mockTransport) {},
			action: func(f *FSM) {
				pkt := makeLIEPacket(100, 1, 99, nil)
				mtu := encoding.MTUSizeType(9000)
				pkt.Content.LIE.LinkMTUSize = &mtu
				f.HandlePacket(pkt, netip.MustParseAddr("10.1.1.0"))
			},
			wantState: OneWay,
		},
		{
			name:  "OneWay stays OneWay on version mismatch",
			setup: func(f *FSM, mt *mockTransport) {},
			action: func(f *FSM) {
				pkt := makeLIEPacket(100, 1, 99, nil)
				pkt.Header.MajorVersion = 99
				f.HandlePacket(pkt, netip.MustParseAddr("10.1.1.0"))
			},
			wantState: OneWay,
		},
		{
			name: "ThreeWay to OneWay on neighbor changed level",
			setup: func(f *FSM, mt *mockTransport) {
				pkt := makeLIEPacket(100, 1, 99, nil)
				f.HandlePacket(pkt, netip.MustParseAddr("10.1.1.0"))
				pkt = makeLIEPacket(100, 1, 99, &encoding.Neighbor{
					Originator: 1,
					RemoteID:   42,
				})
				f.HandlePacket(pkt, netip.MustParseAddr("10.1.1.0"))
			},
			action: func(f *FSM) {
				// Same neighbor, different level.
				pkt := makeLIEPacket(100, 0, 99, &encoding.Neighbor{
					Originator: 1,
					RemoteID:   42,
				})
				f.HandlePacket(pkt, netip.MustParseAddr("10.1.1.0"))
			},
			wantState: OneWay,
		},
		{
			name: "TwoWay to OneWay on neighbor changed address",
			setup: func(f *FSM, mt *mockTransport) {
				pkt := makeLIEPacket(100, 1, 99, nil)
				f.HandlePacket(pkt, netip.MustParseAddr("10.1.1.0"))
			},
			action: func(f *FSM) {
				pkt := makeLIEPacket(100, 1, 99, nil)
				f.HandlePacket(pkt, netip.MustParseAddr("10.2.2.0")) // different addr
			},
			wantState: OneWay,
		},
		{
			name:  "leaf accepts any level neighbor",
			setup: func(f *FSM, mt *mockTransport) {},
			action: func(f *FSM) {
				// Our node is leaf (level 0). RFC 9692 allows a leaf
				// to peer with any non-leaf regardless of level.
				pkt := makeLIEPacket(100, 3, 99, nil)
				f.HandlePacket(pkt, netip.MustParseAddr("10.1.1.0"))
			},
			wantState: TwoWay,
		},
		{
			name: "MultipleNeighbors to MultipleNeighborsWait",
			setup: func(f *FSM, mt *mockTransport) {
				// Get to TwoWay first.
				pkt := makeLIEPacket(100, 1, 99, nil)
				f.HandlePacket(pkt, netip.MustParseAddr("10.1.1.0"))
			},
			action: func(f *FSM) {
				// Different system ID on same interface.
				pkt := makeLIEPacket(200, 1, 88, nil)
				f.HandlePacket(pkt, netip.MustParseAddr("10.1.1.2"))
			},
			wantState: MultipleNeighborsWait,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fsm, mt, _ := newTestFSM(1, 0) // system ID 1, level 0 (leaf)
			tt.setup(fsm, mt)
			tt.action(fsm)
			if fsm.State() != tt.wantState {
				t.Errorf("state: got %s, want %s", fsm.State(), tt.wantState)
			}
		})
	}
}

func TestSendLIEIncludesNeighborReflection(t *testing.T) {
	fsm, mt, _ := newTestFSM(1, 0)

	// Get to TwoWay.
	pkt := makeLIEPacket(100, 1, 99, nil)
	fsm.HandlePacket(pkt, netip.MustParseAddr("10.1.1.0"))

	if fsm.State() != TwoWay {
		t.Fatalf("expected TwoWay, got %s", fsm.State())
	}

	// The FSM should have sent a LIE (pushed by NewNeighbor -> SendLie).
	// Find the sent packet that contains a neighbor reflection.
	var foundReflection bool
	for _, sent := range mt.sent {
		if sent.Content.LIE != nil && sent.Content.LIE.Neighbor != nil {
			nbr := sent.Content.LIE.Neighbor
			if nbr.Originator == 100 && nbr.RemoteID == 99 {
				foundReflection = true
				break
			}
		}
	}
	if !foundReflection {
		t.Error("no LIE sent with neighbor reflection after reaching TwoWay")
	}
}

func TestTimerTickSendsLIE(t *testing.T) {
	fsm, mt, _ := newTestFSM(1, 0)

	mt.sent = nil
	fsm.Tick()

	if len(mt.sent) == 0 {
		t.Error("TimerTick in OneWay should send a LIE")
	}
}

func TestCleanupOnEnterOneWay(t *testing.T) {
	fsm, _, _ := newTestFSM(1, 0)

	// Get to TwoWay with a neighbor.
	pkt := makeLIEPacket(100, 1, 99, nil)
	fsm.HandlePacket(pkt, netip.MustParseAddr("10.1.1.0"))

	if fsm.Neighbor() == nil {
		t.Fatal("expected neighbor after TwoWay")
	}

	// Force back to OneWay via holdtime expiry.
	fsm.lastLIERcvd = time.Now().Add(-10 * time.Second)
	fsm.neighborHoldtime = 3 * time.Second
	fsm.Tick()

	if fsm.State() != OneWay {
		t.Fatalf("expected OneWay, got %s", fsm.State())
	}
	if fsm.Neighbor() != nil {
		t.Error("neighbor should be nil after entering OneWay")
	}
}

func TestFullThreeWayHandshake(t *testing.T) {
	// Simulate two FSMs forming a ThreeWay adjacency.
	fsmA, mtA, adjChA := newTestFSM(1, 0)
	fsmB, mtB, adjChB := newTestFSM(100, 1)
	mtB.localID = 99
	mtB.localAddr = netip.MustParseAddr("10.1.1.0")

	addrA := netip.MustParseAddr("10.1.1.1")
	addrB := netip.MustParseAddr("10.1.1.0")

	// Round 1: Both sides tick, sending LIE without neighbor.
	fsmA.Tick()
	fsmB.Tick()

	// A sends to B (no neighbor reflection).
	if len(mtA.sent) > 0 {
		fsmB.HandlePacket(mtA.sent[len(mtA.sent)-1], addrA)
	}
	// B sends to A (no neighbor reflection).
	if len(mtB.sent) > 0 {
		fsmA.HandlePacket(mtB.sent[len(mtB.sent)-1], addrB)
	}

	// Both should be in TwoWay now.
	if fsmA.State() != TwoWay {
		t.Fatalf("fsmA expected TwoWay, got %s", fsmA.State())
	}
	if fsmB.State() != TwoWay {
		t.Fatalf("fsmB expected TwoWay, got %s", fsmB.State())
	}

	// Round 2: Both tick again, now sending LIE with neighbor reflection.
	mtA.sent = nil
	mtB.sent = nil
	fsmA.Tick()
	fsmB.Tick()

	// Exchange LIEs with reflections.
	if len(mtA.sent) > 0 {
		fsmB.HandlePacket(mtA.sent[len(mtA.sent)-1], addrA)
	}
	if len(mtB.sent) > 0 {
		fsmA.HandlePacket(mtB.sent[len(mtB.sent)-1], addrB)
	}

	// Both should be in ThreeWay now.
	if fsmA.State() != ThreeWay {
		t.Errorf("fsmA expected ThreeWay, got %s", fsmA.State())
	}
	if fsmB.State() != ThreeWay {
		t.Errorf("fsmB expected ThreeWay, got %s", fsmB.State())
	}

	// Verify adjacency events were emitted.
	eventsA := drainAdjEvents(adjChA)
	eventsB := drainAdjEvents(adjChB)

	hasThreeWay := func(events []AdjacencyEvent) bool {
		for _, ev := range events {
			if ev.State == ThreeWay {
				return true
			}
		}
		return false
	}

	if !hasThreeWay(eventsA) {
		t.Error("fsmA did not emit ThreeWay adjacency event")
	}
	if !hasThreeWay(eventsB) {
		t.Error("fsmB did not emit ThreeWay adjacency event")
	}
}

func TestLeafLevelAcceptance(t *testing.T) {
	tests := []struct {
		name        string
		localLevel  encoding.LevelType
		remoteLevel encoding.LevelType
		want        bool
	}{
		{"leaf accepts level 0", encoding.LeafLevel, 0, true},
		{"leaf accepts level 1", encoding.LeafLevel, 1, true},
		{"leaf accepts level 2", encoding.LeafLevel, 2, true},
		{"leaf accepts level 10", encoding.LeafLevel, 10, true},
		{"level 1 accepts leaf", 1, encoding.LeafLevel, true},
		{"level 1 accepts level 1", 1, 1, true},
		{"level 1 accepts level 2", 1, 2, true},
		{"level 1 rejects level 3", 1, 3, false},
		{"level 2 accepts level 1", 2, 1, true},
		{"level 2 rejects level 0 non-leaf", 2, 0, true}, // level 0 == LeafLevel, accepted
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fsm, _, _ := newTestFSM(1, tt.localLevel)
			if got := fsm.levelAcceptable(tt.remoteLevel); got != tt.want {
				t.Errorf("levelAcceptable(%d) = %v, want %v (local level %d)",
					tt.remoteLevel, got, tt.want, tt.localLevel)
			}
		})
	}
}
