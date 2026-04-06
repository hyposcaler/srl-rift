package encoding

// IPv4PrefixType represents an IPv4 prefix.
type IPv4PrefixType struct {
	Address   IPv4Address
	PrefixLen PrefixLenType
}

// IPv6PrefixType represents an IPv6 prefix.
type IPv6PrefixType struct {
	Address   IPv6Address
	PrefixLen PrefixLenType
}

// IPAddressType is a union: exactly one field is set.
type IPAddressType struct {
	IPv4Address *IPv4Address
	IPv6Address IPv6Address // nil means not set
}

// IPPrefixType is a union: exactly one field is set.
type IPPrefixType struct {
	IPv4Prefix *IPv4PrefixType
	IPv6Prefix *IPv6PrefixType
}

// PrefixSequenceType is a sequence number for prefix mobility.
type PrefixSequenceType struct {
	Timestamp     IEEE802_1ASTimeStampType
	TransactionID *PrefixTransactionIDType // optional
}

// PacketHeader is the common RIFT packet header.
type PacketHeader struct {
	MajorVersion VersionType
	MinorVersion MinorVersionType
	Sender       SystemIDType
	Level        *LevelType // optional
}

// Community is a prefix community.
type Community struct {
	Top    int32
	Bottom int32
}

// Neighbor identifies a neighbor.
type Neighbor struct {
	Originator SystemIDType
	RemoteID   LinkIDType
}

// NodeCapabilities describes capabilities a node supports.
type NodeCapabilities struct {
	ProtocolMinorVersion MinorVersionType
	FloodReduction       *bool                 // optional, default true
	HierarchyIndications *HierarchyIndications // optional
}

// LinkCapabilities describes capabilities of a link.
type LinkCapabilities struct {
	BFD                   *bool // optional, default true
	IPv4ForwardingCapable *bool // optional, default true
}

// LIEPacket is a RIFT Link Information Element packet.
type LIEPacket struct {
	Name                     string           // optional (empty = not set)
	LocalID                  LinkIDType       // required
	FloodPort                UDPPortType      // required, default 915
	LinkMTUSize              *MTUSizeType     // optional, default 1400
	LinkBandwidth            *BandwidthInMegaBitsType // optional, default 100
	Neighbor                 *Neighbor        // optional
	Pod                      *PodType         // optional, default 0
	NodeCapabilities         NodeCapabilities // required
	LinkCapabilities         *LinkCapabilities // optional
	Holdtime                 TimeIntervalInSecType // required, default 3
	Label                    *LabelType       // optional
	NotAZTPOffer             *bool            // optional, default false
	YouAreFloodRepeater      *bool            // optional, default true
	YouAreSendingTooQuickly  *bool            // optional, default false
	InstanceName             string           // optional
	FabricID                 *FabricIDType    // optional, default 0
}

// LinkIDPair describes one of parallel links between two nodes.
type LinkIDPair struct {
	LocalID                  LinkIDType
	RemoteID                 LinkIDType
	PlatformInterfaceIndex   *PlatformInterfaceIndex // optional
	PlatformInterfaceName    string                  // optional
	TrustedOuterSecurityKey  *OuterSecurityKeyID     // optional
	BFDUp                    *bool                   // optional
	AddressFamilies          map[AddressFamilyType]struct{} // optional set
}

// TIEID uniquely identifies a TIE.
type TIEID struct {
	Direction  TieDirectionType
	Originator SystemIDType
	TIEType    TIETypeType
	TIENr      TIENrType
}

// TIEHeader is the header of a TIE.
type TIEHeader struct {
	TIEID              TIEID
	SeqNr              SeqNrType
	OriginationTime    *IEEE802_1ASTimeStampType // optional
	OriginationLifetime *LifeTimeInSecType       // optional
}

// TIEHeaderWithLifeTime wraps a TIEHeader with remaining lifetime.
type TIEHeaderWithLifeTime struct {
	Header            TIEHeader
	RemainingLifetime LifeTimeInSecType
}

// TIDEPacket is a Topology Information Distribution Element.
type TIDEPacket struct {
	StartRange TIEID
	EndRange   TIEID
	Headers    []TIEHeaderWithLifeTime
}

// TIREPacket is a Topology Information Request Element.
type TIREPacket struct {
	Headers []TIEHeaderWithLifeTime
}

// NodeNeighborsTIEElement describes a neighbor in a Node TIE.
type NodeNeighborsTIEElement struct {
	Level     LevelType
	Cost      *MetricType             // optional, default 1
	LinkIDs   []LinkIDPair            // optional set
	Bandwidth *BandwidthInMegaBitsType // optional, default 100
}

// NodeFlags contains indication flags of a node.
type NodeFlags struct {
	Overload *bool // optional, default false
}

// NodeTIEElement describes a node in a TIE.
type NodeTIEElement struct {
	Level         LevelType
	Neighbors     map[SystemIDType]*NodeNeighborsTIEElement
	Capabilities  NodeCapabilities
	Flags         *NodeFlags                  // optional
	Name          string                      // optional
	Pod           *PodType                    // optional
	StartupTime   *TimestampInSecsType        // optional
	MiscabledLinks map[LinkIDType]struct{}    // optional set
	SamePlaneTofs  map[SystemIDType]struct{}  // optional set
	FabricID       *FabricIDType             // optional, default 0
}

// PrefixAttributes holds attributes of a prefix.
type PrefixAttributes struct {
	Metric           MetricType            // required, default 1
	Tags             map[RouteTagType]struct{} // optional set
	MonotonicClock   *PrefixSequenceType   // optional
	Loopback         *bool                 // optional, default false
	DirectlyAttached *bool                 // optional, default true
	FromLink         *LinkIDType           // optional
	Label            *LabelType            // optional
}

// PrefixTIEElement carries prefixes.
// Uses a slice of key-value pairs instead of map since IPPrefixType
// is not comparable (contains a slice in IPv6PrefixType).
type PrefixTIEElement struct {
	Prefixes []PrefixEntry
}

// PrefixEntry is a single prefix with its attributes.
type PrefixEntry struct {
	Prefix     IPPrefixType
	Attributes PrefixAttributes
}

// KeyValueTIEElementContent defines targeted nodes and values.
type KeyValueTIEElementContent struct {
	Targets *KeyValueTargetType // optional, default 0
	Value   []byte              // optional
}

// KeyValueTIEElement contains generic key-value pairs.
type KeyValueTIEElement struct {
	KeyValues map[KeyIDType]*KeyValueTIEElementContent
}

// TIEElement is a union: exactly one field is set.
type TIEElement struct {
	Node                                      *NodeTIEElement
	Prefixes                                  *PrefixTIEElement
	PositiveDisaggregationPrefixes            *PrefixTIEElement
	NegativeDisaggregationPrefixes            *PrefixTIEElement
	ExternalPrefixes                          *PrefixTIEElement
	PositiveExternalDisaggregationPrefixes    *PrefixTIEElement
	KeyValues                                 *KeyValueTIEElement
}

// TIEPacket is a Topology Information Element packet.
type TIEPacket struct {
	Header  TIEHeader
	Element TIEElement
}

// PacketContent is a union: exactly one field is set.
type PacketContent struct {
	LIE  *LIEPacket
	TIDE *TIDEPacket
	TIRE *TIREPacket
	TIE  *TIEPacket
}

// ProtocolPacket is the top-level RIFT packet structure.
type ProtocolPacket struct {
	Header  PacketHeader
	Content PacketContent
}
