// Package lie implements the RIFT LIE (Link Information Element) FSM
// per RFC 9692 Section 6.2.1.
package lie

import (
	"fmt"
	"log/slog"
	"net/netip"
	"time"

	"github.com/hyposcaler/srl-rift/internal/encoding"
)

// State represents a LIE FSM state.
type State int

const (
	OneWay State = iota
	TwoWay
	ThreeWay
	MultipleNeighborsWait
)

func (s State) String() string {
	switch s {
	case OneWay:
		return "one-way"
	case TwoWay:
		return "two-way"
	case ThreeWay:
		return "three-way"
	case MultipleNeighborsWait:
		return "multiple-neighbors-wait"
	default:
		return fmt.Sprintf("unknown(%d)", int(s))
	}
}

// Event represents a LIE FSM event.
type Event int

const (
	EventTimerTick Event = iota
	EventLieRcvd
	EventNewNeighbor
	EventValidReflection
	EventNeighborDroppedReflection
	EventNeighborChangedLevel
	EventNeighborChangedAddress
	EventUnacceptableHeader
	EventMTUMismatch
	EventHoldtimeExpired
	EventMultipleNeighbors
	EventMultipleNeighborsDone
	EventSendLie
	EventLevelChanged
	EventNeighborChangedMinorFields
	EventFloodLeadersChanged
	EventUpdateZTPOffer
)

func (e Event) String() string {
	switch e {
	case EventTimerTick:
		return "TimerTick"
	case EventLieRcvd:
		return "LieRcvd"
	case EventNewNeighbor:
		return "NewNeighbor"
	case EventValidReflection:
		return "ValidReflection"
	case EventNeighborDroppedReflection:
		return "NeighborDroppedReflection"
	case EventNeighborChangedLevel:
		return "NeighborChangedLevel"
	case EventNeighborChangedAddress:
		return "NeighborChangedAddress"
	case EventUnacceptableHeader:
		return "UnacceptableHeader"
	case EventMTUMismatch:
		return "MTUMismatch"
	case EventHoldtimeExpired:
		return "HoldtimeExpired"
	case EventMultipleNeighbors:
		return "MultipleNeighbors"
	case EventMultipleNeighborsDone:
		return "MultipleNeighborsDone"
	case EventSendLie:
		return "SendLie"
	case EventLevelChanged:
		return "LevelChanged"
	case EventNeighborChangedMinorFields:
		return "NeighborChangedMinorFields"
	case EventFloodLeadersChanged:
		return "FloodLeadersChanged"
	case EventUpdateZTPOffer:
		return "UpdateZTPOffer"
	default:
		return fmt.Sprintf("unknown(%d)", int(e))
	}
}

// NeighborState holds the "current neighbor" per the RFC.
type NeighborState struct {
	SystemID  encoding.SystemIDType
	Level     encoding.LevelType
	LinkID    encoding.LinkIDType
	Address   netip.Addr
	Name      string
	FloodPort encoding.UDPPortType
	Holdtime  encoding.TimeIntervalInSecType
}

// TransportSender is the interface the FSM uses to send packets.
// Decouples FSM from the transport package.
type TransportSender interface {
	SendLIE(pkt *encoding.ProtocolPacket) error
	LocalID() encoding.LinkIDType
	LocalAddr() netip.Addr
}

// AdjacencyEvent notifies the agent of adjacency state changes.
type AdjacencyEvent struct {
	InterfaceName string
	State         State
	Neighbor      *NeighborState // nil if OneWay
}

// FSM is the per-interface LIE finite state machine.
type FSM struct {
	state      State
	neighbor   *NeighborState
	eventQueue []Event

	// Our identity.
	systemID encoding.SystemIDType
	level    encoding.LevelType
	ifName   string // SRL interface name
	nodeName string // node name for LIE

	// Transport.
	transport TransportSender

	// Timers.
	lastLIERcvd               time.Time
	neighborHoldtime          time.Duration
	multipleNeighborsDeadline time.Time

	// Output.
	stateChangeCh chan<- AdjacencyEvent
	logger        *slog.Logger

	// Temporary state for processLIE.
	pendingLIE    *encoding.LIEPacket
	pendingHeader *encoding.PacketHeader
	pendingSrc    netip.Addr
}

// NewFSM creates a new LIE FSM in OneWay state.
func NewFSM(
	systemID encoding.SystemIDType,
	level encoding.LevelType,
	nodeName string,
	ifName string,
	transport TransportSender,
	stateChangeCh chan<- AdjacencyEvent,
	logger *slog.Logger,
) *FSM {
	return &FSM{
		state:         OneWay,
		systemID:      systemID,
		level:         level,
		nodeName:      nodeName,
		ifName:        ifName,
		transport:     transport,
		stateChangeCh: stateChangeCh,
		logger:        logger.With("interface", ifName, "fsm", "lie"),
	}
}

