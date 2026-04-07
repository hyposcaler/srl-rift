package spf

import (
	"fmt"

	"github.com/hyposcaler/srl-rift/internal/encoding"
	"github.com/hyposcaler/srl-rift/internal/tie"
)

// PrefixToString converts an IPPrefixType to a canonical "a.b.c.d/len" string.
func PrefixToString(p encoding.IPPrefixType) string {
	if p.IPv4Prefix != nil {
		addr := p.IPv4Prefix.Address
		return fmt.Sprintf("%d.%d.%d.%d/%d",
			byte(addr>>24), byte(addr>>16), byte(addr>>8), byte(addr),
			p.IPv4Prefix.PrefixLen)
	}
	return ""
}

// LinkCost extracts the cost from a NodeNeighborsTIEElement, defaulting to 1.
func LinkCost(n *encoding.NodeNeighborsTIEElement) encoding.MetricType {
	if n.Cost != nil {
		return *n.Cost
	}
	return encoding.DefaultDistance
}

// FindNodeTIE looks up a Node TIE by direction and originator in an LSDB snapshot.
func FindNodeTIE(entries map[encoding.TIEID]*tie.LSDBEntry, dir encoding.TieDirectionType, originator encoding.SystemIDType) *encoding.NodeTIEElement {
	id := encoding.TIEID{
		Direction:  dir,
		Originator: originator,
		TIEType:    encoding.TIETypeNodeTIEType,
		TIENr:      1,
	}
	entry, ok := entries[id]
	if !ok || entry.Packet.Element.Node == nil {
		return nil
	}
	return entry.Packet.Element.Node
}

// FindPrefixTIEs returns all Prefix TIE elements for a direction and originator.
func FindPrefixTIEs(entries map[encoding.TIEID]*tie.LSDBEntry, dir encoding.TieDirectionType, originator encoding.SystemIDType) []*encoding.PrefixTIEElement {
	var result []*encoding.PrefixTIEElement
	// Prefix TIEs use TIENr starting at 1. In practice there is one per
	// (direction, originator), but we scan for robustness.
	for id, entry := range entries {
		if id.Direction == dir && id.Originator == originator && id.TIEType == encoding.TIETypePrefixTIEType {
			if entry.Packet.Element.Prefixes != nil {
				result = append(result, entry.Packet.Element.Prefixes)
			}
		}
	}
	return result
}

// MergeNextHops merges two NextHop slices, deduplicating by NeighborID.
func MergeNextHops(a, b []NextHop) []NextHop {
	seen := make(map[encoding.SystemIDType]struct{}, len(a))
	result := make([]NextHop, 0, len(a)+len(b))
	for _, nh := range a {
		seen[nh.NeighborID] = struct{}{}
		result = append(result, nh)
	}
	for _, nh := range b {
		if _, ok := seen[nh.NeighborID]; !ok {
			seen[nh.NeighborID] = struct{}{}
			result = append(result, nh)
		}
	}
	return result
}

// addRoute inserts or merges a route into a RIB. If the prefix already exists
// with a lower or equal metric, the new route is merged (ECMP) or ignored.
func addRoute(rib RIB, prefix string, metric encoding.MetricType, routeType encoding.RouteType, nextHops []NextHop) {
	existing, ok := rib[prefix]
	if !ok {
		rib[prefix] = &Route{
			Prefix:    prefix,
			Metric:    metric,
			RouteType: routeType,
			NextHops:  nextHops,
		}
		return
	}

	// Lower route type ordinal wins (per RFC 9692 Section 6.8.1).
	if routeType < existing.RouteType {
		rib[prefix] = &Route{
			Prefix:    prefix,
			Metric:    metric,
			RouteType: routeType,
			NextHops:  nextHops,
		}
		return
	}
	if routeType > existing.RouteType {
		return
	}

	// Same route type: prefer lower metric, merge on equal.
	if metric < existing.Metric {
		rib[prefix] = &Route{
			Prefix:    prefix,
			Metric:    metric,
			RouteType: routeType,
			NextHops:  nextHops,
		}
	} else if metric == existing.Metric {
		existing.NextHops = MergeNextHops(existing.NextHops, nextHops)
	}
}
