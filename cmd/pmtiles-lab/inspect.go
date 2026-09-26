package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/lansenou/pmtiles-conformance-lab/internal/pmtiles"
)

type inspectReport struct {
	Schema        string         `json:"schema"`
	File          string         `json:"file"`
	Size          int64          `json:"size"`
	SHA256        string         `json:"sha256,omitempty"`
	Valid         bool           `json:"valid"`
	Error         *diag          `json:"error"`
	Header        *headerJSON    `json:"header,omitempty"`
	Stats         *pmtiles.Stats `json:"stats,omitempty"`
	MetadataBytes int            `json:"metadata_bytes,omitempty"`
	Tiles         []tileJSON     `json:"tiles"`
	Warnings      []string       `json:"warnings"`
}

type diag struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type headerJSON struct {
	Version             uint8      `json:"version"`
	RootOffset          uint64     `json:"root_offset"`
	RootLength          uint64     `json:"root_length"`
	MetadataOffset      uint64     `json:"metadata_offset"`
	MetadataLength      uint64     `json:"metadata_length"`
	LeafOffset          uint64     `json:"leaf_directories_offset"`
	LeafLength          uint64     `json:"leaf_directories_length"`
	TileDataOffset      uint64     `json:"tile_data_offset"`
	TileDataLength      uint64     `json:"tile_data_length"`
	AddressedTiles      uint64     `json:"addressed_tiles"`
	TileEntries         uint64     `json:"tile_entries"`
	TileContents        uint64     `json:"tile_contents"`
	Clustered           bool       `json:"clustered"`
	InternalCompression string     `json:"internal_compression"`
	TileCompression     string     `json:"tile_compression"`
	TileType            string     `json:"tile_type"`
	MinZoom             uint8      `json:"min_zoom"`
	MaxZoom             uint8      `json:"max_zoom"`
	Bounds              [4]float64 `json:"bounds"`
	CenterZoom          uint8      `json:"center_zoom"`
	Center              [2]float64 `json:"center"`
}

type tileJSON struct {
	Z             uint8           `json:"z"`
	X             uint32          `json:"x"`
	Y             uint32          `json:"y"`
	TileID        uint64          `json:"tile_id"`
	Status        string          `json:"status"`
	ArchiveOffset uint64          `json:"archive_offset,omitempty"`
	Length        uint64          `json:"length,omitempty"`
	SHA256        string          `json:"sha256,omitempty"`
	Leaves        []pmtiles.Range `json:"leaf_directories,omitempty"`
}

func headerOf(h pmtiles.Header) *headerJSON {
	d := func(v int32) float64 { return float64(v) / 1e7 }
	return &headerJSON{h.Version, h.RootOffset, h.RootLength, h.MetadataOffset, h.MetadataLength,
		h.LeafOffset, h.LeafLength, h.TileDataOffset, h.TileDataLength, h.AddressedTiles, h.TileEntries,
		h.TileContents, h.Clustered == 1, h.InternalCompression.String(), h.TileCompression.String(),
		h.TileType.String(), h.MinZoom, h.MaxZoom,
		[4]float64{d(h.MinLonE7), d(h.MinLatE7), d(h.MaxLonE7), d(h.MaxLatE7)},
		h.CenterZoom, [2]float64{d(h.CenterLonE7), d(h.CenterLatE7)}}
}

