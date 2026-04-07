package spf

import (
	"testing"

	"github.com/hyposcaler/srl-rift/internal/encoding"
	"github.com/hyposcaler/srl-rift/internal/tie"
)

func TestPrefixToString(t *testing.T) {
	tests := []struct {
		name string
		p    encoding.IPPrefixType
		want string
	}{
		{
			name: "ipv4_slash24",
			p:    ipv4Prefix(0x0A010100, 24), // 10.1.1.0/24
			want: "10.1.1.0/24",
		},
		{
			name: "ipv4_host",
			p:    ipv4Prefix(0x0A010101, 32), // 10.1.1.1/32
			want: "10.1.1.1/32",
		},
		{
			name: "ipv4_default",
			p:    ipv4Prefix(0, 0),
			want: "0.0.0.0/0",
		},
		{
			name: "nil_ipv4",
			p:    encoding.IPPrefixType{},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PrefixToString(tt.p)
			if got != tt.want {
				t.Errorf("PrefixToString() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLinkCost(t *testing.T) {
	tests := []struct {
		name string
		n    *encoding.NodeNeighborsTIEElement
		want encoding.MetricType
	}{
		{
			name: "nil_cost_returns_default",
			n:    &encoding.NodeNeighborsTIEElement{Cost: nil},
			want: encoding.DefaultDistance,
		},
		{
			name: "explicit_cost",
			n:    &encoding.NodeNeighborsTIEElement{Cost: metricPtr(10)},
			want: 10,
		},
		{
			name: "zero_cost",
			n:    &encoding.NodeNeighborsTIEElement{Cost: metricPtr(0)},
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LinkCost(tt.n)
			if got != tt.want {
				t.Errorf("LinkCost() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestFindNodeTIE(t *testing.T) {
	// Build a snapshot with a North Node TIE for originator 1.
	id, entry := makeNodeTIE(encoding.TieDirectionNorth, 1, 0, map[encoding.SystemIDType]*encoding.NodeNeighborsTIEElement{
		100: {Level: 1},
	})
	entries := map[encoding.TIEID]*tie.LSDBEntry{id: entry}

	tests := []struct {
		name       string
		entries    map[encoding.TIEID]*tie.LSDBEntry
		dir        encoding.TieDirectionType
		originator encoding.SystemIDType
		wantNil    bool
	}{
		{
			name:       "found",
			entries:    entries,
			dir:        encoding.TieDirectionNorth,
			originator: 1,
		},
		{
			name:       "wrong_direction",
			entries:    entries,
			dir:        encoding.TieDirectionSouth,
			originator: 1,
			wantNil:    true,
		},
		{
			name:       "wrong_originator",
			entries:    entries,
			dir:        encoding.TieDirectionNorth,
			originator: 99,
			wantNil:    true,
		},
		{
			name:       "empty_entries",
			entries:    map[encoding.TIEID]*tie.LSDBEntry{},
			dir:        encoding.TieDirectionNorth,
			originator: 1,
			wantNil:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FindNodeTIE(tt.entries, tt.dir, tt.originator)
			if tt.wantNil && got != nil {
				t.Errorf("FindNodeTIE() = %v, want nil", got)
			}
			if !tt.wantNil && got == nil {
				t.Error("FindNodeTIE() = nil, want non-nil")
			}
		})
	}
}

func TestFindPrefixTIEs(t *testing.T) {
	id1, entry1 := makePrefixTIE(encoding.TieDirectionNorth, 1, []encoding.PrefixEntry{
		{Prefix: ipv4Prefix(0x0A0A0A0A, 32), Attributes: encoding.PrefixAttributes{Metric: 0}},
	})
	// Second prefix TIE with TIENr=2.
	id2 := encoding.TIEID{
		Direction:  encoding.TieDirectionNorth,
		Originator: 1,
		TIEType:    encoding.TIETypePrefixTIEType,
		TIENr:      2,
	}
	entry2 := &tie.LSDBEntry{
		Packet: &encoding.TIEPacket{
			Header: encoding.TIEHeader{TIEID: id2, SeqNr: 1},
			Element: encoding.TIEElement{
				Prefixes: &encoding.PrefixTIEElement{
					Prefixes: []encoding.PrefixEntry{
						{Prefix: ipv4Prefix(0x0A0B0B0B, 32), Attributes: encoding.PrefixAttributes{Metric: 0}},
					},
				},
			},
		},
		RemainingLifetime: 3600,
	}

	entries := map[encoding.TIEID]*tie.LSDBEntry{id1: entry1, id2: entry2}

	tests := []struct {
		name       string
		entries    map[encoding.TIEID]*tie.LSDBEntry
		dir        encoding.TieDirectionType
		originator encoding.SystemIDType
		wantCount  int
	}{
		{
			name:       "two_prefix_ties",
			entries:    entries,
			dir:        encoding.TieDirectionNorth,
			originator: 1,
			wantCount:  2,
		},
		{
			name:       "wrong_direction",
			entries:    entries,
			dir:        encoding.TieDirectionSouth,
			originator: 1,
			wantCount:  0,
		},
		{
			name:       "wrong_originator",
			entries:    entries,
			dir:        encoding.TieDirectionNorth,
			originator: 99,
			wantCount:  0,
		},
		{
			name:       "empty_entries",
			entries:    map[encoding.TIEID]*tie.LSDBEntry{},
			dir:        encoding.TieDirectionNorth,
			originator: 1,
			wantCount:  0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FindPrefixTIEs(tt.entries, tt.dir, tt.originator)
			if len(got) != tt.wantCount {
				t.Errorf("FindPrefixTIEs() returned %d, want %d", len(got), tt.wantCount)
			}
		})
	}
}

func TestMergeNextHops(t *testing.T) {
	nh1 := NextHop{NeighborID: 1}
	nh2 := NextHop{NeighborID: 2}
	nh3 := NextHop{NeighborID: 3}

	tests := []struct {
		name      string
		a, b      []NextHop
		wantCount int
	}{
		{
			name:      "no_overlap",
			a:         []NextHop{nh1},
			b:         []NextHop{nh2},
			wantCount: 2,
		},
		{
			name:      "full_overlap",
			a:         []NextHop{nh1},
			b:         []NextHop{nh1},
			wantCount: 1,
		},
		{
			name:      "partial_overlap",
			a:         []NextHop{nh1, nh2},
			b:         []NextHop{nh2, nh3},
			wantCount: 3,
		},
		{
			name:      "empty_a",
			a:         nil,
			b:         []NextHop{nh1},
			wantCount: 1,
		},
		{
			name:      "both_empty",
			a:         nil,
			b:         nil,
			wantCount: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MergeNextHops(tt.a, tt.b)
			if len(got) != tt.wantCount {
				t.Errorf("MergeNextHops() returned %d hops, want %d", len(got), tt.wantCount)
			}
		})
	}
}

func TestAddRoute(t *testing.T) {
	nh1 := []NextHop{{NeighborID: 1}}
	nh2 := []NextHop{{NeighborID: 2}}

	tests := []struct {
		name        string
		initial     RIB // pre-populated RIB state
		prefix      string
		metric      encoding.MetricType
		routeType   encoding.RouteType
		nextHops    []NextHop
		wantMetric  encoding.MetricType
		wantNHCount int
		wantType    encoding.RouteType
	}{
		{
			name:        "new_prefix",
			initial:     make(RIB),
			prefix:      "10.0.0.0/24",
			metric:      5,
			routeType:   encoding.RouteTypeNorthPrefix,
			nextHops:    nh1,
			wantMetric:  5,
			wantNHCount: 1,
			wantType:    encoding.RouteTypeNorthPrefix,
		},
		{
			name: "lower_metric_wins",
			initial: RIB{"10.0.0.0/24": &Route{
				Prefix: "10.0.0.0/24", Metric: 10, RouteType: encoding.RouteTypeNorthPrefix, NextHops: nh1,
			}},
			prefix:      "10.0.0.0/24",
			metric:      5,
			routeType:   encoding.RouteTypeNorthPrefix,
			nextHops:    nh2,
			wantMetric:  5,
			wantNHCount: 1,
			wantType:    encoding.RouteTypeNorthPrefix,
		},
		{
			name: "higher_metric_ignored",
			initial: RIB{"10.0.0.0/24": &Route{
				Prefix: "10.0.0.0/24", Metric: 5, RouteType: encoding.RouteTypeNorthPrefix, NextHops: nh1,
			}},
			prefix:      "10.0.0.0/24",
			metric:      10,
			routeType:   encoding.RouteTypeNorthPrefix,
			nextHops:    nh2,
			wantMetric:  5,
			wantNHCount: 1,
			wantType:    encoding.RouteTypeNorthPrefix,
		},
		{
			name: "equal_metric_ecmp",
			initial: RIB{"10.0.0.0/24": &Route{
				Prefix: "10.0.0.0/24", Metric: 5, RouteType: encoding.RouteTypeNorthPrefix, NextHops: nh1,
			}},
			prefix:      "10.0.0.0/24",
			metric:      5,
			routeType:   encoding.RouteTypeNorthPrefix,
			nextHops:    nh2,
			wantMetric:  5,
			wantNHCount: 2,
			wantType:    encoding.RouteTypeNorthPrefix,
		},
		{
			name: "lower_route_type_wins",
			initial: RIB{"10.0.0.0/24": &Route{
				Prefix: "10.0.0.0/24", Metric: 5, RouteType: encoding.RouteTypeSouthPrefix, NextHops: nh1,
			}},
			prefix:      "10.0.0.0/24",
			metric:      10,
			routeType:   encoding.RouteTypeNorthPrefix, // lower ordinal
			nextHops:    nh2,
			wantMetric:  10,
			wantNHCount: 1,
			wantType:    encoding.RouteTypeNorthPrefix,
		},
		{
			name: "higher_route_type_ignored",
			initial: RIB{"10.0.0.0/24": &Route{
				Prefix: "10.0.0.0/24", Metric: 5, RouteType: encoding.RouteTypeNorthPrefix, NextHops: nh1,
			}},
			prefix:      "10.0.0.0/24",
			metric:      1,
			routeType:   encoding.RouteTypeSouthPrefix, // higher ordinal
			nextHops:    nh2,
			wantMetric:  5,
			wantNHCount: 1,
			wantType:    encoding.RouteTypeNorthPrefix,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rib := tt.initial
			addRoute(rib, tt.prefix, tt.metric, tt.routeType, tt.nextHops)
			assertRoute(t, rib, tt.prefix, tt.wantMetric, tt.wantNHCount, tt.wantType)
		})
	}
}
