package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mcklmo/loc-history/internal/bucket"
	"github.com/mcklmo/loc-history/internal/cloc"
	"github.com/mcklmo/loc-history/internal/gittest"
	"github.com/mcklmo/loc-history/internal/writer"
)

func TestParseFlagsDefaults(t *testing.T) {
	cfg, err := parseFlags(nil, new(bytes.Buffer))
	if err != nil {
		t.Fatalf("parseFlags() error = %v", err)
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"repo", cfg.Repo, "."},
		{"branch", cfg.Branch, "main"},
		{"folder", cfg.Folder, "src"},
		{"test-regex", cfg.TestRegex, cloc.DefaultTestRegex},
		{"out", strings.Join(cfg.Out, ","), "console"},
		{"jobs", cfg.Jobs, 4},
		{"work-dir", cfg.WorkDir, "/tmp"},
		{"first-parent", cfg.FirstParent, true},
		{"limit", cfg.Limit, 0},
		{"fail-fast", cfg.FailFast, false},
		{"image", cfg.Image, cloc.DefaultImage},
		{"granularity", cfg.Granularity, bucket.GranularityHour},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestParseFlagsReadsEveryFlag(t *testing.T) {
	cfg, err := parseFlags([]string{
		"--repo=/x", "--branch=trunk", "--folder=lib", "--test-regex=_test\\.go$",
		"--out=console", "--jobs=8", "--work-dir=/var/tmp", "--first-parent=false",
		"--limit=5", "--fail-fast", "--image=custom/cloc:2", "--granularity=4h",
	}, new(bytes.Buffer))
	if err != nil {
		t.Fatalf("parseFlags() error = %v", err)
	}

	if cfg.Repo != "/x" || cfg.Branch != "trunk" || cfg.Folder != "lib" {
		t.Errorf("repo/branch/folder = %q/%q/%q", cfg.Repo, cfg.Branch, cfg.Folder)
	}
	if cfg.TestRegex != `_test\.go$` {
		t.Errorf("test-regex = %q", cfg.TestRegex)
	}
	if cfg.Jobs != 8 || cfg.WorkDir != "/var/tmp" || cfg.Limit != 5 {
		t.Errorf("jobs/work-dir/limit = %d/%q/%d", cfg.Jobs, cfg.WorkDir, cfg.Limit)
	}
	if cfg.FirstParent || !cfg.FailFast {
		t.Errorf("first-parent = %v, fail-fast = %v", cfg.FirstParent, cfg.FailFast)
	}
	if cfg.Image != "custom/cloc:2" {
		t.Errorf("image = %q", cfg.Image)
	}
	if cfg.Granularity != 4 {
		t.Errorf("granularity = %d, want a 4-hour bucket", cfg.Granularity)
	}
}

func TestParseFlagsRejectsBadInput(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"unknown sink", []string{"--out=console,telepathy"}, "telepathy"},
		{"empty sink list", []string{"--out="}, "out"},
		{"zero jobs", []string{"--jobs=0"}, "jobs"},
		{"negative jobs", []string{"--jobs=-1"}, "jobs"},
		{"negative limit", []string{"--limit=-3"}, "limit"},
		{"invalid regex", []string{"--test-regex=[unclosed"}, "test-regex"},
		{"unknown granularity", []string{"--granularity=week"}, "granularity"},
		{"granularity that does not tile a day", []string{"--granularity=5h"}, "divide the day"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseFlags(tt.args, new(bytes.Buffer))
			if err == nil {
				t.Fatalf("parseFlags(%v) returned no error", tt.args)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q should mention %q", err, tt.want)
			}
		})
	}
}

func TestParseFlagsTolerateSpacesInTheSinkList(t *testing.T) {
	cfg, err := parseFlags([]string{"--out=console, console"}, new(bytes.Buffer))
	if err != nil {
		t.Fatalf("parseFlags() error = %v", err)
	}
	if len(cfg.Out) != 2 || cfg.Out[1] != "console" {
		t.Errorf("out = %v", cfg.Out)
	}
}

