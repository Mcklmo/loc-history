package pipeline

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mcklmo/loc-history/internal/cloc"
	"github.com/mcklmo/loc-history/internal/gitlog"
	"github.com/mcklmo/loc-history/internal/gittest"
	"github.com/mcklmo/loc-history/internal/report"
)

// collector is a Writer that records everything it is handed.
type collector struct {
	mu       sync.Mutex
	records  []report.Record
	closed   int
	writeErr error
}

func (c *collector) Write(r report.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, r)
	return c.writeErr
}

func (c *collector) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed++
	return nil
}

func (c *collector) shas() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.records))
	for i, r := range c.records {
		out[i] = r.Short
	}
	return out
}

// buildRepo creates a repo whose src/ grows and shrinks in a known pattern.
func buildRepo(t *testing.T, n int) (*gittest.Repo, []report.Commit) {
	t.Helper()
	r := gittest.New(t)
	for i := range n {
		// Line count varies per commit and dips at the halfway point, so the
		// series contains a genuine net deletion rather than only growth.
		lines := i + 1
		if i == n/2 {
			lines = 1
		}
		r.Write("src/app.js", strings.Repeat("const x = 1\n", lines))
		r.Write("src/app.test.js", strings.Repeat("test('x', () => {})\n", i+1))
		r.Commit(fmt.Sprintf("commit %d", i))
	}

	commits, err := gitlog.Commits(gitlog.Options{Repo: r.Dir, Branch: "main", FirstParent: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != n {
		t.Fatalf("fixture has %d commits, want %d", len(commits), n)
	}
	return r, commits
}

func testOptions(repo, workDir string) Options {
	return Options{
		Repo:      repo,
		Folder:    "src",
		TestRegex: cloc.DefaultTestRegex,
		WorkDir:   workDir,
		Jobs:      4,
		ErrOut:    &bytes.Buffer{},
	}
}

// Without randomised delays this test passes vacuously: it is the jitter that
// forces results to complete out of order and actually exercises the reorder
// buffer.
func TestRecordsReachTheWriterInCommitOrderUnderConcurrency(t *testing.T) {
	repo, commits := buildRepo(t, 24)
	out := &collector{}

	rng := rand.New(rand.NewSource(1))
	var mu sync.Mutex
	runner := &cloc.FakeRunner{Delay: func(string) time.Duration {
		mu.Lock()
		defer mu.Unlock()
		return time.Duration(rng.Intn(4000)) * time.Microsecond
	}}

	opts := testOptions(repo.Dir, t.TempDir())
	opts.Jobs = 8

	stats, err := Run(context.Background(), commits, runner, out, opts)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	want := make([]string, len(commits))
	for i, c := range commits {
		want[i] = report.ShortSHA(c.SHA)
	}
	if got := out.shas(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("records arrived out of order\n got: %v\nwant: %v", got, want)
	}
	if stats.Written != len(commits) {
		t.Errorf("Written = %d, want %d", stats.Written, len(commits))
	}
}

func TestDeltasAreRunningDifferencesAndSumToTheFinalTotal(t *testing.T) {
	repo, commits := buildRepo(t, 9)
	out := &collector{}

	if _, err := Run(context.Background(), commits, &cloc.FakeRunner{}, out, testOptions(repo.Dir, t.TempDir())); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	var sum, prev int
	sawNegative := false
	for i, r := range out.records {
		if r.TotalCode != r.Product.Code+r.Test.Code {
			t.Errorf("record %d: TotalCode %d != product %d + test %d", i, r.TotalCode, r.Product.Code, r.Test.Code)
		}
		if r.Delta != r.TotalCode-prev {
			t.Errorf("record %d: Delta = %d, want %d", i, r.Delta, r.TotalCode-prev)
		}
		if r.Delta < 0 {
			sawNegative = true
		}
		sum += r.Delta
		prev = r.TotalCode
	}

	if sum != out.records[len(out.records)-1].TotalCode {
		t.Errorf("deltas sum to %d, final total is %d", sum, out.records[len(out.records)-1].TotalCode)
	}
	if !sawNegative {
		t.Error("fixture produced no net deletion, so signed deltas were never exercised")
	}
}

// Early commits may predate the source folder. That is history, not failure.
func TestCommitsWithoutTheSourceFolderAreSkippedNotFailed(t *testing.T) {
	r := gittest.New(t)
	r.Write("README.md", "# no source yet\n")
	r.Commit("chore: repo init")
	r.Write("src/app.js", "const a = 1\n")
	r.Commit("feat: add source")

	commits, err := gitlog.Commits(gitlog.Options{Repo: r.Dir, Branch: "main", FirstParent: true})
	if err != nil {
		t.Fatal(err)
	}

	out := &collector{}
	stats, err := Run(context.Background(), commits, &cloc.FakeRunner{}, out, testOptions(r.Dir, t.TempDir()))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if len(out.records) != 2 {
		t.Fatalf("got %d records, want 2", len(out.records))
	}
	if !out.records[0].Skipped {
		t.Error("record 0: Skipped = false, want true")
	}
	if out.records[0].TotalCode != 0 {
		t.Errorf("record 0: TotalCode = %d, want 0", out.records[0].TotalCode)
	}
	if out.records[1].Skipped {
		t.Error("record 1: Skipped = true, want false")
	}
	if stats.Skipped != 1 || stats.Failed != 0 {
		t.Errorf("stats = %+v, want 1 skipped and 0 failed", stats)
	}
}

// Reusing a scratch directory across commits would leave deleted files behind
// and turn every deletion into a flat line.
func TestDeletedFilesDoNotLeakBetweenCommits(t *testing.T) {
	r := gittest.New(t)
	r.Write("src/a.js", strings.Repeat("const a = 1\n", 10))
	r.Write("src/b.js", strings.Repeat("const b = 2\n", 10))
	r.Commit("feat: two modules")
	r.Remove("src/b.js")
	r.Commit("refactor: drop b")

	commits, err := gitlog.Commits(gitlog.Options{Repo: r.Dir, Branch: "main", FirstParent: true})
	if err != nil {
		t.Fatal(err)
	}

	out := &collector{}
	if _, err := Run(context.Background(), commits, &cloc.FakeRunner{}, out, testOptions(r.Dir, t.TempDir())); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if out.records[1].TotalCode != 10 {
		t.Errorf("after deleting b.js TotalCode = %d, want 10", out.records[1].TotalCode)
	}
	if out.records[1].Delta != -10 {
		t.Errorf("Delta = %d, want -10", out.records[1].Delta)
	}
}

// One bad commit should not cost a three-minute rebuild.
func TestAFailedCommitIsReportedAndTheRunContinues(t *testing.T) {
	repo, commits := buildRepo(t, 5)
	failing := commits[2]

	runner := &cloc.FakeRunner{Err: func(hostDir string) error {
		if strings.Contains(hostDir, report.ShortSHA(failing.SHA)) {
			return errors.New("cloc exploded")
		}
		return nil
	}}

	var errOut bytes.Buffer
	opts := testOptions(repo.Dir, t.TempDir())
	opts.ErrOut = &errOut
	opts.Jobs = 1

	out := &collector{}
	stats, err := Run(context.Background(), commits, runner, out, opts)
	if err != nil {
		t.Fatalf("Run() error = %v, want the run to survive one bad commit", err)
	}

	if len(out.records) != 4 {
		t.Errorf("got %d records, want the other 4", len(out.records))
	}
	if stats.Failed != 1 {
		t.Errorf("Failed = %d, want 1", stats.Failed)
	}
	for _, r := range out.records {
		if r.Short == report.ShortSHA(failing.SHA) {
			t.Error("the failed commit was emitted anyway")
		}
	}
	if !strings.Contains(errOut.String(), report.ShortSHA(failing.SHA)) {
		t.Errorf("stderr %q should name the commit that failed", errOut.String())
	}
}

func TestFailFastAbortsOnTheFirstError(t *testing.T) {
	repo, commits := buildRepo(t, 20)

	runner := &cloc.FakeRunner{
		Delay: func(string) time.Duration { return time.Millisecond },
		Err:   func(string) error { return errors.New("cloc exploded") },
	}

	opts := testOptions(repo.Dir, t.TempDir())
	opts.FailFast = true
	opts.Jobs = 2

	out := &collector{}
	stats, err := Run(context.Background(), commits, runner, out, opts)

	if err == nil {
		t.Fatal("Run() with --fail-fast returned no error")
	}
	if !strings.Contains(err.Error(), "cloc exploded") {
		t.Errorf("error %q should carry the underlying failure", err)
	}
	if stats.Failed == 0 {
		t.Error("Failed = 0, want at least 1")
	}
	if stats.Failed == len(commits) {
		t.Error("every commit was attempted; --fail-fast did not cancel anything")
	}
}

func TestWriterIsClosedExactlyOnceOnTheHappyPath(t *testing.T) {
	repo, commits := buildRepo(t, 3)
	out := &collector{}

	if _, err := Run(context.Background(), commits, &cloc.FakeRunner{}, out, testOptions(repo.Dir, t.TempDir())); err != nil {
		t.Fatal(err)
	}
	if out.closed != 1 {
		t.Errorf("Close called %d times, want exactly 1", out.closed)
	}
}

// An aborted run should still leave a partial artifact behind.
func TestWriterIsClosedOnErrorPathsToo(t *testing.T) {
	repo, commits := buildRepo(t, 6)
	runner := &cloc.FakeRunner{Err: func(string) error { return errors.New("cloc exploded") }}

	opts := testOptions(repo.Dir, t.TempDir())
	opts.FailFast = true

	out := &collector{}
	if _, err := Run(context.Background(), commits, runner, out, opts); err == nil {
		t.Fatal("expected an error")
	}
	if out.closed != 1 {
		t.Errorf("Close called %d times on the error path, want exactly 1", out.closed)
	}
}

func TestWriteErrorsAbortTheRun(t *testing.T) {
	repo, commits := buildRepo(t, 6)
	boom := errors.New("disk full")
	out := &collector{writeErr: boom}

	_, err := Run(context.Background(), commits, &cloc.FakeRunner{}, out, testOptions(repo.Dir, t.TempDir()))

	if !errors.Is(err, boom) {
		t.Errorf("Run() error = %v, want it to wrap %v", err, boom)
	}
	if out.closed != 1 {
		t.Errorf("Close called %d times, want exactly 1", out.closed)
	}
}

func TestScratchDirectoriesAreRemoved(t *testing.T) {
	repo, commits := buildRepo(t, 8)
	work := t.TempDir()

	if _, err := Run(context.Background(), commits, &cloc.FakeRunner{}, &collector{}, testOptions(repo.Dir, work)); err != nil {
		t.Fatal(err)
	}

	left, err := os.ReadDir(work)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		names := make([]string, len(left))
		for i, e := range left {
			names[i] = filepath.Join(work, e.Name())
		}
		t.Errorf("scratch directories left behind: %v", names)
	}
}

func TestRunWithNoCommitsStillClosesTheWriter(t *testing.T) {
	out := &collector{}
	stats, err := Run(context.Background(), nil, &cloc.FakeRunner{}, out, testOptions(t.TempDir(), t.TempDir()))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if stats.Written != 0 || out.closed != 1 {
		t.Errorf("stats = %+v, closed = %d, want no records and exactly one Close", stats, out.closed)
	}
}

func TestRunHonoursContextCancellation(t *testing.T) {
	repo, commits := buildRepo(t, 20)
	ctx, cancel := context.WithCancel(context.Background())

	runner := &cloc.FakeRunner{Delay: func(string) time.Duration { return 5 * time.Millisecond }}
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	out := &collector{}
	if _, err := Run(ctx, commits, runner, out, testOptions(repo.Dir, t.TempDir())); err == nil {
		t.Error("Run() with a cancelled context returned no error")
	}
	if out.closed != 1 {
		t.Errorf("Close called %d times, want exactly 1", out.closed)
	}
}

func TestJobsBelowOneIsTreatedAsSequential(t *testing.T) {
	repo, commits := buildRepo(t, 4)
	opts := testOptions(repo.Dir, t.TempDir())
	opts.Jobs = 0

	out := &collector{}
	if _, err := Run(context.Background(), commits, &cloc.FakeRunner{}, out, opts); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(out.records) != 4 {
		t.Errorf("got %d records, want 4", len(out.records))
	}
}

// Two containers per commit: one for product code, one for tests.
func TestEachCommitIsCountedTwice(t *testing.T) {
	repo, commits := buildRepo(t, 5)
	runner := &cloc.FakeRunner{}

	if _, err := Run(context.Background(), commits, runner, &collector{}, testOptions(repo.Dir, t.TempDir())); err != nil {
		t.Fatal(err)
	}

	if got := runner.Calls(); got != 2*len(commits) {
		t.Errorf("runner called %d times, want %d (product and test per commit)", got, 2*len(commits))
	}
}
