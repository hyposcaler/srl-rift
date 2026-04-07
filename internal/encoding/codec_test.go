package encoding

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func boolPtr(v bool) *bool          { return &v }
func i32Ptr(v int32) *int32         { return &v }

func TestLIEPacketRoundTrip(t *testing.T) {
	level := LevelType(0)
	original := &ProtocolPacket{
		Header: PacketHeader{
			MajorVersion: ProtocolMajorVersion,
			MinorVersion: ProtocolMinorVersion,
			Sender:       12345,
			Level:        &level,
		},
		Content: PacketContent{
			LIE: &LIEPacket{
				Name:      "leaf1",
				LocalID:   1,
				FloodPort: DefaultTIEUDPFloodPort,
				LinkMTUSize: i32Ptr(int32(DefaultMTUSize)),
				Holdtime:  DefaultLIEHoldtime,
				NodeCapabilities: NodeCapabilities{
					ProtocolMinorVersion: ProtocolMinorVersion,
					FloodReduction:       boolPtr(true),
				},
				LinkCapabilities: &LinkCapabilities{
					BFD:                   boolPtr(true),
					IPv4ForwardingCapable: boolPtr(true),
				},
				Neighbor: &Neighbor{
					Originator: 67890,
					RemoteID:   2,
				},
				YouAreFloodRepeater: boolPtr(true),
			},
		},
	}

	// Encode
	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	if err := enc.EncodeProtocolPacket(original); err != nil {
		t.Fatalf("encode: %v", err)
	}

	// Decode
	dec := NewDecoder(bytes.NewReader(buf.Bytes()))
	decoded, err := dec.DecodeProtocolPacket()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Verify header
	if decoded.Header.MajorVersion != original.Header.MajorVersion {
		t.Errorf("major version: got %d, want %d", decoded.Header.MajorVersion, original.Header.MajorVersion)
	}
	if decoded.Header.MinorVersion != original.Header.MinorVersion {
		t.Errorf("minor version: got %d, want %d", decoded.Header.MinorVersion, original.Header.MinorVersion)
	}
	if decoded.Header.Sender != original.Header.Sender {
		t.Errorf("sender: got %d, want %d", decoded.Header.Sender, original.Header.Sender)
	}
	if decoded.Header.Level == nil || *decoded.Header.Level != *original.Header.Level {
		t.Errorf("level: got %v, want %v", decoded.Header.Level, original.Header.Level)
	}

	// Verify LIE content
	if decoded.Content.LIE == nil {
		t.Fatal("decoded LIE is nil")
	}
	lie := decoded.Content.LIE
	if lie.Name != "leaf1" {
		t.Errorf("name: got %q, want %q", lie.Name, "leaf1")
	}
	if lie.LocalID != 1 {
		t.Errorf("local_id: got %d, want 1", lie.LocalID)
	}
	if lie.FloodPort != DefaultTIEUDPFloodPort {
		t.Errorf("flood_port: got %d, want %d", lie.FloodPort, DefaultTIEUDPFloodPort)
	}
	if lie.Holdtime != DefaultLIEHoldtime {
		t.Errorf("holdtime: got %d, want %d", lie.Holdtime, DefaultLIEHoldtime)
	}
	if lie.Neighbor == nil {
		t.Fatal("neighbor is nil")
	}
	if lie.Neighbor.Originator != 67890 {
		t.Errorf("neighbor originator: got %d, want 67890", lie.Neighbor.Originator)
	}
	if lie.Neighbor.RemoteID != 2 {
		t.Errorf("neighbor remote_id: got %d, want 2", lie.Neighbor.RemoteID)
	}
	if lie.LinkCapabilities == nil {
		t.Fatal("link_capabilities is nil")
	}
	if lie.YouAreFloodRepeater == nil || !*lie.YouAreFloodRepeater {
		t.Error("you_are_flood_repeater: expected true")
	}
}

