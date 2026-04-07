package tie

import (
	"net/netip"
	"time"

	"github.com/hyposcaler/srl-rift/internal/encoding"
)

// originateSelfTIEs generates or re-generates all self-originated TIEs
// based on current adjacency and prefix state. Inserts them into the LSDB
// and returns the TIEIDs that were created or updated.
func (fe *FloodEngine) originateSelfTIEs() []encoding.TIEID {
	var changed []encoding.TIEID

	if id, ok := fe.originateNorthNodeTIE(); ok {
		changed = append(changed, id)
	}
	if id, ok := fe.originateNorthPrefixTIE(); ok {
		changed = append(changed, id)
	}
	// Only spines (level > 0) originate South TIEs.
	if fe.level > encoding.LeafLevel {
		if id, ok := fe.originateSouthNodeTIE(); ok {
			changed = append(changed, id)
		}
		if id, ok := fe.originateSouthPrefixTIE(); ok {
			changed = append(changed, id)
		}
	}

	return changed
}

// originateNorthNodeTIE creates a North Node TIE listing adjacencies to
// higher-level neighbors.
func (fe *FloodEngine) originateNorthNodeTIE() (encoding.TIEID, bool) {
	id := encoding.TIEID{
		Direction:  encoding.TieDirectionNorth,
		Originator: fe.systemID,
		TIEType:    encoding.TIETypeNodeTIEType,
		TIENr:      1,
	}

	neighbors := make(map[encoding.SystemIDType]*encoding.NodeNeighborsTIEElement)
	for _, adj := range fe.adjacencies {
		// North Node TIE contains neighbors at higher or equal level.
		if adj.Info.NeighborLevel >= fe.level {
			cost := encoding.MetricType(1)
			neighbors[adj.Info.NeighborID] = &encoding.NodeNeighborsTIEElement{
				Level: adj.Info.NeighborLevel,
				Cost:  &cost,
				LinkIDs: []encoding.LinkIDPair{
					{
						LocalID:  adj.Info.LocalLinkID,
						RemoteID: adj.Info.NeighborLinkID,
					},
				},
			}
		}
	}

	node := &encoding.NodeTIEElement{
		Level:     fe.level,
		Neighbors: neighbors,
		Capabilities: encoding.NodeCapabilities{
			ProtocolMinorVersion: encoding.ProtocolMinorVersion,
		},
		Name: fe.nodeName,
	}
	// Leaves set overload per RFC 9692 Section 6.8.2.
	if fe.level == encoding.LeafLevel {
		node.Flags = &encoding.NodeFlags{Overload: boolPtr(true)}
	}

	element := encoding.TIEElement{Node: node}

	return id, fe.insertSelfOriginated(id, element)
}

// originateNorthPrefixTIE creates a North Prefix TIE with locally
// attached prefixes (loopback and link addresses).
func (fe *FloodEngine) originateNorthPrefixTIE() (encoding.TIEID, bool) {
	id := encoding.TIEID{
		Direction:  encoding.TieDirectionNorth,
		Originator: fe.systemID,
		TIEType:    encoding.TIETypePrefixTIEType,
		TIENr:      1,
	}

	var prefixes []encoding.PrefixEntry
	for _, lp := range fe.localPrefixes {
		attrs := encoding.PrefixAttributes{
			Metric: lp.Metric,
		}
		if lp.Loopback {
			t := true
			attrs.Loopback = &t
		}
		prefixes = append(prefixes, encoding.PrefixEntry{
			Prefix:     lp.Prefix,
			Attributes: attrs,
		})
	}

	element := encoding.TIEElement{
		Prefixes: &encoding.PrefixTIEElement{
			Prefixes: prefixes,
		},
	}

	return id, fe.insertSelfOriginated(id, element)
}

// originateSouthNodeTIE creates a South Node TIE listing adjacencies to
// lower-level neighbors. Only called for level > 0.
func (fe *FloodEngine) originateSouthNodeTIE() (encoding.TIEID, bool) {
	id := encoding.TIEID{
		Direction:  encoding.TieDirectionSouth,
		Originator: fe.systemID,
		TIEType:    encoding.TIETypeNodeTIEType,
		TIENr:      1,
	}

	neighbors := make(map[encoding.SystemIDType]*encoding.NodeNeighborsTIEElement)
	for _, adj := range fe.adjacencies {
		// South Node TIE contains neighbors at lower level.
		if adj.Info.NeighborLevel < fe.level {
			cost := encoding.MetricType(1)
			neighbors[adj.Info.NeighborID] = &encoding.NodeNeighborsTIEElement{
				Level: adj.Info.NeighborLevel,
				Cost:  &cost,
				LinkIDs: []encoding.LinkIDPair{
					{
						LocalID:  adj.Info.LocalLinkID,
						RemoteID: adj.Info.NeighborLinkID,
					},
				},
			}
		}
	}

	element := encoding.TIEElement{
		Node: &encoding.NodeTIEElement{
			Level:     fe.level,
			Neighbors: neighbors,
			Capabilities: encoding.NodeCapabilities{
				ProtocolMinorVersion: encoding.ProtocolMinorVersion,
			},
			Name: fe.nodeName,
		},
	}

	return id, fe.insertSelfOriginated(id, element)
}

// originateSouthPrefixTIE creates a South Prefix TIE with a default route.
// The actual default route metric is set in M3 after SPF; for now use metric 1.
func (fe *FloodEngine) originateSouthPrefixTIE() (encoding.TIEID, bool) {
	id := encoding.TIEID{
		Direction:  encoding.TieDirectionSouth,
		Originator: fe.systemID,
		TIEType:    encoding.TIETypePrefixTIEType,
		TIENr:      1,
	}

	defaultRoute := encoding.IPPrefixType{
		IPv4Prefix: &encoding.IPv4PrefixType{
			Address:   0,
			PrefixLen: 0,
		},
	}

	element := encoding.TIEElement{
		Prefixes: &encoding.PrefixTIEElement{
			Prefixes: []encoding.PrefixEntry{
				{
					Prefix:     defaultRoute,
					Attributes: encoding.PrefixAttributes{Metric: 1},
				},
			},
		},
	}

	return id, fe.insertSelfOriginated(id, element)
}

// insertSelfOriginated inserts or updates a self-originated TIE in the LSDB.
// Bumps the sequence number if the TIE already exists. Returns true if the
// TIE was actually changed (new or content differs).
func (fe *FloodEngine) insertSelfOriginated(id encoding.TIEID, element encoding.TIEElement) bool {
	existing := fe.lsdb.Get(id)

	seqNr := encoding.SeqNrType(1)
	if existing != nil && existing.SelfOriginated {
		seqNr = existing.Packet.Header.SeqNr + 1
	}

	lifetime := encoding.DefaultLifetime
	pkt := &encoding.TIEPacket{
		Header: encoding.TIEHeader{
			TIEID:               id,
			SeqNr:               seqNr,
			OriginationLifetime: &lifetime,
		},
		Element: element,
	}

	fe.lsdb.Insert(&LSDBEntry{
		Packet:            pkt,
		RemainingLifetime: lifetime,
		LastReceived:      time.Now(),
		SelfOriginated:    true,
	})

	return true
}

// bumpOwnTIE re-originates a self-originated TIE with a higher sequence
// number. Called when a stale version of our own TIE is detected via TIDE
// or received from the network.
func (fe *FloodEngine) bumpOwnTIE(id encoding.TIEID) {
	existing := fe.lsdb.Get(id)
	if existing == nil || !existing.SelfOriginated {
		return
	}

	lifetime := encoding.DefaultLifetime
	newSeqNr := existing.Packet.Header.SeqNr + 1
	existing.Packet.Header.SeqNr = newSeqNr
	existing.Packet.Header.OriginationLifetime = &lifetime
	existing.RemainingLifetime = lifetime
	existing.LastReceived = time.Now()

	fe.lsdb.Insert(existing)
}

// OriginatePositiveDisaggTIE creates or withdraws a South Positive
// Disaggregation Prefix TIE. If prefixes is non-empty, the TIE is
// originated (or updated). If empty and a previous TIE exists, it is removed.
// Returns the TIEID and whether the LSDB was changed.
func (fe *FloodEngine) OriginatePositiveDisaggTIE(prefixes []encoding.PrefixEntry) (encoding.TIEID, bool) {
	id := encoding.TIEID{
		Direction:  encoding.TieDirectionSouth,
		Originator: fe.systemID,
		TIEType:    encoding.TIETypePositiveDisaggregationPrefixTIEType,
		TIENr:      1,
	}

	if len(prefixes) == 0 {
		existing := fe.lsdb.Get(id)
		if existing == nil || !existing.SelfOriginated {
			return id, false
		}
		// Check if already empty (already withdrawn).
		if existing.Packet.Element.PositiveDisaggregationPrefixes != nil &&
			len(existing.Packet.Element.PositiveDisaggregationPrefixes.Prefixes) == 0 {
			return id, false
		}
		// Withdraw: originate empty TIE with bumped sequence number.
		// TIDE/TIRE will propagate the empty version to peers.
		element := encoding.TIEElement{
			PositiveDisaggregationPrefixes: &encoding.PrefixTIEElement{},
		}
		fe.insertSelfOriginated(id, element)
		fe.logger.Info("disaggregation: withdrew positive disagg TIE")
		return id, true
	}

	element := encoding.TIEElement{
		PositiveDisaggregationPrefixes: &encoding.PrefixTIEElement{
			Prefixes: prefixes,
		},
	}

	changed := fe.insertSelfOriginated(id, element)
	if changed {
		fe.logger.Info("disaggregation: originated positive disagg TIE",
			"prefix_count", len(prefixes))
	}
	return id, changed
}

func boolPtr(v bool) *bool { return &v }

// IPv4ToPrefix converts a netip.Addr and prefix length to an IPPrefixType.
func IPv4ToPrefix(addr netip.Addr, prefixLen int8) encoding.IPPrefixType {
	raw := addr.As4()
	ipv4 := encoding.IPv4Address(int32(raw[0])<<24 | int32(raw[1])<<16 | int32(raw[2])<<8 | int32(raw[3]))
	return encoding.IPPrefixType{
		IPv4Prefix: &encoding.IPv4PrefixType{
			Address:   ipv4,
			PrefixLen: encoding.PrefixLenType(prefixLen),
		},
	}
}
