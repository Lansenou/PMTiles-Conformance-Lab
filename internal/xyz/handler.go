// Package xyz serves an MBTiles file as TileJSON plus /{z}/{x}/{y}.pbf XYZ
// tile requests. It is read-only and deliberately small: one tileset, GET and
// HEAD only, no caching headers, no styles. It is independent of the range
// server and of PMTiles parsing.
package xyz

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/lansenou/pmtiles-conformance-lab/internal/mbtiles"
)

// ContentType is sent for every tile.
const ContentType = "application/vnd.mapbox-vector-tile"

// TileJSONPath is the TileJSON endpoint.
const TileJSONPath = "/tiles.json"

var (
	tilePath  = regexp.MustCompile(`^/(\d{1,2})/(\d{1,10})/(\d{1,10})\.pbf$`)
	validHost = regexp.MustCompile(`^[A-Za-z0-9.\-]+(:\d{1,5})?$|^\[[0-9A-Fa-f:.]+\](:\d{1,5})?$`)
)

// Handler serves one MBTiles file.
type Handler struct {
	File *mbtiles.File
	// PublicURL, if set, is the base of the tile URLs in TileJSON (for
	// example "http://127.0.0.1:8080"). Otherwise the request Host is used.
	PublicURL string
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path == TileJSONPath {
		h.tileJSON(w, r)
		return
	}
	m := tilePath.FindStringSubmatch(r.URL.Path)
	if m == nil {
		http.NotFound(w, r)
		return
	}
	z, _ := strconv.ParseUint(m[1], 10, 8)
	x, errX := strconv.ParseUint(m[2], 10, 32)
	y, errY := strconv.ParseUint(m[3], 10, 32)
	if z > 30 || errX != nil || errY != nil || x >= 1<<z || y >= 1<<z {
		http.Error(w, "tile coordinates out of range", http.StatusBadRequest)
		return
	}
	h.tile(w, r, uint8(z), uint32(x), uint32(y))
}

// TileJSON is the TileJSON 3.0.0 document served at /tiles.json.
type TileJSON struct {
	TileJSON     string            `json:"tilejson"`
	Name         string            `json:"name,omitempty"`
	Description  string            `json:"description,omitempty"`
	Attribution  string            `json:"attribution,omitempty"`
	Version      string            `json:"version,omitempty"`
	Scheme       string            `json:"scheme"`
	Tiles        []string          `json:"tiles"`
	MinZoom      int               `json:"minzoom"`
	MaxZoom      int               `json:"maxzoom"`
	Bounds       []float64         `json:"bounds,omitempty"`
	Center       []float64         `json:"center,omitempty"`
	VectorLayers []json.RawMessage `json:"vector_layers"`
}

func (h *Handler) base(r *http.Request) (string, bool) {
	if h.PublicURL != "" {
		return strings.TrimRight(h.PublicURL, "/"), true
	}
	if !validHost.MatchString(r.Host) {
		return "", false
	}
	return "http://" + r.Host, true
}

func (h *Handler) tileJSON(w http.ResponseWriter, r *http.Request) {
	base, ok := h.base(r)
	if !ok {
		http.Error(w, "request has no usable Host header; start the server with --public-url", http.StatusBadRequest)
		return
	}
	m := h.File.Meta
	tj := TileJSON{TileJSON: "3.0.0", Name: m.Name, Description: m.Description, Attribution: m.Attribution,
		Version: m.Version, Scheme: "xyz", Tiles: []string{base + "/{z}/{x}/{y}.pbf"},
		MinZoom: m.MinZoom, MaxZoom: m.MaxZoom, Bounds: m.Bounds, Center: m.Center, VectorLayers: m.VectorLayers}
	b, err := json.MarshalIndent(tj, "", "  ")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)+1))
	if r.Method == http.MethodGet {
		w.Write(append(b, '\n'))
	}
}

func (h *Handler) tile(w http.ResponseWriter, r *http.Request, z uint8, x, y uint32) {
	data, err := h.File.Tile(r.Context(), z, x, y)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if data == nil {
		http.Error(w, fmt.Sprintf("no tile at %d/%d/%d", z, x, y), http.StatusNotFound)
		return
	}
	hd := w.Header()
	hd.Set("Content-Type", ContentType)
	hd.Set("Vary", "Accept-Encoding")
	if len(data) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if mbtiles.IsGzip(data) {
		if acceptsGzip(r.Header.Values("Accept-Encoding")) {
			hd.Set("Content-Encoding", "gzip")
		} else if data, err = gunzip(data); err != nil {
			http.Error(w, "stored tile is not valid gzip: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	hd.Set("Content-Length", strconv.Itoa(len(data)))
	if r.Method == http.MethodGet {
		w.Write(data)
	}
}

// acceptsGzip applies RFC 9110 §12.5.3: no Accept-Encoding field means any
// coding is acceptable; otherwise gzip (or x-gzip, or *) must be listed with
// a non-zero weight.
func acceptsGzip(fields []string) bool {
	if len(fields) == 0 {
		return true
	}
	gzipQ, starQ := -1.0, -1.0
	for _, f := range fields {
		for _, item := range strings.Split(f, ",") {
			parts := strings.Split(item, ";")
			name := strings.ToLower(strings.TrimSpace(parts[0]))
			q := 1.0
			for _, p := range parts[1:] {
				p = strings.TrimSpace(p)
				if strings.HasPrefix(strings.ToLower(p), "q=") {
					if v, err := strconv.ParseFloat(p[2:], 64); err == nil {
						q = v
					}
				}
			}
			switch name {
			case "gzip", "x-gzip":
				gzipQ = q
			case "*":
				starQ = q
			}
		}
	}
	if gzipQ >= 0 {
		return gzipQ > 0
	}
	return starQ > 0
}

func gunzip(b []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	out, err := io.ReadAll(io.LimitReader(zr, mbtiles.MaxTileBytes+1))
	if err == nil && len(out) > mbtiles.MaxTileBytes {
		err = fmt.Errorf("decompressed tile exceeds %d bytes", mbtiles.MaxTileBytes)
	}
	return out, err
}
