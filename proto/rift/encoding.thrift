/**
    Thrift file for packet encodings for RIFT
    Extracted from RFC 9692 Section 7.3
*/

include "common.thrift"

namespace py encoding

const common.VersionType protocol_major_version = 8
const common.MinorVersionType protocol_minor_version = 0

/** Common RIFT packet header. */
struct PacketHeader {
    1: required common.VersionType      major_version = protocol_major_version;
    2: required common.MinorVersionType minor_version = protocol_minor_version;
    3: required common.SystemIDType     sender;
    4: optional common.LevelType        level;
}

/** Prefix community. */
struct Community {
    1: required i32          top;
    2: required i32          bottom;
}

/** Neighbor structure. */
struct Neighbor {
    1: required common.SystemIDType        originator;
    2: required common.LinkIDType          remote_id;
}

/** Capabilities the node supports. */
struct NodeCapabilities {
    1: required common.MinorVersionType     protocol_minor_version = protocol_minor_version;
    2: optional bool                        flood_reduction = common.flood_reduction_default;
    3: optional common.HierarchyIndications hierarchy_indications;
}

/** Link capabilities. */
struct LinkCapabilities {
    1: optional bool                        bfd = common.bfd_default;
    2: optional bool                        ipv4_forwarding_capable = true;
}

/** RIFT LIE Packet. */
struct LIEPacket {
    1: optional string                       name;
    2: required common.LinkIDType            local_id;
    3: required common.UDPPortType           flood_port = common.default_tie_udp_flood_port;
    4: optional common.MTUSizeType           link_mtu_size = common.default_mtu_size;
    5: optional common.BandwidthInMegaBitsType link_bandwidth = common.default_bandwidth;
    6: optional Neighbor                     neighbor;
    7: optional common.PodType              pod = common.default_pod;
   10: required NodeCapabilities             node_capabilities;
   11: optional LinkCapabilities             link_capabilities;
   12: required common.TimeIntervalInSecType holdtime = common.default_lie_holdtime;
   13: optional common.LabelType             label;
   21: optional bool                         not_a_ztp_offer = common.default_not_a_ztp_offer;
   22: optional bool                         you_are_flood_repeater = common.default_you_are_flood_repeater;
   23: optional bool                         you_are_sending_too_quickly = false;
   24: optional string                       instance_name;
   35: optional common.FabricIDType          fabric_id = common.default_fabric_id;
}

/** LinkID pair describes one of parallel links between two nodes. */
struct LinkIDPair {
    1: required common.LinkIDType            local_id;
    2: required common.LinkIDType            remote_id;
   10: optional common.PlatformInterfaceIndex platform_interface_index;
   11: optional string                       platform_interface_name;
   12: optional common.OuterSecurityKeyID    trusted_outer_security_key;
   13: optional bool                         bfd_up;
   14: optional set<common.AddressFamilyType> address_families;
}

/** Unique ID of a TIE. */
struct TIEID {
    1: required common.TieDirectionType    direction;
    2: required common.SystemIDType        originator;
    3: required common.TIETypeType         tietype;
    4: required common.TIENrType           tie_nr;
}

/** Header of a TIE. */
struct TIEHeader {
    2: required TIEID                             tieid;
    3: required common.SeqNrType                  seq_nr;
   10: optional common.IEEE802_1ASTimeStampType   origination_time;
   12: optional common.LifeTimeInSecType          origination_lifetime;
}

/** Header of a TIE as described in TIRE/TIDE. */
struct TIEHeaderWithLifeTime {
    1: required     TIEHeader                     header;
    2: required     common.LifeTimeInSecType      remaining_lifetime;
}

/** TIDE with sorted TIE headers. */
struct TIDEPacket {
    1: required TIEID                       start_range;
    2: required TIEID                       end_range;
    3: required list<TIEHeaderWithLifeTime> headers;
}

/** TIRE packet */
struct TIREPacket {
    1: required set<TIEHeaderWithLifeTime>  headers;
}

/** Neighbor of a node */
struct NodeNeighborsTIEElement {
    1: required common.LevelType                level;
    3: optional common.MetricType               cost = common.default_distance;
    4: optional set<LinkIDPair>                 link_ids;
    5: optional common.BandwidthInMegaBitsType  bandwidth = common.default_bandwidth;
}

/** Indication flags of the node. */
struct NodeFlags {
    1: optional bool         overload = common.overload_default;
}

/** Description of a node. */
struct NodeTIEElement {
    1: required common.LevelType            level;
    2: required map<common.SystemIDType, NodeNeighborsTIEElement> neighbors;
    3: required NodeCapabilities            capabilities;
    4: optional NodeFlags                   flags;
    5: optional string                      name;
    6: optional common.PodType              pod;
    7: optional common.TimestampInSecsType  startup_time;
   10: optional set<common.LinkIDType>      miscabled_links;
   12: optional set<common.SystemIDType>    same_plane_tofs;
   20: optional common.FabricIDType         fabric_id = common.default_fabric_id;
}

/** Attributes of a prefix. */
struct PrefixAttributes {
    2: required common.MetricType            metric = common.default_distance;
    3: optional set<common.RouteTagType>     tags;
    4: optional common.PrefixSequenceType    monotonic_clock;
    6: optional bool                         loopback = false;
    7: optional bool                         directly_attached = true;
   10: optional common.LinkIDType            from_link;
   12: optional common.LabelType             label;
}

/** TIE carrying prefixes */
struct PrefixTIEElement {
    1: required map<common.IPPrefixType, PrefixAttributes> prefixes;
}

/** Defines the targeted nodes and the value carried. */
struct KeyValueTIEElementContent {
    1: optional common.KeyValueTargetType        targets = common.keyvaluetarget_default;
    2: optional binary                           value;
}

/** Generic key value pairs. */
struct KeyValueTIEElement {
    1: required map<common.KeyIDType, KeyValueTIEElementContent> keyvalues;
}

/** Single element in a TIE. */
union TIEElement {
    1: optional NodeTIEElement     node;
    2: optional PrefixTIEElement          prefixes;
    3: optional PrefixTIEElement   positive_disaggregation_prefixes;
    5: optional PrefixTIEElement   negative_disaggregation_prefixes;
    6: optional PrefixTIEElement          external_prefixes;
    7: optional PrefixTIEElement   positive_external_disaggregation_prefixes;
    9: optional KeyValueTIEElement keyvalues;
}

/** TIE packet */
struct TIEPacket {
    1: required TIEHeader  header;
    2: required TIEElement element;
}

/** Content of a RIFT packet. */
union PacketContent {
    1: optional LIEPacket     lie;
    2: optional TIDEPacket    tide;
    3: optional TIREPacket    tire;
    4: optional TIEPacket     tie;
}

/** RIFT packet structure. */
struct ProtocolPacket {
    1: required PacketHeader  header;
    2: required PacketContent content;
}
