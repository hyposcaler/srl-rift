package encoding

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"sort"
)

// Thrift binary protocol type IDs.
const (
	thriftTypeStop   = 0
	thriftTypeBool   = 2
	thriftTypeByte   = 3
	thriftTypeI16    = 6
	thriftTypeI32    = 8
	thriftTypeI64    = 10
	thriftTypeString = 11
	thriftTypeStruct = 12
	thriftTypeMap    = 13
	thriftTypeSet    = 14
	thriftTypeList   = 15
)

// Encoder writes Thrift binary protocol.
type Encoder struct {
	w   io.Writer
	buf [8]byte
}

// NewEncoder creates a new Thrift binary protocol encoder.
func NewEncoder(w io.Writer) *Encoder {
	return &Encoder{w: w}
}

func (e *Encoder) writeByte(v byte) error {
	e.buf[0] = v
	_, err := e.w.Write(e.buf[:1])
	return err
}

func (e *Encoder) writeI16(v int16) error {
	binary.BigEndian.PutUint16(e.buf[:2], uint16(v))
	_, err := e.w.Write(e.buf[:2])
	return err
}

func (e *Encoder) writeI32(v int32) error {
	binary.BigEndian.PutUint32(e.buf[:4], uint32(v))
	_, err := e.w.Write(e.buf[:4])
	return err
}

func (e *Encoder) writeI64(v int64) error {
	binary.BigEndian.PutUint64(e.buf[:8], uint64(v))
	_, err := e.w.Write(e.buf[:8])
	return err
}

func (e *Encoder) writeBool(v bool) error {
	if v {
		return e.writeByte(1)
	}
	return e.writeByte(0)
}

func (e *Encoder) writeString(v string) error {
	if err := e.writeI32(int32(len(v))); err != nil {
		return err
	}
	_, err := e.w.Write([]byte(v))
	return err
}

func (e *Encoder) writeBinary(v []byte) error {
	if err := e.writeI32(int32(len(v))); err != nil {
		return err
	}
	_, err := e.w.Write(v)
	return err
}

func (e *Encoder) writeFieldHeader(fieldType byte, fieldID int16) error {
	if err := e.writeByte(fieldType); err != nil {
		return err
	}
	return e.writeI16(fieldID)
}

func (e *Encoder) writeFieldStop() error {
	return e.writeByte(thriftTypeStop)
}

// EncodeProtocolPacket serializes a ProtocolPacket to Thrift binary format.
func (e *Encoder) EncodeProtocolPacket(p *ProtocolPacket) error {
	// field 1: header (struct)
	if err := e.writeFieldHeader(thriftTypeStruct, 1); err != nil {
		return err
	}
	if err := e.encodePacketHeader(&p.Header); err != nil {
		return err
	}
	// field 2: content (struct/union)
	if err := e.writeFieldHeader(thriftTypeStruct, 2); err != nil {
		return err
	}
	if err := e.encodePacketContent(&p.Content); err != nil {
		return err
	}
	return e.writeFieldStop()
}

func (e *Encoder) encodePacketHeader(h *PacketHeader) error {
	// field 1: major_version (i8)
	if err := e.writeFieldHeader(thriftTypeByte, 1); err != nil {
		return err
	}
	if err := e.writeByte(byte(h.MajorVersion)); err != nil {
		return err
	}
	// field 2: minor_version (i16)
	if err := e.writeFieldHeader(thriftTypeI16, 2); err != nil {
		return err
	}
	if err := e.writeI16(int16(h.MinorVersion)); err != nil {
		return err
	}
	// field 3: sender (i64)
	if err := e.writeFieldHeader(thriftTypeI64, 3); err != nil {
		return err
	}
	if err := e.writeI64(h.Sender); err != nil {
		return err
	}
	// field 4: level (i8, optional)
	if h.Level != nil {
		if err := e.writeFieldHeader(thriftTypeByte, 4); err != nil {
			return err
		}
		if err := e.writeByte(byte(*h.Level)); err != nil {
			return err
		}
	}
	return e.writeFieldStop()
}

func (e *Encoder) encodePacketContent(c *PacketContent) error {
	switch {
	case c.LIE != nil:
		if err := e.writeFieldHeader(thriftTypeStruct, 1); err != nil {
			return err
		}
		if err := e.encodeLIEPacket(c.LIE); err != nil {
			return err
		}
	case c.TIDE != nil:
		if err := e.writeFieldHeader(thriftTypeStruct, 2); err != nil {
			return err
		}
		if err := e.encodeTIDEPacket(c.TIDE); err != nil {
			return err
		}
	case c.TIRE != nil:
		if err := e.writeFieldHeader(thriftTypeStruct, 3); err != nil {
			return err
		}
		if err := e.encodeTIREPacket(c.TIRE); err != nil {
			return err
		}
	case c.TIE != nil:
		if err := e.writeFieldHeader(thriftTypeStruct, 4); err != nil {
			return err
		}
		if err := e.encodeTIEPacket(c.TIE); err != nil {
			return err
		}
	default:
		return fmt.Errorf("PacketContent: no variant set")
	}
	return e.writeFieldStop()
}

func (e *Encoder) encodeLIEPacket(p *LIEPacket) error {
	// field 1: name (optional string)
	if p.Name != "" {
		if err := e.writeFieldHeader(thriftTypeString, 1); err != nil {
			return err
		}
		if err := e.writeString(p.Name); err != nil {
			return err
		}
	}
	// field 2: local_id (i32)
	if err := e.writeFieldHeader(thriftTypeI32, 2); err != nil {
		return err
	}
	if err := e.writeI32(p.LocalID); err != nil {
		return err
	}
	// field 3: flood_port (i16)
	if err := e.writeFieldHeader(thriftTypeI16, 3); err != nil {
		return err
	}
	if err := e.writeI16(int16(p.FloodPort)); err != nil {
		return err
	}
	// field 4: link_mtu_size (optional i32)
	if p.LinkMTUSize != nil {
		if err := e.writeFieldHeader(thriftTypeI32, 4); err != nil {
			return err
		}
		if err := e.writeI32(*p.LinkMTUSize); err != nil {
			return err
		}
	}
	// field 5: link_bandwidth (optional i32)
	if p.LinkBandwidth != nil {
		if err := e.writeFieldHeader(thriftTypeI32, 5); err != nil {
			return err
		}
		if err := e.writeI32(*p.LinkBandwidth); err != nil {
			return err
		}
	}
	// field 6: neighbor (optional struct)
	if p.Neighbor != nil {
		if err := e.writeFieldHeader(thriftTypeStruct, 6); err != nil {
			return err
		}
		if err := e.encodeNeighbor(p.Neighbor); err != nil {
			return err
		}
	}
	// field 7: pod (optional i32)
	if p.Pod != nil {
		if err := e.writeFieldHeader(thriftTypeI32, 7); err != nil {
			return err
		}
		if err := e.writeI32(*p.Pod); err != nil {
			return err
		}
	}
	// field 10: node_capabilities (struct)
	if err := e.writeFieldHeader(thriftTypeStruct, 10); err != nil {
		return err
	}
	if err := e.encodeNodeCapabilities(&p.NodeCapabilities); err != nil {
		return err
	}
	// field 11: link_capabilities (optional struct)
	if p.LinkCapabilities != nil {
		if err := e.writeFieldHeader(thriftTypeStruct, 11); err != nil {
			return err
		}
		if err := e.encodeLinkCapabilities(p.LinkCapabilities); err != nil {
			return err
		}
	}
	// field 12: holdtime (i16)
	if err := e.writeFieldHeader(thriftTypeI16, 12); err != nil {
		return err
	}
	if err := e.writeI16(int16(p.Holdtime)); err != nil {
		return err
	}
	// field 13: label (optional i32)
	if p.Label != nil {
		if err := e.writeFieldHeader(thriftTypeI32, 13); err != nil {
			return err
		}
		if err := e.writeI32(*p.Label); err != nil {
			return err
		}
	}
	// field 21: not_a_ztp_offer (optional bool)
	if p.NotAZTPOffer != nil {
		if err := e.writeFieldHeader(thriftTypeBool, 21); err != nil {
			return err
		}
		if err := e.writeBool(*p.NotAZTPOffer); err != nil {
			return err
		}
	}
	// field 22: you_are_flood_repeater (optional bool)
	if p.YouAreFloodRepeater != nil {
		if err := e.writeFieldHeader(thriftTypeBool, 22); err != nil {
			return err
		}
		if err := e.writeBool(*p.YouAreFloodRepeater); err != nil {
			return err
		}
	}
	// field 23: you_are_sending_too_quickly (optional bool)
	if p.YouAreSendingTooQuickly != nil {
		if err := e.writeFieldHeader(thriftTypeBool, 23); err != nil {
			return err
		}
		if err := e.writeBool(*p.YouAreSendingTooQuickly); err != nil {
			return err
		}
	}
	// field 24: instance_name (optional string)
	if p.InstanceName != "" {
		if err := e.writeFieldHeader(thriftTypeString, 24); err != nil {
			return err
		}
		if err := e.writeString(p.InstanceName); err != nil {
			return err
		}
	}
	// field 35: fabric_id (optional i32)
	if p.FabricID != nil {
		if err := e.writeFieldHeader(thriftTypeI32, 35); err != nil {
			return err
		}
		if err := e.writeI32(*p.FabricID); err != nil {
			return err
		}
	}
	return e.writeFieldStop()
}