// State returns the current FSM state.
func (f *FSM) State() State {
	return f.state
}

// Neighbor returns the current neighbor state (nil if no neighbor).
func (f *FSM) Neighbor() *NeighborState {
	return f.neighbor
}

// HandlePacket processes a received LIE packet. Called from the
// interface goroutine.
func (f *FSM) HandlePacket(pkt *encoding.ProtocolPacket, srcAddr netip.Addr) {
	if pkt.Content.LIE == nil {
		return
	}
	f.pendingLIE = pkt.Content.LIE
	f.pendingHeader = &pkt.Header
	f.pendingSrc = srcAddr
	f.push(EventLieRcvd)
	f.drainQueue()
}

// Tick is called once per second from the interface goroutine.
func (f *FSM) Tick() {
	f.push(EventTimerTick)
	f.drainQueue()
}

// push queues an event for processing.
func (f *FSM) push(ev Event) {
	f.eventQueue = append(f.eventQueue, ev)
}

// drainQueue processes all queued events (PUSH semantics per RFC).
func (f *FSM) drainQueue() {
	for len(f.eventQueue) > 0 {
		ev := f.eventQueue[0]
		f.eventQueue = f.eventQueue[1:]
		f.processEvent(ev)
	}
}

// processEvent handles a single FSM event per the RFC 9692 transition table.
func (f *FSM) processEvent(ev Event) {
	oldState := f.state

	switch f.state {
	case OneWay:
		f.processOneWay(ev)
	case TwoWay:
		f.processTwoWay(ev)
	case ThreeWay:
		f.processThreeWay(ev)
	case MultipleNeighborsWait:
		f.processMultipleNeighborsWait(ev)
	}

	if f.state != oldState {
		f.logger.Info("state transition",
			"from", oldState.String(),
			"to", f.state.String(),
			"event", ev.String(),
		)
	}
}

func (f *FSM) processOneWay(ev Event) {
	switch ev {
	case EventTimerTick:
		f.push(EventSendLie)
	case EventLieRcvd:
		f.processLIE()
	case EventNewNeighbor:
		f.push(EventSendLie)
		f.enterState(TwoWay)
	case EventValidReflection:
		f.enterState(ThreeWay)
	case EventNeighborDroppedReflection,
		EventNeighborChangedLevel,
		EventNeighborChangedAddress,
		EventUnacceptableHeader,
		EventMTUMismatch,
		EventNeighborChangedMinorFields,
		EventHoldtimeExpired:
		// No action, stay in OneWay.
	case EventMultipleNeighbors:
		f.startMultipleNeighborsTimer()
		f.enterState(MultipleNeighborsWait)
	case EventFloodLeadersChanged:
		// Update you_are_flood_repeater (not implemented in M1).
	case EventSendLie:
		f.sendLIE()
	case EventUpdateZTPOffer:
		// No-op for configured levels.
	case EventLevelChanged:
		f.push(EventSendLie)
	}
}

func (f *FSM) processTwoWay(ev Event) {
	switch ev {
	case EventTimerTick:
		f.push(EventSendLie)
		f.checkHoldtime()
	case EventLieRcvd:
		f.processLIE()
	case EventNewNeighbor:
		f.push(EventSendLie)
		f.startMultipleNeighborsTimer()
		f.enterState(MultipleNeighborsWait)
	case EventValidReflection:
		f.enterState(ThreeWay)
	case EventNeighborChangedLevel,
		EventNeighborChangedAddress,
		EventUnacceptableHeader,
		EventMTUMismatch,
		EventHoldtimeExpired:
		f.enterState(OneWay)
	case EventNeighborChangedMinorFields:
		// Stay in TwoWay.
	case EventMultipleNeighbors:
		f.startMultipleNeighborsTimer()
		f.enterState(MultipleNeighborsWait)
	case EventFloodLeadersChanged:
		// Update you_are_flood_repeater (not implemented in M1).
	case EventSendLie:
		f.sendLIE()
	case EventUpdateZTPOffer:
		// No-op.
	case EventLevelChanged:
		f.enterState(OneWay)
	}
}

func (f *FSM) processThreeWay(ev Event) {
	switch ev {
	case EventTimerTick:
		f.push(EventSendLie)
		f.checkHoldtime()
	case EventLieRcvd:
		f.processLIE()
	case EventValidReflection:
		// Stay in ThreeWay.
	case EventNeighborDroppedReflection:
		f.enterState(TwoWay)
	case EventNeighborChangedLevel,
		EventNeighborChangedAddress,
		EventUnacceptableHeader,
		EventMTUMismatch,
		EventHoldtimeExpired,
		EventLevelChanged:
		f.enterState(OneWay)
	case EventMultipleNeighbors:
		f.startMultipleNeighborsTimer()
		f.enterState(MultipleNeighborsWait)
	case EventFloodLeadersChanged:
		f.push(EventSendLie)
	case EventSendLie:
		f.sendLIE()
	case EventUpdateZTPOffer:
		// No-op.
	case EventNewNeighbor:
		f.startMultipleNeighborsTimer()
		f.enterState(MultipleNeighborsWait)
	}
}