func TestExecuteWalksARepositoryEndToEnd(t *testing.T) {
	r := gittest.New(t)
	r.Write("src/app.js", "const a = 1\n")
	r.Write("src/app.test.js", "test('a', () => {})\n")
	r.Commit("feat: first")
	r.Write("src/app.js", "const a = 1\nconst b = 2\n")
	r.Commit("feat: second")
	r.Write("src/app.js", "const a = 1\n")
	r.Commit("refactor: shrink")

	cfg, err := parseFlags([]string{"--repo=" + r.Dir, "--work-dir=" + t.TempDir()}, new(bytes.Buffer))
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := execute(context.Background(), cfg, &cloc.FakeRunner{}, &stdout, &stderr); err != nil {
		t.Fatalf("execute() error = %v\nstderr:\n%s", err, stderr.String())
	}

	lines := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want a header plus 3 commits:\n%s", len(lines), stdout.String())
	}
	// The fixture clock advances an hour per commit, so at the default
	// granularity this is still one row per commit.
	if !strings.HasPrefix(lines[0], "HOUR") {
		t.Errorf("first line is not the header: %q", lines[0])
	}
	for i, want := range []string{"feat: first", "feat: second", "refactor: shrink"} {
		if !strings.HasSuffix(lines[i+1], want) {
			t.Errorf("line %d = %q, want it to end with %q", i+1, lines[i+1], want)
		}
	}
	// 1 product + 1 test, then 2 + 1, then 1 + 1.
	if !strings.Contains(lines[3], "-1") {
		t.Errorf("the shrinking commit should show a negative delta: %q", lines[3])
	}
}

func TestExecuteReportsASummary(t *testing.T) {
	r := gittest.New(t)
	r.Write("README.md", "# no source yet\n")
	r.Commit("chore: init")
	r.Write("src/app.js", "const a = 1\n")
	r.Commit("feat: source")

	cfg, err := parseFlags([]string{"--repo=" + r.Dir, "--work-dir=" + t.TempDir()}, new(bytes.Buffer))
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := execute(context.Background(), cfg, &cloc.FakeRunner{}, &stdout, &stderr); err != nil {
		t.Fatalf("execute() error = %v", err)
	}

	summary := stderr.String()
	for _, want := range []string{"2 commits", "1 skipped"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary %q should mention %q", summary, want)
		}
	}
}

func TestExecuteFailsClearlyOutsideARepository(t *testing.T) {
	cfg, err := parseFlags([]string{"--repo=" + t.TempDir(), "--work-dir=" + t.TempDir()}, new(bytes.Buffer))
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	err = execute(context.Background(), cfg, &cloc.FakeRunner{}, &stdout, &stderr)
	if err == nil {
		t.Fatal("execute() outside a repository returned no error")
	}
	if !strings.Contains(err.Error(), "git log") {
		t.Errorf("error %q should say which step failed", err)
	}
}

// --limit exists so a smoke run does not cost a full history.
func TestExecuteHonoursLimit(t *testing.T) {
	r := gittest.New(t)
	for i := range 5 {
		r.Write("src/app.js", strings.Repeat("const a = 1\n", i+1))
		r.Commit("commit")
	}

	cfg, err := parseFlags([]string{"--repo=" + r.Dir, "--limit=2", "--work-dir=" + t.TempDir()}, new(bytes.Buffer))
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := execute(context.Background(), cfg, &cloc.FakeRunner{}, &stdout, &stderr); err != nil {
		t.Fatalf("execute() error = %v", err)
	}

	if n := strings.Count(stdout.String(), "\n"); n != 3 {
		t.Errorf("got %d lines, want a header plus 2 commits:\n%s", n, stdout.String())
	}
}

func TestParseFlagsCacheDefaults(t *testing.T) {
	cfg, err := parseFlags(nil, new(bytes.Buffer))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NoCache {
		t.Error("no-cache should default to false")
	}
	if !strings.Contains(cfg.CacheDir, "loc-history") {
		t.Errorf("cache-dir = %q, want a loc-history directory", cfg.CacheDir)
	}
	if !filepath.IsAbs(cfg.CacheDir) {
		t.Errorf("cache-dir = %q, want an absolute path", cfg.CacheDir)
	}
}

// The cache is what turns this from a one-off into a repeatable command:
// a second run must be free and produce byte-identical output.
func TestASecondExecuteIsFreeAndIdentical(t *testing.T) {
	r := gittest.New(t)
	for i := range 4 {
		r.Write("src/app.js", strings.Repeat("const a = 1\n", i+1))
		r.Write("src/app.test.js", "test('a', () => {})\n")
		r.Commit("commit")
	}

	cacheDir := t.TempDir()
	args := []string{"--repo=" + r.Dir, "--work-dir=" + t.TempDir(), "--cache-dir=" + cacheDir}
	cfg, err := parseFlags(args, new(bytes.Buffer))
	if err != nil {
		t.Fatal(err)
	}

	cold := &cloc.FakeRunner{}
	var first, stderr bytes.Buffer
	if err := execute(context.Background(), cfg, cold, &first, &stderr); err != nil {
		t.Fatalf("first execute() error = %v", err)
	}

	warm := &cloc.FakeRunner{}
	var second bytes.Buffer
	if err := execute(context.Background(), cfg, warm, &second, &stderr); err != nil {
		t.Fatalf("second execute() error = %v", err)
	}

	if first.String() != second.String() {
		t.Errorf("output differs between runs\nfirst:\n%s\nsecond:\n%s", first.String(), second.String())
	}
	// One call remains: the preflight canary, which is not cached.
	if warm.Calls() != 1 {
		t.Errorf("warm run made %d container calls, want only the preflight", warm.Calls())
	}
	if cold.Calls() <= warm.Calls() {
		t.Errorf("cold run made %d calls, warm made %d; the cache did nothing", cold.Calls(), warm.Calls())
	}
}

