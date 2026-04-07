package tie

import "github.com/hyposcaler/srl-rift/internal/encoding"

// ShouldFloodTIE determines whether a TIE should be flooded on an adjacency
// to the given neighbor, per RFC 9692 Section 6.3.4 (Table 3).
//
// localSystemID is the system making the flood decision.
// localLevel is this node's level.
// neighborLevel is the adjacent node's level.
func ShouldFloodTIE(
	tieID encoding.TIEID,
	localSystemID encoding.SystemIDType,
	localLevel encoding.LevelType,
	neighborLevel encoding.LevelType,
) bool {
	selfOriginated := tieID.Originator == localSystemID
	isNodeTIE := tieID.TIEType == encoding.TIETypeNodeTIEType

	switch tieID.Direction {
	case encoding.TieDirectionNorth:
		// North TIEs flood northbound only (to higher-level neighbors).
		return neighborLevel > localLevel

	case encoding.TieDirectionSouth:
		if neighborLevel < localLevel {
			// Southbound: flood all South Node TIEs and self-originated
			// South non-Node TIEs.
			if isNodeTIE {
				return true
			}
			return selfOriginated
		}
		if neighborLevel > localLevel {
			// Northbound: reflect South Node TIEs northward.
			// Self-originated South non-Node TIEs also go north.
			if isNodeTIE {
				return true
			}
			return selfOriginated
		}
		// East-west (same level): only at non-ToF levels.
		if localLevel >= encoding.TopOfFabricLevel {
			return false
		}
		if isNodeTIE {
			return true
		}
		return selfOriginated
	}
	return false
}

// ShouldIncludeInTIDE determines whether a TIE header should be included
// in a TIDE sent to the given neighbor, per RFC 9692 Section 6.3.4 (Table 3).
//
// TIDE includes headers for all TIEs that could be exchanged (in either
// direction) on the adjacency, enabling full synchronization.
func ShouldIncludeInTIDE(
	tieID encoding.TIEID,
	localSystemID encoding.SystemIDType,
	localLevel encoding.LevelType,
	neighborLevel encoding.LevelType,
) bool {
	selfOriginated := tieID.Originator == localSystemID
	isNodeTIE := tieID.TIEType == encoding.TIETypeNodeTIEType

	if neighborLevel < localLevel {
		// Southbound TIDE to lower-level neighbor.
		switch tieID.Direction {
		case encoding.TieDirectionNorth:
			// Include non-self-originated North TIEs (these came from
			// below and the neighbor may have originated or forwarded them).
			return !selfOriginated
		case encoding.TieDirectionSouth:
			// Include self-originated South TIEs and South Node TIEs
			// from same-level peers (for reflection synchronization).
			if selfOriginated {
				return true
			}
			return isNodeTIE
		}
	}

	if neighborLevel > localLevel {
		// Northbound TIDE to higher-level neighbor.
		switch tieID.Direction {
		case encoding.TieDirectionNorth:
			// Include all North TIEs.
			return true
		case encoding.TieDirectionSouth:
			// Include all South Node TIEs (for reflection) and
			// self-originated South non-Node TIEs.
			if isNodeTIE {
				return true
			}
			return selfOriginated
		}
	}

	// East-west (same level).
	if localLevel >= encoding.TopOfFabricLevel {
		// ToF: include all North TIEs.
		return tieID.Direction == encoding.TieDirectionNorth
	}
	// Non-ToF: self-originated only.
	return selfOriginated
}
