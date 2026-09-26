package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/lansenou/pmtiles-conformance-lab/internal/fixtures"
	"github.com/lansenou/pmtiles-conformance-lab/internal/probe"
)

const maxManifestBytes = 4 << 20

func cmdProbe(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := newFlags("probe", "probe --url URL --manifest FILE [--archive NAME] [--json] [--timeout 5s]",
		`  pmtiles-lab probe --url http://127.0.0.1:8080/valid/root-none.pmtiles --manifest fixtures/manifest.json
  pmtiles-lab probe --url http://127.0.0.1:8080/scenarios/truncated-body/valid/root-none.pmtiles \
      --manifest fixtures/manifest.json --json
`, stderr)
	u := fs.String("url", "", "archive URL")
	manifest := fs.String("manifest", "", "manifest.json written by generate")
	name := fs.String("archive", "", "manifest archive name (default: match the URL path suffix)")
	asJSON := fs.Bool("json", false, "print the JSON report")
	lim := probe.DefaultLimits()
	fs.DurationVar(&lim.RequestTimeout, "timeout", lim.RequestTimeout, "per-request timeout")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if *u == "" || *manifest == "" || len(pos) > 0 {
		return usageError{"--url and --manifest are required"}
	}
	pu, err := url.Parse(*u)
	if err != nil || (pu.Scheme != "http" && pu.Scheme != "https") {
		return usageError{fmt.Sprintf("--url %q is not an http(s) URL", *u)}
	}
	if lim.RequestTimeout <= 0 || lim.RequestTimeout > lim.TotalTimeout {
		return usageError{fmt.Sprintf("--timeout must be in (0, %s]", lim.TotalTimeout)}
	}
	m, err := readManifest(*manifest)
	if err != nil {
		return err
	}
	a, err := pickArchive(m, *name, pu.Path)
	if err != nil {
		return err
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil, DisableCompression: true, MaxResponseHeaderBytes: 64 << 10},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	rep := probe.Run(ctx, client, *u, a, lim)
	if *asJSON {
		if err := writeJSON(stdout, rep); err != nil {
			return err
		}
	} else {
		printProbe(stdout, rep)
	}
	if rep.Result != "pass" {
		return errCheckFailed
	}
	return nil
}

func readManifest(p string) (*fixtures.Manifest, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var m fixtures.Manifest
	if err := json.NewDecoder(io.LimitReader(f, maxManifestBytes)).Decode(&m); err != nil {
		return nil, fmt.Errorf("manifest %s: %w", p, err)
	}
	if m.Schema != fixtures.ManifestSchema {
		return nil, fmt.Errorf("manifest %s: schema %q, want %q", p, m.Schema, fixtures.ManifestSchema)
	}
	return &m, nil
}

func pickArchive(m *fixtures.Manifest, name, path string) (fixtures.Archive, error) {
	var hits []fixtures.Archive
	for _, a := range m.Archives {
		if (name != "" && a.Name == name) || (name == "" && strings.HasSuffix(path, "/"+a.File)) {
			hits = append(hits, a)
		}
	}
	if len(hits) != 1 {
		return fixtures.Archive{}, usageError{fmt.Sprintf("cannot select one manifest archive (name %q, path %q): %d matches; use --archive", name, path, len(hits))}
	}
	if hits[0].Kind != "valid" {
		return fixtures.Archive{}, usageError{fmt.Sprintf("archive %s is %s; probe needs a valid archive", hits[0].Name, hits[0].Kind)}
	}
	return hits[0], nil
}

func printProbe(w io.Writer, r *probe.Report) {
	fmt.Fprintf(w, "probe %s (%s)\n", r.URL, r.Archive)
	for _, q := range r.Requests {
		line := fmt.Sprintf("  #%d %-9s %-22s -> %d %s len=%d", q.Seq, q.Purpose, q.Range, q.Status, q.ContentRange, q.BodyBytes)
		if q.Error != "" {
			line += " ERROR " + q.Error
		}
		fmt.Fprintln(w, line)
	}
	fmt.Fprintf(w, "tiles: %d/%d matched\n", r.Summary.TilesMatched, r.Summary.TilesChecked)
	for _, s := range r.Warnings {
		fmt.Fprintf(w, "warn: %s\n", s)
	}
	for _, f := range r.Failures {
		fmt.Fprintf(w, "FAIL %s: %s\n", f.Code, f.Message)
	}
	fmt.Fprintf(w, "result: %s (%d requests, %d bytes)\n", r.Result, r.Summary.Requests, r.Summary.BytesReceived)
}
