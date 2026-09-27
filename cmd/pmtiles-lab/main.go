// Command pmtiles-lab generates PMTiles v3 conformance fixtures and a shared
// MVT corpus, inspects archives, serves them with scripted HTTP range faults,
// serves PBF MBTiles as TileJSON plus XYZ, and checks both delivery paths.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// Exit codes (docs/plan.md §5).
const (
	exitOK      = 0
	exitFailed  = 1
	exitUsage   = 2
	exitRuntime = 3
)

const usage = `pmtiles-lab: PMTiles v3 conformance lab and small tile delivery test tool

Usage:
  pmtiles-lab generate --out DIR [--force]
  pmtiles-lab inspect ARCHIVE [--tile Z/X/Y]... [--json]
  pmtiles-lab serve (--archive FILE | --dir DIR) [--scenario NAME] [--addr 127.0.0.1:0] [--delay 2s]
  pmtiles-lab probe --url URL --manifest FILE [--archive NAME] [--json] [--timeout 5s]
  pmtiles-lab scenarios [--json]
  pmtiles-lab serve-xyz --mbtiles FILE [--addr 127.0.0.1:0] [--public-url URL]
  pmtiles-lab tilecheck (--pmtiles FILE|URL | --tilejson URL) --manifest FILE [--json]
  pmtiles-lab version

Exit codes: 0 ok, 1 check failed, 2 usage error, 3 I/O or runtime error.
Run "pmtiles-lab COMMAND -h" for command help and examples.
`

// usageError marks errors that should exit with code 2.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

// checkFailed marks a completed check with a negative result (exit 1).
var errCheckFailed = errors.New("check failed")

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	cmds := map[string]func(context.Context, []string, io.Writer, io.Writer) error{
		"generate":  cmdGenerate,
		"inspect":   cmdInspect,
		"serve":     cmdServe,
		"probe":     cmdProbe,
		"scenarios": cmdScenarios,
		"serve-xyz": cmdServeXYZ,
		"tilecheck": cmdTileCheck,
		"version":   cmdVersion,
	}
	name := args[0]
	if name == "-h" || name == "--help" || name == "help" {
		fmt.Fprint(stdout, usage)
		return exitOK
	}
	cmd, ok := cmds[name]
	if !ok {
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", name, usage)
		return exitUsage
	}
	err := cmd(ctx, args[1:], stdout, stderr)
	var ue usageError
	switch {
	case err == nil:
		return exitOK
	case errors.Is(err, flag.ErrHelp):
		return exitOK
	case errors.As(err, &ue):
		fmt.Fprintf(stderr, "pmtiles-lab %s: %v\n", name, err)
		return exitUsage
	case errors.Is(err, errCheckFailed):
		return exitFailed
	default:
		fmt.Fprintf(stderr, "pmtiles-lab %s: %v\n", name, err)
		return exitRuntime
	}
}

// newFlags returns a FlagSet whose help text includes examples.
func newFlags(name, synopsis, examples string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: pmtiles-lab %s\n\nFlags:\n", synopsis)
		fs.PrintDefaults()
		fmt.Fprintf(stderr, "\nExamples:\n%s", examples)
	}
	return fs
}

// parse accepts flags before and after positional arguments.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, err
			}
			return nil, usageError{err.Error()}
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// multiFlag collects a repeated string flag.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }
