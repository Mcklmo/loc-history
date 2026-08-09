package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mcklmo/loc-history/internal/cloc"
	"github.com/mcklmo/loc-history/internal/gittest"
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
		"--limit=5", "--fail-fast", "--image=custom/cloc:2",
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
	if !strings.HasPrefix(lines[0], "DATE") {
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