func TestNoCacheForcesARecount(t *testing.T) {
	r := gittest.New(t)
	r.Write("src/app.js", "const a = 1\n")
	r.Commit("commit")

	cacheDir := t.TempDir()
	base := []string{"--repo=" + r.Dir, "--work-dir=" + t.TempDir(), "--cache-dir=" + cacheDir}

	cfg, err := parseFlags(base, new(bytes.Buffer))
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := execute(context.Background(), cfg, &cloc.FakeRunner{}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}

	cfg, err = parseFlags(append(base, "--no-cache"), new(bytes.Buffer))
	if err != nil {
		t.Fatal(err)
	}
	runner := &cloc.FakeRunner{}
	stdout.Reset()
	if err := execute(context.Background(), cfg, runner, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}

	// Preflight plus product and test for the single commit.
	if runner.Calls() != 3 {
		t.Errorf("made %d calls with --no-cache, want 3", runner.Calls())
	}
}

func TestParseFlagsFileSinkDefaults(t *testing.T) {
	cfg, err := parseFlags(nil, new(bytes.Buffer))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FileOut != "loc-history.csv" {
		t.Errorf("file-out = %q", cfg.FileOut)
	}
	if cfg.FileFormat != writer.FormatCSV {
		t.Errorf("file-format = %v, want csv", cfg.FileFormat)
	}
}

// A bad format must be caught before a multi-minute walk, not after it.
func TestParseFlagsRejectsAnUnknownFileFormat(t *testing.T) {
	_, err := parseFlags([]string{"--file-format=yaml"}, new(bytes.Buffer))
	if err == nil {
		t.Fatal("parseFlags() accepted --file-format=yaml")
	}
	if !strings.Contains(err.Error(), "file-format") {
		t.Errorf("error %q should name the flag", err)
	}
}

// --out=console,file composes: one walk, two artifacts.
func TestExecuteComposesConsoleAndFileSinks(t *testing.T) {
	r := gittest.New(t)
	r.Write("src/app.js", "const a = 1\n")
	r.Commit("feat: first")
	r.Write("src/app.js", "const a = 1\nconst b = 2\n")
	r.Commit("feat: second")

	out := filepath.Join(t.TempDir(), "history.csv")
	cfg, err := parseFlags([]string{
		"--repo=" + r.Dir, "--work-dir=" + t.TempDir(), "--cache-dir=" + t.TempDir(),
		"--out=console,file", "--file-out=" + out,
	}, new(bytes.Buffer))
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := execute(context.Background(), cfg, &cloc.FakeRunner{}, &stdout, &stderr); err != nil {
		t.Fatalf("execute() error = %v", err)
	}

	if !strings.Contains(stdout.String(), "feat: second") {
		t.Errorf("console sink produced nothing useful:\n%s", stdout.String())
	}

	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("file sink wrote nothing: %v", err)
	}
	rows, err := csv.NewReader(bytes.NewReader(b)).ReadAll()
	if err != nil {
		t.Fatalf("file sink output is not valid CSV: %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("got %d CSV rows, want a header plus 2 commits", len(rows))
	}
}

// Granularity is not a graph concern any more: commits sharing a bucket merge
// in every sink, and a wider bucket merges more of them.
func TestExecuteBucketsEverySinkByGranularity(t *testing.T) {
	r := gittest.New(t)
	for i, at := range []string{
		"2026-08-06T09:10:00Z",
		"2026-08-06T09:50:00Z",
		"2026-08-06T14:20:00Z",
	} {
		r.Write("src/app.js", strings.Repeat("const a = 1\n", i+1))
		r.CommitAt("commit", at)
	}

	for _, tt := range []struct {
		gran    string
		buckets int
	}{
		{"hour", 2}, // 09:00 holds two commits, 14:00 one
		{"day", 1},  // all three are one afternoon
	} {
		t.Run(tt.gran, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "history.csv")
			cfg, err := parseFlags([]string{
				"--repo=" + r.Dir, "--work-dir=" + t.TempDir(), "--cache-dir=" + t.TempDir(),
				"--out=console,file", "--file-out=" + out, "--granularity=" + tt.gran,
			}, new(bytes.Buffer))
			if err != nil {
				t.Fatal(err)
			}

			var stdout, stderr bytes.Buffer
			if err := execute(context.Background(), cfg, &cloc.FakeRunner{}, &stdout, &stderr); err != nil {
				t.Fatalf("execute() error = %v", err)
			}

			lines := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
			if len(lines) != tt.buckets+1 {
				t.Errorf("console printed %d lines, want a header plus %d buckets:\n%s",
					len(lines), tt.buckets, stdout.String())
			}

			rows, err := csv.NewReader(bytes.NewReader(mustRead(t, out))).ReadAll()
			if err != nil {
				t.Fatalf("file sink output is not valid CSV: %v", err)
			}
			if len(rows) != tt.buckets+1 {
				t.Errorf("got %d CSV rows, want a header plus %d buckets", len(rows), tt.buckets)
			}
			// The two sinks are fed by one aggregator, so they cannot disagree.
			if len(rows) != len(lines) {
				t.Errorf("console printed %d rows and the CSV %d", len(lines), len(rows))
			}
		})
	}
}

