package spf

import (
	"log/slog"
	"os"
	"testing"

	"github.com/hyposcaler/srl-rift/internal/encoding"
	"github.com/hyposcaler/srl-rift/internal/tie"
)

func TestComputeSouthbound(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Reference topology: spine 1 (level 1), leaves 10/11/12 (level 0).
	const (
		spine1 encoding.SystemIDType = 1
		leaf1  encoding.SystemIDType = 10
		leaf2  encoding.SystemIDType = 11
		leaf3  encoding.SystemIDType = 12
	)

	tests := []struct {
		name        string
		systemID    encoding.SystemIDType
		level       encoding.LevelType
		adjacencies map[string]tie.AdjacencyInfo
		entries     map[encoding.TIEID]*tie.LSDBEntry
		wantRoutes  int
		check       func(t *testing.T, rib RIB)
	}{
		{
			name:     "single_leaf_loopback",
			systemID: spine1,
			level:    1,
			adjacencies: map[string]tie.AdjacencyInfo{
				"ethernet-1/1": makeAdj("ethernet-1/1", leaf1, 0, "10.1.1.1"),
			},
			entries: func() map[encoding.TIEID]*tie.LSDBEntry {
				m := make(map[encoding.TIEID]*tie.LSDBEntry)
				// Spine1 South Node TIE lists leaf1.
				id, e := makeNodeTIE(encoding.TieDirectionSouth, spine1, 1, map[encoding.SystemIDType]*encoding.NodeNeighborsTIEElement{
					leaf1: {Level: 0},
				})
				m[id] = e
				// Leaf1 North Node TIE lists spine1 (backlink).
				id, e = makeNodeTIE(encoding.TieDirectionNorth, leaf1, 0, map[encoding.SystemIDType]*encoding.NodeNeighborsTIEElement{
					spine1: {Level: 1},
				})
				m[id] = e
				// Leaf1 North Prefix TIE with loopback.
				id, e = makePrefixTIE(encoding.TieDirectionNorth, leaf1, []encoding.PrefixEntry{
					{Prefix: ipv4Prefix(0x0A0A0A0A, 32), Attributes: encoding.PrefixAttributes{Metric: 0}},
				})
				m[id] = e
				return m
			}(),
			wantRoutes: 1,
			check: func(t *testing.T, rib RIB) {
				assertRoute(t, rib, "10.10.10.10/32", 1, 1, encoding.RouteTypeNorthPrefix) // 0 + 1 link cost
			},
		},
		{
			name:     "three_leaves",
			systemID: spine1,
			level:    1,
			adjacencies: map[string]tie.AdjacencyInfo{
				"ethernet-1/1": makeAdj("ethernet-1/1", leaf1, 0, "10.1.1.1"),
				"ethernet-1/2": makeAdj("ethernet-1/2", leaf2, 0, "10.1.2.1"),
				"ethernet-1/3": makeAdj("ethernet-1/3", leaf3, 0, "10.1.3.1"),
			},
			entries: func() map[encoding.TIEID]*tie.LSDBEntry {
				m := make(map[encoding.TIEID]*tie.LSDBEntry)
				// Spine South Node TIE lists all leaves.
				id, e := makeNodeTIE(encoding.TieDirectionSouth, spine1, 1, map[encoding.SystemIDType]*encoding.NodeNeighborsTIEElement{
					leaf1: {Level: 0},
					leaf2: {Level: 0},
					leaf3: {Level: 0},
				})
				m[id] = e
				// Each leaf's North Node TIE (backlink).
				for _, lf := range []encoding.SystemIDType{leaf1, leaf2, leaf3} {
					id, e = makeNodeTIE(encoding.TieDirectionNorth, lf, 0, map[encoding.SystemIDType]*encoding.NodeNeighborsTIEElement{
						spine1: {Level: 1},
					})
					m[id] = e
				}
				// Each leaf's loopback prefix.
				id, e = makePrefixTIE(encoding.TieDirectionNorth, leaf1, []encoding.PrefixEntry{
					{Prefix: ipv4Prefix(0x0A0A0A01, 32), Attributes: encoding.PrefixAttributes{Metric: 0}},
				})
				m[id] = e
				id, e = makePrefixTIE(encoding.TieDirectionNorth, leaf2, []encoding.PrefixEntry{
					{Prefix: ipv4Prefix(0x0A0A0A02, 32), Attributes: encoding.PrefixAttributes{Metric: 0}},
				})
				m[id] = e
				id, e = makePrefixTIE(encoding.TieDirectionNorth, leaf3, []encoding.PrefixEntry{
					{Prefix: ipv4Prefix(0x0A0A0A03, 32), Attributes: encoding.PrefixAttributes{Metric: 0}},
				})
				m[id] = e
				return m
			}(),
			wantRoutes: 3,
			check: func(t *testing.T, rib RIB) {
				assertRoute(t, rib, "10.10.10.1/32", 1, 1, encoding.RouteTypeNorthPrefix)
				assertRoute(t, rib, "10.10.10.2/32", 1, 1, encoding.RouteTypeNorthPrefix)
				assertRoute(t, rib, "10.10.10.3/32", 1, 1, encoding.RouteTypeNorthPrefix)
			},
		},
		{
			name:     "backlink_fails",
			systemID: spine1,
			level:    1,
			adjacencies: map[string]tie.AdjacencyInfo{
				"ethernet-1/1": makeAdj("ethernet-1/1", leaf1, 0, "10.1.1.1"),
			},
			entries: func() map[encoding.TIEID]*tie.LSDBEntry {
				m := make(map[encoding.TIEID]*tie.LSDBEntry)
				id, e := makeNodeTIE(encoding.TieDirectionSouth, spine1, 1, map[encoding.SystemIDType]*encoding.NodeNeighborsTIEElement{
					leaf1: {Level: 0},
				})
				m[id] = e
				// Leaf1 North Node TIE does NOT list spine1.
				id, e = makeNodeTIE(encoding.TieDirectionNorth, leaf1, 0, map[encoding.SystemIDType]*encoding.NodeNeighborsTIEElement{
					99: {Level: 1},
				})
				m[id] = e
				return m
			}(),
			wantRoutes: 0,
		},
		{
			name:     "no_north_node_tie",
			systemID: spine1,
			level:    1,
			adjacencies: map[string]tie.AdjacencyInfo{
				"ethernet-1/1": makeAdj("ethernet-1/1", leaf1, 0, "10.1.1.1"),
			},
			entries: func() map[encoding.TIEID]*tie.LSDBEntry {
				m := make(map[encoding.TIEID]*tie.LSDBEntry)
				id, e := makeNodeTIE(encoding.TieDirectionSouth, spine1, 1, map[encoding.SystemIDType]*encoding.NodeNeighborsTIEElement{
					leaf1: {Level: 0},
				})
				m[id] = e
				// No North Node TIE for leaf1.
				return m
			}(),
			wantRoutes: 0,
		},
		{
			name:        "empty_lsdb",
			systemID:    spine1,
			level:       1,
			adjacencies: map[string]tie.AdjacencyInfo{},
			entries:     map[encoding.TIEID]*tie.LSDBEntry{},
			wantRoutes:  0,
		},
		{
			name:     "no_adjacencies",
			systemID: spine1,
			level:    1,
			adjacencies: map[string]tie.AdjacencyInfo{},
			entries: func() map[encoding.TIEID]*tie.LSDBEntry {
				m := make(map[encoding.TIEID]*tie.LSDBEntry)
				id, e := makeNodeTIE(encoding.TieDirectionSouth, spine1, 1, map[encoding.SystemIDType]*encoding.NodeNeighborsTIEElement{
					leaf1: {Level: 0},
				})
				m[id] = e
				id, e = makeNodeTIE(encoding.TieDirectionNorth, leaf1, 0, map[encoding.SystemIDType]*encoding.NodeNeighborsTIEElement{
					spine1: {Level: 1},
				})
				m[id] = e
				id, e = makePrefixTIE(encoding.TieDirectionNorth, leaf1, []encoding.PrefixEntry{
					{Prefix: ipv4Prefix(0x0A0A0A0A, 32), Attributes: encoding.PrefixAttributes{Metric: 0}},
				})
				m[id] = e
				return m
			}(),
			wantRoutes: 0,
		},
		{
			name:     "ew_skipped",
			systemID: spine1,
			level:    1,
			adjacencies: map[string]tie.AdjacencyInfo{
				"ethernet-1/1": makeAdj("ethernet-1/1", 2, 1, "10.1.1.2"), // spine2, same level
			},
			entries: func() map[encoding.TIEID]*tie.LSDBEntry {
				m := make(map[encoding.TIEID]*tie.LSDBEntry)
				// Spine1 South Node TIE lists spine2 at same level.
				id, e := makeNodeTIE(encoding.TieDirectionSouth, spine1, 1, map[encoding.SystemIDType]*encoding.NodeNeighborsTIEElement{
					2: {Level: 1},
				})
				m[id] = e
				return m
			}(),
			wantRoutes: 0,
		},
		{
			name:     "no_south_node_tie_self",
			systemID: spine1,
			level:    1,
			adjacencies: map[string]tie.AdjacencyInfo{
				"ethernet-1/1": makeAdj("ethernet-1/1", leaf1, 0, "10.1.1.1"),
			},
			entries:    map[encoding.TIEID]*tie.LSDBEntry{},
			wantRoutes: 0,
		},
		{
			name:     "prefix_metric_additive",
			systemID: spine1,
			level:    1,
			adjacencies: map[string]tie.AdjacencyInfo{
				"ethernet-1/1": makeAdj("ethernet-1/1", leaf1, 0, "10.1.1.1"),
			},
			entries: func() map[encoding.TIEID]*tie.LSDBEntry {
				m := make(map[encoding.TIEID]*tie.LSDBEntry)
				id, e := makeNodeTIE(encoding.TieDirectionSouth, spine1, 1, map[encoding.SystemIDType]*encoding.NodeNeighborsTIEElement{
					leaf1: {Level: 0, Cost: metricPtr(3)},
				})
				m[id] = e
				id, e = makeNodeTIE(encoding.TieDirectionNorth, leaf1, 0, map[encoding.SystemIDType]*encoding.NodeNeighborsTIEElement{
					spine1: {Level: 1},
				})
				m[id] = e
				id, e = makePrefixTIE(encoding.TieDirectionNorth, leaf1, []encoding.PrefixEntry{
					{Prefix: ipv4Prefix(0x0A0A0A0A, 32), Attributes: encoding.PrefixAttributes{Metric: 5}},
				})
				m[id] = e
				return m
			}(),
			wantRoutes: 1,
			check: func(t *testing.T, rib RIB) {
				assertRoute(t, rib, "10.10.10.10/32", 8, 1, encoding.RouteTypeNorthPrefix) // 5 + 3
			},
		},
		{
			name:     "no_prefix_ties",
			systemID: spine1,
			level:    1,
			adjacencies: map[string]tie.AdjacencyInfo{
				"ethernet-1/1": makeAdj("ethernet-1/1", leaf1, 0, "10.1.1.1"),
			},
			entries: func() map[encoding.TIEID]*tie.LSDBEntry {
				m := make(map[encoding.TIEID]*tie.LSDBEntry)
				id, e := makeNodeTIE(encoding.TieDirectionSouth, spine1, 1, map[encoding.SystemIDType]*encoding.NodeNeighborsTIEElement{
					leaf1: {Level: 0},
				})
				m[id] = e
				id, e = makeNodeTIE(encoding.TieDirectionNorth, leaf1, 0, map[encoding.SystemIDType]*encoding.NodeNeighborsTIEElement{
					spine1: {Level: 1},
				})
				m[id] = e
				// No prefix TIE for leaf1.
				return m
			}(),
			wantRoutes: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rib := ComputeSouthbound(tt.systemID, tt.level, tt.adjacencies, tt.entries, logger)
			if len(rib) != tt.wantRoutes {
				t.Errorf("route count = %d, want %d", len(rib), tt.wantRoutes)
			}
			if tt.check != nil {
				tt.check(t, rib)
			}
		})
	}
}
