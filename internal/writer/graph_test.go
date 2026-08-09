package writer

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mcklmo/loc-history/internal/report"
)

var update = flag.Bool("update", false, "rewrite testdata golden files")

// graphFixture is a fixed history: growth, a quiet day, a big refactor that
// deletes more than it adds, and a commit before the source folder existed.
func graphFixture() []report.Record {
	type row struct {
		day     int
		short   string
		subject string
		product int
		test    int
		skipped bool
	}
	rows := []row{
		{3, "08ab753", "chore: repo init", 0, 0, true},
		{3, "d251527", "feat: first module", 412, 0, false},
		{4, "9a9dab4", "feat: add the panel", 620, 140, false},
		{4, "1c2b3a4", "test: cover the panel", 620, 380, false},
		{6, "5e6f7a8", "refactor: collapse three modules into one", 300, 380, false},
		{9, "b1c2d3e", "docs: comments only", 300, 380, false},
		{11, "f4a5b6c", "feat: the big one", 1400, 900, false},
		{17, "0d1e2f3", "fix: trim dead code", 1310, 900, false},
	}

	var out []report.Record
	prev := 0
	for _, r := range rows {
		rec := report.Record{
			SHA:       r.short + strings.Repeat("0", 33),
			Short:     r.short,
			Timestamp: time.Date(2026, 3, r.day, 14, 30, 0, 0, time.UTC),
			Author:    "mcklmo",
			Subject:   r.subject,
			Product:   report.Count{Files: 3, Code: r.product},
			Test:      report.Count{Files: 2, Code: r.test},
			Skipped:   r.skipped,
		}
		rec.Finalize(prev)
		prev = rec.TotalCode
		out = append(out, rec)
	}
	return out
}

func renderGraph(t *testing.T, records []report.Record) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "graph.html")
	g, err := NewGraph(path, GraphOptions{Title: "loc-history", Subtitle: "fixture · main · src"})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range records {
		if err := g.Write(r); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}
	if err := g.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return readFile(t, path)
}

func TestGraphMatchesGolden(t *testing.T) {
	got := renderGraph(t, graphFixture())
	golden := filepath.Join("testdata", "golden.html")

	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Log("wrote", golden)
		return
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden: %v (run: go test ./internal/writer -update)", err)
	}
	if got != string(want) {
		t.Errorf("output differs from %s; re-run with -update to accept", golden)
	}
}

// "Opens straight from disk" is the requirement; one external reference breaks it.
func TestGraphIsSelfContained(t *testing.T) {
	got := renderGraph(t, graphFixture())

	for _, forbidden := range []string{"http://", "https://", "//cdn", "@import", "<iframe", "srcset"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("output contains %q, so it is not self-contained", forbidden)
		}
	}
	if regexp.MustCompile(`<(script|link|img)\b`).MatchString(got) {
		t.Error("output pulls in an external resource")
	}
}

func TestGraphIsThemeAware(t *testing.T) {
	got := renderGraph(t, graphFixture())

	if !strings.Contains(got, "prefers-color-scheme: dark") {
		t.Error("no dark-scheme block; the page will render light-on-light for dark readers")
	}
	// The dark values must be a selected set, not an inversion filter.
	if strings.Contains(got, "filter: invert") {
		t.Error("dark mode is faked with an invert filter")
	}
}

func TestGraphCellsCarryTheirDayAndSubjects(t *testing.T) {
	got := renderGraph(t, graphFixture())

	if !strings.Contains(got, "feat: add the panel") {
		t.Error("commit subjects are missing from the cell tooltips")
	}
	if !strings.Contains(got, "2026-03-04") {
		t.Error("cell tooltips do not name their day")
	}
}

// Deletions are real history; the scale has to show them as their own pole.
func TestGraphUsesADivergingScale(t *testing.T) {
	got := renderGraph(t, graphFixture())

	for _, want := range []string{`class="cell pos`, `class="cell neg`, `class="cell zero"`, `class="cell none"`} {
		if !strings.Contains(got, want) {
			t.Errorf("no cell rendered with %q; the fixture contains growth, deletion, a flat day and empty days", want)
		}
	}
}

func TestGraphLegendAnchorsBothEnds(t *testing.T) {
	got := renderGraph(t, graphFixture())

	if !strings.Contains(got, "removed") || !strings.Contains(got, "added") {
		t.Error("the legend does not say which end is which")
	}
}

// A tooltip may not be the only way to reach a value.
func TestGraphIncludesATableView(t *testing.T) {
	got := renderGraph(t, graphFixture())

	if !strings.Contains(got, "<table") {
		t.Fatal("no table view; every value would be hover-only")
	}
	for _, r := range graphFixture() {
		if !strings.Contains(got, r.Short) {
			t.Errorf("table view omits commit %s", r.Short)
		}
	}
}

// Subjects come from a repository the tool did not write.
func TestGraphEscapesHostileSubjects(t *testing.T) {
	records := graphFixture()
	records[1].Subject = `<script>alert("xss")</script> & <b>bold</b>`

	got := renderGraph(t, records)

	if strings.Contains(got, "<script>alert") {
		t.Error("a commit subject was interpolated as live markup")
	}
	if !strings.Contains(got, "&lt;script&gt;") {
		t.Error("the subject was dropped rather than escaped")
	}
}

func TestGraphWithNoRecordsIsStillAValidPage(t *testing.T) {
	got := renderGraph(t, nil)

	if !strings.Contains(got, "<!doctype html>") {
		t.Error("empty run did not produce a document")
	}
	if !strings.Contains(got, "No commits") {
		t.Error("empty run should say so rather than showing a blank grid")
	}
}

func TestGraphSpansEveryDayBetweenFirstAndLastCommit(t *testing.T) {
	got := renderGraph(t, graphFixture())

	// The fixture runs 3 to 17 March 2026, which touches three ISO weeks.
	cells := strings.Count(got, `class="cell `)
	if cells != 21 {
		t.Errorf("rendered %d day cells, want 21 (three whole weeks)", cells)
	}
}

func TestGraphHandlesASingleCommit(t *testing.T) {
	one := graphFixture()[1:2]
	got := renderGraph(t, one)

	if cells := strings.Count(got, `class="cell `); cells != 7 {
		t.Errorf("rendered %d cells for a single commit, want one whole week", cells)
	}
}

func TestNewGraphReportsAnUnwritablePath(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewGraph(filepath.Join(blocker, "g.html"), GraphOptions{}); err == nil {
		t.Error("NewGraph() accepted a path underneath a regular file")
	}
}

// The file must be openable the moment the process exits, so rendering has to
// happen on Close rather than being left in a buffer.
func TestGraphRendersOnClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "graph.html")
	g, err := NewGraph(path, GraphOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range graphFixture() {
		if err := g.Write(r); err != nil {
			t.Fatal(err)
		}
	}

	if b, err := os.ReadFile(path); err == nil && strings.Contains(string(b), "</html>") {
		t.Error("the page was written before Close; buffering is the whole design")
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readFile(t, path), "</html>") {
		t.Error("Close did not render the page")
	}
}