func TestNodeTIERoundTrip(t *testing.T) {
	original := &ProtocolPacket{
		Header: PacketHeader{
			MajorVersion: ProtocolMajorVersion,
			MinorVersion: ProtocolMinorVersion,
			Sender:       100,
		},
		Content: PacketContent{
			TIE: &TIEPacket{
				Header: TIEHeader{
					TIEID: TIEID{
						Direction:  TieDirectionNorth,
						Originator: 100,
						TIEType:    TIETypeNodeTIEType,
						TIENr:      1,
					},
					SeqNr: 1,
				},
				Element: TIEElement{
					Node: &NodeTIEElement{
						Level: 0,
						Neighbors: map[SystemIDType]*NodeNeighborsTIEElement{
							200: {
								Level: 1,
								Cost:  i32Ptr(1),
								LinkIDs: []LinkIDPair{
									{LocalID: 1, RemoteID: 1},
								},
							},
						},
						Capabilities: NodeCapabilities{
							ProtocolMinorVersion: ProtocolMinorVersion,
						},
						Name: "leaf1",
					},
				},
			},
		},
	}

	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	if err := enc.EncodeProtocolPacket(original); err != nil {
		t.Fatalf("encode: %v", err)
	}

	dec := NewDecoder(bytes.NewReader(buf.Bytes()))
	decoded, err := dec.DecodeProtocolPacket()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if decoded.Content.TIE == nil {
		t.Fatal("decoded TIE is nil")
	}
	tie := decoded.Content.TIE
	if tie.Header.TIEID.Direction != TieDirectionNorth {
		t.Errorf("direction: got %d, want %d", tie.Header.TIEID.Direction, TieDirectionNorth)
	}
	if tie.Header.TIEID.Originator != 100 {
		t.Errorf("originator: got %d, want 100", tie.Header.TIEID.Originator)
	}
	if tie.Element.Node == nil {
		t.Fatal("node element is nil")
	}
	node := tie.Element.Node
	if node.Name != "leaf1" {
		t.Errorf("name: got %q, want %q", node.Name, "leaf1")
	}
	if len(node.Neighbors) != 1 {
		t.Fatalf("neighbors count: got %d, want 1", len(node.Neighbors))
	}
	nbr, ok := node.Neighbors[200]
	if !ok {
		t.Fatal("neighbor 200 not found")
	}
	if nbr.Level != 1 {
		t.Errorf("neighbor level: got %d, want 1", nbr.Level)
	}
	if nbr.Cost == nil || *nbr.Cost != 1 {
		t.Errorf("neighbor cost: got %v, want 1", nbr.Cost)
	}
}

func TestPrefixTIERoundTrip(t *testing.T) {
	original := &ProtocolPacket{
		Header: PacketHeader{
			MajorVersion: ProtocolMajorVersion,
			MinorVersion: ProtocolMinorVersion,
			Sender:       100,
		},
		Content: PacketContent{
			TIE: &TIEPacket{
				Header: TIEHeader{
					TIEID: TIEID{
						Direction:  TieDirectionNorth,
						Originator: 100,
						TIEType:    TIETypePrefixTIEType,
						TIENr:      1,
					},
					SeqNr: 1,
				},
				Element: TIEElement{
					Prefixes: &PrefixTIEElement{
						Prefixes: []PrefixEntry{
							{
								Prefix: IPPrefixType{
									IPv4Prefix: &IPv4PrefixType{
										Address:   0x0a000001, // 10.0.0.1
										PrefixLen: 32,
									},
								},
								Attributes: PrefixAttributes{
									Metric:   1,
									Loopback: boolPtr(true),
								},
							},
							{
								Prefix: IPPrefixType{
									IPv4Prefix: &IPv4PrefixType{
										Address:   -0x3f57ff00, // 192.168.1.0 as signed int32
										PrefixLen: 24,
									},
								},
								Attributes: PrefixAttributes{
									Metric:           1,
									DirectlyAttached: boolPtr(true),
								},
							},
						},
					},
				},
			},
		},
	}

	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	if err := enc.EncodeProtocolPacket(original); err != nil {
		t.Fatalf("encode: %v", err)
	}

	dec := NewDecoder(bytes.NewReader(buf.Bytes()))
	decoded, err := dec.DecodeProtocolPacket()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if decoded.Content.TIE == nil {
		t.Fatal("decoded TIE is nil")
	}
	prefixes := decoded.Content.TIE.Element.Prefixes
	if prefixes == nil {
		t.Fatal("prefix element is nil")
	}
	if len(prefixes.Prefixes) != 2 {
		t.Fatalf("prefix count: got %d, want 2", len(prefixes.Prefixes))
	}

	p0 := prefixes.Prefixes[0]
	if p0.Prefix.IPv4Prefix == nil {
		t.Fatal("first prefix IPv4 is nil")
	}
	if p0.Prefix.IPv4Prefix.Address != 0x0a000001 {
		t.Errorf("first prefix address: got 0x%08X, want 0x0A000001", p0.Prefix.IPv4Prefix.Address)
	}
	if p0.Prefix.IPv4Prefix.PrefixLen != 32 {
		t.Errorf("first prefix len: got %d, want 32", p0.Prefix.IPv4Prefix.PrefixLen)
	}
	if p0.Attributes.Loopback == nil || !*p0.Attributes.Loopback {
		t.Error("first prefix loopback: expected true")
	}
}

