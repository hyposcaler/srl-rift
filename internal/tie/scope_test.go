package tie

import (
	"testing"

	"github.com/hyposcaler/srl-rift/internal/encoding"
)

func TestShouldFloodTIE(t *testing.T) {
	const (
		localSysID encoding.SystemIDType = 1
		otherSysID encoding.SystemIDType = 101
		leafLevel  encoding.LevelType    = 0
		spineLevel encoding.LevelType    = 1
	)

	tests := []struct {
		name          string
		tieDir        encoding.TieDirectionType
		tieType       encoding.TIETypeType
		tieOriginator encoding.SystemIDType
		localLevel    encoding.LevelType
		neighborLevel encoding.LevelType
		want          bool
	}{
		// North TIEs: flood northbound only.
		{
			name:          "north_node_to_higher_neighbor",
			tieDir:        encoding.TieDirectionNorth,
			tieType:       encoding.TIETypeNodeTIEType,
			tieOriginator: otherSysID,
			localLevel:    leafLevel,
			neighborLevel: spineLevel,
			want:          true,
		},
		{
			name:          "north_node_to_lower_neighbor",
			tieDir:        encoding.TieDirectionNorth,
			tieType:       encoding.TIETypeNodeTIEType,
			tieOriginator: otherSysID,
			localLevel:    spineLevel,
			neighborLevel: leafLevel,
			want:          false,
		},
		{
			name:          "north_prefix_to_higher_neighbor",
			tieDir:        encoding.TieDirectionNorth,
			tieType:       encoding.TIETypePrefixTIEType,
			tieOriginator: otherSysID,
			localLevel:    leafLevel,
			neighborLevel: spineLevel,
			want:          true,
		},
		// South Node TIEs: flood southbound and reflect northbound.
		{
			name:          "south_node_to_lower_neighbor",
			tieDir:        encoding.TieDirectionSouth,
			tieType:       encoding.TIETypeNodeTIEType,
			tieOriginator: otherSysID,
			localLevel:    spineLevel,
			neighborLevel: leafLevel,
			want:          true,
		},
		{
			name:          "south_node_to_higher_neighbor_reflect",
			tieDir:        encoding.TieDirectionSouth,
			tieType:       encoding.TIETypeNodeTIEType,
			tieOriginator: otherSysID,
			localLevel:    leafLevel,
			neighborLevel: spineLevel,
			want:          true,
		},
		// South non-Node TIEs: only self-originated.
		{
			name:          "south_prefix_self_to_lower",
			tieDir:        encoding.TieDirectionSouth,
			tieType:       encoding.TIETypePrefixTIEType,
			tieOriginator: localSysID,
			localLevel:    spineLevel,
			neighborLevel: leafLevel,
			want:          true,
		},
		{
			name:          "south_prefix_other_to_lower",
			tieDir:        encoding.TieDirectionSouth,
			tieType:       encoding.TIETypePrefixTIEType,
			tieOriginator: otherSysID,
			localLevel:    spineLevel,
			neighborLevel: leafLevel,
			want:          false,
		},
		{
			name:          "south_prefix_self_to_higher",
			tieDir:        encoding.TieDirectionSouth,
			tieType:       encoding.TIETypePrefixTIEType,
			tieOriginator: localSysID,
			localLevel:    leafLevel,
			neighborLevel: spineLevel,
			want:          true,
		},
		{
			name:          "south_prefix_other_to_higher",
			tieDir:        encoding.TieDirectionSouth,
			tieType:       encoding.TIETypePrefixTIEType,
			tieOriginator: otherSysID,
			localLevel:    leafLevel,
			neighborLevel: spineLevel,
			want:          false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := encoding.TIEID{
				Direction:  tt.tieDir,
				Originator: tt.tieOriginator,
				TIEType:    tt.tieType,
				TIENr:      1,
			}
			got := ShouldFloodTIE(id, localSysID, tt.localLevel, tt.neighborLevel)
			if got != tt.want {
				t.Errorf("ShouldFloodTIE() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestShouldIncludeInTIDE(t *testing.T) {
	const (
		localSysID encoding.SystemIDType = 1
		otherSysID encoding.SystemIDType = 101
		leafLevel  encoding.LevelType    = 0
		spineLevel encoding.LevelType    = 1
	)

	tests := []struct {
		name          string
		tieDir        encoding.TieDirectionType
		tieType       encoding.TIETypeType
		tieOriginator encoding.SystemIDType
		localLevel    encoding.LevelType
		neighborLevel encoding.LevelType
		want          bool
	}{
		// Southbound TIDE (to lower-level neighbor).
		{
			name:          "southbound_tide_north_tie_not_self",
			tieDir:        encoding.TieDirectionNorth,
			tieType:       encoding.TIETypeNodeTIEType,
			tieOriginator: otherSysID,
			localLevel:    spineLevel,
			neighborLevel: leafLevel,
			want:          true, // non-self-originated North TIEs included
		},
		{
			name:          "southbound_tide_north_tie_self",
			tieDir:        encoding.TieDirectionNorth,
			tieType:       encoding.TIETypeNodeTIEType,
			tieOriginator: localSysID,
			localLevel:    spineLevel,
			neighborLevel: leafLevel,
			want:          false, // self-originated North TIEs excluded
		},
		{
			name:          "southbound_tide_south_node_self",
			tieDir:        encoding.TieDirectionSouth,
			tieType:       encoding.TIETypeNodeTIEType,
			tieOriginator: localSysID,
			localLevel:    spineLevel,
			neighborLevel: leafLevel,
			want:          true,
		},
		{
			name:          "southbound_tide_south_node_other",
			tieDir:        encoding.TieDirectionSouth,
			tieType:       encoding.TIETypeNodeTIEType,
			tieOriginator: otherSysID,
			localLevel:    spineLevel,
			neighborLevel: leafLevel,
			want:          true, // South Node TIEs from peers included
		},
		{
			name:          "southbound_tide_south_prefix_other",
			tieDir:        encoding.TieDirectionSouth,
			tieType:       encoding.TIETypePrefixTIEType,
			tieOriginator: otherSysID,
			localLevel:    spineLevel,
			neighborLevel: leafLevel,
			want:          false, // non-Node South TIEs from others excluded
		},
		// Northbound TIDE (to higher-level neighbor).
		{
			name:          "northbound_tide_north_tie",
			tieDir:        encoding.TieDirectionNorth,
			tieType:       encoding.TIETypeNodeTIEType,
			tieOriginator: otherSysID,
			localLevel:    leafLevel,
			neighborLevel: spineLevel,
			want:          true, // all North TIEs included
		},
		{
			name:          "northbound_tide_south_node",
			tieDir:        encoding.TieDirectionSouth,
			tieType:       encoding.TIETypeNodeTIEType,
			tieOriginator: otherSysID,
			localLevel:    leafLevel,
			neighborLevel: spineLevel,
			want:          true, // South Node TIEs for reflection
		},
		{
			name:          "northbound_tide_south_prefix_self",
			tieDir:        encoding.TieDirectionSouth,
			tieType:       encoding.TIETypePrefixTIEType,
			tieOriginator: localSysID,
			localLevel:    leafLevel,
			neighborLevel: spineLevel,
			want:          true,
		},
		{
			name:          "northbound_tide_south_prefix_other",
			tieDir:        encoding.TieDirectionSouth,
			tieType:       encoding.TIETypePrefixTIEType,
			tieOriginator: otherSysID,
			localLevel:    leafLevel,
			neighborLevel: spineLevel,
			want:          false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := encoding.TIEID{
				Direction:  tt.tieDir,
				Originator: tt.tieOriginator,
				TIEType:    tt.tieType,
				TIENr:      1,
			}
			got := ShouldIncludeInTIDE(id, localSysID, tt.localLevel, tt.neighborLevel)
			if got != tt.want {
				t.Errorf("ShouldIncludeInTIDE() = %v, want %v", got, tt.want)
			}
		})
	}
}
