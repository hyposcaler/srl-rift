package tie

import (
	"sort"
	"sync"
	"time"

	"github.com/hyposcaler/srl-rift/internal/encoding"
)

// LSDBEntry holds one TIE in the database.
type LSDBEntry struct {
	Packet            *encoding.TIEPacket
	RemainingLifetime encoding.LifeTimeInSecType
	LastReceived      time.Time
	SelfOriginated    bool
}

// Header returns a TIEHeaderWithLifeTime for this entry.
func (e *LSDBEntry) Header() encoding.TIEHeaderWithLifeTime {
	return encoding.TIEHeaderWithLifeTime{
		Header:            e.Packet.Header,
		RemainingLifetime: e.RemainingLifetime,
	}
}

// LSDB is the link-state database. All TIEs are indexed by TIEID.
// Thread-safe via sync.RWMutex.
type LSDB struct {
	mu      sync.RWMutex
	entries map[encoding.TIEID]*LSDBEntry
}

// NewLSDB creates an empty LSDB.
func NewLSDB() *LSDB {
	return &LSDB{
		entries: make(map[encoding.TIEID]*LSDBEntry),
	}
}

// Get returns the entry for the given TIEID, or nil if not found.
func (db *LSDB) Get(id encoding.TIEID) *LSDBEntry {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.entries[id]
}

// Insert adds or replaces a TIE in the LSDB. Returns true if the entry
// is new (not a replacement).
func (db *LSDB) Insert(entry *LSDBEntry) bool {
	id := entry.Packet.Header.TIEID
	db.mu.Lock()
	defer db.mu.Unlock()
	_, existed := db.entries[id]
	db.entries[id] = entry
	return !existed
}

// Remove deletes a TIE from the LSDB.
func (db *LSDB) Remove(id encoding.TIEID) {
	db.mu.Lock()
	defer db.mu.Unlock()
	delete(db.entries, id)
}

// Len returns the number of TIEs in the LSDB.
func (db *LSDB) Len() int {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return len(db.entries)
}

// ForEachSorted iterates over all entries in TIEID total order.
// The callback returns false to stop iteration.
func (db *LSDB) ForEachSorted(fn func(encoding.TIEID, *LSDBEntry) bool) {
	db.mu.RLock()
	ids := make([]encoding.TIEID, 0, len(db.entries))
	for id := range db.entries {
		ids = append(ids, id)
	}
	db.mu.RUnlock()

	sort.Slice(ids, func(i, j int) bool {
		return CompareTIEID(ids[i], ids[j]) < 0
	})

	db.mu.RLock()
	defer db.mu.RUnlock()
	for _, id := range ids {
		entry, ok := db.entries[id]
		if !ok {
			continue // removed between snapshot and iteration
		}
		if !fn(id, entry) {
			return
		}
	}
}

// HeadersSorted returns all TIE headers in TIEID total order.
func (db *LSDB) HeadersSorted() []encoding.TIEHeaderWithLifeTime {
	db.mu.RLock()
	defer db.mu.RUnlock()

	ids := make([]encoding.TIEID, 0, len(db.entries))
	for id := range db.entries {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		return CompareTIEID(ids[i], ids[j]) < 0
	})

	headers := make([]encoding.TIEHeaderWithLifeTime, 0, len(ids))
	for _, id := range ids {
		entry := db.entries[id]
		headers = append(headers, entry.Header())
	}
	return headers
}

// Snapshot returns a consistent copy of all LSDB entries under a single
// read lock. Used by SPF for point-in-time computation.
func (db *LSDB) Snapshot() map[encoding.TIEID]*LSDBEntry {
	db.mu.RLock()
	defer db.mu.RUnlock()
	result := make(map[encoding.TIEID]*LSDBEntry, len(db.entries))
	for id, entry := range db.entries {
		result[id] = entry
	}
	return result
}

// DecrementLifetimes decreases remaining_lifetime by delta seconds for all
// entries. Returns the TIEIDs of entries that have expired (lifetime <= 0).
func (db *LSDB) DecrementLifetimes(delta int32) []encoding.TIEID {
	db.mu.Lock()
	defer db.mu.Unlock()

	var expired []encoding.TIEID
	for id, entry := range db.entries {
		entry.RemainingLifetime -= encoding.LifeTimeInSecType(delta)
		if entry.RemainingLifetime <= 0 {
			expired = append(expired, id)
		}
	}
	return expired
}

// CompareTIEID returns -1, 0, or 1 comparing a and b in TIEID total order.
// Order: Direction, Originator, TIEType, TIENr.
func CompareTIEID(a, b encoding.TIEID) int {
	if a.Direction != b.Direction {
		return cmpInt32(int32(a.Direction), int32(b.Direction))
	}
	if a.Originator != b.Originator {
		return cmpInt64(a.Originator, b.Originator)
	}
	if a.TIEType != b.TIEType {
		return cmpInt32(int32(a.TIEType), int32(b.TIEType))
	}
	if a.TIENr != b.TIENr {
		return cmpInt32(a.TIENr, b.TIENr)
	}
	return 0
}

// CompareTIEHeader returns -1, 0, or 1 comparing two TIE headers.
// Compares by SeqNr first, then RemainingLifetime (ignoring differences
// less than LifetimeDiff2Ignore).
func CompareTIEHeader(a, b encoding.TIEHeaderWithLifeTime) int {
	if a.Header.SeqNr != b.Header.SeqNr {
		return cmpInt64(a.Header.SeqNr, b.Header.SeqNr)
	}
	diff := a.RemainingLifetime - b.RemainingLifetime
	if diff < 0 {
		diff = -diff
	}
	if diff > encoding.LifetimeDiff2Ignore {
		return cmpInt32(int32(a.RemainingLifetime), int32(b.RemainingLifetime))
	}
	return 0
}

// MinTIEID is the smallest possible TIEID.
var MinTIEID = encoding.TIEID{
	Direction:  encoding.TieDirectionSouth,
	Originator: 0,
	TIEType:    0,
	TIENr:      0,
}

// MaxTIEID is the largest possible TIEID.
var MaxTIEID = encoding.TIEID{
	Direction:  encoding.TieDirectionDirectionMaxValue,
	Originator: 0x7FFFFFFFFFFFFFFF,
	TIEType:    encoding.TIETypeMaxValue,
	TIENr:      0x7FFFFFFF,
}

func cmpInt32(a, b int32) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func cmpInt64(a, b int64) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
