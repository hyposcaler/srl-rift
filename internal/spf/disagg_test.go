package spf

import (
	"log/slog"
	"os"
	"testing"

	"github.com/hyposcaler/srl-rift/internal/encoding"
	"github.com/hyposcaler/srl-rift/internal/tie"
)

func TestComputeDisaggregation(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Common system IDs.
	const (
		spine1 encoding.SystemIDType = 1
		spine2 encoding.SystemIDType = 2
		leaf1  encoding.SystemIDType = 101
		leaf2  encoding.SystemIDType = 102
		leaf3  encoding.SystemIDType = 103
	)

	// Helper to build a South Node TIE for a spine with given south neighbors.
	makeSouthNodeTIE := func(originator encoding.SystemIDType, level encoding.LevelType, southNeighbors []encoding.SystemIDType) (encoding.TIEID, *tie.LSDBEntry) {
		neighbors := make(map[encoding.SystemIDType]*encoding.NodeNeighborsTIEElement)
		for _, n := range southNeighbors {
			neighbors[n] = &encoding.NodeNeighborsTIEElement{
				Level: 0, // leaf level
				Cost:  metricPtr(1),
			}
		}
		return makeNodeTIE(encoding.TieDirectionSouth, originator, level, neighbors)
	}

	// Helper to build a South RIB with prefix -> next-hop mappings.
	makeSouthRIB := func(entries ...struct {
		prefix string
		metric encoding.MetricType
		nhIDs  []encoding.SystemIDType
	}) RIB {
		rib := make(RIB)
		for _, e := range entries {
			var nhs []NextHop
			for _, id := range e.nhIDs {
				nhs = append(nhs, NextHop{NeighborID: id})
			}
			rib[e.prefix] = &Route{
				Prefix:   e.prefix,
				Metric:   e.metric,
				NextHops: nhs,
			}
		}
		return rib
	}

	type ribEntry struct {
		prefix string
		metric encoding.MetricType
		nhIDs  []encoding.SystemIDType
	}

	tests := []struct {
		name         string
		systemID     encoding.SystemIDType
		level        encoding.LevelType
		adjNeighbors []encoding.SystemIDType   // our south neighbors (from adjacencies)
		peerTIEs     []struct {                 // reflected South Node TIEs from peers
			originator     encoding.SystemIDType
			southNeighbors []encoding.SystemIDType
		}
		southRIB      []ribEntry
		wantPrefixes  int
		wantPrefixStr []string // expected disaggregated prefix strings
	}{
		{
			name:         "no_peers",
			systemID:     spine1,
			level:        1,
			adjNeighbors: []encoding.SystemIDType{leaf1, leaf2, leaf3},
			peerTIEs:     nil,
			southRIB: []ribEntry{
				{"10.0.1.1/32", 2, []encoding.SystemIDType{leaf1}},
			},
			wantPrefixes: 0,
		},
		{
			name:         "symmetric_topology",
			systemID:     spine1,
			level:        1,
			adjNeighbors: []encoding.SystemIDType{leaf1, leaf2, leaf3},
			peerTIEs: []struct {
				originator     encoding.SystemIDType
				southNeighbors []encoding.SystemIDType
			}{
				{spine2, []encoding.SystemIDType{leaf1, leaf2, leaf3}},
			},
			southRIB: []ribEntry{
				{"10.0.1.1/32", 2, []encoding.SystemIDType{leaf1}},
				{"10.0.1.2/32", 2, []encoding.SystemIDType{leaf2}},
				{"10.0.1.3/32", 2, []encoding.SystemIDType{leaf3}},
			},
			wantPrefixes: 0,
		},
		{
			name:         "single_link_down",
			systemID:     spine1,
			level:        1,
			adjNeighbors: []encoding.SystemIDType{leaf1, leaf2, leaf3},
			peerTIEs: []struct {
				originator     encoding.SystemIDType
				southNeighbors []encoding.SystemIDType
			}{
				// spine2 lost link to leaf3
				{spine2, []encoding.SystemIDType{leaf1, leaf2}},
			},
			southRIB: []ribEntry{
				{"10.0.1.1/32", 2, []encoding.SystemIDType{leaf1}},
				{"10.0.1.2/32", 2, []encoding.SystemIDType{leaf2}},
				{"10.0.1.3/32", 2, []encoding.SystemIDType{leaf3}},
				{"10.10.3.0/24", 2, []encoding.SystemIDType{leaf3}},
			},
			wantPrefixes:  2,
			wantPrefixStr: []string{"10.0.1.3/32", "10.10.3.0/24"},
		},
		{
			name:         "no_shared_neighbor",
			systemID:     spine1,
			level:        1,
			adjNeighbors: []encoding.SystemIDType{leaf1, leaf2},
			peerTIEs: []struct {
				originator     encoding.SystemIDType
				southNeighbors []encoding.SystemIDType
			}{
				// spine2 has completely disjoint neighbors
				{spine2, []encoding.SystemIDType{leaf3}},
			},
			southRIB: []ribEntry{
				{"10.0.1.1/32", 2, []encoding.SystemIDType{leaf1}},
			},
			wantPrefixes: 0, // no shared neighbor, no disaggregation
		},
		{
			name:         "multiple_missing",
			systemID:     spine1,
			level:        1,
			adjNeighbors: []encoding.SystemIDType{leaf1, leaf2, leaf3},
			peerTIEs: []struct {
				originator     encoding.SystemIDType
				southNeighbors []encoding.SystemIDType
			}{
				// spine2 lost links to leaf2 and leaf3
				{spine2, []encoding.SystemIDType{leaf1}},
			},
			southRIB: []ribEntry{
				{"10.0.1.1/32", 2, []encoding.SystemIDType{leaf1}},
				{"10.0.1.2/32", 2, []encoding.SystemIDType{leaf2}},
				{"10.0.1.3/32", 2, []encoding.SystemIDType{leaf3}},
			},
			wantPrefixes:  2,
			wantPrefixStr: []string{"10.0.1.2/32", "10.0.1.3/32"},
		},
		{
			name:         "ecmp_route_partial_nexthop",
			systemID:     spine1,
			level:        1,
			adjNeighbors: []encoding.SystemIDType{leaf1, leaf2, leaf3},
			peerTIEs: []struct {
				originator     encoding.SystemIDType
				southNeighbors []encoding.SystemIDType
			}{
				{spine2, []encoding.SystemIDType{leaf1, leaf2}},
			},
			southRIB: []ribEntry{
				// A /24 reachable via both leaf3 (partial) and leaf1 (ok)
				{"10.10.0.0/24", 2, []encoding.SystemIDType{leaf1, leaf3}},
			},
			wantPrefixes:  1,
			wantPrefixStr: []string{"10.10.0.0/24"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Build adjacencies.
			adjacencies := make(map[string]tie.AdjacencyInfo)
			for i, n := range tt.adjNeighbors {
				iface := "ethernet-1/" + string(rune('1'+i))
				adjacencies[iface] = tie.AdjacencyInfo{
					InterfaceName: iface,
					NeighborID:    n,
					NeighborLevel: 0,
				}
			}

			// Build LSDB entries with peer South Node TIEs.
			entries := make(map[encoding.TIEID]*tie.LSDBEntry)
			for _, peer := range tt.peerTIEs {
				id, entry := makeSouthNodeTIE(peer.originator, tt.level, peer.southNeighbors)
				entries[id] = entry
			}

			// Build south RIB.
			var ribEntries []struct {
				prefix string
				metric encoding.MetricType
				nhIDs  []encoding.SystemIDType
			}
			for _, e := range tt.southRIB {
				ribEntries = append(ribEntries, struct {
					prefix string
					metric encoding.MetricType
					nhIDs  []encoding.SystemIDType
				}{e.prefix, e.metric, e.nhIDs})
			}
			southRIB := makeSouthRIB(ribEntries...)

			result := ComputeDisaggregation(tt.systemID, tt.level, adjacencies, entries, southRIB, logger)

			if len(result) != tt.wantPrefixes {
				t.Errorf("got %d disaggregated prefixes, want %d", len(result), tt.wantPrefixes)
				for _, r := range result {
					t.Logf("  prefix: %s", PrefixToString(r.Prefix))
				}
				return
			}

			if tt.wantPrefixStr != nil {
				got := make(map[string]struct{})
				for _, r := range result {
					got[PrefixToString(r.Prefix)] = struct{}{}
				}
				for _, want := range tt.wantPrefixStr {
					if _, ok := got[want]; !ok {
						t.Errorf("missing expected disaggregated prefix %s", want)
					}
				}
			}
		})
	}
}
