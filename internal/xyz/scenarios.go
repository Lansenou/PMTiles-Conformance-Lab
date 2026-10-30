package xyz

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/lansenou/pmtiles-conformance-lab/internal/mbtiles"
)

// Validity labels for XYZ scenarios (docs/xyz-scenarios.md).
const (
	Valid                   = "valid"
	Invalid                 = "invalid"
	ValidHTTPInvalidContent = "valid-http-invalid-content"
	ValidBlocksJS           = "valid-http-blocks-browser-read"
)

// RetryAfter is the Retry-After value of status-429-once and status-503.
const RetryAfter = "1"

// Scenario changes one thing in the normal responses. The zero Scenario (nil
// hooks) is normal behaviour.
type Scenario struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Validity    string `json:"validity"`
	// TMS announces scheme "tms" in TileJSON and reads the request y as the
	// MBTiles tile_row.
	TMS bool `json:"-"`
	// Counted numbers the GETs of each tile path since reset in
	// Exchange.N, for up to 65536 paths; later paths get N 0.
	Counted bool `json:"-"`
	// TileJSON, if set, changes the document before it is encoded.
	TileJSON func(*TileJSON) `json:"-"`
	// Tile, if set, changes the planned response of a tile request with
	// valid coordinates (*x.Resp).
	Tile func(x *Exchange) `json:"-"`
}

// Scenarios returns every XYZ scenario in documentation order. Names are
// stable. They are a separate list from the range server's scenarios.
func Scenarios() []Scenario {
	return []Scenario{
		{
			Name: "normal", Validity: Valid,
			Description: "TileJSON 3.0.0 with absolute URLs; stored tiles with Content-Encoding: gzip, 404 for a missing tile.",
		},
		{
			Name: "truncated-body", Validity: Invalid,
			Description: "Tile 200 with the full Content-Length; the connection is closed after half of the body.",
			Tile:        truncatedBody,
		},
		{
			Name: "stall-body", Validity: Invalid,
			Description: "Half of a tile 200 body is sent, then the server stalls until the client cancels or --delay expires, then closes.",
			Tile:        stallBody,
		},
		{
			Name: "status-429-once", Validity: Valid,
			Description: "The first GET of each tile path since reset gets 429 with Retry-After: 1; later requests are normal.",
			Counted:     true,
			Tile:        status429Once,
		},
		{
			Name: "status-503", Validity: Valid,
			Description: "Every tile request gets 503 with Retry-After: 1.",
			Tile:        status503,
		},
		{
			Name: "absent-204", Validity: Valid,
			Description: "A missing tile gets 204 No Content instead of 404.",
			Tile:        absent204,
		},
		{
			Name: "absent-200-empty", Validity: Valid,
			Description: "A missing tile gets 200 with the tile Content-Type and Content-Length: 0 instead of 404.",
			Tile:        absent200Empty,
		},
		{
			Name: "gzip-unlabelled", Validity: ValidHTTPInvalidContent,
			Description: "A tile stored as gzip is sent as stored without Content-Encoding, whatever the Accept-Encoding.",
			Tile:        gzipUnlabelled,
		},
		{
			Name: "encoding-label-only", Validity: Invalid,
			Description: "A tile stored as gzip is sent decompressed but with Content-Encoding: gzip.",
			Tile:        encodingLabelOnly,
		},
		{
			Name: "double-gzip", Validity: ValidHTTPInvalidContent,
			Description: "A tile stored as gzip is gzipped again and sent with one Content-Encoding: gzip, whatever the Accept-Encoding.",
			Tile:        doubleGzip,
		},
		{
			Name: "cors-missing-tiles", Validity: ValidBlocksJS,
			Description: "Tile responses have no Access-Control-Allow-Origin; TileJSON keeps it.",
			Tile:        corsMissingTiles,
		},
		{
			Name: "tilejson-relative-tiles", Validity: Invalid,
			Description: "TileJSON tiles is the relative reference {z}/{x}/{y}.pbf, which resolves to the normal tile URLs.",
			TileJSON:    relativeTiles,
		},
		{
			Name: "scheme-tms", Validity: Valid,
			Description: "TileJSON scheme is tms, and the server reads the request y as the TMS tile_row.",
			TMS:         true,
		},
		{
			Name: "vector-layers-mismatch", Validity: ValidHTTPInvalidContent,
			Description: "TileJSON vector_layers ids get the suffix -renamed (points-renamed); the tiles keep their layer names.",
			TileJSON:    renameLayers,
		},
	}
}

