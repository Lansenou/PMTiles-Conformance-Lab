package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/lansenou/pmtiles-conformance-lab/internal/mbtiles"
	"github.com/lansenou/pmtiles-conformance-lab/internal/tilecheck"
	"github.com/lansenou/pmtiles-conformance-lab/internal/xyz"
)

func cmdServeXYZ(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fset := newFlags("serve-xyz", "serve-xyz --mbtiles FILE [--addr 127.0.0.1:0] [--public-url URL]",
		`  pmtiles-lab serve-xyz --mbtiles fixtures/mvt/points.mbtiles
  pmtiles-lab serve-xyz --mbtiles my-vector-tiles.mbtiles --addr 127.0.0.1:8081

URLs:
  /tiles.json            TileJSON 3.0.0 with absolute tile URLs and vector_layers
  /{z}/{x}/{y}.pbf       one tile (XYZ rows; converted to the MBTiles TMS tile_row)

Only pbf (gzip-compressed MVT) MBTiles files are served. The file is opened
read-only and never modified.
`, stderr)
	file := fset.String("mbtiles", "", "MBTiles file with format pbf")
	addr := fset.String("addr", "127.0.0.1:0", "listen address; port 0 picks a free port")
	public := fset.String("public-url", "", "base URL for TileJSON tile URLs (default: http://<request Host>)")
	pos, err := parse(fset, args)
	if err != nil {
		return err
	}
	if *file == "" || len(pos) > 0 {
		return usageError{"--mbtiles FILE is required and no positional arguments are accepted"}
	}
	if *public != "" {
		if u, err := url.Parse(*public); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return usageError{fmt.Sprintf("--public-url %q is not an absolute http(s) URL", *public)}
		}
	}
	f, err := mbtiles.Open(*file)
	if errors.Is(err, mbtiles.ErrUnsupported) {
		fmt.Fprintf(stderr, "pmtiles-lab serve-xyz: %v\n", err)
		return errCheckFailed
	}
	if err != nil {
		return err
	}
	defer f.Close()
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	base := "http://" + ln.Addr().String()
	fmt.Fprintf(stdout, "serving %s (%s, zoom %d-%d) on %s\n", *file, f.Meta.Format, f.Meta.MinZoom, f.Meta.MaxZoom, base)
	fmt.Fprintf(stdout, "tilejson: %s%s\n", base, xyz.TileJSONPath)
	fmt.Fprintf(stdout, "tiles: %s/{z}/{x}/{y}.pbf\n", base)
	if o, ok := stdout.(*os.File); ok {
		o.Sync()
	}
	hs := &http.Server{Handler: &xyz.Handler{File: f, PublicURL: *public}, ReadHeaderTimeout: 10 * time.Second,
		MaxHeaderBytes: 16 << 10, WriteTimeout: 30 * time.Second}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := hs.Shutdown(sctx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return nil
}

func cmdTileCheck(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := newFlags("tilecheck", "tilecheck (--pmtiles FILE|URL | --tilejson URL) --manifest FILE [--json] [--timeout 30s]",
		`  pmtiles-lab tilecheck --pmtiles fixtures/mvt/points.pmtiles --manifest fixtures/manifest.json
  pmtiles-lab tilecheck --pmtiles http://127.0.0.1:8080/mvt/points.pmtiles --manifest fixtures/manifest.json --json
  pmtiles-lab tilecheck --tilejson http://127.0.0.1:8081/tiles.json --manifest fixtures/manifest.json --json

Fetches every coordinate of the manifest's mvt_corpus, decompresses and
decodes the MVT, and compares it with the expected features. Exit 1 on any
mismatch.
`, stderr)
	pm := fs.String("pmtiles", "", "PMTiles archive: a local file or an http(s) URL (read with range requests)")
	tj := fs.String("tilejson", "", "TileJSON URL of an XYZ endpoint")
	manifest := fs.String("manifest", "", "manifest.json with an mvt_corpus section")
	asJSON := fs.Bool("json", false, "print the JSON report")
	timeout := fs.Duration("timeout", 30*time.Second, "total timeout")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if (*pm == "") == (*tj == "") || *manifest == "" || len(pos) > 0 {
		return usageError{"exactly one of --pmtiles or --tilejson, and --manifest, are required"}
	}
	if *timeout <= 0 {
		return usageError{"--timeout must be positive"}
	}
	isURL := func(s string) bool { return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") }
	if *tj != "" && !isURL(*tj) {
		return usageError{fmt.Sprintf("--tilejson %q is not an http(s) URL", *tj)}
	}
	m, err := readManifest(*manifest)
	if err != nil {
		return err
	}
	if m.MVTCorpus == nil {
		return fmt.Errorf("manifest %s has no mvt_corpus (generator 0.4.0 or later)", *manifest)
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	client := &http.Client{Transport: &http.Transport{Proxy: nil, DisableCompression: true, MaxResponseHeaderBytes: 64 << 10},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var rep *tilecheck.Report
	switch {
	case *tj != "":
		rep = tilecheck.XYZ(ctx, client, *tj, m.MVTCorpus)
	case isURL(*pm):
		r, err := tilecheck.NewHTTPReaderAt(ctx, client, *pm)
		if err != nil {
			return err
		}
		rep = tilecheck.PMTiles(ctx, r, r.Size, *pm, m.MVTCorpus)
	default:
		f, err := os.Open(*pm)
		if err != nil {
			return err
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			return err
		}
		rep = tilecheck.PMTiles(ctx, f, uint64(st.Size()), *pm, m.MVTCorpus)
	}
	if *asJSON {
		if err := writeJSON(stdout, rep); err != nil {
			return err
		}
	} else {
		printTileCheck(stdout, rep)
	}
	if rep.Result != "pass" {
		return errCheckFailed
	}
	return nil
}

func printTileCheck(w io.Writer, r *tilecheck.Report) {
	fmt.Fprintf(w, "tilecheck %s %s (corpus %s)\n", r.Source, r.Location, r.Corpus)
	for _, c := range r.Metadata {
		mark := "ok"
		if !c.OK {
			mark = "MISMATCH"
		}
		fmt.Fprintf(w, "  meta %-24s %s (%s)\n", c.Name, mark, c.Observed)
	}
	for _, t := range r.Tiles {
		mark := "ok"
		if !t.Match {
			mark = "MISMATCH"
		}
		fmt.Fprintf(w, "  tile %-8s expected %-7s observed %-7s features=%d %s\n",
			fmt.Sprintf("%d/%d/%d", t.Z, t.X, t.Y), t.Expected, t.Observed, len(t.Features), mark)
	}
	for _, f := range r.Failures {
		fmt.Fprintf(w, "FAIL %s: %s\n", f.Code, f.Message)
	}
	fmt.Fprintf(w, "tiles: %d/%d matched\nresult: %s\n", r.Summary.TilesMatched, r.Summary.TilesChecked, r.Result)
}
