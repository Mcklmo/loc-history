package writer

import (
	"flag"
	"fmt"
	"html/template"
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

// escaped mirrors what html/template writes into a text node. Its contextual
// escaper also turns "+" into "&#43;", which the exported HTMLEscapeString
// leaves alone, so signed numbers need this to be found in the output.
func escaped(s string) string {
	return strings.ReplaceAll(template.HTMLEscapeString(s), "+", "&#43;")
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

func TestGraphBarsCarryTheirDayAndSubjects(t *testing.T) {
	got := renderGraph(t, graphFixture())

	if !strings.Contains(got, "feat: add the panel") {
		t.Error("commit subjects are missing from the day tooltips")
	}
	if !strings.Contains(got, "2026-03-04") {
		t.Error("day tooltips do not name their day")
	}
}

// Deletions are real history; the scale has to show them as their own pole.
func TestGraphUsesADivergingScale(t *testing.T) {
	got := renderGraph(t, graphFixture())

	for _, want := range []string{`class="bar up"`, `class="bar down"`} {
		if !strings.Contains(got, want) {
			t.Errorf("no column rendered with %q; the fixture contains both growth and deletion", want)
		}
	}
}

func TestGraphChartsProductAndTestSeparately(t *testing.T) {
	got := renderGraph(t, graphFixture())

	if n := strings.Count(got, "<figure>"); n != 2 {
		t.Errorf("rendered %d figures, want one for product and one for test", n)
	}
	for _, want := range []string{
		"<figcaption>Product files</figcaption>",
		"<figcaption>Test files</figcaption>",
		"net change in product lines of code",
		"net change in test lines of code",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output is missing %q", want)
		}
	}
}

// Shared scale is the whole point of small multiples: a +2000 product day has
// to be visibly ten times a +200 test day.
func TestGraphSharesOneYScaleAcrossBothCharts(t *testing.T) {
	got := renderGraph(t, graphFixture())

	figures := strings.Split(got, "<figure>")[1:]
	if len(figures) != 2 {
		t.Fatalf("got %d figures, want 2", len(figures))
	}
	// The fixture peaks at a +1100 product day, so both axes top out at 2,000.
	for i, fig := range figures {
		for _, tick := range []string{"+2,000", "−2,000"} {
			want := ">" + escaped(tick) + "<"
			if !strings.Contains(fig, want) {
				t.Errorf("figure %d has no %q tick; the two charts are not on one scale", i, want)
			}
		}
	}
}

func TestGraphKeyNamesBothDirections(t *testing.T) {
	got := renderGraph(t, graphFixture())

	for _, want := range []string{`class="swatch added"`, `class="swatch removed"`} {
		if !strings.Contains(got, want) {
			t.Errorf("the key has no %q; nothing says which colour is which", want)
		}
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
	// The fixture runs 3 to 17 March 2026: 15 days on the axis, of which six
	// carry commits (3, 4, 6, 9, 11, 17).
	first := time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC)
	last := time.Date(2026, 3, 17, 0, 0, 0, 0, time.UTC)
	if span := axisSpan(first, last); span != 15 {
		t.Errorf("axis spans %d days, want 15 — quiet days must still take up room", span)
	}

	got := renderGraph(t, graphFixture())
	if hits := strings.Count(got, `class="hit"`); hits != 12 {
		t.Errorf("rendered %d hit targets, want 12 (six commit days × two charts)", hits)
	}
}

func TestGraphHandlesASingleCommit(t *testing.T) {
	one := graphFixture()[1:2]
	got := renderGraph(t, one)

	// 412 product lines, no test lines: one column across the two charts.
	if bars := strings.Count(got, `class="bar `); bars != 1 {
		t.Errorf("rendered %d columns for a single commit, want 1", bars)
	}
	if !strings.Contains(got, "</html>") {
		t.Error("a one-commit history did not produce a page")
	}
	assertFinite(t, got)
}

// A history that never changes size still has to render: yMax is 0 before it is
// floored, and dividing by it would put NaN into every coordinate.
func TestGraphHandlesAFlatHistory(t *testing.T) {
	var flat []report.Record
	for i, short := range []string{"aaaaaaa", "bbbbbbb", "ccccccc"} {
		rec := report.Record{
			Short:     short,
			Timestamp: time.Date(2026, 3, 3+i, 9, 0, 0, 0, time.UTC),
			Subject:   "docs: prose only",
		}
		rec.Finalize(0)
		flat = append(flat, rec)
	}

	got := renderGraph(t, flat)

	if !strings.Contains(got, "</html>") {
		t.Fatal("a flat history did not produce a page")
	}
	if strings.Contains(got, `class="bar `) {
		t.Error("a history with no net change drew a column")
	}
	assertFinite(t, got)
}

func assertFinite(t *testing.T, page string) {
	t.Helper()
	for _, bad := range []string{"NaN", "Inf", "+Inf", "-Inf"} {
		if strings.Contains(page, bad) {
			t.Errorf("page contains %q; a coordinate was computed from a zero scale", bad)
		}
	}
}

// The charts and the commit table are two views of the same numbers; this is
// the invariant that keeps them from disagreeing.
func TestDailyDeltasSumToTheRecordDelta(t *testing.T) {
	records := graphFixture()

	prevProduct, prevTest := 0, 0
	for _, r := range records {
		productDelta := r.Product.Code - prevProduct
		testDelta := r.Test.Code - prevTest
		if productDelta+testDelta != r.Delta {
			t.Errorf("%s: product %+d + test %+d = %+d, want Delta %+d",
				r.Short, productDelta, testDelta, productDelta+testDelta, r.Delta)
		}
		prevProduct, prevTest = r.Product.Code, r.Test.Code
	}

	days, order := groupByDay(records)
	for _, date := range order {
		d := days[dateKey(date)]
		if d.product+d.test != d.total {
			t.Errorf("%s: product %+d + test %+d = %+d, want total %+d",
				dateKey(date), d.product, d.test, d.product+d.test, d.total)
		}
	}
}

// Nothing charted may be hover-only.
func TestGraphTableViewCarriesTheChartedDailyValues(t *testing.T) {
	got := renderGraph(t, graphFixture())

	byDay := strings.SplitN(got, "Table view — by day", 2)
	if len(byDay) != 2 {
		t.Fatal("no by-day table view")
	}
	table := strings.SplitN(byDay[1], "</table>", 2)[0]

	days, order := groupByDay(graphFixture())
	for _, date := range order {
		d := days[dateKey(date)]
		row := fmt.Sprintf(`<td>%s</td>`, dateKey(date))
		if !strings.Contains(table, row) {
			t.Errorf("by-day table omits %s", dateKey(date))
			continue
		}
		for _, v := range []int{d.product, d.test, d.total} {
			want := fmt.Sprintf(`<td class="num">%s</td>`, escaped(fmt.Sprintf("%+d", v)))
			if !strings.Contains(table, want) {
				t.Errorf("by-day table is missing %s for %s", want, dateKey(date))
			}
		}
	}
}

func TestNiceMaxRoundsToRoundNumbers(t *testing.T) {
	for _, c := range []struct{ in, want int }{
		{0, 1}, {1, 1}, {2, 2}, {3, 5}, {6, 10}, {99, 100},
		{412, 500}, {1100, 2000}, {2001, 5000}, {6830, 10000},
	} {
		if got := niceMax(c.in); got != c.want {
			t.Errorf("niceMax(%d) = %d, want %d", c.in, got, c.want)
		}
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
