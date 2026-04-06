// Package encoding implements RIFT protocol packet types and Thrift binary
// protocol serialization per RFC 9692 Section 7.
//
// All types are hand-written Go equivalents of the Thrift schema.
// Signed integers are used as-is (matching Thrift wire format) but must be
// interpreted as unsigned per the RFC. Helper functions are provided for
// the unsigned interpretation where needed.
package encoding

// Type aliases matching common.thrift typedefs.
// All signed integers match Thrift wire types; interpret as unsigned per RFC.
type (
	SystemIDType           = int64
	IPv4Address            = int32
	MTUSizeType            = int32
	SeqNrType              = int64
	LifeTimeInSecType      = int32
	LevelType              = int8
	PacketNumberType       = int16
	PodType                = int32
	UDPPortType            = int16
	TIENrType              = int32
	VersionType            = int8
	MinorVersionType       = int16
	MetricType             = int32
	RouteTagType           = int64
	LabelType              = int32
	BandwidthInMegaBitsType = int32
	KeyIDType              = int32
	LinkIDType             = int32
	PrefixLenType          = int8
	TimestampInSecsType    = int64
	NonceType              = int16
	TimeIntervalInSecType  = int16
	PrefixTransactionIDType = int8
	CounterType            = int64
	PlatformInterfaceIndex = int32
	KeyValueTargetType     = int64
	OuterSecurityKeyID     = int8
	TIESecurityKeyID       = int32
	FabricIDType           = int32
	IPv6Address            = []byte
)

// IEEE802_1ASTimeStampType is a timestamp per IEEE 802.1AS.
type IEEE802_1ASTimeStampType struct {
	ASSec  int64  // required
	ASNsec *int32 // optional
}

// HierarchyIndications indicates node configuration in case of ZTP.
type HierarchyIndications int32

const (
	HierarchyIndicationsLeafOnly                        HierarchyIndications = 0
	HierarchyIndicationsLeafOnlyAndLeaf2LeafProcedures HierarchyIndications = 1
	HierarchyIndicationsTopOfFabric                     HierarchyIndications = 2
)

// TieDirectionType is the direction of TIEs.
type TieDirectionType int32

const (
	TieDirectionIllegal           TieDirectionType = 0
	TieDirectionSouth             TieDirectionType = 1
	TieDirectionNorth             TieDirectionType = 2
	TieDirectionDirectionMaxValue TieDirectionType = 3
)

// AddressFamilyType is the address family type.
type AddressFamilyType int32

const (
	AddressFamilyIllegal  AddressFamilyType = 0
	AddressFamilyMinValue AddressFamilyType = 1
	AddressFamilyIPv4     AddressFamilyType = 2
	AddressFamilyIPv6     AddressFamilyType = 3
	AddressFamilyMaxValue AddressFamilyType = 4
)

// TIETypeType is the type of TIE.
type TIETypeType int32

const (
	TIETypeIllegal                                     TIETypeType = 0
	TIETypeMinValue                                    TIETypeType = 1
	TIETypeNodeTIEType                                 TIETypeType = 2
	TIETypePrefixTIEType                               TIETypeType = 3
	TIETypePositiveDisaggregationPrefixTIEType         TIETypeType = 4
	TIETypeNegativeDisaggregationPrefixTIEType         TIETypeType = 5
	TIETypePGPrefixTIEType                             TIETypeType = 6
	TIETypeKeyValueTIEType                             TIETypeType = 7
	TIETypeExternalPrefixTIEType                       TIETypeType = 8
	TIETypePositiveExternalDisaggregationPrefixTIEType TIETypeType = 9
	TIETypeMaxValue                                    TIETypeType = 10
)

// RouteType represents RIFT route types with ordering.
type RouteType int32

const (
	RouteTypeIllegal             RouteType = 0
	RouteTypeMinValue            RouteType = 1
	RouteTypeDiscard             RouteType = 2
	RouteTypeLocalPrefix         RouteType = 3
	RouteTypeSouthPGPPrefix      RouteType = 4
	RouteTypeNorthPGPPrefix      RouteType = 5
	RouteTypeNorthPrefix         RouteType = 6
	RouteTypeNorthExternalPrefix RouteType = 7
	RouteTypeSouthPrefix         RouteType = 8
	RouteTypeSouthExternalPrefix RouteType = 9
	RouteTypeNegativeSouthPrefix RouteType = 10
	RouteTypeMaxValue            RouteType = 11
)

// KVTypes represents key-value store types.
type KVTypes int32

const (
	KVTypesExperimental KVTypes = 1
	KVTypesWellKnown    KVTypes = 2
	KVTypesOUI          KVTypes = 3
)

// Protocol constants from common.thrift.
const (
	UndefinedPacketNumber                  PacketNumberType       = 0
	TopOfFabricLevel                       LevelType              = 24
	DefaultBandwidth                       BandwidthInMegaBitsType = 100
	LeafLevel                              LevelType              = 0
	DefaultLevel                           LevelType              = 0
	DefaultPod                             PodType                = 0
	UndefinedLinkID                        LinkIDType             = 0
	InvalidKeyValueKey                     KeyIDType              = 0
	DefaultDistance                        MetricType             = 1
	InfiniteDistance                        MetricType             = 0x7FFFFFFF
	InvalidDistance                        MetricType             = 0
	OverloadDefault                                               = false
	FloodReductionDefault                                         = true
	DefaultLIETxInterval                   TimeIntervalInSecType  = 1
	DefaultLIEHoldtime                     TimeIntervalInSecType  = 3
	MultipleNeighborsLIEHoldtimeMultiplier                        = int8(4)
	DefaultZTPHoldtime                     TimeIntervalInSecType  = 1
	DefaultNotAZTPOffer                                           = false
	DefaultYouAreFloodRepeater                                    = true
	IllegalSystemID                        SystemIDType           = 0
	DefaultLifetime                        LifeTimeInSecType      = 604800
	PurgeLifetime                          LifeTimeInSecType      = 300
	RounddownLifetimeInterval              LifeTimeInSecType      = 60
	LifetimeDiff2Ignore                    LifeTimeInSecType      = 400
	DefaultLIEUDPPort                      UDPPortType            = 914
	DefaultTIEUDPFloodPort                 UDPPortType            = 915
	DefaultMTUSize                         MTUSizeType            = 1400
	BFDDefault                                                    = true
	KeyValueTargetDefault                  KeyValueTargetType     = 0
	KeyValueTargetAllSouthLeaves           KeyValueTargetType     = -1
	UndefinedNonce                         NonceType              = 0
	UndefinedSecurityKeyID                 TIESecurityKeyID       = 0
	MaximumValidNonceDelta                                        = int16(5)
	NonceRegenerationInterval              TimeIntervalInSecType  = 300
	DefaultFabricID                        FabricIDType           = 0

	ProtocolMajorVersion VersionType      = 8
	ProtocolMinorVersion MinorVersionType = 0
)
