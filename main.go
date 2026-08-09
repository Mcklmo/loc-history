// Command loc-history charts how a codebase's product and test code grew,
// commit by commit, over the life of a branch.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/mcklmo/loc-history/internal/cache"
	"github.com/mcklmo/loc-history/internal/cloc"
	"github.com/mcklmo/loc-history/internal/gitlog"
	"github.com/mcklmo/loc-history/internal/pipeline"
	"github.com/mcklmo/loc-history/internal/writer"
)

func main() {
	cfg, err := parseFlags(os.Args[1:], os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "loc-history:", err)
		os.Exit(2)
	}

	// Ctrl-C cancels the walk rather than killing it, so the sinks still get
	// closed and a partial artifact survives.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := execute(ctx, cfg, cloc.NewDockerRunner(), os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "loc-history:", err)
		os.Exit(1)
	}
}

// config is the parsed command line.
type config struct {
	Repo      string
	Branch    string
	Folder    string
	TestRegex string

	Out         []string
	FileOut     string
	FileFormat  writer.Format
	GraphOut    string
	Granularity writer.Granularity

	Jobs        int
	WorkDir     string
	CacheDir    string
	NoCache     bool
	FirstParent bool
	Limit       int
	FailFast    bool
	Image       string
}

// knownSinks is the set --out accepts. Adding a sink is additive: implement
// writer.Writer, then name it here.
var knownSinks = map[string]bool{"console": true, "file": true, "graph": true}

func parseFlags(args []string, errOut io.Writer) (config, error) {
	var cfg config
	var out, fileFormat, granularity string

	fs := flag.NewFlagSet("loc-history", flag.ContinueOnError)
	fs.SetOutput(errOut)
	fs.StringVar(&cfg.Repo, "repo", ".", "repository path")
	fs.StringVar(&cfg.Branch, "branch", "main", "branch to walk")
	fs.StringVar(&cfg.Folder, "folder", "src", "source folder to count")
	fs.StringVar(&cfg.TestRegex, "test-regex", cloc.DefaultTestRegex,
		"regex splitting test files from product files, matched against the basename")
	fs.StringVar(&out, "out", "console", "sinks, comma-separated: console, file, graph")
	fs.StringVar(&cfg.FileOut, "file-out", "loc-history.csv", "path for the file sink")
	fs.StringVar(&fileFormat, "file-format", "csv", "file sink format: csv or ndjson")
	fs.StringVar(&cfg.GraphOut, "graph-out", "loc-history.html", "path for the graph sink")
	fs.StringVar(&granularity, "granularity", "hour", "graph time bucket: hour or day")
	fs.IntVar(&cfg.Jobs, "jobs", 4, "commits processed concurrently")
	fs.StringVar(&cfg.WorkDir, "work-dir", "/tmp",
		"scratch root; must be a path Docker is allowed to bind-mount")
	fs.BoolVar(&cfg.FirstParent, "first-parent", true, "follow the trunk only")
	fs.IntVar(&cfg.Limit, "limit", 0, "most recent N commits; 0 means all")
	fs.BoolVar(&cfg.FailFast, "fail-fast", false, "abort on the first commit that fails")
	fs.StringVar(&cfg.CacheDir, "cache-dir", defaultCacheDir(), "where counted commits are remembered")
	fs.BoolVar(&cfg.NoCache, "no-cache", false, "recompute everything, ignoring stored counts")
	fs.StringVar(&cfg.Image, "image", cloc.DefaultImage, "cloc container image")

	if err := fs.Parse(args); err != nil {
		return config{}, err
	}

	for _, name := range strings.Split(out, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if !knownSinks[name] {
			return config{}, fmt.Errorf("unknown sink %q in -out; known sinks: %s",
				name, strings.Join(sinkNames(), ", "))
		}
		cfg.Out = append(cfg.Out, name)
	}
	if len(cfg.Out) == 0 {
		return config{}, errors.New("-out needs at least one sink")
	}
	if cfg.Jobs < 1 {
		return config{}, fmt.Errorf("-jobs must be at least 1, got %d", cfg.Jobs)
	}
	if cfg.Limit < 0 {
		return config{}, fmt.Errorf("-limit cannot be negative, got %d", cfg.Limit)
	}
	if _, err := regexp.Compile(cfg.TestRegex); err != nil {
		return config{}, fmt.Errorf("-test-regex is not a valid expression: %w", err)
	}

	format, err := ParseFileFormat(fileFormat)
	if err != nil {
		return config{}, err
	}
	cfg.FileFormat = format

	gran, err := ParseGranularity(granularity)
	if err != nil {
		return config{}, err
	}
	cfg.Granularity = gran

	return cfg, nil
}