func (e *Encoder) encodeNeighbor(n *Neighbor) error {
	if err := e.writeFieldHeader(thriftTypeI64, 1); err != nil {
		return err
	}
	if err := e.writeI64(n.Originator); err != nil {
		return err
	}
	if err := e.writeFieldHeader(thriftTypeI32, 2); err != nil {
		return err
	}
	if err := e.writeI32(n.RemoteID); err != nil {
		return err
	}
	return e.writeFieldStop()
}

func (e *Encoder) encodeNodeCapabilities(c *NodeCapabilities) error {
	if err := e.writeFieldHeader(thriftTypeI16, 1); err != nil {
		return err
	}
	if err := e.writeI16(int16(c.ProtocolMinorVersion)); err != nil {
		return err
	}
	if c.FloodReduction != nil {
		if err := e.writeFieldHeader(thriftTypeBool, 2); err != nil {
			return err
		}
		if err := e.writeBool(*c.FloodReduction); err != nil {
			return err
		}
	}
	if c.HierarchyIndications != nil {
		if err := e.writeFieldHeader(thriftTypeI32, 3); err != nil {
			return err
		}
		if err := e.writeI32(int32(*c.HierarchyIndications)); err != nil {
			return err
		}
	}
	return e.writeFieldStop()
}

func (e *Encoder) encodeLinkCapabilities(c *LinkCapabilities) error {
	if c.BFD != nil {
		if err := e.writeFieldHeader(thriftTypeBool, 1); err != nil {
			return err
		}
		if err := e.writeBool(*c.BFD); err != nil {
			return err
		}
	}
	if c.IPv4ForwardingCapable != nil {
		if err := e.writeFieldHeader(thriftTypeBool, 2); err != nil {
			return err
		}
		if err := e.writeBool(*c.IPv4ForwardingCapable); err != nil {
			return err
		}
	}
	return e.writeFieldStop()
}

func (e *Encoder) encodeIEEE802_1ASTimestamp(t *IEEE802_1ASTimeStampType) error {
	if err := e.writeFieldHeader(thriftTypeI64, 1); err != nil {
		return err
	}
	if err := e.writeI64(t.ASSec); err != nil {
		return err
	}
	if t.ASNsec != nil {
		if err := e.writeFieldHeader(thriftTypeI32, 2); err != nil {
			return err
		}
		if err := e.writeI32(*t.ASNsec); err != nil {
			return err
		}
	}
	return e.writeFieldStop()
}

func (e *Encoder) encodeTIEID(id *TIEID) error {
	if err := e.writeFieldHeader(thriftTypeI32, 1); err != nil {
		return err
	}
	if err := e.writeI32(int32(id.Direction)); err != nil {
		return err
	}
	if err := e.writeFieldHeader(thriftTypeI64, 2); err != nil {
		return err
	}
	if err := e.writeI64(id.Originator); err != nil {
		return err
	}
	if err := e.writeFieldHeader(thriftTypeI32, 3); err != nil {
		return err
	}
	if err := e.writeI32(int32(id.TIEType)); err != nil {
		return err
	}
	if err := e.writeFieldHeader(thriftTypeI32, 4); err != nil {
		return err
	}
	if err := e.writeI32(id.TIENr); err != nil {
		return err
	}
	return e.writeFieldStop()
}

func (e *Encoder) encodeTIEHeader(h *TIEHeader) error {
	if err := e.writeFieldHeader(thriftTypeStruct, 2); err != nil {
		return err
	}
	if err := e.encodeTIEID(&h.TIEID); err != nil {
		return err
	}
	if err := e.writeFieldHeader(thriftTypeI64, 3); err != nil {
		return err
	}
	if err := e.writeI64(h.SeqNr); err != nil {
		return err
	}
	if h.OriginationTime != nil {
		if err := e.writeFieldHeader(thriftTypeStruct, 10); err != nil {
			return err
		}
		if err := e.encodeIEEE802_1ASTimestamp(h.OriginationTime); err != nil {
			return err
		}
	}
	if h.OriginationLifetime != nil {
		if err := e.writeFieldHeader(thriftTypeI32, 12); err != nil {
			return err
		}
		if err := e.writeI32(*h.OriginationLifetime); err != nil {
			return err
		}
	}
	return e.writeFieldStop()
}

func (e *Encoder) encodeTIEHeaderWithLifeTime(h *TIEHeaderWithLifeTime) error {
	if err := e.writeFieldHeader(thriftTypeStruct, 1); err != nil {
		return err
	}
	if err := e.encodeTIEHeader(&h.Header); err != nil {
		return err
	}
	if err := e.writeFieldHeader(thriftTypeI32, 2); err != nil {
		return err
	}
	if err := e.writeI32(h.RemainingLifetime); err != nil {
		return err
	}
	return e.writeFieldStop()
}

func (e *Encoder) encodeTIDEPacket(p *TIDEPacket) error {
	if err := e.writeFieldHeader(thriftTypeStruct, 1); err != nil {
		return err
	}
	if err := e.encodeTIEID(&p.StartRange); err != nil {
		return err
	}
	if err := e.writeFieldHeader(thriftTypeStruct, 2); err != nil {
		return err
	}
	if err := e.encodeTIEID(&p.EndRange); err != nil {
		return err
	}
	// field 3: headers (list<struct>)
	if err := e.writeFieldHeader(thriftTypeList, 3); err != nil {
		return err
	}
	if err := e.writeByte(thriftTypeStruct); err != nil {
		return err
	}
	if err := e.writeI32(int32(len(p.Headers))); err != nil {
		return err
	}
	for i := range p.Headers {
		if err := e.encodeTIEHeaderWithLifeTimeInline(&p.Headers[i]); err != nil {
			return err
		}
	}
	return e.writeFieldStop()
}

