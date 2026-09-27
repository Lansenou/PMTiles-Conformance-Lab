package fixtures

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"
)

// Minimal SQLite 3 database writer for the MBTiles fixture
// (https://www.sqlite.org/fileformat2.html). Every table and index is a
// single leaf page, so there are no interior pages, overflow pages or
// freelist. Output depends only on this source, not on an SQLite library;
// tests open the result with a real SQLite engine and run
// PRAGMA integrity_check.

const sqlitePageSize = 4096

// sqlTable is a rowid table; rows get rowids 1..n in order.
type sqlTable struct {
	name, sql string
	rows      [][]any // int64, string or []byte
}

// sqlIndex indexes columns cols of table (by position in tables).
type sqlIndex struct {
	name, sql string
	table     int
	cols      []int
}

// sqliteVarint is SQLite's big-endian varint (fileformat2 §1.6), for values
// below 2^56.
func sqliteVarint(v uint64) []byte {
	if v >= 1<<56 {
		panic("sqliteVarint: value too large")
	}
	var tmp []byte
	for {
		tmp = append(tmp, byte(v&0x7f))
		v >>= 7
		if v == 0 {
			break
		}
	}
	out := make([]byte, len(tmp))
	for i := range tmp {
		b := tmp[len(tmp)-1-i]
		if i < len(tmp)-1 {
			b |= 0x80
		}
		out[i] = b
	}
	return out
}

// sqliteRecord encodes a record (fileformat2 §2.1).
func sqliteRecord(vals []any) []byte {
	var types, body []byte
	for _, v := range vals {
		switch v := v.(type) {
		case int64:
			switch {
			case v == 0:
				types = append(types, sqliteVarint(8)...)
			case v == 1:
				types = append(types, sqliteVarint(9)...)
			case v >= -128 && v <= 127:
				types = append(types, sqliteVarint(1)...)
				body = append(body, byte(v))
			case v >= -32768 && v <= 32767:
				types = append(types, sqliteVarint(2)...)
				body = binary.BigEndian.AppendUint16(body, uint16(v))
			case v >= -1<<31 && v < 1<<31:
				types = append(types, sqliteVarint(4)...)
				body = binary.BigEndian.AppendUint32(body, uint32(v))
			default:
				types = append(types, sqliteVarint(6)...)
				body = binary.BigEndian.AppendUint64(body, uint64(v))
			}
		case string:
			types = append(types, sqliteVarint(uint64(13+2*len(v)))...)
			body = append(body, v...)
		case []byte:
			types = append(types, sqliteVarint(uint64(12+2*len(v)))...)
			body = append(body, v...)
		default:
			panic(fmt.Sprintf("sqliteRecord: unsupported %T", v))
		}
	}
	// The header size varint counts itself; every header here is < 128 bytes.
	if len(types)+1 > 127 {
		panic("sqliteRecord: header too long")
	}
	out := append([]byte{byte(len(types) + 1)}, types...)
	return append(out, body...)
}

// sqliteLeaf builds one leaf b-tree page (type 13 table, 10 index) holding
// cells in the given order. hdrOff is 100 on page 1, else 0.
func sqliteLeaf(kind byte, hdrOff int, cells [][]byte) []byte {
	p := make([]byte, sqlitePageSize)
	ptr := hdrOff + 8
	end := sqlitePageSize
	for _, c := range cells {
		end -= len(c)
		if end < ptr+2*len(cells) {
			panic("sqliteLeaf: cells do not fit in one page")
		}
		copy(p[end:], c)
		binary.BigEndian.PutUint16(p[ptr:], uint16(end))
		ptr += 2
	}
	p[hdrOff] = kind
	binary.BigEndian.PutUint16(p[hdrOff+3:], uint16(len(cells)))
	binary.BigEndian.PutUint16(p[hdrOff+5:], uint16(end))
	return p
}

// Largest payload stored without overflow for a 4096-byte page
// (fileformat2 §1.6: U-35 for table leaves, ((U-12)*64/255)-23 for indexes).
const (
	sqliteMaxTableLocal = sqlitePageSize - 35
	sqliteMaxIndexLocal = (sqlitePageSize-12)*64/255 - 23
)

func tableLeaf(hdrOff int, rows [][]any) []byte {
	var cells [][]byte
	for i, r := range rows {
		rec := sqliteRecord(r)
		if len(rec) > sqliteMaxTableLocal {
			panic("sqlite: row needs an overflow page")
		}
		c := append(sqliteVarint(uint64(len(rec))), sqliteVarint(uint64(i+1))...)
		cells = append(cells, append(c, rec...))
	}
	return sqliteLeaf(13, hdrOff, cells)
}

// compareKey orders index keys of int64 values.
func compareKey(a, b []any) int {
	for i := range a {
		x, y := a[i].(int64), b[i].(int64)
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func indexLeaf(t sqlTable, cols []int) []byte {
	var keys [][]any
	for i, r := range t.rows {
		var k []any
		for _, c := range cols {
			k = append(k, r[c])
		}
		keys = append(keys, append(k, int64(i+1)))
	}
	sort.Slice(keys, func(i, j int) bool { return compareKey(keys[i], keys[j]) < 0 })
	var cells [][]byte
	for i, k := range keys {
		if i > 0 && compareKey(keys[i-1][:len(cols)], k[:len(cols)]) == 0 {
			panic("sqlite: duplicate key in unique index")
		}
		rec := sqliteRecord(k)
		if len(rec) > sqliteMaxIndexLocal {
			panic("sqlite: index key needs an overflow page")
		}
		cells = append(cells, append(sqliteVarint(uint64(len(rec))), rec...))
	}
	return sqliteLeaf(10, 0, cells)
}

// sqliteDB returns a database file with the tables on pages 2.. and the
// indexes after them, and appID in the header.
func sqliteDB(tables []sqlTable, indexes []sqlIndex, appID uint32) []byte {
	var schema [][]any
	var pages [][]byte
	next := int64(2)
	for _, t := range tables {
		schema = append(schema, []any{"table", t.name, t.name, next, t.sql})
		pages = append(pages, tableLeaf(0, t.rows))
		next++
	}
	for _, ix := range indexes {
		t := tables[ix.table]
		schema = append(schema, []any{"index", ix.name, t.name, next, ix.sql})
		pages = append(pages, indexLeaf(t, ix.cols))
		next++
	}
	page1 := tableLeaf(100, schema)
	h := page1[:100]
	copy(h, "SQLite format 3\x00")
	binary.BigEndian.PutUint16(h[16:], sqlitePageSize)
	h[18], h[19] = 1, 1                   // legacy (rollback journal) read/write versions
	h[21], h[22], h[23] = 64, 32, 32      // payload fractions, fixed by the format
	binary.BigEndian.PutUint32(h[24:], 1) // file change counter
	binary.BigEndian.PutUint32(h[28:], uint32(1+len(pages)))
	binary.BigEndian.PutUint32(h[40:], 1) // schema cookie
	binary.BigEndian.PutUint32(h[44:], 4) // schema format 4
	binary.BigEndian.PutUint32(h[56:], 1) // UTF-8
	binary.BigEndian.PutUint32(h[68:], appID)
	binary.BigEndian.PutUint32(h[92:], 1) // version-valid-for = change counter
	// SQLITE_VERSION_NUMBER of the last writer. This writer is not SQLite;
	// 3000000 is the lowest version MBTiles 1.3 requires.
	binary.BigEndian.PutUint32(h[96:], 3000000)
	var out bytes.Buffer
	out.Write(page1)
	for _, p := range pages {
		out.Write(p)
	}
	return out.Bytes()
}
