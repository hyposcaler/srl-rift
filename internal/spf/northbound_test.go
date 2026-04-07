package spf

import (
	"log/slog"
	"os"
	"testing"

	"github.com/hyposcaler/srl-rift/internal/encoding"
	"github.com/hyposcaler/srl-rift/internal/tie"
)

func TestComputeNorthbound(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Reference topology: leaf 10 (level 0), spines 1 and 2 (level 1).
	const (
		leaf1  encoding.SystemIDType = 10
		spine1 encoding.SystemIDType = 1
		spine2 encoding.SystemIDType = 2
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
			name:     "single_spine_default_route",
			systemID: leaf1,
			level:    0,
			adjacencies: map[string]tie.AdjacencyInfo{
				"ethernet-1/1": makeAdj("ethernet-1/1", spine1, 1, "10.1.1.0"),
			},
			entries: func() map[encoding.TIEID]*tie.LSDBEntry {
				m := make(map[encoding.TIEID]*tie.LSDBEntry)
				// Spine1 South Node TIE lists leaf1.
				id, e := makeNodeTIE(encoding.TieDirectionSouth, spine1, 1, map[encoding.SystemIDType]*encoding.NodeNeighborsTIEElement{
					leaf1: {Level: 0},
				})
				m[id] = e
				// Leaf's own North Node TIE (for link cost).
				id, e = makeNodeTIE(encoding.TieDirectionNorth, leaf1, 0, map[encoding.SystemIDType]*encoding.NodeNeighborsTIEElement{
					spine1: {Level: 1},
				})
				m[id] = e
				return m
			}(),
			wantRoutes: 1,
			check: func(t *testing.T, rib RIB) {
				assertRoute(t, rib, "0.0.0.0/0", 1, 1, encoding.RouteTypeSouthPrefix)
			},
		},
		{
			name:     "two_spines_ecmp",
			systemID: leaf1,
			level:    0,
			adjacencies: map[string]tie.AdjacencyInfo{
				"ethernet-1/1": makeAdj("ethernet-1/1", spine1, 1, "10.1.1.0"),
				"ethernet-1/2": makeAdj("ethernet-1/2", spine2, 1, "10.1.2.0"),
			},
			entries: func() map[encoding.TIEID]*tie.LSDBEntry {
				m := make(map[encoding.TIEID]*tie.LSDBEntry)
				id, e := makeNodeTIE(encoding.TieDirectionSouth, spine1, 1, map[encoding.SystemIDType]*encoding.NodeNeighborsTIEElement{
					leaf1: {Level: 0},
				})
				m[id] = e
				id, e = makeNodeTIE(encoding.TieDirectionSouth, spine2, 1, map[encoding.SystemIDType]*encoding.NodeNeighborsTIEElement{
					leaf1: {Level: 0},
				})
				m[id] = e
				id, e = makeNodeTIE(encoding.TieDirectionNorth, leaf1, 0, map[encoding.SystemIDType]*encoding.NodeNeighborsTIEElement{
					spine1: {Level: 1},
					spine2: {Level: 1},
				})
				m[id] = e
				return m
			}(),
			wantRoutes: 1,
			check: func(t *testing.T, rib RIB) {
				assertRoute(t, rib, "0.0.0.0/0", 1, 2, encoding.RouteTypeSouthPrefix)
			},
		},
		{
			name:     "backlink_fails",
			systemID: leaf1,
			level:    0,
			adjacencies: map[string]tie.AdjacencyInfo{
				"ethernet-1/1": makeAdj("ethernet-1/1", spine1, 1, "10.1.1.0"),
			},
			entries: func() map[encoding.TIEID]*tie.LSDBEntry {
				m := make(map[encoding.TIEID]*tie.LSDBEntry)
				// Spine1 South Node TIE does NOT list leaf1.
				id, e := makeNodeTIE(encoding.TieDirectionSouth, spine1, 1, map[encoding.SystemIDType]*encoding.NodeNeighborsTIEElement{
					99: {Level: 0}, // some other node
				})
				m[id] = e
				return m
			}(),
			wantRoutes: 0,
		},
		{
			name:     "no_south_node_tie",
			systemID: leaf1,
			level:    0,
			adjacencies: map[string]tie.AdjacencyInfo{
				"ethernet-1/1": makeAdj("ethernet-1/1", spine1, 1, "10.1.1.0"),
			},
			entries:    map[encoding.TIEID]*tie.LSDBEntry{},
			wantRoutes: 0,
		},
		{
			name:        "no_adjacencies",
			systemID:    leaf1,
			level:       0,
			adjacencies: map[string]tie.AdjacencyInfo{},
			entries:     map[encoding.TIEID]*tie.LSDBEntry{},
			wantRoutes:  0,
		},
		{
			name:     "south_prefix_attached",
			systemID: leaf1,
			level:    0,
			adjacencies: map[string]tie.AdjacencyInfo{
				"ethernet-1/1": makeAdj("ethernet-1/1", spine1, 1, "10.1.1.0"),
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
				// Spine1 South Prefix TIE advertising 10.2.0.0/24 with metric 5.
				id, e = makePrefixTIE(encoding.TieDirectionSouth, spine1, []encoding.PrefixEntry{
					{Prefix: ipv4Prefix(0x0A020000, 24), Attributes: encoding.PrefixAttributes{Metric: 5}},
				})
				m[id] = e
				return m
			}(),
			wantRoutes: 2, // default route + 10.2.0.0/24
			check: func(t *testing.T, rib RIB) {
				assertRoute(t, rib, "0.0.0.0/0", 1, 1, encoding.RouteTypeSouthPrefix)
				assertRoute(t, rib, "10.2.0.0/24", 6, 1, encoding.RouteTypeSouthPrefix) // 5 + 1
			},
		},
		{
			name:     "unequal_cost",
			systemID: leaf1,
			level:    0,
			adjacencies: map[string]tie.AdjacencyInfo{
				"ethernet-1/1": makeAdj("ethernet-1/1", spine1, 1, "10.1.1.0"),
				"ethernet-1/2": makeAdj("ethernet-1/2", spine2, 1, "10.1.2.0"),
			},
			entries: func() map[encoding.TIEID]*tie.LSDBEntry {
				m := make(map[encoding.TIEID]*tie.LSDBEntry)
				id, e := makeNodeTIE(encoding.TieDirectionSouth, spine1, 1, map[encoding.SystemIDType]*encoding.NodeNeighborsTIEElement{
					leaf1: {Level: 0},
				})
				m[id] = e
				id, e = makeNodeTIE(encoding.TieDirectionSouth, spine2, 1, map[encoding.SystemIDType]*encoding.NodeNeighborsTIEElement{
					leaf1: {Level: 0},
				})
				m[id] = e
				// Own North Node TIE with different costs.
				id, e = makeNodeTIE(encoding.TieDirectionNorth, leaf1, 0, map[encoding.SystemIDType]*encoding.NodeNeighborsTIEElement{
					spine1: {Level: 1, Cost: metricPtr(1)},
					spine2: {Level: 1, Cost: metricPtr(10)},
				})
				m[id] = e
				return m
			}(),
			wantRoutes: 1,
			check: func(t *testing.T, rib RIB) {
				assertRoute(t, rib, "0.0.0.0/0", 1, 1, encoding.RouteTypeSouthPrefix)
			},
		},
		{
			name:     "same_level_skipped",
			systemID: leaf1,
			level:    0,
			adjacencies: map[string]tie.AdjacencyInfo{
				"ethernet-1/1": makeAdj("ethernet-1/1", 11, 0, "10.1.1.2"), // another leaf
			},
			entries:    map[encoding.TIEID]*tie.LSDBEntry{},
			wantRoutes: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rib := ComputeNorthbound(tt.systemID, tt.level, tt.adjacencies, tt.entries, logger)
			if len(rib) != tt.wantRoutes {
				t.Errorf("route count = %d, want %d", len(rib), tt.wantRoutes)
			}
			if tt.check != nil {
				tt.check(t, rib)
			}
		})
	}
}