func cmdInspect(_ context.Context, args []string, stdout, stderr io.Writer) error {
	fs := newFlags("inspect", "inspect ARCHIVE [--tile Z/X/Y]... [--json]",
		"  pmtiles-lab inspect fixtures/valid/root-none.pmtiles\n  pmtiles-lab inspect fixtures/valid/leaves-gzip.pmtiles --tile 12/3423/1763 --json\n  pmtiles-lab inspect fixtures/malformed/bad-magic.pmtiles --json   # exit 1, error.code bad_magic\n", stderr)
	asJSON := fs.Bool("json", false, "print a JSON report")
	var tiles multiFlag
	fs.Var(&tiles, "tile", "resolve tile Z/X/Y (repeatable); an absent tile is not an error")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageError{"exactly one ARCHIVE is required"}
	}
	type coord struct {
		z    uint8
		x, y uint32
	}
	var coords []coord
	for _, t := range tiles {
		parts := strings.Split(t, "/")
		var v [3]uint64
		ok := len(parts) == 3
		for i := 0; ok && i < 3; i++ {
			var err error
			v[i], err = strconv.ParseUint(parts[i], 10, 32)
			ok = err == nil
		}
		if !ok || v[0] > pmtiles.MaxZoom {
			return usageError{fmt.Sprintf("--tile %q: want Z/X/Y with Z <= %d", t, pmtiles.MaxZoom)}
		}
		coords = append(coords, coord{uint8(v[0]), uint32(v[1]), uint32(v[2])})
	}

	path := pos[0]
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	rep := inspectReport{Schema: "pmtiles-lab-inspect/1", File: path, Size: st.Size(), Tiles: []tileJSON{}, Warnings: []string{}}
	lim := pmtiles.DefaultLimits
	if uint64(st.Size()) <= lim.MaxFileSize {
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return err
		}
		rep.SHA256 = hex.EncodeToString(h.Sum(nil))
	}
	check := func() error {
		a, err := pmtiles.Open(f, uint64(st.Size()), lim)
		if err != nil {
			return err
		}
		rep.Header = headerOf(a.Header)
		m, err := a.Metadata()
		if err != nil {
			return err
		}
		rep.MetadataBytes = len(m)
		s, err := a.Walk()
		if err != nil {
			return err
		}
		rep.Stats = &s
		hd := a.Header
		for _, c := range []struct {
			name        string
			header, got uint64
		}{{"addressed_tiles", hd.AddressedTiles, s.AddressedTiles}, {"tile_entries", hd.TileEntries, s.TileEntries}} {
			if c.header != 0 && c.header != c.got {
				rep.Warnings = append(rep.Warnings, fmt.Sprintf("header %s is %d but directories contain %d", c.name, c.header, c.got))
			}
		}
		for _, c := range coords {
			loc, err := a.Locate(c.z, c.x, c.y)
			if err != nil {
				return err
			}
			tj := tileJSON{Z: c.z, X: c.x, Y: c.y, TileID: loc.TileID, Status: "absent", Leaves: loc.Leaves}
			if loc.Found {
				b, err := a.ReadTile(loc)
				if err != nil {
					return err
				}
				sum := sha256.Sum256(b)
				tj.Status, tj.ArchiveOffset, tj.Length, tj.SHA256 = "present", loc.Offset, loc.Length, hex.EncodeToString(sum[:])
			}
			rep.Tiles = append(rep.Tiles, tj)
		}
		return nil
	}
	if err := check(); err != nil {
		code := pmtiles.CodeOf(err)
		if code == "" {
			return err // I/O error, not a verdict on the archive
		}
		rep.Error = &diag{string(code), err.Error()}
	} else {
		rep.Valid = true
	}
	if *asJSON {
		if err := writeJSON(stdout, rep); err != nil {
			return err
		}
	} else {
		printInspect(stdout, rep)
	}
	if !rep.Valid {
		return errCheckFailed
	}
	return nil
}

func printInspect(w io.Writer, r inspectReport) {
	var b bytes.Buffer
	fmt.Fprintf(&b, "file:   %s (%d bytes, sha256 %s)\n", r.File, r.Size, r.SHA256)
	if r.Header != nil {
		h := r.Header
		fmt.Fprintf(&b, "header: v%d internal=%s tile=%s type=%s clustered=%v zoom=%d-%d\n",
			h.Version, h.InternalCompression, h.TileCompression, h.TileType, h.Clustered, h.MinZoom, h.MaxZoom)
		fmt.Fprintf(&b, "        root [%d,+%d) metadata [%d,+%d) leaves [%d,+%d) data [%d,+%d)\n",
			h.RootOffset, h.RootLength, h.MetadataOffset, h.MetadataLength, h.LeafOffset, h.LeafLength, h.TileDataOffset, h.TileDataLength)
	}
	if r.Stats != nil {
		s := r.Stats
		fmt.Fprintf(&b, "dirs:   %d (%d leaf, depth %d), %d tile entries, %d addressed tiles\n", s.Directories, s.LeafDirs, s.MaxDepth, s.TileEntries, s.AddressedTiles)
	}
	for _, t := range r.Tiles {
		if t.Status == "present" {
			fmt.Fprintf(&b, "tile:   %d/%d/%d id=%d present [%d,+%d) sha256 %s\n", t.Z, t.X, t.Y, t.TileID, t.ArchiveOffset, t.Length, t.SHA256)
		} else {
			fmt.Fprintf(&b, "tile:   %d/%d/%d id=%d absent\n", t.Z, t.X, t.Y, t.TileID)
		}
	}
	for _, s := range r.Warnings {
		fmt.Fprintf(&b, "warn:   %s\n", s)
	}
	if r.Valid {
		b.WriteString("result: valid\n")
	} else {
		fmt.Fprintf(&b, "result: invalid: %s\n", r.Error.Message)
	}
	w.Write(b.Bytes())
}
