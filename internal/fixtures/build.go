package fixtures

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/lansenou/pmtiles-conformance-lab/internal/pmtiles"
)

// tile places one blob (by content key) at z/x/y.
type tile struct {
	z       uint8
	x, y    uint32
	content string
}

// spec describes a valid archive to build.
type spec struct {
	name        string
	description string
	features    []string
	compression pmtiles.Compression
	tiles       []tile
	absent      [][3]uint32 // z, x, y coordinates recorded as absent
	leafSize    int         // 0: root only; otherwise max tile entries per leaf
	// The fields below are set only by the MVT corpus. Zero values keep the
	// PNG fixtures byte for byte as they were.
	tileType        pmtiles.TileType    // 0: PNG
	tileCompression pmtiles.Compression // 0: none
	blobs           map[string][]byte   // nil: contents
	meta            []byte              // nil: metadataFor
}

// blobMap returns the content blobs the spec's tiles refer to.
func (s spec) blobMap() map[string][]byte {
	if s.blobs != nil {
		return s.blobs
	}
	return contents
}

// built is an assembled archive plus facts needed for the manifest.
type built struct {
	bytes  []byte
	header pmtiles.Header
	// sections, uncompressed, for malformed variants
	rootRaw []byte
	meta    []byte
	leaves  []byte
	data    []byte
	entries []pmtiles.Entry
	// rootEntries is the root directory as the builder laid it out: tile
	// entries, or leaf entries whose offsets are relative to the leaf
	// section.
	rootEntries []pmtiles.Entry
	// offsets maps a content key to its offset inside the tile data section.
	offsets map[string]uint64
}

// contents are the named tile blobs. Keys are stable.
var contents = map[string][]byte{
	"red":    solidPNG(4, 4, 0xd0, 0x30, 0x30),
	"green":  solidPNG(4, 4, 0x30, 0xa0, 0x40),
	"blue":   solidPNG(4, 4, 0x30, 0x50, 0xd0),
	"gray":   solidPNG(4, 4, 0x80, 0x80, 0x80),
	"yellow": solidPNG(4, 4, 0xe0, 0xc0, 0x20),
	"purple": solidPNG(4, 4, 0x80, 0x40, 0xb0),
	"orange": solidPNG(4, 4, 0xf0, 0x80, 0x20),
	"cyan":   solidPNG(4, 4, 0x20, 0xc0, 0xd0),
	// big (96x96, 27812 bytes) pushes every tile stored after it past the
	// first 16 KiB of the archive.
	"big": solidPNG(96, 96, 0x20, 0x80, 0x80),
	// teal (48x52, 7608 bytes) makes exact-8192 exactly 8192 bytes long.
	"teal": solidPNG(48, 52, 0x30, 0x90, 0x60),
}

func compress(c pmtiles.Compression, b []byte) []byte {
	switch c {
	case pmtiles.CompressionNone:
		return b
	case pmtiles.CompressionGzip:
		return gzipStored(b)
	case pmtiles.CompressionZstd:
		return zstdRaw(b)
	}
	panic(fmt.Sprintf("generator cannot compress with %v", c))
}

// buildEntries sorts tiles by id, deduplicates content (later tiles point
// back at the first copy) and merges consecutive ids with equal content into
// runs. Data is laid out in order of first appearance, so the archive is
// clustered. It returns the entries, the tile data section and the offset of
// each content key inside it.
func buildEntries(tiles []tile, blobs map[string][]byte) ([]pmtiles.Entry, []byte, map[string]uint64) {
	type item struct {
		id      uint64
		content string
	}
	items := make([]item, len(tiles))
	for i, t := range tiles {
		id, err := pmtiles.ZxyToID(t.z, t.x, t.y)
		if err != nil {
			panic(err)
		}
		items[i] = item{id, t.content}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].id < items[j].id })
	for i := 1; i < len(items); i++ {
		if items[i].id == items[i-1].id {
			panic(fmt.Sprintf("tile id %d listed twice", items[i].id))
		}
	}
	var data []byte
	offsets := map[string]uint64{}
	var entries []pmtiles.Entry
	for _, it := range items {
		blob, ok := blobs[it.content]
		if !ok {
			panic("unknown content " + it.content)
		}
		off, seen := offsets[it.content]
		if !seen {
			off = uint64(len(data))
			offsets[it.content] = off
			data = append(data, blob...)
		}
		if n := len(entries); n > 0 {
			last := &entries[n-1]
			if last.Offset == off && last.TileID+last.RunLength == it.id {
				last.RunLength++
				continue
			}
		}
		entries = append(entries, pmtiles.Entry{TileID: it.id, Offset: off, Length: uint64(len(blob)), RunLength: 1})
	}
	return entries, data, offsets
}