func (e *Encoder) encodeTIEHeaderWithLifeTimeInline(h *TIEHeaderWithLifeTime) error {
	// Same as encodeTIEHeaderWithLifeTime but without the outer field header
	// (used inside lists/sets).
	if err := e.writeFieldHeader(thriftTypeStruct, 1); err != nil {
		return err
	}
	if err := e.encodeTIEHeader(&h.Header); err != nil {
		return err
	}
	if err := e.writeFieldHeader(thriftTypeI32, 2); err != nil {
		return err
	}
	if err := e.writeI32(h.RemainingLifetime); err != nil {
		return err
	}
	return e.writeFieldStop()
}

func (e *Encoder) encodeTIREPacket(p *TIREPacket) error {
	// field 1: headers (set<struct>)
	if err := e.writeFieldHeader(thriftTypeSet, 1); err != nil {
		return err
	}
	if err := e.writeByte(thriftTypeStruct); err != nil {
		return err
	}
	if err := e.writeI32(int32(len(p.Headers))); err != nil {
		return err
	}
	for i := range p.Headers {
		if err := e.encodeTIEHeaderWithLifeTimeInline(&p.Headers[i]); err != nil {
			return err
		}
	}
	return e.writeFieldStop()
}

func (e *Encoder) encodeTIEPacket(p *TIEPacket) error {
	if err := e.writeFieldHeader(thriftTypeStruct, 1); err != nil {
		return err
	}
	if err := e.encodeTIEHeader(&p.Header); err != nil {
		return err
	}
	if err := e.writeFieldHeader(thriftTypeStruct, 2); err != nil {
		return err
	}
	if err := e.encodeTIEElement(&p.Element); err != nil {
		return err
	}
	return e.writeFieldStop()
}

func (e *Encoder) encodeTIEElement(el *TIEElement) error {
	switch {
	case el.Node != nil:
		if err := e.writeFieldHeader(thriftTypeStruct, 1); err != nil {
			return err
		}
		if err := e.encodeNodeTIEElement(el.Node); err != nil {
			return err
		}
	case el.Prefixes != nil:
		if err := e.writeFieldHeader(thriftTypeStruct, 2); err != nil {
			return err
		}
		if err := e.encodePrefixTIEElement(el.Prefixes); err != nil {
			return err
		}
	case el.PositiveDisaggregationPrefixes != nil:
		if err := e.writeFieldHeader(thriftTypeStruct, 3); err != nil {
			return err
		}
		if err := e.encodePrefixTIEElement(el.PositiveDisaggregationPrefixes); err != nil {
			return err
		}
	case el.NegativeDisaggregationPrefixes != nil:
		if err := e.writeFieldHeader(thriftTypeStruct, 5); err != nil {
			return err
		}
		if err := e.encodePrefixTIEElement(el.NegativeDisaggregationPrefixes); err != nil {
			return err
		}
	case el.ExternalPrefixes != nil:
		if err := e.writeFieldHeader(thriftTypeStruct, 6); err != nil {
			return err
		}
		if err := e.encodePrefixTIEElement(el.ExternalPrefixes); err != nil {
			return err
		}
	case el.PositiveExternalDisaggregationPrefixes != nil:
		if err := e.writeFieldHeader(thriftTypeStruct, 7); err != nil {
			return err
		}
		if err := e.encodePrefixTIEElement(el.PositiveExternalDisaggregationPrefixes); err != nil {
			return err
		}
	case el.KeyValues != nil:
		if err := e.writeFieldHeader(thriftTypeStruct, 9); err != nil {
			return err
		}
		if err := e.encodeKeyValueTIEElement(el.KeyValues); err != nil {
			return err
		}
	default:
		return fmt.Errorf("TIEElement: no variant set")
	}
	return e.writeFieldStop()
}

