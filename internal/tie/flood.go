package tie

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/hyposcaler/srl-rift/internal/encoding"
)

const (
	// TIDEInterval is how often TIDE packets are sent per adjacency.
	TIDEInterval = 5 * time.Second

	// TXInterval is how often the TX/ACK/REQ queues are drained.
	TXInterval = 200 * time.Millisecond

	// MaxTIDEHeaders is the maximum number of headers per TIDE packet,
	// chosen to stay well within the default MTU of 1400 bytes.
	MaxTIDEHeaders = 25

	// ReoriginateBeforeExpiry is how long before expiry to re-originate
	// a self-originated TIE.
	ReoriginateBeforeExpiry = encoding.LifeTimeInSecType(3600)
)

// adjFloodState tracks per-adjacency flooding queues.
type adjFloodState struct {
	Info    AdjacencyInfo
	TIEsTX  map[encoding.TIEID]struct{} // TIEs to transmit
	TIEsACK map[encoding.TIEID]struct{} // TIEs to acknowledge (via TIRE)
	TIEsREQ map[encoding.TIEID]struct{} // TIEs to request (via TIRE)
}

func newAdjFloodState(info AdjacencyInfo) *adjFloodState {
	return &adjFloodState{
		Info:    info,
		TIEsTX:  make(map[encoding.TIEID]struct{}),
		TIEsACK: make(map[encoding.TIEID]struct{}),
		TIEsREQ: make(map[encoding.TIEID]struct{}),
	}
}

// FloodEngine manages the LSDB, TIE origination, and flooding.
// It runs as a single goroutine to serialize all LSDB mutations.
type FloodEngine struct {
	lsdb     *LSDB
	systemID encoding.SystemIDType
	level    encoding.LevelType
	nodeName string

	// Per-adjacency flood state, keyed by interface name.
	adjacencies map[string]*adjFloodState
	adjMu       sync.RWMutex // protects adjacencies for external reads

	// Locally attached prefixes for TIE origination.
	localPrefixes []LocalPrefix

	// Channels.
	AdjChangeCh  chan AdjacencyChange  // from agent
	FloodRecvCh  chan ReceivedFloodPkt // from transport
	FloodSendCh  chan FloodPacket      // to transport
	LSDBChangeCh chan struct{}          // notifies SPF of LSDB mutations

	logger *slog.Logger
}

// ReceivedFloodPkt is a decoded TIE/TIDE/TIRE from the unicast socket.
type ReceivedFloodPkt struct {
	Packet  *encoding.ProtocolPacket
	SrcAddr string // source IP as string
	IfName  string
}

// NewFloodEngine creates a flood engine. Start it with Run().
func NewFloodEngine(
	systemID encoding.SystemIDType,
	level encoding.LevelType,
	nodeName string,
	localPrefixes []LocalPrefix,
	logger *slog.Logger,
) *FloodEngine {
	return &FloodEngine{
		lsdb:          NewLSDB(),
		systemID:      systemID,
		level:         level,
		nodeName:      nodeName,
		adjacencies:   make(map[string]*adjFloodState),
		localPrefixes: localPrefixes,
		AdjChangeCh:   make(chan AdjacencyChange, 16),
		FloodRecvCh:   make(chan ReceivedFloodPkt, 64),
		FloodSendCh:   make(chan FloodPacket, 256),
		LSDBChangeCh:  make(chan struct{}, 1),
		logger:        logger,
	}
}

// LSDB returns the engine's link-state database (for read access by SPF).
func (fe *FloodEngine) LSDB() *LSDB {
	return fe.lsdb
}

// UpdateLocalPrefixes replaces the local prefix set and re-originates
// prefix TIEs.
func (fe *FloodEngine) UpdateLocalPrefixes(prefixes []LocalPrefix) {
	fe.localPrefixes = prefixes
}

// Adjacencies returns a snapshot of current adjacency info.
// Safe to call from any goroutine.
func (fe *FloodEngine) Adjacencies() map[string]AdjacencyInfo {
	fe.adjMu.RLock()
	defer fe.adjMu.RUnlock()
	result := make(map[string]AdjacencyInfo, len(fe.adjacencies))
	for k, v := range fe.adjacencies {
		result[k] = v.Info
	}
	return result
}

// notifyLSDBChange sends a non-blocking notification that the LSDB changed.
func (fe *FloodEngine) notifyLSDBChange() {
	select {
	case fe.LSDBChangeCh <- struct{}{}:
	default:
	}
}

