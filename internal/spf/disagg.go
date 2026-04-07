package spf

import (
	"log/slog"

	"github.com/hyposcaler/srl-rift/internal/encoding"
	"github.com/hyposcaler/srl-rift/internal/tie"
)

// DisaggPrefix describes a prefix that needs positive disaggregation.
type DisaggPrefix struct {
	Prefix   encoding.IPPrefixType
	Distance encoding.MetricType
}

// ComputeDisaggregation determines which S-SPF prefixes need positive
// disaggregation per RFC 9692 Section 6.5.1. Only meaningful on spines
// (level > 0). Returns nil if no disaggregation is needed.
func ComputeDisaggregation(
	systemID encoding.SystemIDType,
	level encoding.LevelType,
	adjacencies map[string]tie.AdjacencyInfo,
	entries map[encoding.TIEID]*tie.LSDBEntry,
	southRIB RIB,
	logger *slog.Logger,
) []DisaggPrefix {
	// Step 1: Build own southbound neighbor set.
	southNeighbors := make(map[encoding.SystemIDType]struct{})
	for _, adj := range adjacencies {
		if adj.NeighborLevel < level {
			southNeighbors[adj.NeighborID] = struct{}{}
		}
	}
	if len(southNeighbors) == 0 {
		return nil
	}

	// Step 2: Find same-level peers from reflected South Node TIEs.
	type peerInfo struct {
		southNeighbors map[encoding.SystemIDType]struct{}
	}
	peers := make(map[encoding.SystemIDType]*peerInfo)

	for id, entry := range entries {
		if id.Direction != encoding.TieDirectionSouth ||
			id.TIEType != encoding.TIETypeNodeTIEType ||
			id.Originator == systemID {
			continue
		}
		node := entry.Packet.Element.Node
		if node == nil || node.Level != level {
			continue
		}
		pi := &peerInfo{southNeighbors: make(map[encoding.SystemIDType]struct{})}
		for neighborID, neighborEntry := range node.Neighbors {
			if neighborEntry.Level < level {
				pi.southNeighbors[neighborID] = struct{}{}
			}
		}
		peers[id.Originator] = pi
	}

	if len(peers) == 0 {
		return nil // no same-level peers
	}

	// Step 3: Compute partial_neighbors.
	// partial_neighbors[N] = set of same-level peers that CANNOT reach N.
	partialNeighbors := make(map[encoding.SystemIDType]map[encoding.SystemIDType]struct{})

	for peerID, peer := range peers {
		// Must share at least one south neighbor with us.
		hasShared := false
		for n := range peer.southNeighbors {
			if _, ok := southNeighbors[n]; ok {
				hasShared = true
				break
			}
		}
		if !hasShared {
			continue
		}

		// For each of OUR south neighbors NOT in peer's set, mark as partial.
		for n := range southNeighbors {
			if _, ok := peer.southNeighbors[n]; !ok {
				if partialNeighbors[n] == nil {
					partialNeighbors[n] = make(map[encoding.SystemIDType]struct{})
				}
				partialNeighbors[n][peerID] = struct{}{}
			}
		}
	}

	if len(partialNeighbors) == 0 {
		return nil
	}

	logger.Info("disaggregation: partial neighbors detected",
		"partial_count", len(partialNeighbors))

	// Step 4: Identify prefixes to disaggregate.
	// For each route in southRIB, check if any next-hop is a partial neighbor.
	// If so, the prefix needs disaggregation.
	var result []DisaggPrefix

	for _, route := range southRIB {
		needsDisagg := false
		for _, nh := range route.NextHops {
			if _, ok := partialNeighbors[nh.NeighborID]; ok {
				needsDisagg = true
				break
			}
		}
		if !needsDisagg {
			continue
		}

		// Find the encoding.IPPrefixType for this route's prefix.
		// We need to convert from the string prefix back to IPPrefixType.
		ipPrefix := StringToPrefix(route.Prefix)
		if ipPrefix.IPv4Prefix == nil {
			continue
		}

		result = append(result, DisaggPrefix{
			Prefix:   ipPrefix,
			Distance: route.Metric,
		})
	}

	if len(result) > 0 {
		logger.Info("disaggregation: prefixes to disaggregate",
			"count", len(result))
	}

	return result
}
