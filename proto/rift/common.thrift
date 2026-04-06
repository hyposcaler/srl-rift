/**
    Thrift file with common definitions for RIFT
    Extracted from RFC 9692 Section 7.2
*/

namespace py common

/** @note MUST be interpreted in implementation as unsigned 64 bits. */
typedef i64      SystemIDType
typedef i32      IPv4Address
typedef i32      MTUSizeType
/** @note MUST be interpreted in implementation as unsigned rolling over number */
typedef i64      SeqNrType
/** @note MUST be interpreted in implementation as unsigned */
typedef i32      LifeTimeInSecType
/** @note MUST be interpreted in implementation as unsigned */
typedef i8       LevelType
typedef i16      PacketNumberType
/** @note MUST be interpreted in implementation as unsigned */
typedef i32      PodType
/** this has to be long enough to accommodate prefix */
typedef binary   IPv6Address
/** @note MUST be interpreted in implementation as unsigned */
typedef i16      UDPPortType
/** @note MUST be interpreted in implementation as unsigned */
typedef i32      TIENrType
/** @note MUST be interpreted in implementation as unsigned.
          This is carried in the security envelope and must
          hence fit into 8 bits. */
typedef i8       VersionType
/** @note MUST be interpreted in implementation as unsigned */
typedef i16      MinorVersionType
/** @note MUST be interpreted in implementation as unsigned */
typedef i32      MetricType
/** @note MUST be interpreted in implementation as unsigned and unstructured */
typedef i64      RouteTagType
/** @note MUST be interpreted in implementation as unstructured label value */
typedef i32      LabelType
/** @note MUST be interpreted in implementation as unsigned */
typedef i32      BandwidthInMegaBitsType
/** @note Key Value key ID type */
typedef i32      KeyIDType
/** node local, unique identification for a link */
typedef i32      LinkIDType
/** @note MUST be interpreted in implementation as unsigned */
typedef i8       PrefixLenType
/** timestamp in seconds since the epoch */
typedef i64      TimestampInSecsType
/** security nonce. @note MUST be interpreted as rolling over unsigned value */
typedef i16      NonceType
/** LIE FSM holdtime type */
typedef i16      TimeIntervalInSecType
/** Transaction ID type for prefix mobility as specified by RFC 6550 */
typedef i8       PrefixTransactionIDType
/** generic counter type */
typedef i64      CounterType
/** Platform Interface Index type */
typedef i32      PlatformInterfaceIndex
/** type used to target nodes with key value */
typedef i64      KeyValueTargetType
/** outer security key ID */
typedef i8       OuterSecurityKeyID
/** security key ID */
typedef i32      TIESecurityKeyID
/** Fabric ID type (not explicitly defined in RFC text, but referenced) */
typedef i32      FabricIDType

/** Timestamp per IEEE 802.1AS */
struct IEEE802_1ASTimeStampType {
    1: required     i64     AS_sec;
    2: optional     i32     AS_nsec;
}

/** Flags indicating node configuration in case of ZTP. */
enum HierarchyIndications {
    leaf_only                            = 0,
    leaf_only_and_leaf_2_leaf_procedures = 1,
    top_of_fabric                        = 2,
}

const PacketNumberType  undefined_packet_number    = 0
const LevelType   top_of_fabric_level              = 24
const BandwidthInMegaBitsType  default_bandwidth   = 100
const LevelType   leaf_level                       = 0
const LevelType   default_level                    = 0
const PodType     default_pod                      = 0
const LinkIDType  undefined_linkid                 = 0
const KeyIDType   invalid_key_value_key            = 0
const MetricType  default_distance                 = 1
const MetricType  infinite_distance                = 0x7FFFFFFF
const MetricType  invalid_distance                 = 0
const bool        overload_default                 = false
const bool        flood_reduction_default          = true
const TimeIntervalInSecType   default_lie_tx_interval  = 1
const TimeIntervalInSecType   default_lie_holdtime     = 3
const i8 multiple_neighbors_lie_holdtime_multiplier    = 4
const TimeIntervalInSecType   default_ztp_holdtime     = 1
const bool default_not_a_ztp_offer                     = false
const bool default_you_are_flood_repeater              = true
const SystemIDType IllegalSystemID                     = 0
const set<SystemIDType> empty_set_of_nodeids           = {}
const LifeTimeInSecType default_lifetime               = 604800
const LifeTimeInSecType purge_lifetime                 = 300
const LifeTimeInSecType rounddown_lifetime_interval    = 60
const LifeTimeInSecType lifetime_diff2ignore           = 400
const UDPPortType     default_lie_udp_port             = 914
const UDPPortType     default_tie_udp_flood_port       = 915
const MTUSizeType     default_mtu_size                 = 1400
const bool            bfd_default                      = true
const KeyValueTargetType    keyvaluetarget_default     = 0
const KeyValueTargetType    keyvaluetarget_all_south_leaves = -1
const NonceType       undefined_nonce                  = 0
const TIESecurityKeyID undefined_securitykey_id        = 0
const i16             maximum_valid_nonce_delta         = 5
const TimeIntervalInSecType   nonce_regeneration_interval = 300
const FabricIDType    default_fabric_id                = 0

/** Direction of TIEs. */
enum TieDirectionType {
    Illegal           = 0,
    South             = 1,
    North             = 2,
    DirectionMaxValue = 3,
}

/** Address family type. */
enum AddressFamilyType {
    Illegal                = 0,
    AddressFamilyMinValue  = 1,
    IPv4                   = 2,
    IPv6                   = 3,
    AddressFamilyMaxValue  = 4,
}

/** IPv4 prefix type. */
struct IPv4PrefixType {
    1: required IPv4Address    address;
    2: required PrefixLenType  prefixlen;
}

/** IPv6 prefix type. */
struct IPv6PrefixType {
    1: required IPv6Address    address;
    2: required PrefixLenType  prefixlen;
}

/** IP address type. */
union IPAddressType {
    1: optional IPv4Address   ipv4address;
    2: optional IPv6Address   ipv6address;
}

/** Prefix advertisement. */
union IPPrefixType {
    1: optional IPv4PrefixType   ipv4prefix;
    2: optional IPv6PrefixType   ipv6prefix;
}

/** Sequence of a prefix in case of move. */
struct PrefixSequenceType {
    1: required IEEE802_1ASTimeStampType  timestamp;
    2: optional PrefixTransactionIDType   transactionid;
}

/** Type of TIE. */
enum TIETypeType {
    Illegal                                     = 0,
    TIETypeMinValue                             = 1,
    NodeTIEType                                 = 2,
    PrefixTIEType                               = 3,
    PositiveDisaggregationPrefixTIEType         = 4,
    NegativeDisaggregationPrefixTIEType         = 5,
    PGPrefixTIEType                             = 6,
    KeyValueTIEType                             = 7,
    ExternalPrefixTIEType                       = 8,
    PositiveExternalDisaggregationPrefixTIEType = 9,
    TIETypeMaxValue                             = 10,
}

/** RIFT route types. */
enum RouteType {
    Illegal               =  0,
    RouteTypeMinValue     =  1,
    Discard               =  2,
    LocalPrefix           =  3,
    SouthPGPPrefix        =  4,
    NorthPGPPrefix        =  5,
    NorthPrefix           =  6,
    NorthExternalPrefix   =  7,
    SouthPrefix           =  8,
    SouthExternalPrefix   =  9,
    NegativeSouthPrefix   = 10,
    RouteTypeMaxValue     = 11,
}

enum KVTypes {
    Experimental = 1,
    WellKnown    = 2,
    OUI          = 3,
}