// defaultCacheDir puts entries where the platform expects a cache to live —
// ~/Library/Caches on macOS, $XDG_CACHE_HOME or ~/.cache elsewhere.
func defaultCacheDir() string {
	base, err := os.UserCacheDir()
	if err != nil {
		return filepath.Join(".", ".loc-history-cache")
	}
	return filepath.Join(base, "loc-history")
}

// ParseFileFormat is exported for the flag layer to validate before any work
// starts, rather than failing after a full walk.
func ParseFileFormat(s string) (writer.Format, error) {
	f, err := writer.ParseFormat(s)
	if err != nil {
		return 0, fmt.Errorf("-file-format: %w", err)
	}
	return f, nil
}

// ParseGranularity validates the graph's time bucket at the flag layer, for the
// same reason as ParseFileFormat.
func ParseGranularity(s string) (writer.Granularity, error) {
	g, err := writer.ParseGranularity(s)
	if err != nil {
		return 0, fmt.Errorf("-granularity: %w", err)
	}
	return g, nil
}

// graphSubtitle says what was counted, so a saved page is still legible a
// month later.
func graphSubtitle(cfg config) string {
	repo := cfg.Repo
	if abs, err := filepath.Abs(repo); err == nil {
		repo = filepath.Base(abs)
	}
	folder := cfg.Folder
	if folder == "" {
		folder = "whole tree"
	}
	return fmt.Sprintf("%s · %s · %s", repo, cfg.Branch, folder)
}

func sinkNames() []string {
	names := make([]string, 0, len(knownSinks))
	for name := range knownSinks {
		names = append(names, name)
	}
	return names
}

func execute(ctx context.Context, cfg config, runner cloc.Runner, stdout, stderr io.Writer) error {
	commits, err := gitlog.Commits(gitlog.Options{
		Repo:        cfg.Repo,
		Branch:      cfg.Branch,
		FirstParent: cfg.FirstParent,
		Limit:       cfg.Limit,
	})
	if err != nil {
		return err
	}

	// One canary container before spending a hundred more: a scratch directory
	// Docker cannot see mounts empty, and cloc reports that as a clean zero.
	clocVersion, err := cloc.VerifyMount(ctx, runner, cfg.WorkDir)
	if err != nil {
		return err
	}

	var store cache.Store
	if !cfg.NoCache {
		// A commit tree never changes, so yesterday's count is still today's
		// answer as long as the question is identical.
		c, err := cache.New(cfg.CacheDir)
		if err != nil {
			return err
		}
		store = c
	}

	sinks := make([]writer.Writer, 0, len(cfg.Out))
	for _, name := range cfg.Out {
		switch name {
		case "console":
			sinks = append(sinks, writer.NewConsole(stdout))
		case "file":
			f, err := writer.NewFile(cfg.FileOut, cfg.FileFormat)
			if err != nil {
				// Close whatever is already open; pipeline.Run never got the
				// chance to take ownership of them.
				writer.MultiWriter(sinks...).Close()
				return err
			}
			sinks = append(sinks, f)
		case "graph":
			g, err := writer.NewGraph(cfg.GraphOut, writer.GraphOptions{
				Title:       "loc-history",
				Subtitle:    graphSubtitle(cfg),
				Granularity: cfg.Granularity,
			})
			if err != nil {
				writer.MultiWriter(sinks...).Close()
				return err
			}
			sinks = append(sinks, g)
		}
	}

	start := time.Now()
	stats, err := pipeline.Run(ctx, commits, runner, writer.MultiWriter(sinks...), pipeline.Options{
		Repo:      cfg.Repo,
		Folder:    cfg.Folder,
		TestRegex: cfg.TestRegex,
		Image:     cfg.Image,
		Jobs:      cfg.Jobs,
		WorkDir:   cfg.WorkDir,
		FailFast:  cfg.FailFast,

		Cache:       store,
		ClocVersion: clocVersion,

		ErrOut: stderr,
	})

	fmt.Fprintf(stderr, "%d commits, %d skipped, %d failed in %s\n",
		len(commits), stats.Skipped, stats.Failed, time.Since(start).Truncate(time.Millisecond))

	return err
}
