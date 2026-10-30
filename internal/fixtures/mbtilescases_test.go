package fixtures

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	_ "modernc.org/sqlite" // the SQLite engine reads the writer's output
)

// openSQLite writes b to a temporary file and opens it read-only with the
// SQLite engine.
func openSQLite(t *testing.T, b []byte) *sql.DB {
	t.Helper()
	p := filepath.ToSlash(filepath.Join(t.TempDir(), "db.sqlite"))
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	db, err := sql.Open("sqlite", "file://"+p+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// Rows around each local-size rule (P = X, P = X+1 with K > X, K <= X, and
// many overflow pages) read back intact through the SQLite engine, and
// integrity_check accepts the overflow chains.
func TestSQLiteOverflowRows(t *testing.T) {
	// A one-blob record of n bytes (58 <= n <= 8185) has a 3-byte header, so
	// its payload P is n+3. K is the local size the overflow rule picks.
	sizes := []int{4000, sqliteMaxTableLocal - 3, sqliteMaxTableLocal - 2, sqliteMinTableLocal + (sqlitePageSize - 4) + 100 - 3,
		sqliteMaxTableLocal + (sqlitePageSize - 4) - 3, sqliteMinTableLocal + (sqlitePageSize - 4) - 3, 65568}
	var s sqlSchema
	var want [][]byte
	for i, n := range sizes {
		b := make([]byte, n)
		for j := range b {
			b[j] = byte(j*7 + i)
		}
		want = append(want, b)
		s.tables = append(s.tables, sqlTable{name: fmt.Sprintf("t%d", i), sql: fmt.Sprintf("CREATE TABLE t%d (data blob)", i),
			rows: [][]any{{b}}})
	}
	file := sqliteDB(s, 0)
	// Overflow pages: none (P < X), none (P = X), 1 (P = X+1, M local),
	// 1 (K local), 1 (K = X), 1 (K = M, a full overflow page), 16.
	if pages := len(file) / sqlitePageSize; pages != 1+len(sizes)+0+0+1+1+1+1+16 {
		t.Errorf("%d pages", pages)
	}
	db := openSQLite(t, file)
	var ic string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&ic); err != nil || ic != "ok" {
		t.Fatalf("integrity_check: %q %v", ic, err)
	}
	for i := range sizes {
		var got []byte
		if err := db.QueryRow(fmt.Sprintf("SELECT data FROM t%d", i)).Scan(&got); err != nil || !bytes.Equal(got, want[i]) {
			t.Errorf("t%d: %d bytes read, want %d (%v)", i, len(got), len(want[i]), err)
		}
	}
}

func TestDeflateZeroRunDynamic(t *testing.T) {
	for _, matches := range []int{0, 1, 2, 7, 100} {
		s, n := deflateZeroRunDynamic(matches)
		out, err := io.ReadAll(flate.NewReader(bytes.NewReader(s)))
		if err != nil || uint64(len(out)) != n || n != 1+258*uint64(matches) || !bytes.Equal(out, make([]byte, n)) {
			t.Errorf("matches %d: %d bytes (want %d), %v", matches, len(out), n, err)
		}
	}
}

// zeroCounter counts written bytes and remembers whether any was not zero.
type zeroCounter struct {
	n       uint64
	nonZero bool
}

func (z *zeroCounter) Write(p []byte) (int, error) {
	z.n += uint64(len(p))
	z.nonZero = z.nonZero || slices.ContainsFunc(p, func(c byte) bool { return c != 0 })
	return len(p), nil
}

// The MBTiles bomb is a 65,568-byte gzip member that the standard library
// inflates to exactly 67,633,153 zero bytes, accepting its CRC-32 and ISIZE.
func TestMBTilesDecompressionBomb(t *testing.T) {
	bomb, n := gzipZeroBombDynamic(mbtilesBombMatches)
	if len(bomb) != 65568 || n != 67633153 || n <= 64<<20 {
		t.Fatalf("member %d bytes, inflated %d", len(bomb), n)
	}
	zr, err := gzip.NewReader(bytes.NewReader(bomb))
	if err != nil {
		t.Fatal(err)
	}
	var zc zeroCounter
	if _, err := io.Copy(&zc, zr); err != nil || zc.n != n || zc.nonZero {
		t.Fatalf("stdlib inflated %d bytes (non-zero %v), %v; want %d zeros", zc.n, zc.nonZero, err, n)
	}
}

// schemaDiff lists what s changes in base: "+table x", "-index y",
// "~index y" (other SQL or uniqueness), "+view v", and for a table in both,
// "-name label" or "+name label" for each row only on one side, where label
// is every column but the last, joined by "/". It does not see a change of
// row order alone; TestGolden pins the bytes.
func schemaDiff(base, s sqlSchema) []string {
	objects := func(s sqlSchema) map[string]string {
		m := map[string]string{}
		for _, t := range s.tables {
			m["table "+t.name] = t.sql
		}
		for _, ix := range s.indexes {
			m["index "+ix.name] = fmt.Sprint(ix.sql, ix.unique, ix.table, ix.cols)
		}
		for _, v := range s.views {
			m["view "+v.name] = v.sql
		}
		return m
	}
	a, b := objects(base), objects(s)
	var out []string
	for k, v := range a {
		if w, ok := b[k]; !ok {
			out = append(out, "-"+k)
		} else if v != w {
			out = append(out, "~"+k)
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			out = append(out, "+"+k)
		}
	}
	rows := func(t *sqlTable) map[string][]string {
		m := map[string][]string{}
		for _, r := range t.rows {
			var label []string
			for _, v := range r[:len(r)-1] {
				label = append(label, fmt.Sprint(v))
			}
			k := fmt.Sprintf("%#v", r)
			m[k] = append(m[k], strings.Join(label, "/"))
		}
		return m
	}
	for _, t := range base.tables {
		if _, ok := b["table "+t.name]; !ok {
			continue
		}
		ra, rb := rows(&t), rows(s.table(t.name))
		for k, labels := range ra {
			for i := len(rb[k]); i < len(labels); i++ {
				out = append(out, "-"+t.name+" "+labels[i])
			}
		}
		for k, labels := range rb {
			for i := len(ra[k]); i < len(labels); i++ {
				out = append(out, "+"+t.name+" "+labels[i])
			}
		}
	}
	sort.Strings(out)
	return out
}

// Each case changes only what its description names: schema cases differ
// from the corpus schema in exactly these objects and rows, byte cases in
// exactly these bytes.
func TestMBTilesOneChangeEach(t *testing.T) {
	schemaCases := map[string][]string{
		"tiles-view": {"+index images_id", "+index map_index", "+table images", "+table map", "+view tiles",
			"-index tile_index", "-table tiles"},
		"no-metadata-table":   {"-table metadata"},
		"no-tiles-table":      {"-index tile_index", "-table tiles"},
		"wrong-format":        {"+metadata format", "-metadata format"},
		"no-vector-layers":    {"-metadata json"},
		"zoom-range-mismatch": {"+metadata maxzoom", "-metadata maxzoom"},
		"corrupt-gzip":        {"+tiles 1/1/0", "-tiles 1/1/0"},
		"mixed-gzip":          {"+tiles 2/1/3", "-tiles 2/1/3"},
		"decompression-bomb":  {"+tiles 0/0/0", "-tiles 0/0/0"},
		"duplicate-rows":      {"+tiles 1/0/1", "~index tile_index"},
	}
	byteCases := map[string]struct {
		diffAt []int // offsets that differ, same length as the base
		prefix int   // or: the file is this many leading bytes of the base
	}{
		"wal-mode":   {diffAt: []int{18, 19}},
		"not-sqlite": {diffAt: []int{0}},
		"truncated":  {prefix: 3 * sqlitePageSize},
		"empty-file": {prefix: 0},
	}
	base := buildMBTiles(mvtBlobs())
	cases := mbtilesCases()
	if len(cases) != len(schemaCases)+len(byteCases) {
		t.Errorf("%d cases", len(cases))
	}
	for _, c := range cases {
		b := c.build()
		if bc, ok := byteCases[c.name]; ok {
			if c.patch == nil || c.edit != nil {
				t.Errorf("%s: not a byte case", c.name)
			}
			if bc.diffAt == nil {
				if len(b) != bc.prefix || !bytes.Equal(b, base[:bc.prefix]) {
					t.Errorf("%s: not the %d-byte prefix of the corpus file", c.name, bc.prefix)
				}
				continue
			}
			var at []int
			for i := range b {
				if i < len(base) && b[i] != base[i] {
					at = append(at, i)
				}
			}
			if len(b) != len(base) || !slices.Equal(at, bc.diffAt) {
				t.Errorf("%s: %d bytes, differs at %v; want %d bytes differing at %v", c.name, len(b), at, len(base), bc.diffAt)
			}
			continue
		}
		want, ok := schemaCases[c.name]
		if !ok || c.edit == nil || c.patch != nil {
			t.Errorf("%s: not a pinned schema case", c.name)
			continue
		}
		s := mbtilesSchema(mvtBlobs())
		c.edit(&s)
		if got := schemaDiff(mbtilesSchema(mvtBlobs()), s); !slices.Equal(got, want) {
			t.Errorf("%s: changes %q, want %q", c.name, got, want)
		}
		if !bytes.Equal(b, sqliteDB(s, mbtilesAppID)) {
			t.Errorf("%s: file is not the edited schema", c.name)
		}
	}
}

// The changed values are the ones the descriptions name.
func TestMBTilesCaseValues(t *testing.T) {
	blobs, raw := mvtBlobs(), mvtRaw()
	schemas := map[string]sqlSchema{}
	for _, c := range mbtilesCases() {
		if c.edit != nil {
			s := mbtilesSchema(mvtBlobs())
			c.edit(&s)
			schemas[c.name] = s
		}
	}
	tileData := func(name string, z uint8, x, y uint32) []byte {
		s := schemas[name]
		return s.tileRow(z, x, y)[3].([]byte)
	}
	half := blobs[mvtKey(1, 1, 1)]
	if got := tileData("corrupt-gzip", 1, 1, 1); !bytes.Equal(got, half[:len(half)/2]) {
		t.Errorf("corrupt-gzip: 1/1/1 is not the first half of its member")
	}
	if got := tileData("mixed-gzip", 2, 1, 0); !bytes.Equal(got, raw[mvtKey(2, 1, 0)]) {
		t.Errorf("mixed-gzip: 2/1/0 is not the plain MVT")
	}
	if bomb, _ := gzipZeroBombDynamic(mbtilesBombMatches); !bytes.Equal(tileData("decompression-bomb", 0, 0, 0), bomb) {
		t.Errorf("decompression-bomb: 0/0/0 is not the bomb member")
	}
	dup := schemas["duplicate-rows"]
	first := dup.table("tiles").rows[0]
	if first[0] != int64(1) || first[1] != int64(0) || first[2] != int64(1) || !bytes.Equal(first[3].([]byte), blobs[mvtKey(1, 1, 1)]) {
		t.Errorf("duplicate-rows: first row %v is not 1/0/0 (tile_row 1) with 1/1/1's bytes", first[:3])
	}
	for name, kv := range map[string][2]string{"wrong-format": {"format", "png"}, "zoom-range-mismatch": {"maxzoom", "2"}} {
		s := schemas[name]
		if v := s.metadataRow(kv[0])[1]; v != kv[1] {
			t.Errorf("%s: %s = %v", name, kv[0], v)
		}
	}
}

// Every manifest entry is complete and consistent with its file, and its
// outcomes name only corpus coordinates.
func TestMBTilesManifest(t *testing.T) {
	files, m, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string][]byte{}
	for _, f := range files {
		byPath[f.Path] = f.Bytes
	}
	corpus := map[string]CorpusTile{}
	for _, ct := range m.MVTCorpus.Tiles {
		corpus[fmt.Sprintf("%d/%d/%d", ct.Z, ct.X, ct.Y)] = ct
	}
	names := map[string]bool{}
	for _, a := range m.MBTilesArchives {
		b, ok := byPath[a.File]
		if names[a.Name] || !ok || a.File != "mbtiles/"+a.Kind+"/"+a.Name+".mbtiles" || len(b) != a.Size || sum(b) != a.SHA256 ||
			a.Base != mvtMBTilesPath || a.Change == "" || a.Description == "" || a.Validity.Source == "" {
			t.Errorf("%s: inconsistent entry %+v", a.Name, a)
		}
		names[a.Name] = true
		v := a.Validity
		if !slices.Contains([]string{"valid", "invalid"}, v.SQLite) || !slices.Contains([]string{"valid", "invalid", "not-addressed"}, v.MBTiles) ||
			(a.Kind == "valid") != (v.MBTiles == "valid") || (a.Kind != "valid" && a.Kind != "malformed") {
			t.Errorf("%s: kind %s, validity %+v", a.Name, a.Kind, v)
		}
		if len(a.ExpectedOpen) == 0 || slices.ContainsFunc(a.ExpectedOpen, func(s string) bool { return s != "ok" && s != "refuse" }) ||
			(!slices.Contains(a.ExpectedOpen, "ok") && len(a.Tiles) > 0) {
			t.Errorf("%s: expected_open %q with %d tiles", a.Name, a.ExpectedOpen, len(a.Tiles))
		}
		for _, tl := range a.Tiles {
			ct, ok := corpus[fmt.Sprintf("%d/%d/%d", tl.Z, tl.X, tl.Y)]
			if !ok || tl.TMSRow != ct.TMSRow || len(tl.Outcomes) == 0 || tl.Purpose == "" {
				t.Errorf("%s: tile %+v", a.Name, tl)
			}
			for _, o := range tl.Outcomes {
				coord, isFeatures := strings.CutPrefix(o, "features:")
				if o != "refuse" && o != "absent" && (!isFeatures || corpus[coord].Status != "present") {
					t.Errorf("%s: outcome %q", a.Name, o)
				}
			}
		}
	}
	if len(m.MBTilesArchives) != 14 {
		t.Errorf("%d MBTiles archives, want 2 valid + 12 malformed", len(m.MBTilesArchives))
	}
}