func metadataFor(s spec) []byte {
	m := struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Type        string `json:"type"`
		Version     string `json:"version"`
	}{"pmtiles-lab " + s.name, "Synthetic pmtiles-lab fixture. Solid-colour PNG tiles; not map data.", "overlay", "1.0.0"}
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return b
}

// assemble lays out header, root, metadata, leaves, tile data in spec order.
func assemble(h pmtiles.Header, root, meta, leaves, data []byte) []byte {
	return assemblePadded(h, 0, root, meta, leaves, data)
}

// assemblePadded is assemble with pad zero bytes between the header and the
// root directory. Only the root-beyond-16k fixture uses pad > 0.
func assemblePadded(h pmtiles.Header, pad int, root, meta, leaves, data []byte) []byte {
	h.Version = pmtiles.SpecVersion
	h.RootOffset = pmtiles.HeaderLen + uint64(pad)
	h.RootLength = uint64(len(root))
	h.MetadataOffset = h.RootOffset + h.RootLength
	h.MetadataLength = uint64(len(meta))
	h.LeafOffset = h.MetadataOffset + h.MetadataLength
	h.LeafLength = uint64(len(leaves))
	h.TileDataOffset = h.LeafOffset + h.LeafLength
	h.TileDataLength = uint64(len(data))
	out := h.Encode()
	out = append(out, make([]byte, pad)...)
	for _, s := range [][]byte{root, meta, leaves, data} {
		out = append(out, s...)
	}
	return out
}

func build(s spec) built { return buildWith(s, nil) }

// leafHook may replace the encoded bytes of leaf directory i. off is the
// leaf's offset inside the leaf section and firstID the TileID its root entry
// will carry. Only malformed variants use it.
type leafHook func(i int, off, firstID uint64, enc []byte) []byte

func buildWith(s spec, hook leafHook) built {
	entries, data, offsets := buildEntries(s.tiles, s.blobMap())
	var addressed uint64
	minZ, maxZ := uint8(255), uint8(0)
	for _, e := range entries {
		addressed += e.RunLength
	}
	for _, t := range s.tiles {
		minZ, maxZ = min(minZ, t.z), max(maxZ, t.z)
	}
	h := pmtiles.Header{
		AddressedTiles:      addressed,
		TileEntries:         uint64(len(entries)),
		TileContents:        uint64(len(offsets)),
		Clustered:           1,
		InternalCompression: s.compression,
		TileCompression:     pmtiles.CompressionNone,
		TileType:            pmtiles.TileTypePNG,
		MinZoom:             minZ,
		MaxZoom:             maxZ,
		MinLonE7:            pmtiles.E7(-180),
		MinLatE7:            pmtiles.E7(-85.0511287),
		MaxLonE7:            pmtiles.E7(180),
		MaxLatE7:            pmtiles.E7(85.0511287),
		CenterZoom:          minZ,
	}
	if s.tileType != 0 {
		h.TileType = s.tileType
	}
	if s.tileCompression != 0 {
		h.TileCompression = s.tileCompression
	}
	rootEntries := entries
	var leaves []byte
	if s.leafSize > 0 {
		rootEntries = nil
		for i := 0; i < len(entries); i += s.leafSize {
			chunk := entries[i:min(i+s.leafSize, len(entries))]
			enc := compress(s.compression, pmtiles.EncodeDirectory(chunk))
			if hook != nil {
				enc = hook(len(rootEntries), uint64(len(leaves)), chunk[0].TileID, enc)
			}
			rootEntries = append(rootEntries, pmtiles.Entry{TileID: chunk[0].TileID, Offset: uint64(len(leaves)), Length: uint64(len(enc))})
			leaves = append(leaves, enc...)
		}
	}
	rootRaw := pmtiles.EncodeDirectory(rootEntries)
	meta := s.meta
	if meta == nil {
		meta = metadataFor(s)
	}
	b := assemble(h, compress(s.compression, rootRaw), compress(s.compression, meta), leaves, data)
	hdr, err := pmtiles.DecodeHeader(b)
	if err != nil {
		panic(err)
	}
	return built{bytes: b, header: hdr, rootRaw: rootRaw, meta: meta, leaves: leaves, data: data, entries: entries,
		rootEntries: rootEntries, offsets: offsets}
}