func TestExecuteWritesNDJSON(t *testing.T) {
	r := gittest.New(t)
	r.Write("src/app.js", "const a = 1\n")
	r.Commit("feat: first")

	out := filepath.Join(t.TempDir(), "history.ndjson")
	cfg, err := parseFlags([]string{
		"--repo=" + r.Dir, "--work-dir=" + t.TempDir(), "--cache-dir=" + t.TempDir(),
		"--out=file", "--file-out=" + out, "--file-format=ndjson",
	}, new(bytes.Buffer))
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := execute(context.Background(), cfg, &cloc.FakeRunner{}, &stdout, &stderr); err != nil {
		t.Fatalf("execute() error = %v", err)
	}

	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var got bucket.Bucket
	if err := json.Unmarshal(bytes.TrimSpace(b), &got); err != nil {
		t.Fatalf("output is not NDJSON: %v", err)
	}
	// The records nest inside their bucket, so NDJSON is lossless whatever the
	// granularity.
	if len(got.Records) != 1 || got.Records[0].Subject != "feat: first" {
		t.Errorf("bucket = %+v, want it to nest the one commit", got)
	}
	if got.Last().Subject != "feat: first" {
		t.Errorf("last subject = %q", got.Last().Subject)
	}
	if stdout.Len() != 0 {
		t.Errorf("--out=file should not print a table:\n%s", stdout.String())
	}
}

func TestExecuteWritesAGraph(t *testing.T) {
	r := gittest.New(t)
	r.Write("src/app.js", "const a = 1\n")
	r.Commit("feat: first")
	r.Write("src/app.js", "const a = 1\nconst b = 2\n")
	r.Commit("feat: second")

	out := filepath.Join(t.TempDir(), "graph.html")
	cfg, err := parseFlags([]string{
		"--repo=" + r.Dir, "--work-dir=" + t.TempDir(), "--cache-dir=" + t.TempDir(),
		"--out=graph", "--graph-out=" + out,
	}, new(bytes.Buffer))
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := execute(context.Background(), cfg, &cloc.FakeRunner{}, &stdout, &stderr); err != nil {
		t.Fatalf("execute() error = %v", err)
	}

	page := string(mustRead(t, out))
	if !strings.Contains(page, "</html>") {
		t.Fatal("graph sink produced an incomplete page")
	}
	// The subtitle says what was counted, so a saved page stays legible.
	if !strings.Contains(page, "main") || !strings.Contains(page, "src") {
		t.Errorf("page does not record the branch and folder it charted")
	}
	if !strings.Contains(page, "feat: second") {
		t.Error("page does not list the commits")
	}
}

// All three sinks from one walk.
func TestExecuteComposesAllThreeSinks(t *testing.T) {
	r := gittest.New(t)
	r.Write("src/app.js", "const a = 1\n")
	r.Commit("feat: first")

	dir := t.TempDir()
	csvOut := filepath.Join(dir, "h.csv")
	htmlOut := filepath.Join(dir, "h.html")
	cfg, err := parseFlags([]string{
		"--repo=" + r.Dir, "--work-dir=" + t.TempDir(), "--cache-dir=" + t.TempDir(),
		"--out=console,file,graph", "--file-out=" + csvOut, "--graph-out=" + htmlOut,
	}, new(bytes.Buffer))
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := execute(context.Background(), cfg, &cloc.FakeRunner{}, &stdout, &stderr); err != nil {
		t.Fatalf("execute() error = %v", err)
	}

	if !strings.Contains(stdout.String(), "feat: first") {
		t.Error("console sink produced nothing")
	}
	if !strings.Contains(string(mustRead(t, csvOut)), "feat: first") {
		t.Error("file sink produced nothing")
	}
	if !strings.Contains(string(mustRead(t, htmlOut)), "feat: first") {
		t.Error("graph sink produced nothing")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}