func (e *Encoder) encodeNodeTIEElement(n *NodeTIEElement) error {
	// field 1: level (i8)
	if err := e.writeFieldHeader(thriftTypeByte, 1); err != nil {
		return err
	}
	if err := e.writeByte(byte(n.Level)); err != nil {
		return err
	}
	// field 2: neighbors map<i64, struct>
	if err := e.writeFieldHeader(thriftTypeMap, 2); err != nil {
		return err
	}
	if err := e.writeByte(thriftTypeI64); err != nil {
		return err
	}
	if err := e.writeByte(thriftTypeStruct); err != nil {
		return err
	}
	if err := e.writeI32(int32(len(n.Neighbors))); err != nil {
		return err
	}
	// Sort keys for deterministic output.
	keys := make([]int64, 0, len(n.Neighbors))
	for k := range n.Neighbors {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, k := range keys {
		v := n.Neighbors[k]
		if err := e.writeI64(k); err != nil {
			return err
		}
		if err := e.encodeNodeNeighborsTIEElement(v); err != nil {
			return err
		}
	}
	// field 3: capabilities (struct)
	if err := e.writeFieldHeader(thriftTypeStruct, 3); err != nil {
		return err
	}
	if err := e.encodeNodeCapabilities(&n.Capabilities); err != nil {
		return err
	}
	// field 4: flags (optional struct)
	if n.Flags != nil {
		if err := e.writeFieldHeader(thriftTypeStruct, 4); err != nil {
			return err
		}
		if err := e.encodeNodeFlags(n.Flags); err != nil {
			return err
		}
	}
	// field 5: name (optional string)
	if n.Name != "" {
		if err := e.writeFieldHeader(thriftTypeString, 5); err != nil {
			return err
		}
		if err := e.writeString(n.Name); err != nil {
			return err
		}
	}
	// field 6: pod (optional i32)
	if n.Pod != nil {
		if err := e.writeFieldHeader(thriftTypeI32, 6); err != nil {
			return err
		}
		if err := e.writeI32(*n.Pod); err != nil {
			return err
		}
	}
	// field 7: startup_time (optional i64)
	if n.StartupTime != nil {
		if err := e.writeFieldHeader(thriftTypeI64, 7); err != nil {
			return err
		}
		if err := e.writeI64(*n.StartupTime); err != nil {
			return err
		}
	}
	// field 10: miscabled_links (optional set<i32>)
	if len(n.MiscabledLinks) > 0 {
		if err := e.writeFieldHeader(thriftTypeSet, 10); err != nil {
			return err
		}
		if err := e.writeByte(thriftTypeI32); err != nil {
			return err
		}
		if err := e.writeI32(int32(len(n.MiscabledLinks))); err != nil {
			return err
		}
		for k := range n.MiscabledLinks {
			if err := e.writeI32(k); err != nil {
				return err
			}
		}
	}
	// field 12: same_plane_tofs (optional set<i64>)
	if len(n.SamePlaneTofs) > 0 {
		if err := e.writeFieldHeader(thriftTypeSet, 12); err != nil {
			return err
		}
		if err := e.writeByte(thriftTypeI64); err != nil {
			return err
		}
		if err := e.writeI32(int32(len(n.SamePlaneTofs))); err != nil {
			return err
		}
		for k := range n.SamePlaneTofs {
			if err := e.writeI64(k); err != nil {
				return err
			}
		}
	}
	// field 20: fabric_id (optional i32)
	if n.FabricID != nil {
		if err := e.writeFieldHeader(thriftTypeI32, 20); err != nil {
			return err
		}
		if err := e.writeI32(*n.FabricID); err != nil {
			return err
		}
	}
	return e.writeFieldStop()
}

func (e *Encoder) encodeNodeNeighborsTIEElement(n *NodeNeighborsTIEElement) error {
	if err := e.writeFieldHeader(thriftTypeByte, 1); err != nil {
		return err
	}
	if err := e.writeByte(byte(n.Level)); err != nil {
		return err
	}
	if n.Cost != nil {
		if err := e.writeFieldHeader(thriftTypeI32, 3); err != nil {
			return err
		}
		if err := e.writeI32(*n.Cost); err != nil {
			return err
		}
	}
	if len(n.LinkIDs) > 0 {
		if err := e.writeFieldHeader(thriftTypeSet, 4); err != nil {
			return err
		}
		if err := e.writeByte(thriftTypeStruct); err != nil {
			return err
		}
		if err := e.writeI32(int32(len(n.LinkIDs))); err != nil {
			return err
		}
		for i := range n.LinkIDs {
			if err := e.encodeLinkIDPair(&n.LinkIDs[i]); err != nil {
				return err
			}
		}
	}
	if n.Bandwidth != nil {
		if err := e.writeFieldHeader(thriftTypeI32, 5); err != nil {
			return err
		}
		if err := e.writeI32(*n.Bandwidth); err != nil {
			return err
		}
	}
	return e.writeFieldStop()
}

func (e *Encoder) encodeNodeFlags(f *NodeFlags) error {
	if f.Overload != nil {
		if err := e.writeFieldHeader(thriftTypeBool, 1); err != nil {
			return err
		}
		if err := e.writeBool(*f.Overload); err != nil {
			return err
		}
	}
	return e.writeFieldStop()
}

func (e *Encoder) encodeLinkIDPair(p *LinkIDPair) error {
	if err := e.writeFieldHeader(thriftTypeI32, 1); err != nil {
		return err
	}
	if err := e.writeI32(p.LocalID); err != nil {
		return err
	}
	if err := e.writeFieldHeader(thriftTypeI32, 2); err != nil {
		return err
	}
	if err := e.writeI32(p.RemoteID); err != nil {
		return err
	}
	if p.PlatformInterfaceIndex != nil {
		if err := e.writeFieldHeader(thriftTypeI32, 10); err != nil {
			return err
		}
		if err := e.writeI32(*p.PlatformInterfaceIndex); err != nil {
			return err
		}
	}
	if p.PlatformInterfaceName != "" {
		if err := e.writeFieldHeader(thriftTypeString, 11); err != nil {
			return err
		}
		if err := e.writeString(p.PlatformInterfaceName); err != nil {
			return err
		}
	}
	if p.TrustedOuterSecurityKey != nil {
		if err := e.writeFieldHeader(thriftTypeByte, 12); err != nil {
			return err
		}
		if err := e.writeByte(byte(*p.TrustedOuterSecurityKey)); err != nil {
			return err
		}
	}
	if p.BFDUp != nil {
		if err := e.writeFieldHeader(thriftTypeBool, 13); err != nil {
			return err
		}
		if err := e.writeBool(*p.BFDUp); err != nil {
			return err
		}
	}
	if len(p.AddressFamilies) > 0 {
		if err := e.writeFieldHeader(thriftTypeSet, 14); err != nil {
			return err
		}
		if err := e.writeByte(thriftTypeI32); err != nil {
			return err
		}
		if err := e.writeI32(int32(len(p.AddressFamilies))); err != nil {
			return err
		}
		for k := range p.AddressFamilies {
			if err := e.writeI32(int32(k)); err != nil {
				return err
			}
		}
	}
	return e.writeFieldStop()
}

func (e *Encoder) encodeIPPrefixType(p *IPPrefixType) error {
	if p.IPv4Prefix != nil {
		if err := e.writeFieldHeader(thriftTypeStruct, 1); err != nil {
			return err
		}
		if err := e.encodeIPv4PrefixType(p.IPv4Prefix); err != nil {
			return err
		}
	} else if p.IPv6Prefix != nil {
		if err := e.writeFieldHeader(thriftTypeStruct, 2); err != nil {
			return err
		}
		if err := e.encodeIPv6PrefixType(p.IPv6Prefix); err != nil {
			return err
		}
	}
	return e.writeFieldStop()
}

func (e *Encoder) encodeIPv4PrefixType(p *IPv4PrefixType) error {
	if err := e.writeFieldHeader(thriftTypeI32, 1); err != nil {
		return err
	}
	if err := e.writeI32(p.Address); err != nil {
		return err
	}
	if err := e.writeFieldHeader(thriftTypeByte, 2); err != nil {
		return err
	}
	if err := e.writeByte(byte(p.PrefixLen)); err != nil {
		return err
	}
	return e.writeFieldStop()
}

func (e *Encoder) encodeIPv6PrefixType(p *IPv6PrefixType) error {
	if err := e.writeFieldHeader(thriftTypeString, 1); err != nil {
		return err
	}
	if err := e.writeBinary(p.Address); err != nil {
		return err
	}
	if err := e.writeFieldHeader(thriftTypeByte, 2); err != nil {
		return err
	}
	if err := e.writeByte(byte(p.PrefixLen)); err != nil {
		return err
	}
	return e.writeFieldStop()
}

func (e *Encoder) encodePrefixAttributes(a *PrefixAttributes) error {
	if err := e.writeFieldHeader(thriftTypeI32, 2); err != nil {
		return err
	}
	if err := e.writeI32(a.Metric); err != nil {
		return err
	}
	if len(a.Tags) > 0 {
		if err := e.writeFieldHeader(thriftTypeSet, 3); err != nil {
			return err
		}
		if err := e.writeByte(thriftTypeI64); err != nil {
			return err
		}
		if err := e.writeI32(int32(len(a.Tags))); err != nil {
			return err
		}
		for k := range a.Tags {
			if err := e.writeI64(k); err != nil {
				return err
			}
		}
	}
	if a.MonotonicClock != nil {
		if err := e.writeFieldHeader(thriftTypeStruct, 4); err != nil {
			return err
		}
		if err := e.encodePrefixSequenceType(a.MonotonicClock); err != nil {
			return err
		}
	}
	if a.Loopback != nil {
		if err := e.writeFieldHeader(thriftTypeBool, 6); err != nil {
			return err
		}
		if err := e.writeBool(*a.Loopback); err != nil {
			return err
		}
	}
	if a.DirectlyAttached != nil {
		if err := e.writeFieldHeader(thriftTypeBool, 7); err != nil {
			return err
		}
		if err := e.writeBool(*a.DirectlyAttached); err != nil {
			return err
		}
	}
	if a.FromLink != nil {
		if err := e.writeFieldHeader(thriftTypeI32, 10); err != nil {
			return err
		}
		if err := e.writeI32(*a.FromLink); err != nil {
			return err
		}
	}
	if a.Label != nil {
		if err := e.writeFieldHeader(thriftTypeI32, 12); err != nil {
			return err
		}
		if err := e.writeI32(*a.Label); err != nil {
			return err
		}
	}
	return e.writeFieldStop()
}

func (e *Encoder) encodePrefixSequenceType(p *PrefixSequenceType) error {
	if err := e.writeFieldHeader(thriftTypeStruct, 1); err != nil {
		return err
	}
	if err := e.encodeIEEE802_1ASTimestamp(&p.Timestamp); err != nil {
		return err
	}
	if p.TransactionID != nil {
		if err := e.writeFieldHeader(thriftTypeByte, 2); err != nil {
			return err
		}
		if err := e.writeByte(byte(*p.TransactionID)); err != nil {
			return err
		}
	}
	return e.writeFieldStop()
}

func (e *Encoder) encodePrefixTIEElement(p *PrefixTIEElement) error {
	// field 1: prefixes map<IPPrefixType, PrefixAttributes>
	// Since IPPrefixType is a union (struct), the map key type is struct.
	if err := e.writeFieldHeader(thriftTypeMap, 1); err != nil {
		return err
	}
	if err := e.writeByte(thriftTypeStruct); err != nil { // key type
		return err
	}
	if err := e.writeByte(thriftTypeStruct); err != nil { // value type
		return err
	}
	if err := e.writeI32(int32(len(p.Prefixes))); err != nil {
		return err
	}
	for i := range p.Prefixes {
		if err := e.encodeIPPrefixType(&p.Prefixes[i].Prefix); err != nil {
			return err
		}
		if err := e.encodePrefixAttributes(&p.Prefixes[i].Attributes); err != nil {
			return err
		}
	}
	return e.writeFieldStop()
}

func (e *Encoder) encodeKeyValueTIEElement(kv *KeyValueTIEElement) error {
	if err := e.writeFieldHeader(thriftTypeMap, 1); err != nil {
		return err
	}
	if err := e.writeByte(thriftTypeI32); err != nil {
		return err
	}
	if err := e.writeByte(thriftTypeStruct); err != nil {
		return err
	}
	if err := e.writeI32(int32(len(kv.KeyValues))); err != nil {
		return err
	}
	for k, v := range kv.KeyValues {
		if err := e.writeI32(k); err != nil {
			return err
		}
		if err := e.encodeKeyValueTIEElementContent(v); err != nil {
			return err
		}
	}
	return e.writeFieldStop()
}

func (e *Encoder) encodeKeyValueTIEElementContent(c *KeyValueTIEElementContent) error {
	if c.Targets != nil {
		if err := e.writeFieldHeader(thriftTypeI64, 1); err != nil {
			return err
		}
		if err := e.writeI64(*c.Targets); err != nil {
			return err
		}
	}
	if c.Value != nil {
		if err := e.writeFieldHeader(thriftTypeString, 2); err != nil {
			return err
		}
		if err := e.writeBinary(c.Value); err != nil {
			return err
		}
	}
	return e.writeFieldStop()
}

// Decoder reads Thrift binary protocol.
type Decoder struct {
	r   io.Reader
	buf [8]byte
}

// NewDecoder creates a new Thrift binary protocol decoder.
func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{r: r}
}

