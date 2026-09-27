// Package tilecheck fetches the shared MVT corpus through one delivery path
// (a PMTiles archive, read locally or with HTTP range requests, or a TileJSON
// plus XYZ endpoint), decodes every tile and compares it with the manifest's
// expected content. Both paths produce the same report shape, so their
// results can be compared field by field.
package tilecheck

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/lansenou/pmtiles-conformance-lab/internal/fixtures"
	"github.com/lansenou/pmtiles-conformance-lab/internal/mvt"
	"github.com/lansenou/pmtiles-conformance-lab/internal/pmtiles"
)

// Schema identifies the report format.
const Schema = "pmtiles-lab-tilecheck/1"

// Limits.
const (
	maxBody         = 4 << 20
	maxDecompressed = 4 << 20
)

// Report is the machine-readable result. Field names are a stable contract.
type Report struct {
	Schema   string       `json:"schema"`
	Source   string       `json:"source"` // pmtiles or xyz
	Location string       `json:"location"`
	Corpus   string       `json:"corpus"`
	Result   string       `json:"result"` // pass or fail
	Metadata []Check      `json:"metadata"`
	Tiles    []TileResult `json:"tiles"`
	Failures []Failure    `json:"failures"`
	Summary  Summary      `json:"summary"`
}

// Check is one named metadata check.
type Check struct {
	Name     string `json:"name"`
	Expected string `json:"expected"`
	Observed string `json:"observed"`
	OK       bool   `json:"ok"`
}

// TileResult compares one corpus coordinate.
type TileResult struct {
	Z         uint8                      `json:"z"`
	X         uint32                     `json:"x"`
	Y         uint32                     `json:"y"`
	Expected  string                     `json:"expected"`
	Observed  string                     `json:"observed"` // present, absent or error
	Detail    string                     `json:"detail,omitempty"`
	SHA256    string                     `json:"sha256,omitempty"`
	MVTSHA256 string                     `json:"mvt_sha256,omitempty"`
	Features  []fixtures.ExpectedFeature `json:"features,omitempty"`
	Match     bool                       `json:"match"`
}

// Failure is one reason for a fail result.
type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Summary counts tiles.
type Summary struct {
	TilesChecked int `json:"tiles_checked"`
	TilesMatched int `json:"tiles_matched"`
}

// fetched is what a delivery path returned for one coordinate: nil data for
// absent, with detail describing the protocol-level observation.
type fetched struct {
	data   []byte // stored (gzip) bytes
	detail string
}

