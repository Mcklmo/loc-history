// Package pipeline walks a branch's commits in parallel and emits records in
// commit order.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/mcklmo/loc-history/internal/cache"
	"github.com/mcklmo/loc-history/internal/cloc"
	"github.com/mcklmo/loc-history/internal/report"
	"github.com/mcklmo/loc-history/internal/tree"
)

// Sink consumes records in commit order, oldest first, and is closed exactly
// once when the walk ends. A writer.Writer behind a bucket.Aggregator is the
// shipped implementation; pipeline does not care which.
type Sink interface {
	Write(report.Record) error
	Close() error
}

// Options configures a walk.
type Options struct {
	Repo      string // repository to read
	Folder    string // source folder to count
	TestRegex string // splits product code from test code
	Image     string // cloc container image; empty means the default

	Jobs     int    // commits processed concurrently; below 1 means sequential
	WorkDir  string // scratch root; must be a path Docker can bind-mount
	FailFast bool   // abort the whole run on the first commit that fails

	// Cache serves commits that have already been counted. Nil disables it.
	Cache cache.Store
	// ClocVersion keys the cache; counts from another version are not answers
	// to the same question.
	ClocVersion string

	ErrOut io.Writer // where per-commit failures are reported; nil means stderr
}

// Stats summarises what a walk did.
type Stats struct {
	Written int // records handed to the sink
	Skipped int // commits with no source folder
	Failed  int // commits that errored
}

// Run counts every commit and writes the results in chronological order.
//
// Container startup, not counting, dominates the runtime — two containers per
// commit at roughly a second each — so commits are processed concurrently.
// Results therefore complete out of order while Delta is a running difference
// and every sink expects oldest-first, which a reorder buffer reconciles:
// completed results wait in a map until the next expected index is available.
// Delta is computed here at emit time and never in a worker, because a worker
// has no reliable view of its predecessor.
//
// The sink is closed exactly once, including on every error path, so an
// aborted run still leaves a partial artifact behind.
func Run(
	ctx context.Context,
	commits []report.Commit,
	runner cloc.Runner,
	w Sink,
	opts Options,
) (stats Stats, err error) {
	defer func() {
		if cerr := w.Close(); cerr != nil {
			err = errors.Join(err, cerr)
		}
	}()

	errLog := errOut(opts)
	jobCount := max(opts.Jobs, 1)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan int)
	results := make(chan result, jobCount)

	go func() {
		defer close(jobs)
		for i := range commits {
			select {
			case jobs <- i:
			case <-runCtx.Done():
				return
			}
		}
	}()

	var wg sync.WaitGroup
	for range jobCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				rec, err := process(runCtx, commits[i], runner, opts)
				results <- result{index: i, rec: rec, err: err}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	var (
		pending   = make(map[int]result, jobCount)
		next      int
		prevTotal int
		firstErr  error
		writeErr  error
	)

	for res := range results {
		pending[res.index] = res

		if res.err != nil {
			stats.Failed++
			short := report.ShortSHA(commits[res.index].SHA)
			fmt.Fprintf(errLog, "loc-history: commit %s: %v\n", short, res.err)
			if opts.FailFast && firstErr == nil {
				firstErr = fmt.Errorf("commit %s: %w", short, res.err)
				cancel()
			}
		}

		if writeErr != nil {
			continue // keep draining so the workers can finish and exit
		}

		// Release everything that has become contiguous from `next`.
		for {
			r, ok := pending[next]
			if !ok {
				break
			}
			delete(pending, next)
			next++

			if r.err != nil {
				continue // a failed commit contributes no record and no total
			}
			if r.rec.Skipped {
				stats.Skipped++
			}

			r.rec.Finalize(prevTotal)
			prevTotal = r.rec.TotalCode

			if err := w.Write(r.rec); err != nil {
				writeErr = fmt.Errorf("write record %s: %w", r.rec.Short, err)
				cancel()
				break
			}
			stats.Written++
		}
	}

	switch {
	case writeErr != nil:
		return stats, writeErr
	case firstErr != nil:
		return stats, firstErr
	default:
		// A cancelled parent context is a failed run even though individual
		// commits merely reported context.Canceled and were tolerated.
		return stats, ctx.Err()
	}
}

type result struct {
	index int
	rec   report.Record
	err   error
}

// process materialises one commit and counts it twice.
//
// The cache is consulted before anything else happens, so a hit skips the
// extraction as well as both containers.
func process(ctx context.Context, c report.Commit, runner cloc.Runner, opts Options) (report.Record, error) {
	rec := report.NewRecord(c)

	key := cache.Key{
		SHA:         c.SHA,
		Folder:      opts.Folder,
		TestRegex:   opts.TestRegex,
		ClocVersion: opts.ClocVersion,
	}
	if opts.Cache != nil {
		if e, ok := opts.Cache.Get(key); ok {
			rec.Product, rec.Test, rec.Skipped = e.Product, e.Test, e.Skipped
			return rec, nil
		}
	}

	// A fresh directory per commit, not per worker: a reused one would keep
	// files that the next commit deleted, flattening every deletion.
	dir, err := os.MkdirTemp(opts.WorkDir, "loc-history-"+rec.Short+"-")
	if err != nil {
		return rec, fmt.Errorf("create scratch directory in %s: %w", opts.WorkDir, err)
	}
	defer os.RemoveAll(dir)

	extracted, err := tree.Extract(opts.Repo, c.SHA, opts.Folder, dir)
	if err != nil {
		return rec, err
	}

	if extracted.Found {
		base := cloc.Options{TestRegex: opts.TestRegex, Image: opts.Image}

		product, err := runner.Count(ctx, dir, opts.Folder, base)
		if err != nil {
			return rec, fmt.Errorf("count product code: %w", err)
		}

		base.OnlyTests = true
		test, err := runner.Count(ctx, dir, opts.Folder, base)
		if err != nil {
			return rec, fmt.Errorf("count test code: %w", err)
		}

		rec.Product = product.Count
		rec.Test = test.Count
	} else {
		rec.Skipped = true
	}

	// Only successful counts are stored; a failure is not an answer.
	if opts.Cache != nil {
		if err := opts.Cache.Put(key, cache.Entry{
			Product: rec.Product,
			Test:    rec.Test,
			Skipped: rec.Skipped,
		}); err != nil {
			// A cache that cannot be written is a lost optimisation, not a
			// failed run — the numbers in hand are still correct.
			fmt.Fprintf(errOut(opts), "loc-history: cache write for %s: %v\n", rec.Short, err)
		}
	}

	return rec, nil
}

func errOut(opts Options) io.Writer {
	if opts.ErrOut == nil {
		return os.Stderr
	}
	return opts.ErrOut
}
