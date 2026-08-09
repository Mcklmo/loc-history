// Package gittest builds throwaway git repositories for tests.
//
// Several packages need a real repository to run real git against — a fake
// would only prove that the fake agrees with itself.
package gittest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Repo is a repository under t.TempDir(), removed when the test ends.
type Repo struct {
	t   *testing.T
	Dir string

	// clock advances one hour per commit so history is strictly ordered
	// without every test having to spell out dates.
	clock time.Time
}

// New initialises an empty repository on branch main.
func New(t *testing.T) *Repo {
	t.Helper()
	r := &Repo{
		t:     t,
		Dir:   t.TempDir(),
		clock: time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC),
	}
	r.Git("init", "-q", "-b", "main")
	r.Git("config", "user.name", "Fixture")
	r.Git("config", "user.email", "fixture@example.com")
	return r
}

// Git runs a git command in the repository and returns its trimmed output.
func (r *Repo) Git(args ...string) string {
	r.t.Helper()
	return r.GitEnv(nil, args...)
}

// GitEnv is Git with extra environment variables, for pinning dates.
func (r *Repo) GitEnv(env []string, args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", r.Dir}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// Write creates a file at a repository-relative path, making parent directories.
func (r *Repo) Write(rel, content string) {
	r.t.Helper()
	path := filepath.Join(r.Dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// Remove deletes a repository-relative path.
func (r *Repo) Remove(rel string) {
	r.t.Helper()
	if err := os.RemoveAll(filepath.Join(r.Dir, rel)); err != nil {
		r.t.Fatal(err)
	}
}

// Commit stages everything and commits, advancing the clock by an hour.
// It returns the new commit's full SHA.
func (r *Repo) Commit(subject string) string {
	r.t.Helper()
	r.clock = r.clock.Add(time.Hour)
	date := r.clock.Format(time.RFC3339)
	return r.CommitAtDates(subject, date, date)
}

// CommitAt commits with both dates pinned to date (RFC 3339).
func (r *Repo) CommitAt(subject, date string) string {
	r.t.Helper()
	return r.CommitAtDates(subject, date, date)
}

// CommitAtDates commits with the author and committer dates set independently,
// which is how a rebase leaves history.
func (r *Repo) CommitAtDates(subject, authorDate, committerDate string) string {
	r.t.Helper()
	// Guarantee there is something to commit even when the caller wrote no file.
	if r.Git("status", "--porcelain") == "" {
		r.Write(slug(subject)+".txt", subject+"\n")
	}
	r.Git("add", "-A")
	r.GitEnv([]string{
		"GIT_AUTHOR_DATE=" + authorDate,
		"GIT_COMMITTER_DATE=" + committerDate,
	}, "commit", "-q", "--allow-empty", "-m", subject)
	return r.SHA("HEAD")
}

// SHA resolves a revision to its full hash.
func (r *Repo) SHA(rev string) string {
	r.t.Helper()
	return r.Git("rev-parse", rev)
}

// Clean reports whether the working tree has no uncommitted changes.
func (r *Repo) Clean() bool {
	r.t.Helper()
	return r.Git("status", "--porcelain") == ""
}

func slug(s string) string {
	return strings.NewReplacer(" ", "_", ":", "_", "/", "_", `"`, "", "'", "", "|", "_",
		"\\", "_", "\t", "_", ",", "_").Replace(s)
}