type source interface {
	metadata() []Check
	fetch(ctx context.Context, z uint8, x, y uint32) (fetched, error)
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (r *Report) fail(code, format string, args ...any) {
	r.Failures = append(r.Failures, Failure{code, fmt.Sprintf(format, args...)})
}

func run(ctx context.Context, rep *Report, c *fixtures.MVTCorpus, src source) *Report {
	rep.Metadata = src.metadata()
	for _, ch := range rep.Metadata {
		if !ch.OK {
			rep.fail("metadata_mismatch", "%s: expected %s, observed %s", ch.Name, ch.Expected, ch.Observed)
		}
	}
	for _, t := range c.Tiles {
		tr := checkTile(ctx, c, t, src)
		rep.Tiles = append(rep.Tiles, tr)
		rep.Summary.TilesChecked++
		if tr.Match {
			rep.Summary.TilesMatched++
		} else {
			rep.fail("tile_mismatch", "%d/%d/%d: expected %s, observed %s %s", t.Z, t.X, t.Y, tr.Expected, tr.Observed, tr.Detail)
		}
	}
	rep.Result = "pass"
	if len(rep.Failures) > 0 {
		rep.Result = "fail"
	}
	return rep
}

func checkTile(ctx context.Context, c *fixtures.MVTCorpus, t fixtures.CorpusTile, src source) TileResult {
	tr := TileResult{Z: t.Z, X: t.X, Y: t.Y, Expected: t.Status}
	f, err := src.fetch(ctx, t.Z, t.X, t.Y)
	if err != nil {
		tr.Observed, tr.Detail = "error", err.Error()
		return tr
	}
	tr.Detail = f.detail
	if f.data == nil {
		tr.Observed = "absent"
		tr.Match = t.Status == "absent"
		return tr
	}
	tr.Observed, tr.SHA256 = "present", sum(f.data)
	if t.Status != "present" {
		return tr
	}
	var problems []string
	if tr.SHA256 != t.SHA256 {
		problems = append(problems, "stored bytes sha256 differs")
	}
	raw, err := gunzip(f.data)
	if err != nil {
		tr.Detail = strings.TrimSpace(tr.Detail + " gunzip: " + err.Error())
		return tr
	}
	tr.MVTSHA256 = sum(raw)
	feats, err := mvt.Decode(raw)
	if err != nil {
		tr.Detail = strings.TrimSpace(tr.Detail + " " + err.Error())
		return tr
	}
	for _, df := range feats {
		if df.Extent != uint32(c.Extent) {
			problems = append(problems, fmt.Sprintf("layer %s extent %d", df.Layer, df.Extent))
		}
		tr.Features = append(tr.Features, normalize(df))
	}
	if !sameFeatures(tr.Features, t.Features) {
		problems = append(problems, "decoded features differ")
	}
	if len(problems) > 0 {
		tr.Detail = strings.TrimSpace(tr.Detail + " " + strings.Join(problems, "; "))
		return tr
	}
	tr.Match = true
	return tr
}

// normalize turns a decoded feature into the manifest's shape. A Point with
// one part of one point gets [x, y]; anything else is flattened so it can
// never compare equal to a single point.
func normalize(f mvt.Feature) fixtures.ExpectedFeature {
	e := fixtures.ExpectedFeature{Layer: f.Layer, ID: f.ID, Type: f.Type, Properties: f.Properties}
	if f.Type == "Point" && len(f.Geometry) == 1 && len(f.Geometry[0]) == 1 {
		e.Coordinates = []int64{f.Geometry[0][0][0], f.Geometry[0][0][1]}
		return e
	}
	if f.Type == "Point" {
		e.Type = "MultiPoint"
	}
	for _, part := range f.Geometry {
		for _, p := range part {
			e.Coordinates = append(e.Coordinates, p[0], p[1])
		}
	}
	return e
}

// sameFeatures compares canonical JSON, so a decoded uint64 3 equals a
// manifest number 3.
func sameFeatures(a, b []fixtures.ExpectedFeature) bool {
	canon := func(fs []fixtures.ExpectedFeature) string {
		var out []string
		for _, f := range fs {
			j, _ := json.Marshal(f)
			var v any
			json.Unmarshal(j, &v)
			j, _ = json.Marshal(v)
			out = append(out, string(j))
		}
		return strings.Join(out, "\n")
	}
	return len(a) == len(b) && canon(a) == canon(b)
}

func gunzip(b []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	out, err := io.ReadAll(io.LimitReader(zr, maxDecompressed+1))
	if err == nil && len(out) > maxDecompressed {
		err = fmt.Errorf("decompressed tile exceeds %d bytes", maxDecompressed)
	}
	return out, err
}

func check(name, expected, observed string) Check {
	return Check{name, expected, observed, expected == observed}
}

// layerIDs returns the sorted ids of vector_layers entries.
func layerIDs(raw []json.RawMessage) string {
	var ids []string
	for _, l := range raw {
		var v struct {
			ID     string            `json:"id"`
			Fields map[string]string `json:"fields"`
		}
		json.Unmarshal(l, &v)
		var fields []string
		for k, t := range v.Fields {
			fields = append(fields, k+":"+t)
		}
		sort.Strings(fields)
		ids = append(ids, v.ID+"{"+strings.Join(fields, ",")+"}")
	}
	sort.Strings(ids)
	return strings.Join(ids, ";")
}

func corpusLayers(c *fixtures.MVTCorpus) string {
	var raw []json.RawMessage
	for _, l := range c.VectorLayers {
		b, _ := json.Marshal(l)
		raw = append(raw, b)
	}
	return layerIDs(raw)
}

// ---- PMTiles path ----

type pmtilesSource struct {
	a      *pmtiles.Archive
	c      *fixtures.MVTCorpus
	checks []Check
}

func (s *pmtilesSource) metadata() []Check { return s.checks }

func (s *pmtilesSource) fetch(_ context.Context, z uint8, x, y uint32) (fetched, error) {
	loc, err := s.a.Locate(z, x, y)
	if err != nil {
		return fetched{}, err
	}
	var leaves []string
	for _, l := range loc.Leaves {
		leaves = append(leaves, fmt.Sprintf("%d+%d", l.Offset, l.Length))
	}
	detail := fmt.Sprintf("tile_id=%d leaves=[%s]", loc.TileID, strings.Join(leaves, " "))
	for _, t := range s.c.Tiles {
		if t.Z == z && t.X == x && t.Y == y && t.PMTiles != nil {
			if err := lookupMatches(loc, t.PMTiles); err != nil {
				return fetched{}, fmt.Errorf("%s: %v", detail, err)
			}
		}
	}
	if !loc.Found {
		return fetched{detail: detail}, nil
	}
	b, err := s.a.ReadTile(loc)
	if err != nil {
		return fetched{}, err
	}
	return fetched{data: b, detail: fmt.Sprintf("%s offset=%d length=%d", detail, loc.Offset, loc.Length)}, nil
}

// lookupMatches checks the directory walk against the manifest lookup.
func lookupMatches(loc pmtiles.Location, want *fixtures.PMTilesLookup) error {
	if loc.TileID != want.TileID {
		return fmt.Errorf("tile id %d, manifest %d", loc.TileID, want.TileID)
	}
	if want.ArchiveOffset != nil && (!loc.Found || loc.Offset != *want.ArchiveOffset) {
		return fmt.Errorf("tile at offset %d (found %v), manifest %d", loc.Offset, loc.Found, *want.ArchiveOffset)
	}
	if want.LeafDirectory != nil {
		n := len(loc.Leaves)
		if n == 0 || loc.Leaves[n-1].Offset != want.LeafDirectory.ArchiveOffset || loc.Leaves[n-1].Length != want.LeafDirectory.Length {
			return fmt.Errorf("leaf directories %v, manifest %+v", loc.Leaves, *want.LeafDirectory)
		}
	}
	return nil
}

// PMTiles checks an archive read through r (size bytes). location is only
// reported.
func PMTiles(ctx context.Context, r io.ReaderAt, size uint64, location string, c *fixtures.MVTCorpus) *Report {
	rep := &Report{Schema: Schema, Source: "pmtiles", Location: location, Corpus: c.Name, Failures: []Failure{}}
	a, err := pmtiles.Open(r, size, pmtiles.DefaultLimits)
	if err != nil {
		rep.fail("open_failed", "%v", err)
		rep.Result = "fail"
		return rep
	}
	s := &pmtilesSource{a: a, c: c}
	s.checks = append(s.checks,
		check("tile_type", c.TileFormat, a.Header.TileType.String()),
		check("tile_compression", c.TileCompression, a.Header.TileCompression.String()),
		check("root_has_only_leaves", "true", strconv.FormatBool(onlyLeaves(a.Root))))
	meta, err := a.Metadata()
	var mv struct {
		VectorLayers []json.RawMessage `json:"vector_layers"`
	}
	if err == nil {
		err = json.Unmarshal(meta, &mv)
	}
	obs := layerIDs(mv.VectorLayers)
	if err != nil {
		obs = "error: " + err.Error()
	}
	s.checks = append(s.checks, check("vector_layers", corpusLayers(c), obs))
	return run(ctx, rep, c, s)
}

func onlyLeaves(root []pmtiles.Entry) bool {
	for _, e := range root {
		if e.RunLength != 0 {
			return false
		}
	}
	return len(root) > 0
}

// HTTPReaderAt reads a remote file with single-range GET requests and
// accepts only an exact 206 answer.
type HTTPReaderAt struct {
	Ctx    context.Context
	Client *http.Client
	URL    string
	Size   uint64
}

// NewHTTPReaderAt learns the size from the Content-Range of a first request.
func NewHTTPReaderAt(ctx context.Context, client *http.Client, u string) (*HTTPReaderAt, error) {
	h := &HTTPReaderAt{Ctx: ctx, Client: client, URL: u}
	_, total, err := h.get(0, 0)
	if err != nil {
		return nil, err
	}
	h.Size = total
	return h, nil
}

func (h *HTTPReaderAt) get(off, last uint64) ([]byte, uint64, error) {
	req, err := http.NewRequestWithContext(h.Ctx, http.MethodGet, h.URL, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, last))
	resp, err := h.Client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return nil, 0, fmt.Errorf("range %d-%d: status %d, want 206", off, last, resp.StatusCode)
	}
	var a, b, total uint64
	cr := resp.Header.Get("Content-Range")
	if n, _ := fmt.Sscanf(cr, "bytes %d-%d/%d", &a, &b, &total); n != 3 || a != off || b < a || b-a >= maxBody {
		return nil, 0, fmt.Errorf("range %d-%d: Content-Range %q", off, last, cr)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(b-a)+2))
	if err != nil {
		return nil, 0, err
	}
	if uint64(len(body)) != b-a+1 {
		return nil, 0, fmt.Errorf("range %d-%d: body %d bytes, Content-Range says %d", off, last, len(body), b-a+1)
	}
	return body, total, nil
}

