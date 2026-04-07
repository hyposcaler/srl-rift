package spf

import (
	"log/slog"
	"sort"
	"sync"

	"github.com/hyposcaler/srl-rift/internal/encoding"
	"github.com/hyposcaler/srl-rift/internal/tie"
)

// Engine runs SPF computations triggered by LSDB changes.
type Engine struct {
	systemID encoding.SystemIDType
	level    encoding.LevelType
	lsdb     *tie.LSDB
	adjFn    func() map[string]tie.AdjacencyInfo
	logger   *slog.Logger

	rib      RIB
	southRIB RIB // S-SPF result before merging N-SPF (for disaggregation)
	ribMu    sync.RWMutex
}

// NewEngine creates an SPF engine. Call Run() to trigger computation.
func NewEngine(
	systemID encoding.SystemIDType,
	level encoding.LevelType,
	lsdb *tie.LSDB,
	adjFn func() map[string]tie.AdjacencyInfo,
	logger *slog.Logger,
) *Engine {
	return &Engine{
		systemID: systemID,
		level:    level,
		lsdb:     lsdb,
		adjFn:    adjFn,
		logger:   logger,
		rib:      make(RIB),
	}
}

// Run performs a full SPF computation from the current LSDB and adjacency state.
func (e *Engine) Run() {
	entries := e.lsdb.Snapshot()
	adjacencies := e.adjFn()

	var rib RIB
	var sRIB RIB

	if e.level == encoding.LeafLevel {
		// Leaf: northbound only (leaf optimization).
		rib = ComputeNorthbound(e.systemID, e.level, adjacencies, entries, e.logger)
	} else {
		// Spine/higher: southbound Dijkstra + northbound for default route.
		sRIB = ComputeSouthbound(e.systemID, e.level, adjacencies, entries, e.logger)
		rib = make(RIB, len(sRIB))
		for k, v := range sRIB {
			rib[k] = v
		}

		// Also compute northbound reachability (default route from above).
		northRIB := ComputeNorthbound(e.systemID, e.level, adjacencies, entries, e.logger)
		for prefix, route := range northRIB {
			addRoute(rib, prefix, route.Metric, route.RouteType, route.NextHops)
		}
	}

	e.ribMu.Lock()
	e.rib = rib
	e.southRIB = sRIB
	e.ribMu.Unlock()

	e.logRIB(rib)
}

// RIB returns a copy of the current computed RIB.
func (e *Engine) RIB() RIB {
	e.ribMu.RLock()
	defer e.ribMu.RUnlock()
	result := make(RIB, len(e.rib))
	for k, v := range e.rib {
		result[k] = v
	}
	return result
}

// SouthRIB returns a copy of the last S-SPF result (before N-SPF merge).
// Used by disaggregation computation on spines.
func (e *Engine) SouthRIB() RIB {
	e.ribMu.RLock()
	defer e.ribMu.RUnlock()
	if e.southRIB == nil {
		return nil
	}
	result := make(RIB, len(e.southRIB))
	for k, v := range e.southRIB {
		result[k] = v
	}
	return result
}

// logRIB logs all RIB entries.
func (e *Engine) logRIB(rib RIB) {
	if len(rib) == 0 {
		e.logger.Info("SPF: RIB is empty")
		return
	}

	// Sort prefixes for deterministic output.
	prefixes := make([]string, 0, len(rib))
	for p := range rib {
		prefixes = append(prefixes, p)
	}
	sort.Strings(prefixes)

	for _, p := range prefixes {
		route := rib[p]
		nhIDs := make([]encoding.SystemIDType, 0, len(route.NextHops))
		for _, nh := range route.NextHops {
			nhIDs = append(nhIDs, nh.NeighborID)
		}
		e.logger.Info("SPF: route",
			"prefix", route.Prefix,
			"metric", route.Metric,
			"type", route.RouteType,
			"next_hops", nhIDs,
		)
	}
}
