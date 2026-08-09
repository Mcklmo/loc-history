package gitlog

import (
	"strings"
	"testing"
	"time"

	"github.com/mcklmo/loc-history/internal/gittest"
)

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
	r := gittest.New(t)
	r.CommitAt("first commit", "2026-08-06T21:23:18+02:00")
	r.CommitAt("git add init", "2026-08-06T22:13:14+02:00")
	r.CommitAt("slop", "2026-08-07T09:02:00+02:00")

	got, err := Commits(Options{Repo: r.Dir, Branch: "main", FirstParent: true})
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
	r := gittest.New(t)
	r.CommitAt("first commit", "2026-08-06T21:23:18+02:00")

	got, err := Commits(Options{Repo: r.Dir, Branch: "main", FirstParent: true})
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
	r := gittest.New(t)
	r.CommitAtDates("rebased work", "2020-01-01T00:00:00+00:00", "2026-08-06T21:23:18+02:00")

	got, err := Commits(Options{Repo: r.Dir, Branch: "main", FirstParent: true})
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
	r := gittest.New(t)
	hostile := `feat: a,b	c "quoted" 'single' | pipe \ back`
	r.CommitAt(hostile, "2026-08-06T21:23:18+02:00")

	got, err := Commits(Options{Repo: r.Dir, Branch: "main", FirstParent: true})
	if err != nil {
		t.Fatalf("Commits() error = %v", err)
	}
	if got[0].Subject != hostile {
		t.Errorf("Subject = %q, want %q", got[0].Subject, hostile)
	}
}

func TestCommitsLimitKeepsMostRecentInChronologicalOrder(t *testing.T) {
	r := gittest.New(t)
	for _, s := range []string{"c1", "c2", "c3", "c4", "c5"} {
		r.CommitAt(s, "2026-08-06T21:23:18+02:00")
	}

	got, err := Commits(Options{Repo: r.Dir, Branch: "main", FirstParent: true, Limit: 2})
	if err != nil {
		t.Fatalf("Commits() error = %v", err)
	}

	want := []string{"c4", "c5"}
	if !equal(subjects(got), want) {
		t.Errorf("subjects = %v, want the two most recent %v, oldest first", subjects(got), want)
	}
}

func TestCommitsLimitZeroMeansEverything(t *testing.T) {
	r := gittest.New(t)
	for _, s := range []string{"c1", "c2", "c3"} {
		r.CommitAt(s, "2026-08-06T21:23:18+02:00")
	}

	got, err := Commits(Options{Repo: r.Dir, Branch: "main", FirstParent: true, Limit: 0})
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
	r := gittest.New(t)
	r.CommitAt("base", "2026-08-06T10:00:00+00:00")
	r.Git("checkout", "-q", "-b", "side")
	r.CommitAt("side work", "2026-08-06T11:00:00+00:00")
	r.Git("checkout", "-q", "main")
	r.CommitAt("trunk work", "2026-08-06T12:00:00+00:00")
	r.GitEnv([]string{"GIT_COMMITTER_DATE=2026-08-06T13:00:00+00:00"},
		"merge", "-q", "--no-ff", "-m", "merge side", "side")

	withFP, err := Commits(Options{Repo: r.Dir, Branch: "main", FirstParent: true})
	if err != nil {
		t.Fatalf("Commits(first-parent) error = %v", err)
	}
	if !equal(subjects(withFP), []string{"base", "trunk work", "merge side"}) {
		t.Errorf("first-parent subjects = %v, want the trunk only", subjects(withFP))
	}

	without, err := Commits(Options{Repo: r.Dir, Branch: "main", FirstParent: false})
	if err != nil {
		t.Fatalf("Commits(all) error = %v", err)
	}
	if len(without) != 4 {
		t.Errorf("got %d commits without --first-parent, want all 4 including the side branch", len(without))
	}
}

func TestCommitsDefaultsRepoToCurrentDirectory(t *testing.T) {
	r := gittest.New(t)
	r.CommitAt("first commit", "2026-08-06T21:23:18+02:00")
	t.Chdir(r.Dir)

	got, err := Commits(Options{Branch: "main", FirstParent: true})
	if err != nil {
		t.Fatalf("Commits() error = %v", err)
	}
	if len(got) != 1 {
		t.Errorf("got %d commits, want 1", len(got))
	}
}

func TestCommitsErrorsMentionTheBranch(t *testing.T) {
	r := gittest.New(t)
	r.CommitAt("first commit", "2026-08-06T21:23:18+02:00")

	_, err := Commits(Options{Repo: r.Dir, Branch: "nonexistent", FirstParent: true})
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
