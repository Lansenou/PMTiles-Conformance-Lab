package pmtiles

import "sort"

// Entry is one directory entry (spec §4.1). RunLength 0 marks a leaf
// directory entry.
type Entry struct {
	TileID    uint64 `json:"tile_id"`
	Offset    uint64 `json:"offset"`
	Length    uint64 `json:"length"`
	RunLength uint64 `json:"run_length"`
}

// EncodeDirectory serialises entries without compression (spec §4.2, A.1).
// It does not validate its input so that malformed fixtures can be built.
func EncodeDirectory(entries []Entry) []byte {
	b := appendUvarint(nil, uint64(len(entries)))
	var last uint64
	for _, e := range entries {
		b = appendUvarint(b, e.TileID-last)
		last = e.TileID
	}
	for _, e := range entries {
		b = appendUvarint(b, e.RunLength)
	}
	for _, e := range entries {
		b = appendUvarint(b, e.Length)
	}
	var next uint64
	for i, e := range entries {
		if i > 0 && e.Offset == next {
			b = appendUvarint(b, 0)
		} else {
			b = appendUvarint(b, e.Offset+1)
		}
		next = e.Offset + e.Length
	}
	return b
}

// DecodeDirectory parses an uncompressed directory (spec §4.3, A.2). The
// entry count is checked against maxEntries and against the bytes available
// (every entry needs at least four one-byte varints) before allocation.
func DecodeDirectory(b []byte, maxEntries int) ([]Entry, error) {
	n, pos, err := readUvarint(b, 0)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, errf(CodeEmptyDirectory, "directory has 0 entries")
	}
	if n > uint64(maxEntries) {
		return nil, errf(CodeTooManyEntries, "directory claims %d entries, limit %d", n, maxEntries)
	}
	if n > uint64(len(b)-pos)/4 {
		return nil, errf(CodeTruncatedDirectory, "directory claims %d entries but has only %d bytes after the count", n, len(b)-pos)
	}
	entries := make([]Entry, n)
	var last uint64
	for i := range entries {
		var d uint64
		if d, pos, err = readUvarint(b, pos); err != nil {
			return nil, err
		}
		id, ok := addU64(last, d)
		if !ok {
			return nil, errf(CodeTileIDOverflow, "entry %d tile id overflows uint64", i)
		}
		entries[i].TileID, last = id, id
	}
	for i := range entries {
		if entries[i].RunLength, pos, err = readUvarint(b, pos); err != nil {
			return nil, err
		}
	}
	for i := range entries {
		if entries[i].Length, pos, err = readUvarint(b, pos); err != nil {
			return nil, err
		}
		if entries[i].Length == 0 {
			return nil, errf(CodeZeroLengthEntry, "entry %d (tile id %d) has length 0", i, entries[i].TileID)
		}
	}
	for i := range entries {
		var v uint64
		if v, pos, err = readUvarint(b, pos); err != nil {
			return nil, err
		}
		switch {
		case v == 0 && i > 0:
			off, ok := addU64(entries[i-1].Offset, entries[i-1].Length)
			if !ok {
				return nil, errf(CodeInvalidOffset, "entry %d contiguous offset overflows", i)
			}
			entries[i].Offset = off
		case v == 0:
			return nil, errf(CodeInvalidOffset, "first entry uses the contiguous-offset marker 0")
		default:
			entries[i].Offset = v - 1
		}
	}
	return entries, nil
}

// findEntry returns the entry covering id: a tile entry whose run includes
// id, or the leaf entry with the greatest TileID <= id. ok is false if no
// entry can contain id.
func findEntry(entries []Entry, id uint64) (Entry, bool) {
	i := sort.Search(len(entries), func(i int) bool { return entries[i].TileID > id }) - 1
	if i < 0 {
		return Entry{}, false
	}
	e := entries[i]
	if e.RunLength == 0 {
		return e, true
	}
	if id-e.TileID < e.RunLength {
		return e, true
	}
	return Entry{}, false
}
