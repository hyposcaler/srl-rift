package tie

import (
	"testing"
	"time"

	"github.com/hyposcaler/srl-rift/internal/encoding"
)

func TestCompareTIEID(t *testing.T) {
	tests := []struct {
		name string
		a, b encoding.TIEID
		want int
	}{
		{
			name: "equal",
			a:    encoding.TIEID{Direction: encoding.TieDirectionNorth, Originator: 1, TIEType: encoding.TIETypeNodeTIEType, TIENr: 1},
			b:    encoding.TIEID{Direction: encoding.TieDirectionNorth, Originator: 1, TIEType: encoding.TIETypeNodeTIEType, TIENr: 1},
			want: 0,
		},
		{
			name: "direction_south_before_north",
			a:    encoding.TIEID{Direction: encoding.TieDirectionSouth, Originator: 1, TIEType: encoding.TIETypeNodeTIEType, TIENr: 1},
			b:    encoding.TIEID{Direction: encoding.TieDirectionNorth, Originator: 1, TIEType: encoding.TIETypeNodeTIEType, TIENr: 1},
			want: -1,
		},
		{
			name: "originator_less",
			a:    encoding.TIEID{Direction: encoding.TieDirectionNorth, Originator: 1, TIEType: encoding.TIETypeNodeTIEType, TIENr: 1},
			b:    encoding.TIEID{Direction: encoding.TieDirectionNorth, Originator: 2, TIEType: encoding.TIETypeNodeTIEType, TIENr: 1},
			want: -1,
		},
		{
			name: "type_node_before_prefix",
			a:    encoding.TIEID{Direction: encoding.TieDirectionNorth, Originator: 1, TIEType: encoding.TIETypeNodeTIEType, TIENr: 1},
			b:    encoding.TIEID{Direction: encoding.TieDirectionNorth, Originator: 1, TIEType: encoding.TIETypePrefixTIEType, TIENr: 1},
			want: -1,
		},
		{
			name: "tienr_less",
			a:    encoding.TIEID{Direction: encoding.TieDirectionNorth, Originator: 1, TIEType: encoding.TIETypeNodeTIEType, TIENr: 1},
			b:    encoding.TIEID{Direction: encoding.TieDirectionNorth, Originator: 1, TIEType: encoding.TIETypeNodeTIEType, TIENr: 2},
			want: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CompareTIEID(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("CompareTIEID(%v, %v) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
			// Test symmetry.
			reverse := CompareTIEID(tt.b, tt.a)
			if reverse != -tt.want {
				t.Errorf("CompareTIEID(%v, %v) = %d, want %d (reverse)", tt.b, tt.a, reverse, -tt.want)
			}
		})
	}
}

func TestCompareTIEHeader(t *testing.T) {
	tests := []struct {
		name string
		a, b encoding.TIEHeaderWithLifeTime
		want int
	}{
		{
			name: "equal_seq_and_lifetime",
			a:    encoding.TIEHeaderWithLifeTime{Header: encoding.TIEHeader{SeqNr: 5}, RemainingLifetime: 1000},
			b:    encoding.TIEHeaderWithLifeTime{Header: encoding.TIEHeader{SeqNr: 5}, RemainingLifetime: 1000},
			want: 0,
		},
		{
			name: "higher_seq_wins",
			a:    encoding.TIEHeaderWithLifeTime{Header: encoding.TIEHeader{SeqNr: 6}, RemainingLifetime: 100},
			b:    encoding.TIEHeaderWithLifeTime{Header: encoding.TIEHeader{SeqNr: 5}, RemainingLifetime: 1000},
			want: 1,
		},
		{
			name: "same_seq_lifetime_diff_ignored_below_threshold",
			a:    encoding.TIEHeaderWithLifeTime{Header: encoding.TIEHeader{SeqNr: 5}, RemainingLifetime: 1000},
			b:    encoding.TIEHeaderWithLifeTime{Header: encoding.TIEHeader{SeqNr: 5}, RemainingLifetime: 700},
			want: 0, // diff=300 < LifetimeDiff2Ignore=400
		},
		{
			name: "same_seq_lifetime_diff_significant",
			a:    encoding.TIEHeaderWithLifeTime{Header: encoding.TIEHeader{SeqNr: 5}, RemainingLifetime: 1000},
			b:    encoding.TIEHeaderWithLifeTime{Header: encoding.TIEHeader{SeqNr: 5}, RemainingLifetime: 500},
			want: 1, // diff=500 > LifetimeDiff2Ignore=400
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CompareTIEHeader(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("CompareTIEHeader() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestLSDB_InsertGetRemove(t *testing.T) {
	db := NewLSDB()

	id := encoding.TIEID{
		Direction:  encoding.TieDirectionNorth,
		Originator: 101,
		TIEType:    encoding.TIETypeNodeTIEType,
		TIENr:      1,
	}

	// Get on empty LSDB returns nil.
	if got := db.Get(id); got != nil {
		t.Fatal("expected nil for missing entry")
	}

	// Insert.
	entry := &LSDBEntry{
		Packet: &encoding.TIEPacket{
			Header:  encoding.TIEHeader{TIEID: id, SeqNr: 1},
			Element: encoding.TIEElement{Node: &encoding.NodeTIEElement{Level: 0}},
		},
		RemainingLifetime: encoding.DefaultLifetime,
		LastReceived:      time.Now(),
		SelfOriginated:    true,
	}
	isNew := db.Insert(entry)
	if !isNew {
		t.Error("Insert should return true for new entry")
	}
	if db.Len() != 1 {
		t.Errorf("Len = %d, want 1", db.Len())
	}

	// Get returns the entry.
	got := db.Get(id)
	if got == nil {
		t.Fatal("expected entry, got nil")
	}
	if got.Packet.Header.SeqNr != 1 {
		t.Errorf("SeqNr = %d, want 1", got.Packet.Header.SeqNr)
	}

	// Replace.
	entry2 := &LSDBEntry{
		Packet: &encoding.TIEPacket{
			Header:  encoding.TIEHeader{TIEID: id, SeqNr: 2},
			Element: encoding.TIEElement{Node: &encoding.NodeTIEElement{Level: 0}},
		},
		RemainingLifetime: encoding.DefaultLifetime,
		LastReceived:      time.Now(),
		SelfOriginated:    true,
	}
	isNew = db.Insert(entry2)
	if isNew {
		t.Error("Insert should return false for replacement")
	}
	if db.Len() != 1 {
		t.Errorf("Len = %d, want 1 after replace", db.Len())
	}
	got = db.Get(id)
	if got.Packet.Header.SeqNr != 2 {
		t.Errorf("SeqNr = %d, want 2 after replace", got.Packet.Header.SeqNr)
	}

	// Remove.
	db.Remove(id)
	if db.Len() != 0 {
		t.Errorf("Len = %d, want 0 after remove", db.Len())
	}
	if got := db.Get(id); got != nil {
		t.Error("expected nil after remove")
	}
}

func TestLSDB_HeadersSorted(t *testing.T) {
	db := NewLSDB()

	// Insert in reverse order.
	ids := []encoding.TIEID{
		{Direction: encoding.TieDirectionNorth, Originator: 102, TIEType: encoding.TIETypePrefixTIEType, TIENr: 1},
		{Direction: encoding.TieDirectionNorth, Originator: 101, TIEType: encoding.TIETypeNodeTIEType, TIENr: 1},
		{Direction: encoding.TieDirectionSouth, Originator: 1, TIEType: encoding.TIETypeNodeTIEType, TIENr: 1},
	}
	for i, id := range ids {
		db.Insert(&LSDBEntry{
			Packet: &encoding.TIEPacket{
				Header:  encoding.TIEHeader{TIEID: id, SeqNr: encoding.SeqNrType(i + 1)},
				Element: encoding.TIEElement{Node: &encoding.NodeTIEElement{}},
			},
			RemainingLifetime: encoding.DefaultLifetime,
			LastReceived:      time.Now(),
		})
	}

	headers := db.HeadersSorted()
	if len(headers) != 3 {
		t.Fatalf("got %d headers, want 3", len(headers))
	}

	// Should be sorted: South before North, then by originator.
	if headers[0].Header.TIEID.Direction != encoding.TieDirectionSouth {
		t.Error("first header should be South")
	}
	if headers[1].Header.TIEID.Originator != 101 {
		t.Errorf("second header originator = %d, want 101", headers[1].Header.TIEID.Originator)
	}
	if headers[2].Header.TIEID.Originator != 102 {
		t.Errorf("third header originator = %d, want 102", headers[2].Header.TIEID.Originator)
	}
}

func TestLSDB_DecrementLifetimes(t *testing.T) {
	db := NewLSDB()

	id1 := encoding.TIEID{Direction: encoding.TieDirectionNorth, Originator: 1, TIEType: encoding.TIETypeNodeTIEType, TIENr: 1}
	id2 := encoding.TIEID{Direction: encoding.TieDirectionNorth, Originator: 2, TIEType: encoding.TIETypeNodeTIEType, TIENr: 1}

	db.Insert(&LSDBEntry{
		Packet:            &encoding.TIEPacket{Header: encoding.TIEHeader{TIEID: id1, SeqNr: 1}},
		RemainingLifetime: 10,
		LastReceived:      time.Now(),
	})
	db.Insert(&LSDBEntry{
		Packet:            &encoding.TIEPacket{Header: encoding.TIEHeader{TIEID: id2, SeqNr: 1}},
		RemainingLifetime: 100,
		LastReceived:      time.Now(),
	})

	expired := db.DecrementLifetimes(11)
	if len(expired) != 1 {
		t.Fatalf("got %d expired, want 1", len(expired))
	}
	if expired[0] != id1 {
		t.Errorf("expired[0] = %v, want %v", expired[0], id1)
	}

	// id2 should still be alive with lifetime 89.
	entry := db.Get(id2)
	if entry == nil {
		t.Fatal("id2 should still exist")
	}
	if entry.RemainingLifetime != 89 {
		t.Errorf("id2 lifetime = %d, want 89", entry.RemainingLifetime)
	}
}

func TestSnapshotDeepCopy(t *testing.T) {
	db := NewLSDB()

	origLifetime := encoding.LifeTimeInSecType(3600)
	id := encoding.TIEID{
		Direction:  encoding.TieDirectionNorth,
		Originator: 100,
		TIEType:    encoding.TIETypeNodeTIEType,
		TIENr:      1,
	}
	db.Insert(&LSDBEntry{
		Packet: &encoding.TIEPacket{
			Header: encoding.TIEHeader{
				TIEID:               id,
				SeqNr:               1,
				OriginationLifetime: &origLifetime,
			},
		},
		RemainingLifetime: 3600,
		LastReceived:      time.Now(),
		SelfOriginated:    true,
	})

	// Take snapshot.
	snap := db.Snapshot()

	// Mutate the live entry (simulating bumpOwnTIE).
	live := db.Get(id)
	live.Packet.Header.SeqNr = 99
	newLT := encoding.LifeTimeInSecType(7200)
	live.Packet.Header.OriginationLifetime = &newLT

	// Snapshot must be unaffected.
	snapEntry := snap[id]
	if snapEntry.Packet.Header.SeqNr != 1 {
		t.Errorf("snapshot SeqNr = %d, want 1", snapEntry.Packet.Header.SeqNr)
	}
	if *snapEntry.Packet.Header.OriginationLifetime != 3600 {
		t.Errorf("snapshot OriginationLifetime = %d, want 3600",
			*snapEntry.Packet.Header.OriginationLifetime)
	}

	// Decrement lifetimes on live LSDB.
	db.DecrementLifetimes(10)

	// Snapshot RemainingLifetime must be unaffected.
	if snapEntry.RemainingLifetime != 3600 {
		t.Errorf("snapshot RemainingLifetime = %d after DecrementLifetimes, want 3600",
			snapEntry.RemainingLifetime)
	}
}