func (f *FSM) processMultipleNeighborsWait(ev Event) {
	switch ev {
	case EventTimerTick:
		if !f.multipleNeighborsDeadline.IsZero() && time.Now().After(f.multipleNeighborsDeadline) {
			f.push(EventMultipleNeighborsDone)
		}
	case EventMultipleNeighborsDone:
		f.enterState(OneWay)
	case EventLevelChanged:
		f.enterState(OneWay)
	case EventMultipleNeighbors:
		f.startMultipleNeighborsTimer()
	case EventLieRcvd,
		EventValidReflection,
		EventNeighborDroppedReflection,
		EventNeighborChangedAddress,
		EventUnacceptableHeader,
		EventMTUMismatch,
		EventHoldtimeExpired,
		EventSendLie,
		EventNewNeighbor:
		// Quietly ignored in MultipleNeighborsWait.
	case EventFloodLeadersChanged:
		// Update you_are_flood_repeater.
	case EventUpdateZTPOffer:
		// No-op.
	}
}

// enterState handles state transitions including entry actions.
func (f *FSM) enterState(newState State) {
	f.state = newState

	// Entry action for OneWay: CLEANUP.
	if newState == OneWay {
		f.cleanup()
	}

	// Notify agent of state change.
	ev := AdjacencyEvent{
		InterfaceName: f.ifName,
		State:         newState,
	}
	if f.neighbor != nil {
		nbr := *f.neighbor
		ev.Neighbor = &nbr
	}
	f.stateChangeCh <- ev
}

// cleanup implements the CLEANUP procedure: delete current neighbor.
func (f *FSM) cleanup() {
	f.neighbor = nil
	f.lastLIERcvd = time.Time{}
	f.neighborHoldtime = 0
}

// processLIE implements the PROCESS_LIE procedure per RFC 9692 Section 6.2.1.
func (f *FSM) processLIE() {
	header := f.pendingHeader
	lie := f.pendingLIE
	srcAddr := f.pendingSrc

	if header == nil || lie == nil {
		return
	}

	// 1. Version and system ID validation.
	if header.MajorVersion != encoding.ProtocolMajorVersion {
		f.logger.Debug("version mismatch",
			"remote", header.MajorVersion,
			"local", encoding.ProtocolMajorVersion,
		)
		f.cleanup()
		return
	}
	if header.Sender == f.systemID {
		f.logger.Debug("received own LIE", "system_id", header.Sender)
		f.cleanup()
		return
	}
	if header.Sender == encoding.IllegalSystemID {
		f.logger.Debug("illegal system ID")
		f.cleanup()
		return
	}

	// 2. MTU validation.
	localMTU := encoding.DefaultMTUSize
	remoteMTU := encoding.DefaultMTUSize
	if lie.LinkMTUSize != nil {
		remoteMTU = *lie.LinkMTUSize
	}
	if localMTU != remoteMTU {
		f.logger.Debug("MTU mismatch", "local", localMTU, "remote", remoteMTU)
		f.cleanup()
		f.push(EventUpdateZTPOffer)
		f.push(EventMTUMismatch)
		return
	}

	// 3. Level validation.
	if header.Level == nil {
		f.logger.Debug("remote level undefined")
		f.cleanup()
		f.push(EventUpdateZTPOffer)
		f.push(EventUnacceptableHeader)
		return
	}
	remoteLevel := *header.Level

	// Check level relationship.
	if !f.levelAcceptable(remoteLevel) {
		f.logger.Debug("unacceptable level",
			"local", f.level,
			"remote", remoteLevel,
		)
		f.cleanup()
		f.push(EventUpdateZTPOffer)
		f.push(EventUnacceptableHeader)
		return
	}

	// 4. Neighbor tracking.
	f.push(EventUpdateZTPOffer)

	// Update last valid LIE reception time.
	f.lastLIERcvd = time.Now()
	f.neighborHoldtime = time.Duration(lie.Holdtime) * time.Second

	if f.neighbor == nil {
		// No current neighbor: set and push NewNeighbor.
		f.neighbor = &NeighborState{
			SystemID:  header.Sender,
			Level:     remoteLevel,
			LinkID:    lie.LocalID,
			Address:   srcAddr,
			Name:      lie.Name,
			FloodPort: lie.FloodPort,
			Holdtime:  lie.Holdtime,
		}
		f.push(EventNewNeighbor)
	} else {
		// Current neighbor exists: check for changes.
		if f.neighbor.SystemID != header.Sender {
			f.push(EventMultipleNeighbors)
			return
		}
		if f.neighbor.Level != remoteLevel {
			f.push(EventNeighborChangedLevel)
			return
		}
		if f.neighbor.Address != srcAddr {
			f.push(EventNeighborChangedAddress)
			return
		}
		// Check minor field changes.
		if f.neighbor.FloodPort != lie.FloodPort ||
			f.neighbor.Name != lie.Name ||
			f.neighbor.LinkID != lie.LocalID {
			f.neighbor.FloodPort = lie.FloodPort
			f.neighbor.Name = lie.Name
			f.neighbor.LinkID = lie.LocalID
			f.push(EventNeighborChangedMinorFields)
		}
		// Update holdtime.
		f.neighbor.Holdtime = lie.Holdtime
	}

	// CHECK_THREE_WAY.
	f.checkThreeWay(lie)
}