// LookupScenario returns the scenario with the given name.
func LookupScenario(name string) (Scenario, bool) {
	for _, s := range Scenarios() {
		if s.Name == name {
			return s, true
		}
	}
	return Scenario{}, false
}

// tileBody reports whether normal planned a tile 200 with a body.
func tileBody(x *Exchange) bool {
	return x.Resp.Status == http.StatusOK && len(x.Resp.Body) > 0
}

// storedGzip reports whether normal planned a tile 200 for a tile stored as
// gzip.
func storedGzip(x *Exchange) bool {
	return x.Resp.Status == http.StatusOK && mbtiles.IsGzip(x.Stored)
}

func setBody(x *Exchange, b []byte) {
	x.Resp.Body = b
	x.Resp.Header.Set("Content-Length", strconv.Itoa(len(b)))
}

func truncatedBody(x *Exchange) {
	if tileBody(x) {
		x.Resp.CutAfter = len(x.Resp.Body) / 2
	}
}

func stallBody(x *Exchange) {
	if tileBody(x) {
		x.Resp.StallAfter = len(x.Resp.Body) / 2
	}
}

// retryLater replaces the response with an error that asks for a retry; it
// keeps CORS like every normal error response.
func retryLater(x *Exchange, status int) {
	x.Resp = errorResponse(status, http.StatusText(status))
	x.Resp.Header.Set("Retry-After", RetryAfter)
}

func status429Once(x *Exchange) {
	if x.N == 1 {
		retryLater(x, http.StatusTooManyRequests)
	}
}

func status503(x *Exchange) { retryLater(x, http.StatusServiceUnavailable) }

// emptyTile replaces a 404 for a missing tile with status and no content,
// with the headers of an empty stored tile.
func emptyTile(x *Exchange, status int) {
	if !x.Absent {
		return
	}
	x.Resp = newResponse(status)
	x.Resp.Header.Set("Content-Type", ContentType)
	x.Resp.Header.Set("Vary", "Accept-Encoding")
	if status != http.StatusNoContent {
		x.Resp.Header.Set("Content-Length", "0")
	}
}

func absent204(x *Exchange) { emptyTile(x, http.StatusNoContent) }

func absent200Empty(x *Exchange) { emptyTile(x, http.StatusOK) }

func gzipUnlabelled(x *Exchange) {
	if storedGzip(x) {
		x.Resp.Header.Del("Content-Encoding")
		setBody(x, x.Stored)
	}
}

func encodingLabelOnly(x *Exchange) {
	if !storedGzip(x) {
		return
	}
	if mvt, err := gunzip(x.Stored); err == nil {
		x.Resp.Header.Set("Content-Encoding", "gzip")
		setBody(x, mvt)
	}
}

func doubleGzip(x *Exchange) {
	if !storedGzip(x) {
		return
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(x.Stored) // writes to a bytes.Buffer do not fail
	_ = zw.Close()
	x.Resp.Header.Set("Content-Encoding", "gzip")
	setBody(x, buf.Bytes())
}

func corsMissingTiles(x *Exchange) { x.Resp.Header.Del("Access-Control-Allow-Origin") }

func relativeTiles(tj *TileJSON) { tj.Tiles = []string{"{z}/{x}/{y}.pbf"} }

// renameLayers appends -renamed to every vector_layers id. It copies the
// layers: tj shares them with the file's metadata.
func renameLayers(tj *TileJSON) {
	out := make([]json.RawMessage, 0, len(tj.VectorLayers))
	for _, l := range tj.VectorLayers {
		var m map[string]json.RawMessage
		var id string
		if json.Unmarshal(l, &m) == nil && json.Unmarshal(m["id"], &id) == nil {
			m["id"], _ = json.Marshal(id + "-renamed")
			l, _ = json.Marshal(m)
		}
		out = append(out, l)
	}
	tj.VectorLayers = out
}
