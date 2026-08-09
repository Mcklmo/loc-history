package cloc

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mcklmo/loc-history/internal/report"
)

// FakeRunner counts lines on this machine instead of in a container.
//
// It is a working counter rather than a table of canned answers: given a real
// extracted tree it returns real numbers, so pipeline tests exercise git
// extraction and the ordering logic together and still finish in under a
// second. Its arithmetic mirrors cloc's where it matters — the same regex
// splits the tree, so product and test remain exact complements.
type FakeRunner struct {
	// Fn replaces the counting entirely when set.
	Fn func(ctx context.Context, hostDir, folder string, opts Options) (Output, error)

	// Delay simulates the container startup that dominates real runtime.
	// Randomised delays are what make an ordering test non-vacuous.
	Delay func(hostDir string) time.Duration

	// Err fails selected counts, for exercising the error policy.
	Err func(hostDir string) error

	mu    sync.Mutex
	calls int
}

func (f *FakeRunner) Count(ctx context.Context, hostDir, folder string, opts Options) (Output, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()

	if f.Delay != nil {
		select {
		case <-time.After(f.Delay(hostDir)):
		case <-ctx.Done():
			return Output{}, ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return Output{}, err
	}
	if f.Err != nil {
		if err := f.Err(hostDir); err != nil {
			return Output{}, err
		}
	}
	if f.Fn != nil {
		return f.Fn(ctx, hostDir, folder, opts)
	}

	return countLocally(filepath.Join(hostDir, folder), opts)
}

// Calls reports how many counts have been requested, for cache tests.
func (f *FakeRunner) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func countLocally(root string, opts Options) (Output, error) {
	var re *regexp.Regexp
	if opts.TestRegex != "" {
		var err error
		if re, err = regexp.Compile(opts.TestRegex); err != nil {
			return Output{}, err
		}
	}

	var count report.Count
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil // an absent tree counts as zero, exactly like cloc
			}
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if re != nil && re.MatchString(d.Name()) != opts.OnlyTests {
			return nil
		}

		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		count.Files++
		for _, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
			if strings.TrimSpace(line) == "" {
				count.Blank++
			} else {
				count.Code++
			}
		}
		return nil
	})
	if err != nil {
		return Output{}, err
	}

	return Output{
		Count:   count,
		Version: "fake",
		Empty:   count.Files == 0,
	}, nil
}