// checkThreeWay implements the CHECK_THREE_WAY procedure.
func (f *FSM) checkThreeWay(lie *encoding.LIEPacket) {
	if f.state == OneWay {
		return
	}

	if lie.Neighbor == nil {
		// No neighbor element in received LIE.
		if f.state == ThreeWay {
			f.push(EventNeighborDroppedReflection)
		}
		return
	}

	// Check if neighbor reflects us.
	localID := f.transport.LocalID()
	if lie.Neighbor.Originator == f.systemID && lie.Neighbor.RemoteID == localID {
		f.push(EventValidReflection)
	} else {
		f.push(EventMultipleNeighbors)
	}
}

// levelAcceptable checks if the remote level is acceptable per RFC 9692.
func (f *FSM) levelAcceptable(remoteLevel encoding.LevelType) bool {
	// If this node is a leaf (level 0), accept any neighbor level.
	// RFC 9692 allows a leaf to peer with any non-leaf regardless of level.
	if f.level == encoding.LeafLevel {
		return true
	}

	// If remote is a leaf, always accept.
	if remoteLevel == encoding.LeafLevel {
		return true
	}

	// Neither is a leaf: level difference must be at most 1.
	diff := int(f.level) - int(remoteLevel)
	if diff < 0 {
		diff = -diff
	}
	return diff <= 1
}

// sendLIE implements the SEND_LIE procedure.
func (f *FSM) sendLIE() {
	level := f.level
	holdtime := encoding.DefaultLIEHoldtime

	pkt := &encoding.ProtocolPacket{
		Header: encoding.PacketHeader{
			MajorVersion: encoding.ProtocolMajorVersion,
			MinorVersion: encoding.ProtocolMinorVersion,
			Sender:       f.systemID,
			Level:        &level,
		},
		Content: encoding.PacketContent{
			LIE: &encoding.LIEPacket{
				Name:      f.nodeName,
				LocalID:   f.transport.LocalID(),
				FloodPort: encoding.DefaultTIEUDPFloodPort,
				Holdtime:  holdtime,
				NodeCapabilities: encoding.NodeCapabilities{
					ProtocolMinorVersion: encoding.ProtocolMinorVersion,
				},
			},
		},
	}

	// Reflect neighbor if we have one.
	if f.neighbor != nil {
		pkt.Content.LIE.Neighbor = &encoding.Neighbor{
			Originator: f.neighbor.SystemID,
			RemoteID:   f.neighbor.LinkID,
		}
	}

	if err := f.transport.SendLIE(pkt); err != nil {
		f.logger.Warn("send LIE failed", "error", err)
	}
}

// checkHoldtime checks if the neighbor holdtime has expired.
func (f *FSM) checkHoldtime() {
	if f.lastLIERcvd.IsZero() || f.neighborHoldtime == 0 {
		return
	}
	if time.Since(f.lastLIERcvd) > f.neighborHoldtime {
		f.logger.Info("holdtime expired",
			"last_lie", f.lastLIERcvd,
			"holdtime", f.neighborHoldtime,
		)
		f.push(EventHoldtimeExpired)
	}
}

// startMultipleNeighborsTimer sets the multiple neighbors timer.
// Timeout = multiple_neighbors_lie_holdtime_multiplier * default_lie_holdtime = 4 * 3 = 12 seconds.
func (f *FSM) startMultipleNeighborsTimer() {
	timeout := time.Duration(encoding.MultipleNeighborsLIEHoldtimeMultiplier) *
		time.Duration(encoding.DefaultLIEHoldtime) * time.Second
	f.multipleNeighborsDeadline = time.Now().Add(timeout)
}