// Run is the main flood engine loop. It processes adjacency changes,
// incoming flood packets, TIDE generation, lifetime ticking, and TX queue
// draining. Blocks until ctx is cancelled.
func (fe *FloodEngine) Run(ctx context.Context) error {
	tideTicker := time.NewTicker(TIDEInterval)
	defer tideTicker.Stop()

	lifetimeTicker := time.NewTicker(1 * time.Second)
	defer lifetimeTicker.Stop()

	txTicker := time.NewTicker(TXInterval)
	defer txTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil

		case change := <-fe.AdjChangeCh:
			fe.handleAdjChange(change)

		case pkt := <-fe.FloodRecvCh:
			fe.handleFloodPacket(pkt)

		case <-tideTicker.C:
			fe.generateAndSendTIDEs()

		case <-lifetimeTicker.C:
			fe.tickLifetimes()

		case <-txTicker.C:
			fe.drainQueues()
		}
	}
}

// handleAdjChange processes an adjacency up/down event.
func (fe *FloodEngine) handleAdjChange(change AdjacencyChange) {
	fe.adjMu.Lock()
	if change.Info != nil {
		// Adjacency up: add flood state, re-originate TIEs.
		fe.logger.Info("flood adjacency up",
			"interface", change.InterfaceName,
			"neighbor", change.Info.NeighborID,
			"neighbor_level", change.Info.NeighborLevel,
		)
		fe.adjacencies[change.InterfaceName] = newAdjFloodState(*change.Info)
	} else {
		// Adjacency down: remove flood state.
		fe.logger.Info("flood adjacency down", "interface", change.InterfaceName)
		delete(fe.adjacencies, change.InterfaceName)
	}
	fe.adjMu.Unlock()

	// Re-originate self TIEs (adjacency list changed).
	changed := fe.originateSelfTIEs()
	for _, id := range changed {
		fe.floodTIEToAll(id)
	}
	fe.notifyLSDBChange()
}

// handleFloodPacket dispatches an incoming packet to the correct handler.
func (fe *FloodEngine) handleFloodPacket(rpkt ReceivedFloodPkt) {
	pkt := rpkt.Packet
	ifName := rpkt.IfName

	switch {
	case pkt.Content.TIE != nil:
		fe.handleTIE(ifName, pkt)
	case pkt.Content.TIDE != nil:
		fe.handleTIDE(ifName, pkt.Content.TIDE)
	case pkt.Content.TIRE != nil:
		fe.handleTIRE(ifName, pkt.Content.TIRE)
	}
}

// handleTIE processes a received TIE packet per RFC 9692 Section 6.3.3.1.4.
func (fe *FloodEngine) handleTIE(ifName string, pkt *encoding.ProtocolPacket) {
	tie := pkt.Content.TIE
	id := tie.Header.TIEID

	adj, ok := fe.adjacencies[ifName]
	if !ok {
		return
	}

	existing := fe.lsdb.Get(id)

	rxHeader := encoding.TIEHeaderWithLifeTime{
		Header:            tie.Header,
		RemainingLifetime: encoding.DefaultLifetime, // use origination lifetime
	}
	if tie.Header.OriginationLifetime != nil {
		rxHeader.RemainingLifetime = *tie.Header.OriginationLifetime
	}

	if existing == nil {
		// Not in LSDB.
		if id.Originator == fe.systemID {
			// Stale copy of our own TIE. Bump it.
			fe.bumpOwnTIE(id)
			fe.floodTIEToAll(id)
			return
		}
		// Only accept TIEs we should hold based on flooding scope.
		if !fe.shouldAcceptTIE(id, adj.Info.NeighborLevel) {
			return
		}
		// New TIE from the network. Insert and flood.
		fe.lsdb.Insert(&LSDBEntry{
			Packet:            tie,
			RemainingLifetime: rxHeader.RemainingLifetime,
			LastReceived:      time.Now(),
			SelfOriginated:    false,
		})
		fe.ackTIE(adj, id)
		fe.floodTIEToAllExcept(id, ifName)
		fe.notifyLSDBChange()
		fe.logger.Info("TIE received and installed",
			"id", id,
			"seq", tie.Header.SeqNr,
			"from", ifName,
		)
		return
	}

	existingHeader := existing.Header()
	cmp := CompareTIEHeader(rxHeader, existingHeader)

	if cmp > 0 {
		// Received TIE is newer.
		if id.Originator == fe.systemID {
			fe.bumpOwnTIE(id)
			fe.floodTIEToAll(id)
			return
		}
		fe.lsdb.Insert(&LSDBEntry{
			Packet:            tie,
			RemainingLifetime: rxHeader.RemainingLifetime,
			LastReceived:      time.Now(),
			SelfOriginated:    false,
		})
		fe.ackTIE(adj, id)
		fe.floodTIEToAllExcept(id, ifName)
		fe.notifyLSDBChange()
		fe.logger.Info("TIE updated",
			"id", id,
			"seq", tie.Header.SeqNr,
			"from", ifName,
		)
	} else if cmp < 0 {
		// Our copy is newer. Send it back.
		fe.enqueueTX(adj, id)
	} else {
		// Same version. Acknowledge.
		fe.ackTIE(adj, id)
	}
}

