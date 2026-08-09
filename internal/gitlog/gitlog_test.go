package gitlog

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixture builds a throwaway repo in t.TempDir() and returns its path.
type fixture struct {
	t   *testing.T
	dir string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, dir: t.TempDir()}
	f.git("init", "-q", "-b", "main")
	f.git("config", "user.name", "Fixture")
	f.git("config", "user.email", "fixture@example.com")
	return f
}

func (f *fixture) git(args ...string) string {
	f.t.Helper()
	return f.gitEnv(nil, args...)
}

func (f *fixture) gitEnv(env []string, args ...string) string {
	f.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", f.dir}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// commit writes a file and commits it with fully pinned author and committer dates.
func (f *fixture) commit(subject, date string) {
	f.t.Helper()
	f.commitDates(subject, date, date)
}

func (f *fixture) commitDates(subject, authorDate, committerDate string) {
	f.t.Helper()
	name := strings.NewReplacer(" ", "_", ":", "_", "/", "_").Replace(subject)
	path := filepath.Join(f.dir, name+".txt")
	if err := os.WriteFile(path, []byte(subject+"\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
	f.git("add", "-A")
	f.gitEnv([]string{
		"GIT_AUTHOR_DATE=" + authorDate,
		"GIT_COMMITTER_DATE=" + committerDate,
	}, "commit", "-q", "-m", subject)
}

func subjects(cs []Commit) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Subject
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestCommitsReturnsChronologicalOrder(t *testing.T) {
	f := newFixture(t)
	f.commit("first commit", "2026-08-06T21:23:18+02:00")
	f.commit("git add init", "2026-08-06T22:13:14+02:00")
	f.commit("slop", "2026-08-07T09:02:00+02:00")

	got, err := Commits(Options{Repo: f.dir, Branch: "main", FirstParent: true})
	if err != nil {
		t.Fatalf("Commits() error = %v", err)
	}

	want := []string{"first commit", "git add init", "slop"}
	if !equal(subjects(got), want) {
		t.Errorf("subjects = %v, want %v (oldest first)", subjects(got), want)
	}
	for i := 1; i < len(got); i++ {
		if got[i].Timestamp.Before(got[i-1].Timestamp) {
			t.Errorf("timestamps are not ascending at index %d", i)
		}
	}
}

func TestCommitsParsesEveryField(t *testing.T) {
	f := newFixture(t)
	f.commit("first commit", "2026-08-06T21:23:18+02:00")

	got, err := Commits(Options{Repo: f.dir, Branch: "main", FirstParent: true})
	if err != nil {
		t.Fatalf("Commits() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d commits, want 1", len(got))
	}

	c := got[0]
	if len(c.SHA) != 40 {
		t.Errorf("SHA = %q, want 40 hex characters", c.SHA)
	}
	if c.Author != "Fixture" {
		t.Errorf("Author = %q, want %q", c.Author, "Fixture")
	}
	if c.Subject != "first commit" {
		t.Errorf("Subject = %q, want %q", c.Subject, "first commit")
	}
	want := time.Date(2026, 8, 6, 21, 23, 18, 0, time.FixedZone("", 2*3600))
	if !c.Timestamp.Equal(want) {
		t.Errorf("Timestamp = %s, want %s", c.Timestamp, want)
	}
}

// Author dates run backwards after a rebase, which would scramble the time axis.
func TestCommitsUsesCommitterDateNotAuthorDate(t *testing.T) {
	f := newFixture(t)
	f.commitDates("rebased work", "2020-01-01T00:00:00+00:00", "2026-08-06T21:23:18+02:00")

	got, err := Commits(Options{Repo: f.dir, Branch: "main", FirstParent: true})
	if err != nil {
		t.Fatalf("Commits() error = %v", err)
	}

	if got[0].Timestamp.Year() != 2026 {
		t.Errorf("Timestamp = %s, want the 2026 committer date, not the 2020 author date", got[0].Timestamp)
	}
}

// %x1f is the ASCII unit separator: it cannot occur in a subject, so subjects
// full of delimiters that would break a comma- or tab-separated format are safe.
func TestCommitsSurvivesHostileSubjects(t *testing.T) {
	f := newFixture(t)
	hostile := `feat: a,b	c "quoted" 'single' | pipe \ back`
	f.commit(hostile, "2026-08-06T21:23:18+02:00")

	got, err := Commits(Options{Repo: f.dir, Branch: "main", FirstParent: true})
	if err != nil {
		t.Fatalf("Commits() error = %v", err)
	}
	if got[0].Subject != hostile {
		t.Errorf("Subject = %q, want %q", got[0].Subject, hostile)
	}
}

func TestCommitsLimitKeepsMostRecentInChronologicalOrder(t *testing.T) {
	f := newFixture(t)
	for _, s := range []string{"c1", "c2", "c3", "c4", "c5"} {
		f.commit(s, "2026-08-06T21:23:18+02:00")
	}

	got, err := Commits(Options{Repo: f.dir, Branch: "main", FirstParent: true, Limit: 2})
	if err != nil {
		t.Fatalf("Commits() error = %v", err)
	}

	want := []string{"c4", "c5"}
	if !equal(subjects(got), want) {
		t.Errorf("subjects = %v, want the two most recent %v, oldest first", subjects(got), want)
	}
}

func TestCommitsLimitZeroMeansEverything(t *testing.T) {
	f := newFixture(t)
	for _, s := range []string{"c1", "c2", "c3"} {
		f.commit(s, "2026-08-06T21:23:18+02:00")
	}

	got, err := Commits(Options{Repo: f.dir, Branch: "main", FirstParent: true, Limit: 0})
	if err != nil {
		t.Fatalf("Commits() error = %v", err)
	}
	if len(got) != 3 {
		t.Errorf("got %d commits, want all 3", len(got))
	}
}

// --first-parent gives the trunk's size over time rather than an interleaving
// of branch-internal states.
func TestCommitsFirstParentHidesBranchInternalCommits(t *testing.T) {
	f := newFixture(t)
	f.commit("base", "2026-08-06T10:00:00+00:00")
	f.git("checkout", "-q", "-b", "side")
	f.commit("side work", "2026-08-06T11:00:00+00:00")
	f.git("checkout", "-q", "main")
	f.commit("trunk work", "2026-08-06T12:00:00+00:00")
	f.gitEnv([]string{"GIT_COMMITTER_DATE=2026-08-06T13:00:00+00:00"},
		"merge", "-q", "--no-ff", "-m", "merge side", "side")

	withFP, err := Commits(Options{Repo: f.dir, Branch: "main", FirstParent: true})
	if err != nil {
		t.Fatalf("Commits(first-parent) error = %v", err)
	}
	if !equal(subjects(withFP), []string{"base", "trunk work", "merge side"}) {
		t.Errorf("first-parent subjects = %v, want the trunk only", subjects(withFP))
	}

	without, err := Commits(Options{Repo: f.dir, Branch: "main", FirstParent: false})
	if err != nil {
		t.Fatalf("Commits(all) error = %v", err)
	}
	if len(without) != 4 {
		t.Errorf("got %d commits without --first-parent, want all 4 including the side branch", len(without))
	}
}

func TestCommitsDefaultsRepoToCurrentDirectory(t *testing.T) {
	f := newFixture(t)
	f.commit("first commit", "2026-08-06T21:23:18+02:00")
	t.Chdir(f.dir)

	got, err := Commits(Options{Branch: "main", FirstParent: true})
	if err != nil {
		t.Fatalf("Commits() error = %v", err)
	}
	if len(got) != 1 {
		t.Errorf("got %d commits, want 1", len(got))
	}
}

func TestCommitsErrorsMentionTheBranch(t *testing.T) {
	f := newFixture(t)
	f.commit("first commit", "2026-08-06T21:23:18+02:00")

	_, err := Commits(Options{Repo: f.dir, Branch: "nonexistent", FirstParent: true})
	if err == nil {
		t.Fatal("Commits() on a missing branch returned no error")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error %q should name the branch that was not found", err)
	}
}

func TestCommitsErrorsOnNonRepository(t *testing.T) {
	if _, err := Commits(Options{Repo: t.TempDir(), Branch: "main", FirstParent: true}); err == nil {
		t.Fatal("Commits() on a directory that is not a repository returned no error")
	}
}
