// Package mbtiles reads PBF (vector) MBTiles 1.3 files read-only through a
// CGO-free SQLite engine (modernc.org/sqlite). It supports what the XYZ test
// path needs: metadata, vector_layers, and tile lookup by XYZ coordinates. It
// is not an MBTiles validator.
package mbtiles

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// MaxTileBytes bounds one tile_data blob.
const MaxTileBytes = 4 << 20

// ErrUnsupported is wrapped by errors for files this package does not serve.
var ErrUnsupported = errors.New("unsupported MBTiles")

// Metadata is the subset of the metadata table the XYZ path uses.
type Metadata struct {
	Name         string            `json:"name"`
	Format       string            `json:"format"`
	Description  string            `json:"description,omitempty"`
	Attribution  string            `json:"attribution,omitempty"`
	Version      string            `json:"version,omitempty"`
	MinZoom      int               `json:"minzoom"`
	MaxZoom      int               `json:"maxzoom"`
	Bounds       []float64         `json:"bounds,omitempty"`
	Center       []float64         `json:"center,omitempty"`
	VectorLayers []json.RawMessage `json:"vector_layers"`
	Raw          map[string]string `json:"-"`
}

// File is an open MBTiles file. It is safe for concurrent use.
type File struct {
	db   *sql.DB
	Meta Metadata
}

// Open opens path read-only and checks that it is a pbf tileset with
// vector_layers.
func Open(path string) (*File, error) {
	if err := checkSQLiteMagic(path); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	p := filepath.ToSlash(abs)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p // Windows drive letter: file:///C:/...
	}
	u := url.URL{Scheme: "file", Path: p, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	f := &File{db: db}
	if err := f.readMetadata(); err != nil {
		db.Close()
		return nil, err
	}
	return f, nil
}

func checkSQLiteMagic(path string) error {
	fh, err := os.Open(path)
	if err != nil {
		return err
	}
	defer fh.Close()
	var magic [16]byte
	if _, err := io.ReadFull(fh, magic[:]); err != nil || string(magic[:]) != "SQLite format 3\x00" {
		return fmt.Errorf("%w: %s is not an SQLite 3 database", ErrUnsupported, path)
	}
	return nil
}

// Close closes the database.
func (f *File) Close() error { return f.db.Close() }

func (f *File) readMetadata() error {
	rows, err := f.db.Query("SELECT name, value FROM metadata")
	if err != nil {
		return fmt.Errorf("%w: reading metadata table: %v", ErrUnsupported, err)
	}
	defer rows.Close()
	raw := map[string]string{}
	for rows.Next() {
		var k, v sql.NullString
		if err := rows.Scan(&k, &v); err != nil {
			return err
		}
		if k.Valid {
			raw[k.String] = v.String
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	m := Metadata{Raw: raw, Name: raw["name"], Format: raw["format"], Description: raw["description"],
		Attribution: raw["attribution"], Version: raw["version"], MinZoom: 0, MaxZoom: 30}
	switch m.Format {
	case "pbf":
	case "":
		return fmt.Errorf("%w: metadata has no format row", ErrUnsupported)
	default:
		return fmt.Errorf("%w: format %q; only pbf (gzip-compressed Mapbox Vector Tiles) is served", ErrUnsupported, m.Format)
	}
	var js struct {
		VectorLayers []json.RawMessage `json:"vector_layers"`
	}
	if err := json.Unmarshal([]byte(raw["json"]), &js); err != nil || js.VectorLayers == nil {
		return fmt.Errorf("%w: a pbf tileset needs a metadata json row with vector_layers (MBTiles 1.3)", ErrUnsupported)
	}
	m.VectorLayers = js.VectorLayers
	if v, ok := raw["minzoom"]; ok {
		if m.MinZoom, err = zoom(v); err != nil {
			return fmt.Errorf("%w: minzoom %q", ErrUnsupported, v)
		}
	}
	if v, ok := raw["maxzoom"]; ok {
		if m.MaxZoom, err = zoom(v); err != nil {
			return fmt.Errorf("%w: maxzoom %q", ErrUnsupported, v)
		}
	}
	if m.MinZoom > m.MaxZoom {
		return fmt.Errorf("%w: minzoom %d > maxzoom %d", ErrUnsupported, m.MinZoom, m.MaxZoom)
	}
	m.Bounds = numbers(raw["bounds"], 4)
	m.Center = numbers(raw["center"], 3)
	f.Meta = m
	return nil
}

func zoom(s string) (int, error) {
	z, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || z < 0 || z > 30 {
		return 0, fmt.Errorf("bad zoom")
	}
	return z, nil
}

// numbers parses n comma-separated numbers, or returns nil.
func numbers(s string, n int) []float64 {
	parts := strings.Split(s, ",")
	if len(parts) != n {
		return nil
	}
	out := make([]float64, n)
	for i, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return nil
		}
		out[i] = v
	}
	return out
}

// TMSRow converts an XYZ row to the MBTiles tile_row (TMS, y grows north).
func TMSRow(z uint8, y uint32) uint32 { return uint32(1)<<z - 1 - y }

// Tile returns the stored tile_data for XYZ z/x/y, or nil with a nil error
// if there is no such row. x and y must be below 2^z.
func (f *File) Tile(ctx context.Context, z uint8, x, y uint32) ([]byte, error) {
	if z > 30 || uint64(x) >= 1<<z || uint64(y) >= 1<<z {
		return nil, fmt.Errorf("tile %d/%d/%d out of range", z, x, y)
	}
	var b []byte
	err := f.db.QueryRowContext(ctx,
		"SELECT tile_data FROM tiles WHERE zoom_level = ? AND tile_column = ? AND tile_row = ? AND length(tile_data) <= ?",
		z, x, TMSRow(z, y), MaxTileBytes).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		// Distinguish an oversized blob from a missing row.
		var n int64
		if f.db.QueryRowContext(ctx, "SELECT length(tile_data) FROM tiles WHERE zoom_level = ? AND tile_column = ? AND tile_row = ?",
			z, x, TMSRow(z, y)).Scan(&n) == nil {
			return nil, fmt.Errorf("tile %d/%d/%d is %d bytes, limit %d", z, x, y, n, MaxTileBytes)
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if b == nil {
		b = []byte{} // a present row with empty or NULL data
	}
	return b, nil
}

// IsGzip reports whether b starts with the gzip magic bytes.
func IsGzip(b []byte) bool { return bytes.HasPrefix(b, []byte{0x1f, 0x8b}) }