// handleTIDE processes a received TIDE packet per RFC 9692 Section 6.3.3.1.2.2.
func (fe *FloodEngine) handleTIDE(ifName string, tide *encoding.TIDEPacket) {
	adj, ok := fe.adjacencies[ifName]
	if !ok {
		return
	}

	fe.logger.Debug("TIDE received",
		"interface", ifName,
		"headers", len(tide.Headers),
	)

	// Track which LSDB TIEs fall in the TIDE range but were not in the TIDE.
	// We need to send those to the peer.
	tideSet := make(map[encoding.TIEID]encoding.TIEHeaderWithLifeTime, len(tide.Headers))
	for _, h := range tide.Headers {
		tideSet[h.Header.TIEID] = h
	}

	// Process each header in the TIDE.
	for _, hdr := range tide.Headers {
		id := hdr.Header.TIEID
		existing := fe.lsdb.Get(id)

		if existing == nil {
			// We don't have this TIE.
			if id.Originator == fe.systemID {
				// Peer has a stale version of our TIE. Re-originate.
				fe.originateSelfTIEs()
				fe.floodTIEToAll(id)
			} else if fe.shouldAcceptTIE(id, adj.Info.NeighborLevel) {
				// Request it only if we should hold this TIE.
				fe.enqueueREQ(adj, id)
			}
			continue
		}

		existingHeader := existing.Header()
		cmp := CompareTIEHeader(hdr, existingHeader)

		if cmp > 0 {
			// Peer has a newer version.
			if id.Originator == fe.systemID {
				fe.bumpOwnTIE(id)
				fe.floodTIEToAll(id)
			} else if fe.shouldAcceptTIE(id, adj.Info.NeighborLevel) {
				fe.enqueueREQ(adj, id)
			}
		} else if cmp < 0 {
			// We have a newer version. Send it.
			fe.enqueueTX(adj, id)
		} else {
			// Same version. Clear from TX queue if present.
			delete(adj.TIEsTX, id)
		}
	}

	// Send TIEs in our LSDB that fall in the TIDE range but were not listed.
	fe.lsdb.ForEachSorted(func(id encoding.TIEID, entry *LSDBEntry) bool {
		if CompareTIEID(id, tide.StartRange) < 0 {
			return true // before range
		}
		if CompareTIEID(id, tide.EndRange) > 0 {
			return false // past range
		}
		if _, inTide := tideSet[id]; !inTide {
			// We have a TIE the peer doesn't know about. Check scope.
			if ShouldFloodTIE(id, fe.systemID, fe.level, adj.Info.NeighborLevel) {
				fe.enqueueTX(adj, id)
			}
		}
		return true
	})
}

// handleTIRE processes a received TIRE packet per RFC 9692 Section 6.3.3.1.3.2.
func (fe *FloodEngine) handleTIRE(ifName string, tire *encoding.TIREPacket) {
	adj, ok := fe.adjacencies[ifName]
	if !ok {
		return
	}

	fe.logger.Debug("TIRE received",
		"interface", ifName,
		"headers", len(tire.Headers),
	)

	for _, hdr := range tire.Headers {
		id := hdr.Header.TIEID

		if hdr.RemainingLifetime == 0 {
			// This is a request. Send the TIE if we have it.
			existing := fe.lsdb.Get(id)
			if existing == nil {
				continue
			}
			existingHeader := existing.Header()
			cmp := CompareTIEHeader(hdr, existingHeader)
			if cmp <= 0 {
				// We have a same or newer version.
				fe.enqueueTX(adj, id)
			}
		} else {
			// This is an acknowledgment. Clear from TX queue.
			existing := fe.lsdb.Get(id)
			if existing == nil {
				continue
			}
			existingHeader := existing.Header()
			if CompareTIEHeader(hdr, existingHeader) == 0 {
				delete(adj.TIEsTX, id)
			}
		}
	}
}