func (d *Decoder) readByte() (byte, error) {
	_, err := io.ReadFull(d.r, d.buf[:1])
	return d.buf[0], err
}

func (d *Decoder) readI16() (int16, error) {
	_, err := io.ReadFull(d.r, d.buf[:2])
	return int16(binary.BigEndian.Uint16(d.buf[:2])), err
}

func (d *Decoder) readI32() (int32, error) {
	_, err := io.ReadFull(d.r, d.buf[:4])
	return int32(binary.BigEndian.Uint32(d.buf[:4])), err
}

func (d *Decoder) readI64() (int64, error) {
	_, err := io.ReadFull(d.r, d.buf[:8])
	return int64(binary.BigEndian.Uint64(d.buf[:8])), err
}

func (d *Decoder) readBool() (bool, error) {
	b, err := d.readByte()
	return b != 0, err
}

func (d *Decoder) readString() (string, error) {
	n, err := d.readI32()
	if err != nil {
		return "", err
	}
	if n < 0 || n > 1<<20 {
		return "", fmt.Errorf("string length %d out of range", n)
	}
	buf := make([]byte, n)
	_, err = io.ReadFull(d.r, buf)
	return string(buf), err
}

func (d *Decoder) readBinary() ([]byte, error) {
	n, err := d.readI32()
	if err != nil {
		return nil, err
	}
	if n < 0 || n > 1<<20 {
		return nil, fmt.Errorf("binary length %d out of range", n)
	}
	buf := make([]byte, n)
	_, err = io.ReadFull(d.r, buf)
	return buf, err
}

func (d *Decoder) readFieldHeader() (byte, int16, error) {
	ftype, err := d.readByte()
	if err != nil {
		return 0, 0, err
	}
	if ftype == thriftTypeStop {
		return thriftTypeStop, 0, nil
	}
	fid, err := d.readI16()
	return ftype, fid, err
}

