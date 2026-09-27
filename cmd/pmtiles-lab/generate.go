package main

import (
	"context"
	"fmt"
	"io"

	"github.com/lansenou/pmtiles-conformance-lab/internal/fixtures"
)

// version is the CLI release version, set at release build time with
// -ldflags "-X main.version=v0.4.N". It is independent of fixtures.Version,
// the generator (corpus) version.
var version = "dev"

func cmdVersion(_ context.Context, _ []string, stdout, _ io.Writer) error {
	fmt.Fprintf(stdout, "pmtiles-lab %s (fixtures generator %s, PMTiles spec %s)\n", version, fixtures.Version, "v3.6@8b8ddea")
	return nil
}

func cmdGenerate(_ context.Context, args []string, stdout, stderr io.Writer) error {
	fs := newFlags("generate", "generate --out DIR [--force]",
		"  pmtiles-lab generate --out fixtures\n  pmtiles-lab generate --out fixtures --force   # regenerate an existing corpus\n", stderr)
	out := fs.String("out", "", "output directory (must be empty, or an existing corpus with --force)")
	force := fs.Bool("force", false, "overwrite a directory that already holds a pmtiles-lab corpus")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if *out == "" || len(pos) > 0 {
		return usageError{"--out DIR is required and no positional arguments are accepted"}
	}
	files, m, err := fixtures.Generate()
	if err != nil {
		return err
	}
	if err := fixtures.WriteDir(*out, files, *force); err != nil {
		return err
	}
	for _, a := range m.Archives {
		fmt.Fprintf(stdout, "%-11s %-40s %s\n", a.Kind, a.File, a.SHA256)
	}
	for _, f := range m.MVTCorpus.Files {
		fmt.Fprintf(stdout, "%-11s %-40s %s\n", "mvt-corpus", f.File, f.SHA256)
	}
	fmt.Fprintf(stdout, "wrote %d archives, %d mvt corpus files and manifest.json to %s\n", len(m.Archives), len(m.MVTCorpus.Files), *out)
	return nil
}
