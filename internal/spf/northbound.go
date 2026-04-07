package spf

import (
	"log/slog"

	"github.com/hyposcaler/srl-rift/internal/encoding"
	"github.com/hyposcaler/srl-rift/internal/tie"
)

// validatedNeighbor is a northbound neighbor that passed backlink verification.
type validatedNeighbor struct {
	nextHop  NextHop
	linkCost encoding.MetricType
}

// ComputeNorthbound computes northbound reachability per RFC 9692 Section 6.8.3
// (leaf optimization). Validates northbound neighbors via South Node TIE backlink
// check, then installs default route with ECMP and attaches South Prefix TIE
// prefixes from validated neighbors.
func ComputeNorthbound(
	systemID encoding.SystemIDType,
	level encoding.LevelType,
	adjacencies map[string]tie.AdjacencyInfo,
	entries map[encoding.TIEID]*tie.LSDBEntry,
	logger *slog.Logger,
) RIB {
	rib := make(RIB)

	// Find our own North Node TIE for link cost information.
	ownNodeTIE := FindNodeTIE(entries, encoding.TieDirectionNorth, systemID)

	// Validate each northbound neighbor.
	var validated []validatedNeighbor
	for _, adj := range adjacencies {
		if adj.NeighborLevel <= level {
			continue // not a northbound neighbor
		}

		// Backlink check: neighbor's South Node TIE must list us.
		neighborSouthNode := FindNodeTIE(entries, encoding.TieDirectionSouth, adj.NeighborID)
		if neighborSouthNode == nil {
			logger.Debug("N-SPF: neighbor has no South Node TIE, skipping",
				"neighbor", adj.NeighborID)
			continue
		}
		if _, listed := neighborSouthNode.Neighbors[systemID]; !listed {
			logger.Debug("N-SPF: neighbor South Node TIE does not list us, skipping",
				"neighbor", adj.NeighborID)
			continue
		}

		// Get link cost from our North Node TIE, defaulting to 1.
		cost := encoding.DefaultDistance
		if ownNodeTIE != nil {
			if neighborEntry, ok := ownNodeTIE.Neighbors[adj.NeighborID]; ok {
				cost = LinkCost(neighborEntry)
			}
		}

		validated = append(validated, validatedNeighbor{
			nextHop: NextHop{
				NeighborID: adj.NeighborID,
				Address:    adj.NeighborAddr,
				Interface:  adj.InterfaceName,
			},
			linkCost: cost,
		})
	}

	if len(validated) == 0 {
		logger.Info("N-SPF: no validated northbound neighbors")
		return rib
	}

	// Find minimum cost.
	minCost := validated[0].linkCost
	for _, v := range validated[1:] {
		if v.linkCost < minCost {
			minCost = v.linkCost
		}
	}

	// ECMP: collect all neighbors at minimum cost for default route.
	var defaultNextHops []NextHop
	for _, v := range validated {
		if v.linkCost == minCost {
			defaultNextHops = append(defaultNextHops, v.nextHop)
		}
	}

	// Install default route 0.0.0.0/0.
	addRoute(rib, "0.0.0.0/0", minCost, encoding.RouteTypeSouthPrefix, defaultNextHops)
	logger.Info("N-SPF: default route installed",
		"next_hops", len(defaultNextHops),
		"metric", minCost)

	// Attach South Prefix TIE prefixes from each validated neighbor.
	for _, v := range validated {
		prefixTIEs := FindPrefixTIEs(entries, encoding.TieDirectionSouth, v.nextHop.NeighborID)
		for _, pt := range prefixTIEs {
			for _, pe := range pt.Prefixes {
				prefix := PrefixToString(pe.Prefix)
				if prefix == "" {
					continue
				}
				totalCost := pe.Attributes.Metric + v.linkCost
				addRoute(rib, prefix, totalCost, encoding.RouteTypeSouthPrefix, []NextHop{v.nextHop})
			}
		}

		// Attach South Positive Disaggregation Prefix TIEs.
		disaggTIEs := FindPositiveDisaggPrefixTIEs(entries, encoding.TieDirectionSouth, v.nextHop.NeighborID)
		for _, pt := range disaggTIEs {
			for _, pe := range pt.Prefixes {
				prefix := PrefixToString(pe.Prefix)
				if prefix == "" {
					continue
				}
				totalCost := pe.Attributes.Metric + v.linkCost
				addRoute(rib, prefix, totalCost, encoding.RouteTypeSouthPrefix, []NextHop{v.nextHop})
			}
		}
	}

	return rib
}
