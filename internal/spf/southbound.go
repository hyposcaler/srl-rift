package spf

import (
	"container/heap"
	"log/slog"

	"github.com/hyposcaler/srl-rift/internal/encoding"
	"github.com/hyposcaler/srl-rift/internal/tie"
)

// spfNode tracks Dijkstra state for a single node.
type spfNode struct {
	systemID encoding.SystemIDType
	distance encoding.MetricType
	nextHops []NextHop // first-hop next-hops inherited through the tree
}

// ComputeSouthbound runs S-SPF using South Node TIEs with Dijkstra.
// Backlink check uses North Node TIEs. Prefix attachment uses North Prefix TIEs.
// E-W adjacencies are never used (RFC 9692 Section 6.4.2).
func ComputeSouthbound(
	systemID encoding.SystemIDType,
	level encoding.LevelType,
	adjacencies map[string]tie.AdjacencyInfo,
	entries map[encoding.TIEID]*tie.LSDBEntry,
	logger *slog.Logger,
) RIB {
	rib := make(RIB)

	// Build adjacency lookup by neighbor system ID for first-hop resolution.
	adjByNeighbor := make(map[encoding.SystemIDType]tie.AdjacencyInfo)
	for _, adj := range adjacencies {
		adjByNeighbor[adj.NeighborID] = adj
	}

	// Dijkstra initialization.
	dist := make(map[encoding.SystemIDType]*spfNode)
	dist[systemID] = &spfNode{
		systemID: systemID,
		distance: 0,
	}

	pq := &priorityQueue{}
	heap.Init(pq)
	heap.Push(pq, &pqItem{systemID: systemID, distance: 0})

	for pq.Len() > 0 {
		item := heap.Pop(pq).(*pqItem)
		current := item.systemID
		currentDist := item.distance

		node := dist[current]
		if node == nil || currentDist > node.distance {
			continue // stale entry
		}

		// Find this node's South Node TIE for its southbound adjacencies.
		southNode := FindNodeTIE(entries, encoding.TieDirectionSouth, current)
		if southNode == nil {
			continue // no southbound adjacencies known
		}

		for neighborID, neighborEntry := range southNode.Neighbors {
			// S-SPF only traverses southbound: neighbor must be at lower level.
			if neighborEntry.Level >= southNode.Level {
				continue
			}

			// Backlink check: neighbor's North Node TIE must list current node.
			neighborNorthNode := FindNodeTIE(entries, encoding.TieDirectionNorth, neighborID)
			if neighborNorthNode == nil {
				logger.Debug("S-SPF: neighbor has no North Node TIE",
					"current", current, "neighbor", neighborID)
				continue
			}
			if _, listed := neighborNorthNode.Neighbors[current]; !listed {
				logger.Debug("S-SPF: backlink check failed",
					"current", current, "neighbor", neighborID)
				continue
			}

			cost := LinkCost(neighborEntry)
			newDist := currentDist + cost

			// Resolve next-hops: for first-hop neighbors, use adjacency table.
			// For multi-hop, inherit from current node.
			var nextHops []NextHop
			if current == systemID {
				adj, ok := adjByNeighbor[neighborID]
				if !ok {
					continue // no direct adjacency to this neighbor
				}
				nextHops = []NextHop{{
					NeighborID: neighborID,
					Address:    adj.NeighborAddr,
					Interface:  adj.InterfaceName,
				}}
			} else {
				nextHops = node.nextHops
			}

			existing, ok := dist[neighborID]
			if !ok || newDist < existing.distance {
				dist[neighborID] = &spfNode{
					systemID: neighborID,
					distance: newDist,
					nextHops: nextHops,
				}
				heap.Push(pq, &pqItem{systemID: neighborID, distance: newDist})
			} else if newDist == existing.distance {
				// ECMP: merge next-hops.
				existing.nextHops = MergeNextHops(existing.nextHops, nextHops)
			}
		}
	}

	// Attach North Prefix TIEs at each reachable node (except self).
	for nodeID, node := range dist {
		if nodeID == systemID {
			continue
		}
		if len(node.nextHops) == 0 {
			continue
		}

		prefixTIEs := FindPrefixTIEs(entries, encoding.TieDirectionNorth, nodeID)
		for _, pt := range prefixTIEs {
			for _, pe := range pt.Prefixes {
				prefix := PrefixToString(pe.Prefix)
				if prefix == "" {
					continue
				}
				totalCost := pe.Attributes.Metric + node.distance
				addRoute(rib, prefix, totalCost, encoding.RouteTypeNorthPrefix, node.nextHops)
			}
		}
	}

	logger.Info("S-SPF: computation complete",
		"reachable_nodes", len(dist)-1,
		"routes", len(rib))

	return rib
}

// Priority queue for Dijkstra.

type pqItem struct {
	systemID encoding.SystemIDType
	distance encoding.MetricType
	index    int
}

type priorityQueue []*pqItem

func (pq priorityQueue) Len() int            { return len(pq) }
func (pq priorityQueue) Less(i, j int) bool  { return pq[i].distance < pq[j].distance }
func (pq priorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}

func (pq *priorityQueue) Push(x any) {
	item := x.(*pqItem)
	item.index = len(*pq)
	*pq = append(*pq, item)
}

func (pq *priorityQueue) Pop() any {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	item.index = -1
	*pq = old[:n-1]
	return item
}