// ReadAt implements io.ReaderAt.
func (h *HTTPReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if off < 0 || uint64(off) >= h.Size {
		return 0, io.EOF
	}
	last := min(uint64(off)+uint64(len(p))-1, h.Size-1)
	body, _, err := h.get(uint64(off), last)
	if err != nil {
		return 0, err
	}
	if uint64(len(body)) != last-uint64(off)+1 {
		return 0, fmt.Errorf("short range response")
	}
	n := copy(p, body)
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// ---- XYZ path ----

type xyzSource struct {
	client   *http.Client
	template string
	checks   []Check
}

func (s *xyzSource) metadata() []Check { return s.checks }

func (s *xyzSource) fetch(ctx context.Context, z uint8, x, y uint32) (fetched, error) {
	u := strings.NewReplacer("{z}", strconv.Itoa(int(z)), "{x}", strconv.Itoa(int(x)), "{y}", strconv.Itoa(int(y))).Replace(s.template)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fetched{}, err
	}
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := s.client.Do(req)
	if err != nil {
		return fetched{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return fetched{}, err
	}
	if len(body) > maxBody {
		return fetched{}, fmt.Errorf("%s: body exceeds %d bytes", u, maxBody)
	}
	detail := fmt.Sprintf("GET %s -> %d", u, resp.StatusCode)
	switch resp.StatusCode {
	case http.StatusNotFound:
		return fetched{detail: detail}, nil
	case http.StatusOK:
	default:
		return fetched{}, fmt.Errorf("%s: unexpected status", detail)
	}
	ct, ce := resp.Header.Get("Content-Type"), resp.Header.Get("Content-Encoding")
	detail += fmt.Sprintf(" content-type=%q content-encoding=%q", ct, ce)
	if ct != "application/vnd.mapbox-vector-tile" && ct != "application/x-protobuf" {
		return fetched{}, fmt.Errorf("%s: tile content type", detail)
	}
	// The stored bytes are gzip; a server that answers Accept-Encoding: gzip
	// must label them so.
	if ce != "gzip" {
		return fetched{}, fmt.Errorf("%s: want Content-Encoding gzip", detail)
	}
	return fetched{data: body, detail: detail}, nil
}

// XYZ fetches the TileJSON at tileJSONURL and checks every corpus tile
// through its first tile URL template. client must not decompress
// transparently.
func XYZ(ctx context.Context, client *http.Client, tileJSONURL string, c *fixtures.MVTCorpus) *Report {
	rep := &Report{Schema: Schema, Source: "xyz", Location: tileJSONURL, Corpus: c.Name, Failures: []Failure{}}
	failNow := func(code, format string, args ...any) *Report {
		rep.fail(code, format, args...)
		rep.Result = "fail"
		return rep
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tileJSONURL, nil)
	if err != nil {
		return failNow("tilejson_failed", "%v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return failNow("tilejson_failed", "%v", err)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK {
		return failNow("tilejson_failed", "GET %s: status %d %v", tileJSONURL, resp.StatusCode, err)
	}
	var tj struct {
		TileJSON     string            `json:"tilejson"`
		Tiles        []string          `json:"tiles"`
		Scheme       string            `json:"scheme"`
		VectorLayers []json.RawMessage `json:"vector_layers"`
	}
	if err := json.Unmarshal(body, &tj); err != nil {
		return failNow("tilejson_invalid", "%v", err)
	}
	s := &xyzSource{client: client}
	tmpl := ""
	if len(tj.Tiles) > 0 {
		tmpl = tj.Tiles[0]
	}
	pu, perr := url.Parse(tmpl)
	absolute := perr == nil && (pu.Scheme == "http" || pu.Scheme == "https") && pu.Host != "" &&
		strings.Contains(tmpl, "{z}") && strings.Contains(tmpl, "{x}") && strings.Contains(tmpl, "{y}")
	scheme := tj.Scheme
	if scheme == "" {
		scheme = "xyz" // TileJSON default
	}
	s.checks = append(s.checks,
		check("tilejson_version", "3.0.0", tj.TileJSON),
		check("tiles_absolute_template", "true", strconv.FormatBool(absolute)),
		check("scheme", "xyz", scheme),
		check("vector_layers", corpusLayers(c), layerIDs(tj.VectorLayers)))
	if !absolute {
		rep.Metadata = s.checks
		return failNow("tilejson_invalid", "tiles[0] %q is not an absolute http(s) URL template with {z}, {x} and {y}", tmpl)
	}
	s.template = tmpl
	return run(ctx, rep, c, s)
}
