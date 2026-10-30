package verify

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paulmach/orb/encoding/mvt"
)

// openCopy writes b to a temporary file and opens it read-only. A WAL-mode
// file makes SQLite create -wal and -shm files beside it, so the committed
// fixtures are never opened in place.
func openCopy(t *testing.T, b []byte) *sql.DB {
	t.Helper()
	p, err := filepath.Abs(filepath.Join(t.TempDir(), "case.mbtiles"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	p = filepath.ToSlash(p)
	if p[0] != '/' {
		p = "/" + p
	}
	db, err := sql.Open("sqlite", "file://"+p+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func hexSum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// tileRows returns tile_data of every row at XYZ z/x/y, ordered by the SQL
// clause order ("", or "ORDER BY rowid" where tiles is a table).
func tileRows(t *testing.T, db *sql.DB, order string, z, x, y uint32) [][]byte {
	t.Helper()
	rows, err := db.Query("SELECT tile_data FROM tiles WHERE zoom_level = ? AND tile_column = ? AND tile_row = ? "+order, z, x, (1<<z)-1-y)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out [][]byte
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			t.Fatal(err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func queryString(t *testing.T, db *sql.DB, q string) string {
	t.Helper()
	var s string
	if err := db.QueryRow(q).Scan(&s); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return s
}

// The facts each case states in docs/fixtures.md, read with the SQLite
// engine and the standard library only. corpus maps "z/x/y" to the manifest
// tile.
var mbtilesCaseChecks = map[string]func(t *testing.T, db *sql.DB, corpus map[string]corpusTile){
	"tiles-view": func(t *testing.T, db *sql.DB, _ map[string]corpusTile) {
		if s := queryString(t, db, "SELECT type FROM sqlite_master WHERE name = 'tiles'"); s != "view" {
			t.Errorf("tiles is a %s", s)
		}
	},
	"wal-mode": func(t *testing.T, db *sql.DB, _ map[string]corpusTile) {
		if s := queryString(t, db, "PRAGMA journal_mode"); s != "wal" {
			t.Errorf("journal_mode %s", s)
		}
	},
	"empty-file": func(t *testing.T, db *sql.DB, _ map[string]corpusTile) {
		if s := queryString(t, db, "SELECT count(*) FROM sqlite_master"); s != "0" {
			t.Errorf("%s schema objects", s)
		}
	},
	"no-metadata-table": func(t *testing.T, db *sql.DB, _ map[string]corpusTile) {
		if s := queryString(t, db, "SELECT group_concat(name) FROM (SELECT name FROM sqlite_master ORDER BY name)"); s != "tile_index,tiles" {
			t.Errorf("schema %s", s)
		}
	},
	"no-tiles-table": func(t *testing.T, db *sql.DB, _ map[string]corpusTile) {
		if s := queryString(t, db, "SELECT group_concat(name) FROM sqlite_master"); s != "metadata" {
			t.Errorf("schema %s", s)
		}
	},
	"wrong-format": func(t *testing.T, db *sql.DB, _ map[string]corpusTile) {
		if s := queryString(t, db, "SELECT value FROM metadata WHERE name = 'format'"); s != "png" {
			t.Errorf("format %s", s)
		}
	},
	"no-vector-layers": func(t *testing.T, db *sql.DB, _ map[string]corpusTile) {
		if s := queryString(t, db, "SELECT count(*) FROM metadata WHERE name = 'json'"); s != "0" {
			t.Errorf("%s json rows", s)
		}
	},
	"zoom-range-mismatch": func(t *testing.T, db *sql.DB, corpus map[string]corpusTile) {
		rows := tileRows(t, db, "", 3, 5, 2)
		if s := queryString(t, db, "SELECT value FROM metadata WHERE name = 'maxzoom'"); s != "2" || len(rows) != 1 || hexSum(rows[0]) != corpus["3/5/2"].SHA256 {
			t.Errorf("maxzoom %s, %d rows at 3/5/2", s, len(rows))
		}
	},
	"corrupt-gzip": func(t *testing.T, db *sql.DB, corpus map[string]corpusTile) {
		rows := tileRows(t, db, "", 1, 1, 1)
		if len(rows) != 1 || uint64(len(rows[0])) != corpus["1/1/1"].Length/2 {
			t.Fatalf("%d rows at 1/1/1", len(rows))
		}
		zr, err := gzip.NewReader(bytes.NewReader(rows[0]))
		if err == nil {
			_, err = io.ReadAll(zr)
		}
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("gunzip 1/1/1: %v", err)
		}
	},
	"mixed-gzip": func(t *testing.T, db *sql.DB, corpus map[string]corpusTile) {
		rows := tileRows(t, db, "", 2, 1, 0)
		if len(rows) != 1 || rows[0][0] == 0x1f {
			t.Fatalf("%d rows at 2/1/0", len(rows))
		}
		layers, err := mvt.Unmarshal(rows[0])
		want := corpus["2/1/0"].Features[0].Properties["name"]
		if err != nil || len(layers) != 1 || len(layers[0].Features) != 1 || layers[0].Features[0].Properties["name"] != want {
			t.Errorf("orb: %v, want one feature named %v", err, want)
		}
	},
	"decompression-bomb": func(t *testing.T, db *sql.DB, _ map[string]corpusTile) {
		rows := tileRows(t, db, "", 0, 0, 0)
		if len(rows) != 1 || len(rows[0]) != 65568 {
			t.Fatalf("%d rows at 0/0/0", len(rows))
		}
		zr, err := gzip.NewReader(bytes.NewReader(rows[0]))
		if err != nil {
			t.Fatal(err)
		}
		var nonZero bool
		buf := make([]byte, 64<<10)
		n := 0
		for {
			k, err := zr.Read(buf)
			n += k
			nonZero = nonZero || bytes.Count(buf[:k], []byte{0}) != k
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		if n != 67633153 || nonZero {
			t.Errorf("inflated %d bytes (non-zero %v), want 67633153 zeros", n, nonZero)
		}
	},
	"duplicate-rows": func(t *testing.T, db *sql.DB, corpus map[string]corpusTile) {
		rows := tileRows(t, db, "ORDER BY rowid", 1, 0, 0)
		if len(rows) != 2 || hexSum(rows[0]) != corpus["1/1/1"].SHA256 || hexSum(rows[1]) != corpus["1/0/0"].SHA256 {
			t.Errorf("%d rows at 1/0/0; want 1/1/1's bytes then 1/0/0's, by rowid", len(rows))
		}
		if s := queryString(t, db, "SELECT \"unique\" FROM pragma_index_list('tiles') WHERE name = 'tile_index'"); s != "0" {
			t.Errorf("tile_index unique %s", s)
		}
	},
}

type corpusTile struct {
	Status   string
	SHA256   string
	Length   uint64
	Features []struct{ Properties map[string]any }
}

// TestMBTilesCasesWithSQLite opens every MBTiles case with the SQLite engine.
// SQLite-valid files pass integrity_check and the others fail with the
// engine's error. Where a file has tiles, every coordinate its manifest entry
// does not list holds the corpus bytes, and the case's own change is there.
func TestMBTilesCasesWithSQLite(t *testing.T) {
	m, _ := load(t)
	engineErr := map[string]string{"not-sqlite": "file is not a database", "truncated": "database disk image is malformed"}
	corpus := map[string]corpusTile{}
	for _, tl := range m.MVTCorpus.Tiles {
		ct := corpusTile{Status: tl.Status, SHA256: tl.SHA256, Length: tl.Length}
		for _, f := range tl.Features {
			ct.Features = append(ct.Features, struct{ Properties map[string]any }{f.Properties})
		}
		corpus[fmt.Sprintf("%d/%d/%d", tl.Z, tl.X, tl.Y)] = ct
	}
	if len(m.MBTilesArchives) != 14 || len(mbtilesCaseChecks)+len(engineErr) != 14 {
		t.Fatalf("%d MBTiles cases", len(m.MBTilesArchives))
	}
	for _, a := range m.MBTilesArchives {
		t.Run(a.Name, func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join(fixturesDir(), filepath.FromSlash(a.File)))
			if err != nil || hexSum(b) != a.SHA256 {
				t.Fatalf("%s: %v or sha256 differs from manifest", a.File, err)
			}
			db := openCopy(t, b)
			var ic string
			err = db.QueryRow("PRAGMA integrity_check").Scan(&ic)
			if want, invalid := engineErr[a.Name]; invalid || a.Validity.SQLite != "valid" {
				if !invalid || a.Validity.SQLite != "invalid" || err == nil || !strings.Contains(err.Error(), want) {
					t.Errorf("SQLite validity %s: integrity_check %q %v", a.Validity.SQLite, ic, err)
				}
				return
			}
			if err != nil || ic != "ok" {
				t.Fatalf("integrity_check: %q %v", ic, err)
			}
			mbtilesCaseChecks[a.Name](t, db, corpus)
			if queryString(t, db, "SELECT count(*) FROM sqlite_master WHERE name = 'tiles'") == "0" {
				return
			}
			listed := map[string]bool{}
			for _, tl := range a.Tiles {
				listed[fmt.Sprintf("%d/%d/%d", tl.Z, tl.X, tl.Y)] = true
			}
			for _, tl := range m.MVTCorpus.Tiles {
				key := fmt.Sprintf("%d/%d/%d", tl.Z, tl.X, tl.Y)
				if listed[key] {
					continue
				}
				rows := tileRows(t, db, "", tl.Z, tl.X, tl.Y)
				if (tl.Status == "absent" && len(rows) != 0) || (tl.Status == "present" && (len(rows) != 1 || hexSum(rows[0]) != tl.SHA256)) {
					t.Errorf("%s: %d rows, corpus status %s", key, len(rows), tl.Status)
				}
			}
		})
	}
}