func TestEnvelopeRoundTrip(t *testing.T) {
	payload := []byte("test protocol packet payload")
	original := &SecurityEnvelope{
		PacketNumber:      1,
		OuterKeyID:        OuterSecurityKeyID(UndefinedSecurityKeyID),
		NonceLocal:        42,
		NonceRemote:       43,
		RemainingLifetime: DefaultLifetime,
		Payload:           payload,
	}

	var buf bytes.Buffer
	if err := EncodeEnvelope(&buf, original); err != nil {
		t.Fatalf("encode envelope: %v", err)
	}

	decoded, err := DecodeEnvelope(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("decode envelope: %v", err)
	}

	if decoded.PacketNumber != original.PacketNumber {
		t.Errorf("packet number: got %d, want %d", decoded.PacketNumber, original.PacketNumber)
	}
	if decoded.NonceLocal != original.NonceLocal {
		t.Errorf("nonce local: got %d, want %d", decoded.NonceLocal, original.NonceLocal)
	}
	if decoded.NonceRemote != original.NonceRemote {
		t.Errorf("nonce remote: got %d, want %d", decoded.NonceRemote, original.NonceRemote)
	}
	if decoded.RemainingLifetime != original.RemainingLifetime {
		t.Errorf("remaining lifetime: got %d, want %d", decoded.RemainingLifetime, original.RemainingLifetime)
	}
	if !bytes.Equal(decoded.Payload, original.Payload) {
		t.Errorf("payload: got %q, want %q", decoded.Payload, original.Payload)
	}
}

// thriftFieldHeader returns the binary encoding of a Thrift field header.
func thriftFieldHeader(fieldType byte, fieldID int16) []byte {
	var buf [3]byte
	buf[0] = fieldType
	binary.BigEndian.PutUint16(buf[1:], uint16(fieldID))
	return buf[:]
}

// thriftListHeader returns element-type byte + i32 count for a list/set.
func thriftListHeader(elemType byte, count int32) []byte {
	var buf [5]byte
	buf[0] = elemType
	binary.BigEndian.PutUint32(buf[1:], uint32(count))
	return buf[:]
}

// thriftMapHeader returns key-type, value-type, i32 count for a map.
func thriftMapHeader(keyType, valType byte, count int32) []byte {
	var buf [6]byte
	buf[0] = keyType
	buf[1] = valType
	binary.BigEndian.PutUint32(buf[2:], uint32(count))
	return buf[:]
}

func TestDecoderBoundsCheck(t *testing.T) {
	huge := int32(0x7FFFFFFF)

	tests := []struct {
		name   string
		decode func(d *Decoder) error
		data   []byte
	}{
		{
			name: "TIDE oversized headers list",
			decode: func(d *Decoder) error {
				var p TIDEPacket
				return d.decodeTIDEPacket(&p)
			},
			// field 3 (list<struct>), huge count
			data: append(thriftFieldHeader(thriftTypeList, 3),
				thriftListHeader(thriftTypeStruct, huge)...),
		},
		{
			name: "TIRE oversized headers set",
			decode: func(d *Decoder) error {
				var p TIREPacket
				return d.decodeTIREPacket(&p)
			},
			// field 1 (set<struct>), huge count
			data: append(thriftFieldHeader(thriftTypeSet, 1),
				thriftListHeader(thriftTypeStruct, huge)...),
		},
		{
			name: "NodeTIE oversized neighbors map",
			decode: func(d *Decoder) error {
				var n NodeTIEElement
				return d.decodeNodeTIEElement(&n)
			},
			// field 3 (level, i16), then field 2 (map<i64,struct>), huge count
			data: func() []byte {
				var buf []byte
				// field 3 (level) - skip it to reach field 2...
				// Actually field 2 is the neighbors map.
				buf = append(buf, thriftFieldHeader(thriftTypeMap, 2)...)
				buf = append(buf, thriftMapHeader(thriftTypeI64, thriftTypeStruct, huge)...)
				return buf
			}(),
		},
		{
			name: "PrefixTIE oversized prefixes map",
			decode: func(d *Decoder) error {
				var p PrefixTIEElement
				return d.decodePrefixTIEElement(&p)
			},
			// field 1 (map<struct,struct>), huge count
			data: append(thriftFieldHeader(thriftTypeMap, 1),
				thriftMapHeader(thriftTypeStruct, thriftTypeStruct, huge)...),
		},
		{
			name: "negative collection length",
			decode: func(d *Decoder) error {
				var p TIDEPacket
				return d.decodeTIDEPacket(&p)
			},
			data: append(thriftFieldHeader(thriftTypeList, 3),
				thriftListHeader(thriftTypeStruct, -1)...),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dec := NewDecoder(bytes.NewReader(tt.data))
			err := tt.decode(dec)
			if err == nil {
				t.Fatal("expected error for oversized collection, got nil")
			}
		})
	}
}