// generateAndSendTIDEs sends TIDE packets on each adjacency with the
// appropriate scope-filtered LSDB headers.
func (fe *FloodEngine) generateAndSendTIDEs() {
	for _, adj := range fe.adjacencies {
		fe.sendTIDEsToAdj(adj)
	}
}

// sendTIDEsToAdj generates and sends TIDE packets for one adjacency.
func (fe *FloodEngine) sendTIDEsToAdj(adj *adjFloodState) {
	// Collect scope-filtered headers.
	var headers []encoding.TIEHeaderWithLifeTime
	fe.lsdb.ForEachSorted(func(id encoding.TIEID, entry *LSDBEntry) bool {
		if ShouldIncludeInTIDE(id, fe.systemID, fe.level, adj.Info.NeighborLevel) {
			headers = append(headers, entry.Header())
		}
		return true
	})

	// Split into TIDE packets.
	for i := 0; i <= len(headers)/MaxTIDEHeaders; i++ {
		start := i * MaxTIDEHeaders
		end := start + MaxTIDEHeaders
		if end > len(headers) {
			end = len(headers)
		}

		chunk := headers[start:end]

		startRange := MinTIEID
		endRange := MaxTIEID
		if len(chunk) > 0 {
			startRange = chunk[0].Header.TIEID
			endRange = chunk[len(chunk)-1].Header.TIEID
		}
		// First TIDE starts at MinTIEID, last ends at MaxTIEID.
		if i == 0 {
			startRange = MinTIEID
		}
		if end >= len(headers) {
			endRange = MaxTIEID
		}

		tide := &encoding.TIDEPacket{
			StartRange: startRange,
			EndRange:   endRange,
			Headers:    chunk,
		}

		pkt := &encoding.ProtocolPacket{
			Header: encoding.PacketHeader{
				MajorVersion: encoding.ProtocolMajorVersion,
				MinorVersion: encoding.ProtocolMinorVersion,
				Sender:       fe.systemID,
				Level:        &fe.level,
			},
			Content: encoding.PacketContent{
				TIDE: tide,
			},
		}

		fe.sendFloodPacket(adj, pkt, 0)
	}
}

// tickLifetimes decrements all TIE lifetimes and handles expiry and
// re-origination.
func (fe *FloodEngine) tickLifetimes() {
	expired := fe.lsdb.DecrementLifetimes(1)
	for _, id := range expired {
		fe.lsdb.Remove(id)
		fe.logger.Info("TIE expired", "id", id)
	}
	if len(expired) > 0 {
		fe.notifyLSDBChange()
	}

	// Re-originate own TIEs approaching expiry.
	fe.lsdb.ForEachSorted(func(id encoding.TIEID, entry *LSDBEntry) bool {
		if entry.SelfOriginated && entry.RemainingLifetime < ReoriginateBeforeExpiry {
			fe.bumpOwnTIE(id)
			fe.floodTIEToAll(id)
		}
		return true
	})
}

// drainQueues sends pending TIE, ACK, and REQ packets for all adjacencies.
func (fe *FloodEngine) drainQueues() {
	for _, adj := range fe.adjacencies {
		fe.drainAdjQueues(adj)
	}
}

// drainAdjQueues processes the TX/ACK/REQ queues for one adjacency.
func (fe *FloodEngine) drainAdjQueues(adj *adjFloodState) {
	// Send TIRE for ACKs.
	if len(adj.TIEsACK) > 0 {
		var headers []encoding.TIEHeaderWithLifeTime
		for id := range adj.TIEsACK {
			entry := fe.lsdb.Get(id)
			if entry != nil {
				headers = append(headers, entry.Header())
			}
		}
		if len(headers) > 0 {
			fe.sendTIRE(adj, headers)
		}
		adj.TIEsACK = make(map[encoding.TIEID]struct{})
	}

	// Send TIRE for REQs (with remaining_lifetime = 0).
	if len(adj.TIEsREQ) > 0 {
		var headers []encoding.TIEHeaderWithLifeTime
		for id := range adj.TIEsREQ {
			headers = append(headers, encoding.TIEHeaderWithLifeTime{
				Header: encoding.TIEHeader{
					TIEID: id,
				},
				RemainingLifetime: 0,
			})
		}
		fe.sendTIRE(adj, headers)
		adj.TIEsREQ = make(map[encoding.TIEID]struct{})
	}

	// Send TIEs from TX queue.
	for id := range adj.TIEsTX {
		entry := fe.lsdb.Get(id)
		if entry == nil {
			delete(adj.TIEsTX, id)
			continue
		}

		pkt := &encoding.ProtocolPacket{
			Header: encoding.PacketHeader{
				MajorVersion: encoding.ProtocolMajorVersion,
				MinorVersion: encoding.ProtocolMinorVersion,
				Sender:       fe.systemID,
				Level:        &fe.level,
			},
			Content: encoding.PacketContent{
				TIE: entry.Packet,
			},
		}

		fe.sendFloodPacket(adj, pkt, entry.RemainingLifetime)
		delete(adj.TIEsTX, id)
	}
}

