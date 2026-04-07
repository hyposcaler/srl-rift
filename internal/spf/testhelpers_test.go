package spf

import (
	"net/netip"
	"testing"
	"time"

	"github.com/hyposcaler/srl-rift/internal/encoding"
	"github.com/hyposcaler/srl-rift/internal/tie"
)

// makeNodeTIE builds an LSDB entry containing a NodeTIEElement.
func makeNodeTIE(
	dir encoding.TieDirectionType,
	originator encoding.SystemIDType,
	level encoding.LevelType,
	neighbors map[encoding.SystemIDType]*encoding.NodeNeighborsTIEElement,
) (encoding.TIEID, *tie.LSDBEntry) {
	id := encoding.TIEID{
		Direction:  dir,
		Originator: originator,
		TIEType:    encoding.TIETypeNodeTIEType,
		TIENr:      1,
	}
	entry := &tie.LSDBEntry{
		Packet: &encoding.TIEPacket{
			Header: encoding.TIEHeader{TIEID: id, SeqNr: 1},
			Element: encoding.TIEElement{
				Node: &encoding.NodeTIEElement{
					Level:     level,
					Neighbors: neighbors,
				},
			},
		},
		RemainingLifetime: 3600,
		LastReceived:      time.Now(),
	}
	return id, entry
}

// makePrefixTIE builds an LSDB entry containing a PrefixTIEElement.
func makePrefixTIE(
	dir encoding.TieDirectionType,
	originator encoding.SystemIDType,
	prefixes []encoding.PrefixEntry,
) (encoding.TIEID, *tie.LSDBEntry) {
	id := encoding.TIEID{
		Direction:  dir,
		Originator: originator,
		TIEType:    encoding.TIETypePrefixTIEType,
		TIENr:      1,
	}
	entry := &tie.LSDBEntry{
		Packet: &encoding.TIEPacket{
			Header: encoding.TIEHeader{TIEID: id, SeqNr: 1},
			Element: encoding.TIEElement{
				Prefixes: &encoding.PrefixTIEElement{
					Prefixes: prefixes,
				},
			},
		},
		RemainingLifetime: 3600,
		LastReceived:      time.Now(),
	}
	return id, entry
}

// makeAdj builds an AdjacencyInfo.
func makeAdj(iface string, neighborID encoding.SystemIDType, neighborLevel encoding.LevelType, addr string) tie.AdjacencyInfo {
	return tie.AdjacencyInfo{
		InterfaceName: iface,
		NeighborID:    neighborID,
		NeighborLevel: neighborLevel,
		NeighborAddr:  netip.MustParseAddr(addr),
	}
}

// ipv4Prefix builds an IPPrefixType from raw values.
func ipv4Prefix(addr uint32, prefixLen uint8) encoding.IPPrefixType {
	return encoding.IPPrefixType{
		IPv4Prefix: &encoding.IPv4PrefixType{
			Address:   encoding.IPv4Address(addr),
			PrefixLen: encoding.PrefixLenType(prefixLen),
		},
	}
}

// metricPtr returns a pointer to a MetricType.
func metricPtr(m encoding.MetricType) *encoding.MetricType {
	return &m
}

// assertRoute checks that a RIB contains a route with expected properties.
func assertRoute(t *testing.T, rib RIB, prefix string, wantMetric encoding.MetricType, wantNHCount int, wantType encoding.RouteType) {
	t.Helper()
	route, ok := rib[prefix]
	if !ok {
		t.Errorf("missing route for %s", prefix)
		return
	}
	if route.Metric != wantMetric {
		t.Errorf("route %s: metric = %d, want %d", prefix, route.Metric, wantMetric)
	}
	if len(route.NextHops) != wantNHCount {
		t.Errorf("route %s: next-hop count = %d, want %d", prefix, len(route.NextHops), wantNHCount)
	}
	if route.RouteType != wantType {
		t.Errorf("route %s: type = %d, want %d", prefix, route.RouteType, wantType)
	}
}
