package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/lansenou/pmtiles-conformance-lab/internal/rangeserver"
	"github.com/lansenou/pmtiles-conformance-lab/internal/scenarios"
)

const maxServedFiles = 256

func cmdServe(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fset := newFlags("serve", "serve (--archive FILE | --dir DIR) [--scenario NAME] [--addr 127.0.0.1:0] [--delay 2s]",
		`  pmtiles-lab serve --archive fixtures/valid/root-none.pmtiles
  pmtiles-lab serve --dir fixtures --scenario truncated-body --addr 127.0.0.1:8080

URLs:
  /<file>                     the file under the default --scenario
  /scenarios/<name>/<file>    the file under any scenario
  GET /__lab/trace            request trace (JSON)
  POST /__lab/reset           clear trace and scenario state
  GET /__lab/scenarios        scenario list (JSON)
`, stderr)
	archive := fset.String("archive", "", "serve one archive at /<basename>")
	dir := fset.String("dir", "", "serve every *.pmtiles file under DIR at its relative path")
	scenario := fset.String("scenario", "normal", "default scenario (see: pmtiles-lab scenarios)")
	addr := fset.String("addr", "127.0.0.1:0", "listen address; port 0 picks a free port")
	delay := fset.Duration("delay", 2*time.Second, "delay used by slow-headers and stall-body (max 10s)")
	pos, err := parse(fset, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 || (*archive == "") == (*dir == "") {
		return usageError{"exactly one of --archive or --dir is required"}
	}
	if *delay < 0 || *delay > rangeserver.MaxDelay {
		return usageError{fmt.Sprintf("--delay must be between 0 and %s", rangeserver.MaxDelay)}
	}
	if _, ok := scenarios.Lookup(*scenario); !ok {
		return usageError{fmt.Sprintf("unknown scenario %q", *scenario)}
	}
	files, err := loadFiles(*archive, *dir)
	if err != nil {
		return err
	}
	srv, err := rangeserver.New(rangeserver.Config{Files: files, Scenarios: scenarios.All(), Default: *scenario, Delay: *delay})
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	base := "http://" + ln.Addr().String()
	fmt.Fprintf(stdout, "listening on %s (scenario %s)\n", base, *scenario)
	for _, p := range srv.Paths() {
		fmt.Fprintf(stdout, "%s/%s\n", base, p)
	}
	fmt.Fprintf(stdout, "trace: %s/__lab/trace\n", base)
	if f, ok := stdout.(*os.File); ok {
		f.Sync()
	}

	hs := &http.Server{Handler: srv, ReadHeaderTimeout: 10 * time.Second, MaxHeaderBytes: 16 << 10,
		WriteTimeout: rangeserver.MaxDelay + 20*time.Second, ErrorLog: nil}
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

func loadFiles(archive, dir string) ([]*rangeserver.File, error) {
	if archive != "" {
		b, err := readBounded(archive)
		if err != nil {
			return nil, err
		}
		return []*rangeserver.File{rangeserver.NewFile(filepath.Base(archive), b)}, nil
	}
	var files []*rangeserver.File
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".pmtiles") {
			return err
		}
		if len(files) >= maxServedFiles {
			return fmt.Errorf("more than %d .pmtiles files under %s", maxServedFiles, dir)
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		b, err := readBounded(p)
		if err != nil {
			return err
		}
		files = append(files, rangeserver.NewFile(filepath.ToSlash(rel), b))
		return nil
	})
	if err == nil && len(files) == 0 {
		err = fmt.Errorf("no .pmtiles files under %s", dir)
	}
	return files, err
}

func readBounded(p string) ([]byte, error) {
	st, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if st.Size() > rangeserver.MaxArchiveBytes {
		return nil, fmt.Errorf("%s is %d bytes, limit %d", p, st.Size(), rangeserver.MaxArchiveBytes)
	}
	return os.ReadFile(p)
}

func cmdScenarios(_ context.Context, args []string, stdout, stderr io.Writer) error {
	fs := newFlags("scenarios", "scenarios [--json]", "  pmtiles-lab scenarios\n  pmtiles-lab scenarios --json\n", stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	if pos, err := parse(fs, args); err != nil {
		return err
	} else if len(pos) > 0 {
		return usageError{"no positional arguments accepted"}
	}
	all := scenarios.All()
	if *asJSON {
		return writeJSON(stdout, all)
	}
	for _, s := range all {
		fmt.Fprintf(stdout, "%-24s %-31s %s\n", s.Name, s.Validity, s.Description)
	}
	return nil
}
