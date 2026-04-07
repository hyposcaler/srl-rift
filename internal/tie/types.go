package tie

import (
	"net/netip"

	"github.com/hyposcaler/srl-rift/internal/encoding"
)

// AdjacencyInfo describes a ThreeWay adjacency as seen by the flood engine.
type AdjacencyInfo struct {
	InterfaceName string
	NeighborID    encoding.SystemIDType
	NeighborLevel encoding.LevelType
	NeighborAddr  netip.Addr
	FloodPort       encoding.UDPPortType
	LocalLinkID     encoding.LinkIDType
	NeighborLinkID  encoding.LinkIDType
}

// AdjacencyChange signals an adjacency state change to the flood engine.
// Info is nil when the adjacency has dropped.
type AdjacencyChange struct {
	InterfaceName string
	Info          *AdjacencyInfo // nil = adjacency down
}

// LocalPrefix describes a locally attached prefix for TIE origination.
type LocalPrefix struct {
	Prefix   encoding.IPPrefixType
	Loopback bool
	Metric   encoding.MetricType
}

// FloodPacket carries an outbound flood packet to transport.
type FloodPacket struct {
	InterfaceName     string
	Packet            *encoding.ProtocolPacket
	DestAddr          netip.Addr
	DestPort          int
	RemainingLifetime encoding.LifeTimeInSecType
}