// sendTIRE sends a TIRE packet to an adjacency.
func (fe *FloodEngine) sendTIRE(adj *adjFloodState, headers []encoding.TIEHeaderWithLifeTime) {
	pkt := &encoding.ProtocolPacket{
		Header: encoding.PacketHeader{
			MajorVersion: encoding.ProtocolMajorVersion,
			MinorVersion: encoding.ProtocolMinorVersion,
			Sender:       fe.systemID,
			Level:        &fe.level,
		},
		Content: encoding.PacketContent{
			TIRE: &encoding.TIREPacket{
				Headers: headers,
			},
		},
	}
	fe.sendFloodPacket(adj, pkt, 0)
}

// floodTIEToAll enqueues a TIE for transmission on all adjacencies
// where flooding scope permits.
func (fe *FloodEngine) floodTIEToAll(id encoding.TIEID) {
	for _, adj := range fe.adjacencies {
		if ShouldFloodTIE(id, fe.systemID, fe.level, adj.Info.NeighborLevel) {
			adj.TIEsTX[id] = struct{}{}
		}
	}
}

// floodTIEToAllExcept enqueues a TIE for transmission on all adjacencies
// except the one it was received from.
func (fe *FloodEngine) floodTIEToAllExcept(id encoding.TIEID, excludeIf string) {
	for _, adj := range fe.adjacencies {
		if adj.Info.InterfaceName == excludeIf {
			continue
		}
		if ShouldFloodTIE(id, fe.systemID, fe.level, adj.Info.NeighborLevel) {
			adj.TIEsTX[id] = struct{}{}
		}
	}
}

// ackTIE enqueues an acknowledgment for a TIE on the given adjacency.
func (fe *FloodEngine) ackTIE(adj *adjFloodState, id encoding.TIEID) {
	adj.TIEsACK[id] = struct{}{}
}

// enqueueTX enqueues a TIE for transmission on the given adjacency.
func (fe *FloodEngine) enqueueTX(adj *adjFloodState, id encoding.TIEID) {
	adj.TIEsTX[id] = struct{}{}
}

// enqueueREQ enqueues a TIE request on the given adjacency.
func (fe *FloodEngine) enqueueREQ(adj *adjFloodState, id encoding.TIEID) {
	adj.TIEsREQ[id] = struct{}{}
}

// shouldAcceptTIE checks whether this node should hold the given TIE based
// on the neighbor it would come from. A node accepts a TIE if it would be
// flooded toward us from the peer's direction.
func (fe *FloodEngine) shouldAcceptTIE(id encoding.TIEID, neighborLevel encoding.LevelType) bool {
	// We always accept our own TIEs.
	if id.Originator == fe.systemID {
		return true
	}

	switch id.Direction {
	case encoding.TieDirectionNorth:
		// North TIEs flow northbound. We accept them from lower-level
		// neighbors (they are flooding north to us).
		return neighborLevel < fe.level
	case encoding.TieDirectionSouth:
		// South Node TIEs flow southbound + reflect north.
		// We accept South Node TIEs from any neighbor.
		if id.TIEType == encoding.TIETypeNodeTIEType {
			return true
		}
		// South non-Node TIEs: accept only from higher-level neighbors
		// (they originate southbound) or if self-originated.
		return neighborLevel > fe.level
	}
	return false
}

// sendFloodPacket sends a packet to an adjacency via the FloodSendCh.
func (fe *FloodEngine) sendFloodPacket(adj *adjFloodState, pkt *encoding.ProtocolPacket, remainingLifetime encoding.LifeTimeInSecType) {
	fp := FloodPacket{
		InterfaceName:     adj.Info.InterfaceName,
		Packet:            pkt,
		DestAddr:          adj.Info.NeighborAddr,
		DestPort:          int(adj.Info.FloodPort),
		RemainingLifetime: remainingLifetime,
	}

	select {
	case fe.FloodSendCh <- fp:
	default:
		fe.logger.Warn("flood send channel full, dropping packet",
			"interface", adj.Info.InterfaceName,
		)
	}
}