func (d *Decoder) skip(ftype byte) error {
	switch ftype {
	case thriftTypeBool, thriftTypeByte:
		_, err := d.readByte()
		return err
	case thriftTypeI16:
		_, err := d.readI16()
		return err
	case thriftTypeI32:
		_, err := d.readI32()
		return err
	case thriftTypeI64:
		_, err := d.readI64()
		return err
	case thriftTypeString:
		_, err := d.readBinary()
		return err
	case thriftTypeStruct:
		for {
			ft, _, err := d.readFieldHeader()
			if err != nil {
				return err
			}
			if ft == thriftTypeStop {
				return nil
			}
			if err := d.skip(ft); err != nil {
				return err
			}
		}
	case thriftTypeMap:
		kt, err := d.readByte()
		if err != nil {
			return err
		}
		vt, err := d.readByte()
		if err != nil {
			return err
		}
		n, err := d.readI32()
		if err != nil {
			return err
		}
		for i := int32(0); i < n; i++ {
			if err := d.skip(kt); err != nil {
				return err
			}
			if err := d.skip(vt); err != nil {
				return err
			}
		}
		return nil
	case thriftTypeSet, thriftTypeList:
		et, err := d.readByte()
		if err != nil {
			return err
		}
		n, err := d.readI32()
		if err != nil {
			return err
		}
		for i := int32(0); i < n; i++ {
			if err := d.skip(et); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown thrift type %d", ftype)
	}
}

// DecodeProtocolPacket deserializes a ProtocolPacket from Thrift binary format.
func (d *Decoder) DecodeProtocolPacket() (*ProtocolPacket, error) {
	p := &ProtocolPacket{}
	for {
		ftype, fid, err := d.readFieldHeader()
		if err != nil {
			return nil, err
		}
		if ftype == thriftTypeStop {
			break
		}
		switch fid {
		case 1:
			if err := d.decodePacketHeader(&p.Header); err != nil {
				return nil, err
			}
		case 2:
			if err := d.decodePacketContent(&p.Content); err != nil {
				return nil, err
			}
		default:
			if err := d.skip(ftype); err != nil {
				return nil, err
			}
		}
	}
	return p, nil
}

func (d *Decoder) decodePacketHeader(h *PacketHeader) error {
	for {
		ftype, fid, err := d.readFieldHeader()
		if err != nil {
			return err
		}
		if ftype == thriftTypeStop {
			return nil
		}
		switch fid {
		case 1:
			b, err := d.readByte()
			if err != nil {
				return err
			}
			h.MajorVersion = VersionType(b)
		case 2:
			v, err := d.readI16()
			if err != nil {
				return err
			}
			h.MinorVersion = MinorVersionType(v)
		case 3:
			v, err := d.readI64()
			if err != nil {
				return err
			}
			h.Sender = v
		case 4:
			b, err := d.readByte()
			if err != nil {
				return err
			}
			lvl := LevelType(b)
			h.Level = &lvl
		default:
			if err := d.skip(ftype); err != nil {
				return err
			}
		}
	}
}

func (d *Decoder) decodePacketContent(c *PacketContent) error {
	for {
		ftype, fid, err := d.readFieldHeader()
		if err != nil {
			return err
		}
		if ftype == thriftTypeStop {
			return nil
		}
		switch fid {
		case 1:
			c.LIE = &LIEPacket{}
			if err := d.decodeLIEPacket(c.LIE); err != nil {
				return err
			}
		case 2:
			c.TIDE = &TIDEPacket{}
			if err := d.decodeTIDEPacket(c.TIDE); err != nil {
				return err
			}
		case 3:
			c.TIRE = &TIREPacket{}
			if err := d.decodeTIREPacket(c.TIRE); err != nil {
				return err
			}
		case 4:
			c.TIE = &TIEPacket{}
			if err := d.decodeTIEPacket(c.TIE); err != nil {
				return err
			}
		default:
			if err := d.skip(ftype); err != nil {
				return err
			}
		}
	}
}

func (d *Decoder) decodeLIEPacket(p *LIEPacket) error {
	// Set defaults
	p.FloodPort = DefaultTIEUDPFloodPort
	p.Holdtime = DefaultLIEHoldtime
	for {
		ftype, fid, err := d.readFieldHeader()
		if err != nil {
			return err
		}
		if ftype == thriftTypeStop {
			return nil
		}
		switch fid {
		case 1:
			p.Name, err = d.readString()
		case 2:
			p.LocalID, err = d.readI32()
		case 3:
			v, e := d.readI16()
			p.FloodPort = UDPPortType(v)
			err = e
		case 4:
			v, e := d.readI32()
			p.LinkMTUSize = &v
			err = e
		case 5:
			v, e := d.readI32()
			bw := BandwidthInMegaBitsType(v)
			p.LinkBandwidth = &bw
			err = e
		case 6:
			p.Neighbor = &Neighbor{}
			err = d.decodeNeighbor(p.Neighbor)
		case 7:
			v, e := d.readI32()
			pod := PodType(v)
			p.Pod = &pod
			err = e
		case 10:
			err = d.decodeNodeCapabilities(&p.NodeCapabilities)
		case 11:
			p.LinkCapabilities = &LinkCapabilities{}
			err = d.decodeLinkCapabilities(p.LinkCapabilities)
		case 12:
			v, e := d.readI16()
			p.Holdtime = TimeIntervalInSecType(v)
			err = e
		case 13:
			v, e := d.readI32()
			label := LabelType(v)
			p.Label = &label
			err = e
		case 21:
			v, e := d.readBool()
			p.NotAZTPOffer = &v
			err = e
		case 22:
			v, e := d.readBool()
			p.YouAreFloodRepeater = &v
			err = e
		case 23:
			v, e := d.readBool()
			p.YouAreSendingTooQuickly = &v
			err = e
		case 24:
			p.InstanceName, err = d.readString()
		case 35:
			v, e := d.readI32()
			fid := FabricIDType(v)
			p.FabricID = &fid
			err = e
		default:
			err = d.skip(ftype)
		}
		if err != nil {
			return err
		}
	}
}

func (d *Decoder) decodeNeighbor(n *Neighbor) error {
	for {
		ftype, fid, err := d.readFieldHeader()
		if err != nil {
			return err
		}
		if ftype == thriftTypeStop {
			return nil
		}
		switch fid {
		case 1:
			n.Originator, err = d.readI64()
		case 2:
			n.RemoteID, err = d.readI32()
		default:
			err = d.skip(ftype)
		}
		if err != nil {
			return err
		}
	}
}

func (d *Decoder) decodeNodeCapabilities(c *NodeCapabilities) error {
	for {
		ftype, fid, err := d.readFieldHeader()
		if err != nil {
			return err
		}
		if ftype == thriftTypeStop {
			return nil
		}
		switch fid {
		case 1:
			v, e := d.readI16()
			c.ProtocolMinorVersion = MinorVersionType(v)
			err = e
		case 2:
			v, e := d.readBool()
			c.FloodReduction = &v
			err = e
		case 3:
			v, e := d.readI32()
			hi := HierarchyIndications(v)
			c.HierarchyIndications = &hi
			err = e
		default:
			err = d.skip(ftype)
		}
		if err != nil {
			return err
		}
	}
}

func (d *Decoder) decodeLinkCapabilities(c *LinkCapabilities) error {
	for {
		ftype, fid, err := d.readFieldHeader()
		if err != nil {
			return err
		}
		if ftype == thriftTypeStop {
			return nil
		}
		switch fid {
		case 1:
			v, e := d.readBool()
			c.BFD = &v
			err = e
		case 2:
			v, e := d.readBool()
			c.IPv4ForwardingCapable = &v
			err = e
		default:
			err = d.skip(ftype)
		}
		if err != nil {
			return err
		}
	}
}

func (d *Decoder) decodeIEEE802_1ASTimestamp(t *IEEE802_1ASTimeStampType) error {
	for {
		ftype, fid, err := d.readFieldHeader()
		if err != nil {
			return err
		}
		if ftype == thriftTypeStop {
			return nil
		}
		switch fid {
		case 1:
			t.ASSec, err = d.readI64()
		case 2:
			v, e := d.readI32()
			t.ASNsec = &v
			err = e
		default:
			err = d.skip(ftype)
		}
		if err != nil {
			return err
		}
	}
}

func (d *Decoder) decodeTIEID(id *TIEID) error {
	for {
		ftype, fid, err := d.readFieldHeader()
		if err != nil {
			return err
		}
		if ftype == thriftTypeStop {
			return nil
		}
		switch fid {
		case 1:
			v, e := d.readI32()
			id.Direction = TieDirectionType(v)
			err = e
		case 2:
			id.Originator, err = d.readI64()
		case 3:
			v, e := d.readI32()
			id.TIEType = TIETypeType(v)
			err = e
		case 4:
			id.TIENr, err = d.readI32()
		default:
			err = d.skip(ftype)
		}
		if err != nil {
			return err
		}
	}
}

func (d *Decoder) decodeTIEHeader(h *TIEHeader) error {
	for {
		ftype, fid, err := d.readFieldHeader()
		if err != nil {
			return err
		}
		if ftype == thriftTypeStop {
			return nil
		}
		switch fid {
		case 2:
			err = d.decodeTIEID(&h.TIEID)
		case 3:
			h.SeqNr, err = d.readI64()
		case 10:
			h.OriginationTime = &IEEE802_1ASTimeStampType{}
			err = d.decodeIEEE802_1ASTimestamp(h.OriginationTime)
		case 12:
			v, e := d.readI32()
			lt := LifeTimeInSecType(v)
			h.OriginationLifetime = &lt
			err = e
		default:
			err = d.skip(ftype)
		}
		if err != nil {
			return err
		}
	}
}

func (d *Decoder) decodeTIEHeaderWithLifeTime(h *TIEHeaderWithLifeTime) error {
	for {
		ftype, fid, err := d.readFieldHeader()
		if err != nil {
			return err
		}
		if ftype == thriftTypeStop {
			return nil
		}
		switch fid {
		case 1:
			err = d.decodeTIEHeader(&h.Header)
		case 2:
			h.RemainingLifetime, err = d.readI32()
		default:
			err = d.skip(ftype)
		}
		if err != nil {
			return err
		}
	}
}

func (d *Decoder) decodeTIDEPacket(p *TIDEPacket) error {
	for {
		ftype, fid, err := d.readFieldHeader()
		if err != nil {
			return err
		}
		if ftype == thriftTypeStop {
			return nil
		}
		switch fid {
		case 1:
			err = d.decodeTIEID(&p.StartRange)
		case 2:
			err = d.decodeTIEID(&p.EndRange)
		case 3:
			// list<TIEHeaderWithLifeTime>
			_, err := d.readByte() // element type
			if err != nil {
				return err
			}
			n, err := d.readI32()
			if err != nil {
				return err
			}
			p.Headers = make([]TIEHeaderWithLifeTime, n)
			for i := int32(0); i < n; i++ {
				if err := d.decodeTIEHeaderWithLifeTime(&p.Headers[i]); err != nil {
					return err
				}
			}
		default:
			err = d.skip(ftype)
		}
		if err != nil {
			return err
		}
	}
}

func (d *Decoder) decodeTIREPacket(p *TIREPacket) error {
	for {
		ftype, fid, err := d.readFieldHeader()
		if err != nil {
			return err
		}
		if ftype == thriftTypeStop {
			return nil
		}
		switch fid {
		case 1:
			// set<TIEHeaderWithLifeTime>
			_, err := d.readByte() // element type
			if err != nil {
				return err
			}
			n, err := d.readI32()
			if err != nil {
				return err
			}
			p.Headers = make([]TIEHeaderWithLifeTime, n)
			for i := int32(0); i < n; i++ {
				if err := d.decodeTIEHeaderWithLifeTime(&p.Headers[i]); err != nil {
					return err
				}
			}
		default:
			err = d.skip(ftype)
		}
		if err != nil {
			return err
		}
	}
}

func (d *Decoder) decodeTIEPacket(p *TIEPacket) error {
	for {
		ftype, fid, err := d.readFieldHeader()
		if err != nil {
			return err
		}
		if ftype == thriftTypeStop {
			return nil
		}
		switch fid {
		case 1:
			err = d.decodeTIEHeader(&p.Header)
		case 2:
			err = d.decodeTIEElement(&p.Element)
		default:
			err = d.skip(ftype)
		}
		if err != nil {
			return err
		}
	}
}

func (d *Decoder) decodeTIEElement(el *TIEElement) error {
	for {
		ftype, fid, err := d.readFieldHeader()
		if err != nil {
			return err
		}
		if ftype == thriftTypeStop {
			return nil
		}
		switch fid {
		case 1:
			el.Node = &NodeTIEElement{}
			err = d.decodeNodeTIEElement(el.Node)
		case 2:
			el.Prefixes = &PrefixTIEElement{}
			err = d.decodePrefixTIEElement(el.Prefixes)
		case 3:
			el.PositiveDisaggregationPrefixes = &PrefixTIEElement{}
			err = d.decodePrefixTIEElement(el.PositiveDisaggregationPrefixes)
		case 5:
			el.NegativeDisaggregationPrefixes = &PrefixTIEElement{}
			err = d.decodePrefixTIEElement(el.NegativeDisaggregationPrefixes)
		case 6:
			el.ExternalPrefixes = &PrefixTIEElement{}
			err = d.decodePrefixTIEElement(el.ExternalPrefixes)
		case 7:
			el.PositiveExternalDisaggregationPrefixes = &PrefixTIEElement{}
			err = d.decodePrefixTIEElement(el.PositiveExternalDisaggregationPrefixes)
		case 9:
			el.KeyValues = &KeyValueTIEElement{}
			err = d.decodeKeyValueTIEElement(el.KeyValues)
		default:
			err = d.skip(ftype)
		}
		if err != nil {
			return err
		}
	}
}

func (d *Decoder) decodeNodeTIEElement(n *NodeTIEElement) error {
	n.Neighbors = make(map[SystemIDType]*NodeNeighborsTIEElement)
	for {
		ftype, fid, err := d.readFieldHeader()
		if err != nil {
			return err
		}
		if ftype == thriftTypeStop {
			return nil
		}
		switch fid {
		case 1:
			b, e := d.readByte()
			n.Level = LevelType(b)
			err = e
		case 2:
			// map<i64, NodeNeighborsTIEElement>
			_, err := d.readByte() // key type
			if err != nil {
				return err
			}
			_, err = d.readByte() // value type
			if err != nil {
				return err
			}
			count, err := d.readI32()
			if err != nil {
				return err
			}
			for i := int32(0); i < count; i++ {
				k, err := d.readI64()
				if err != nil {
					return err
				}
				v := &NodeNeighborsTIEElement{}
				if err := d.decodeNodeNeighborsTIEElement(v); err != nil {
					return err
				}
				n.Neighbors[k] = v
			}
		case 3:
			err = d.decodeNodeCapabilities(&n.Capabilities)
		case 4:
			n.Flags = &NodeFlags{}
			err = d.decodeNodeFlags(n.Flags)
		case 5:
			n.Name, err = d.readString()
		case 6:
			v, e := d.readI32()
			pod := PodType(v)
			n.Pod = &pod
			err = e
		case 7:
			v, e := d.readI64()
			ts := TimestampInSecsType(v)
			n.StartupTime = &ts
			err = e
		case 10:
			// set<LinkIDType>
			_, err := d.readByte()
			if err != nil {
				return err
			}
			count, err := d.readI32()
			if err != nil {
				return err
			}
			n.MiscabledLinks = make(map[LinkIDType]struct{}, count)
			for i := int32(0); i < count; i++ {
				v, err := d.readI32()
				if err != nil {
					return err
				}
				n.MiscabledLinks[v] = struct{}{}
			}
		case 12:
			// set<SystemIDType>
			_, err := d.readByte()
			if err != nil {
				return err
			}
			count, err := d.readI32()
			if err != nil {
				return err
			}
			n.SamePlaneTofs = make(map[SystemIDType]struct{}, count)
			for i := int32(0); i < count; i++ {
				v, err := d.readI64()
				if err != nil {
					return err
				}
				n.SamePlaneTofs[v] = struct{}{}
			}
		case 20:
			v, e := d.readI32()
			fid := FabricIDType(v)
			n.FabricID = &fid
			err = e
		default:
			err = d.skip(ftype)
		}
		if err != nil {
			return err
		}
	}
}

func (d *Decoder) decodeNodeNeighborsTIEElement(n *NodeNeighborsTIEElement) error {
	for {
		ftype, fid, err := d.readFieldHeader()
		if err != nil {
			return err
		}
		if ftype == thriftTypeStop {
			return nil
		}
		switch fid {
		case 1:
			b, e := d.readByte()
			n.Level = LevelType(b)
			err = e
		case 3:
			v, e := d.readI32()
			cost := MetricType(v)
			n.Cost = &cost
			err = e
		case 4:
			// set<LinkIDPair>
			_, err := d.readByte()
			if err != nil {
				return err
			}
			count, err := d.readI32()
			if err != nil {
				return err
			}
			n.LinkIDs = make([]LinkIDPair, count)
			for i := int32(0); i < count; i++ {
				if err := d.decodeLinkIDPair(&n.LinkIDs[i]); err != nil {
					return err
				}
			}
		case 5:
			v, e := d.readI32()
			bw := BandwidthInMegaBitsType(v)
			n.Bandwidth = &bw
			err = e
		default:
			err = d.skip(ftype)
		}
		if err != nil {
			return err
		}
	}
}

func (d *Decoder) decodeNodeFlags(f *NodeFlags) error {
	for {
		ftype, fid, err := d.readFieldHeader()
		if err != nil {
			return err
		}
		if ftype == thriftTypeStop {
			return nil
		}
		switch fid {
		case 1:
			v, e := d.readBool()
			f.Overload = &v
			err = e
		default:
			err = d.skip(ftype)
		}
		if err != nil {
			return err
		}
	}
}

func (d *Decoder) decodeLinkIDPair(p *LinkIDPair) error {
	for {
		ftype, fid, err := d.readFieldHeader()
		if err != nil {
			return err
		}
		if ftype == thriftTypeStop {
			return nil
		}
		switch fid {
		case 1:
			p.LocalID, err = d.readI32()
		case 2:
			p.RemoteID, err = d.readI32()
		case 10:
			v, e := d.readI32()
			idx := PlatformInterfaceIndex(v)
			p.PlatformInterfaceIndex = &idx
			err = e
		case 11:
			p.PlatformInterfaceName, err = d.readString()
		case 12:
			b, e := d.readByte()
			key := OuterSecurityKeyID(b)
			p.TrustedOuterSecurityKey = &key
			err = e
		case 13:
			v, e := d.readBool()
			p.BFDUp = &v
			err = e
		case 14:
			// set<AddressFamilyType>
			_, err := d.readByte()
			if err != nil {
				return err
			}
			count, err := d.readI32()
			if err != nil {
				return err
			}
			p.AddressFamilies = make(map[AddressFamilyType]struct{}, count)
			for i := int32(0); i < count; i++ {
				v, err := d.readI32()
				if err != nil {
					return err
				}
				p.AddressFamilies[AddressFamilyType(v)] = struct{}{}
			}
		default:
			err = d.skip(ftype)
		}
		if err != nil {
			return err
		}
	}
}

func (d *Decoder) decodeIPPrefixType(p *IPPrefixType) error {
	for {
		ftype, fid, err := d.readFieldHeader()
		if err != nil {
			return err
		}
		if ftype == thriftTypeStop {
			return nil
		}
		switch fid {
		case 1:
			p.IPv4Prefix = &IPv4PrefixType{}
			err = d.decodeIPv4PrefixType(p.IPv4Prefix)
		case 2:
			p.IPv6Prefix = &IPv6PrefixType{}
			err = d.decodeIPv6PrefixType(p.IPv6Prefix)
		default:
			err = d.skip(ftype)
		}
		if err != nil {
			return err
		}
	}
}

func (d *Decoder) decodeIPv4PrefixType(p *IPv4PrefixType) error {
	for {
		ftype, fid, err := d.readFieldHeader()
		if err != nil {
			return err
		}
		if ftype == thriftTypeStop {
			return nil
		}
		switch fid {
		case 1:
			p.Address, err = d.readI32()
		case 2:
			b, e := d.readByte()
			p.PrefixLen = PrefixLenType(b)
			err = e
		default:
			err = d.skip(ftype)
		}
		if err != nil {
			return err
		}
	}
}

func (d *Decoder) decodeIPv6PrefixType(p *IPv6PrefixType) error {
	for {
		ftype, fid, err := d.readFieldHeader()
		if err != nil {
			return err
		}
		if ftype == thriftTypeStop {
			return nil
		}
		switch fid {
		case 1:
			p.Address, err = d.readBinary()
		case 2:
			b, e := d.readByte()
			p.PrefixLen = PrefixLenType(b)
			err = e
		default:
			err = d.skip(ftype)
		}
		if err != nil {
			return err
		}
	}
}

func (d *Decoder) decodePrefixTIEElement(p *PrefixTIEElement) error {
	for {
		ftype, fid, err := d.readFieldHeader()
		if err != nil {
			return err
		}
		if ftype == thriftTypeStop {
			return nil
		}
		switch fid {
		case 1:
			// map<IPPrefixType, PrefixAttributes>
			_, err := d.readByte() // key type
			if err != nil {
				return err
			}
			_, err = d.readByte() // value type
			if err != nil {
				return err
			}
			count, err := d.readI32()
			if err != nil {
				return err
			}
			p.Prefixes = make([]PrefixEntry, count)
			for i := int32(0); i < count; i++ {
				if err := d.decodeIPPrefixType(&p.Prefixes[i].Prefix); err != nil {
					return err
				}
				if err := d.decodePrefixAttributes(&p.Prefixes[i].Attributes); err != nil {
					return err
				}
			}
		default:
			err = d.skip(ftype)
		}
		if err != nil {
			return err
		}
	}
}

func (d *Decoder) decodePrefixAttributes(a *PrefixAttributes) error {
	a.Metric = DefaultDistance // default
	for {
		ftype, fid, err := d.readFieldHeader()
		if err != nil {
			return err
		}
		if ftype == thriftTypeStop {
			return nil
		}
		switch fid {
		case 2:
			a.Metric, err = d.readI32()
		case 3:
			// set<RouteTagType>
			_, err := d.readByte()
			if err != nil {
				return err
			}
			count, err := d.readI32()
			if err != nil {
				return err
			}
			a.Tags = make(map[RouteTagType]struct{}, count)
			for i := int32(0); i < count; i++ {
				v, err := d.readI64()
				if err != nil {
					return err
				}
				a.Tags[v] = struct{}{}
			}
		case 4:
			a.MonotonicClock = &PrefixSequenceType{}
			err = d.decodePrefixSequenceType(a.MonotonicClock)
		case 6:
			v, e := d.readBool()
			a.Loopback = &v
			err = e
		case 7:
			v, e := d.readBool()
			a.DirectlyAttached = &v
			err = e
		case 10:
			v, e := d.readI32()
			link := LinkIDType(v)
			a.FromLink = &link
			err = e
		case 12:
			v, e := d.readI32()
			label := LabelType(v)
			a.Label = &label
			err = e
		default:
			err = d.skip(ftype)
		}
		if err != nil {
			return err
		}
	}
}

func (d *Decoder) decodePrefixSequenceType(p *PrefixSequenceType) error {
	for {
		ftype, fid, err := d.readFieldHeader()
		if err != nil {
			return err
		}
		if ftype == thriftTypeStop {
			return nil
		}
		switch fid {
		case 1:
			err = d.decodeIEEE802_1ASTimestamp(&p.Timestamp)
		case 2:
			b, e := d.readByte()
			txid := PrefixTransactionIDType(b)
			p.TransactionID = &txid
			err = e
		default:
			err = d.skip(ftype)
		}
		if err != nil {
			return err
		}
	}
}

func (d *Decoder) decodeKeyValueTIEElement(kv *KeyValueTIEElement) error {
	for {
		ftype, fid, err := d.readFieldHeader()
		if err != nil {
			return err
		}
		if ftype == thriftTypeStop {
			return nil
		}
		switch fid {
		case 1:
			// map<KeyIDType, KeyValueTIEElementContent>
			_, err := d.readByte() // key type
			if err != nil {
				return err
			}
			_, err = d.readByte() // value type
			if err != nil {
				return err
			}
			count, err := d.readI32()
			if err != nil {
				return err
			}
			kv.KeyValues = make(map[KeyIDType]*KeyValueTIEElementContent, count)
			for i := int32(0); i < count; i++ {
				k, err := d.readI32()
				if err != nil {
					return err
				}
				v := &KeyValueTIEElementContent{}
				if err := d.decodeKeyValueTIEElementContent(v); err != nil {
					return err
				}
				kv.KeyValues[k] = v
			}
		default:
			err = d.skip(ftype)
		}
		if err != nil {
			return err
		}
	}
}

func (d *Decoder) decodeKeyValueTIEElementContent(c *KeyValueTIEElementContent) error {
	for {
		ftype, fid, err := d.readFieldHeader()
		if err != nil {
			return err
		}
		if ftype == thriftTypeStop {
			return nil
		}
		switch fid {
		case 1:
			v, e := d.readI64()
			t := KeyValueTargetType(v)
			c.Targets = &t
			err = e
		case 2:
			c.Value, err = d.readBinary()
		default:
			err = d.skip(ftype)
		}
		if err != nil {
			return err
		}
	}
}

// Helper to suppress unused import warnings.
var _ = math.MaxInt32
var _ = sort.Slice
