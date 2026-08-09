package writer

import (
	"flag"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mcklmo/loc-history/internal/bucket"
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

// renderGraph uses the default granularity; renderGraphAt pins one.
func renderGraph(t *testing.T, records []report.Record) string {
	t.Helper()
	return renderGraphAt(t, records, bucket.GranularityHour)
}

func renderGraphAt(t *testing.T, records []report.Record, gran bucket.Granularity) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "graph.html")
	g, err := NewGraph(path, GraphOptions{Title: "loc-history", Subtitle: "fixture · main · src"})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bucketsOf(t, gran, records...) {
		if err := g.Write(b); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}
	if err := g.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return readFile(t, path)
}

func TestGraphMatchesGolden(t *testing.T) {
	for _, tt := range []struct {
		name string
		gran bucket.Granularity
		file string
	}{
		{"hour", bucket.GranularityHour, "golden.html"},
		{"day", bucket.GranularityDay, "golden-day.html"},
		{"4h", 4, "golden-4h.html"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := renderGraphAt(t, graphFixture(), tt.gran)
			golden := filepath.Join("testdata", tt.file)

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
		})
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

func TestGraphBarsCarryTheirBucketAndSubjects(t *testing.T) {
	got := renderGraph(t, graphFixture())

	if !strings.Contains(got, "feat: add the panel") {
		t.Error("commit subjects are missing from the bucket tooltips")
	}
	if !strings.Contains(got, "2026-03-04") {
		t.Error("bucket tooltips do not name their bucket")
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

func TestGraphSpansEveryBucketBetweenFirstAndLastCommit(t *testing.T) {
	// The fixture runs 14:30 on 3 March 2026 to 14:30 on the 17th. Every commit
	// lands at the same time of day, so both granularities see the same six
	// buckets (3, 4, 6, 9, 11, 17) — only the number of empty slots between
	// them differs.
	first := time.Date(2026, 3, 3, 14, 30, 0, 0, time.UTC)
	last := time.Date(2026, 3, 17, 14, 30, 0, 0, time.UTC)

	for _, tt := range []struct {
		gran bucket.Granularity
		want int
	}{
		{bucket.GranularityDay, 15},
		{bucket.GranularityHour, 337},
	} {
		t.Run(tt.gran.Noun(), func(t *testing.T) {
			span := axisSpan(tt.gran.Truncate(first), tt.gran.Truncate(last), tt.gran)
			if span != tt.want {
				t.Errorf("axis spans %d %ss, want %d — quiet %ss must still take up room",
					span, tt.gran.Noun(), tt.want, tt.gran.Noun())
			}

			got := renderGraphAt(t, graphFixture(), tt.gran)
			if hits := strings.Count(got, `class="hit"`); hits != 12 {
				t.Errorf("rendered %d hit targets, want 12 (six commit buckets × two charts)", hits)
			}
		})
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

// Nothing charted may be hover-only.
func TestGraphTableViewCarriesTheChartedBucketValues(t *testing.T) {
	for _, gran := range []bucket.Granularity{bucket.GranularityHour, bucket.GranularityDay} {
		t.Run(gran.Noun(), func(t *testing.T) {
			got := renderGraphAt(t, graphFixture(), gran)

			split := strings.SplitN(got, "Table view — by "+gran.Noun(), 2)
			if len(split) != 2 {
				t.Fatalf("no by-%s table view", gran.Noun())
			}
			table := strings.SplitN(split[1], "</table>", 2)[0]

			for _, b := range bucketsOf(t, gran, graphFixture()...) {
				when := b.Start.Format(gran.RowFormat())
				if !strings.Contains(table, fmt.Sprintf(`<td>%s</td>`, when)) {
					t.Errorf("by-%s table omits %s", gran.Noun(), when)
					continue
				}
				for _, v := range []int{b.ProductDelta, b.TestDelta, b.Delta} {
					want := fmt.Sprintf(`<td class="num">%s</td>`, escaped(fmt.Sprintf("%+d", v)))
					if !strings.Contains(table, want) {
						t.Errorf("by-%s table is missing %s for %s", gran.Noun(), want, when)
					}
				}
			}
		})
	}
}

// A year of hourly slots puts a column at a tenth of a unit — invisible, and
// too thin to hover. The floors trade exact slot width for a chart that can
// still be read and used.
func TestGraphFloorsColumnAndTargetWidths(t *testing.T) {
	// 40 commits spread over ~357 days: 8,581 hourly slots, pitch 0.10.
	var at []time.Time
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range 40 {
		at = append(at, start.Add(time.Duration(i*220)*time.Hour))
	}
	got := renderGraphAt(t, commitsAt(t, at...), bucket.GranularityHour)

	for _, tt := range []struct {
		what  string
		re    *regexp.Regexp
		floor float64
	}{
		{"column", regexp.MustCompile(`<rect class="bar (?:up|down)" x="([\d.]+)" y="[\d.]+" width="([\d.]+)"`), minBarWidth},
		{"hit target", regexp.MustCompile(`<rect class="hit" x="([\d.]+)" y="\d+" width="([\d.]+)"`), minHitWidth},
	} {
		found := tt.re.FindAllStringSubmatch(got, -1)
		if len(found) == 0 {
			t.Fatalf("no %s rendered; the assertion below would be vacuous", tt.what)
		}
		for _, m := range found {
			x, w := mustFloat(t, m[1]), mustFloat(t, m[2])
			if w < tt.floor {
				t.Errorf("%s is %.2f units wide, below the %.0f floor", tt.what, w, tt.floor)
			}
			// A floored width can outgrow its slot, so it has to be held
			// inside the plot rather than centred blindly.
			if x < gutterLeft || x+w > plotRight {
				t.Errorf("%s spans %.2f–%.2f, outside the plot %d–%d", tt.what, x, x+w, gutterLeft, plotRight)
			}
		}
	}
	assertFinite(t, got)
}

func TestAxisLabelUnitFollowsTheSpan(t *testing.T) {
	for _, tt := range []struct {
		name  string
		first time.Time
		last  time.Time
		gran  bucket.Granularity
		want  labelUnit
	}{
		{"three hours hourly", hm(17), hm(19), bucket.GranularityHour, unitHour},
		{"fifteen days hourly", hm(0), hm(15 * 24), bucket.GranularityHour, unitDay},
		{"fifteen days daily", hm(0), hm(15 * 24), bucket.GranularityDay, unitDay},
		{"two years hourly", hm(0), hm(2 * 365 * 24), bucket.GranularityHour, unitMonth},
		{"two years daily", hm(0), hm(2 * 365 * 24), bucket.GranularityDay, unitMonth},
		// A day bucket always starts at 00:00, so an hour label would say
		// nothing however short the history.
		{"one day daily", hm(0), hm(24), bucket.GranularityDay, unitDay},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := pickLabelUnit(axisFor(tt.first, tt.last, tt.gran)); got != tt.want {
				t.Errorf("pickLabelUnit() = %d, want %d", got, tt.want)
			}
		})
	}
}

// The three-hour history is the case the hour bucket exists for: at day
// granularity it is one column under a single bare month label.
func TestAxisLabelsNameTheHoursOfAShortHistory(t *testing.T) {
	ax := axisFor(hm(17), hm(19), bucket.GranularityHour)

	var texts []string
	for _, l := range buildAxisLabels(ax) {
		texts = append(texts, l.Text)
	}
	want := []string{"17:00", "18:00", "19:00"}
	if !slices.Equal(texts, want) {
		t.Errorf("labels = %v, want %v", texts, want)
	}
}

// Whatever the unit, labels run left to right and never sit on top of one
// another — that is what the width claiming buys.
func TestAxisLabelsAreOrderedAndNeverOverlap(t *testing.T) {
	for _, tt := range []struct {
		name       string
		first      time.Time
		last       time.Time
		gran       bucket.Granularity
		firstLabel string
	}{
		{name: "three hours hourly", first: hm(17), last: hm(19), gran: bucket.GranularityHour, firstLabel: "17:00"},
		{name: "fifteen days hourly", first: hm(0), last: hm(15 * 24), gran: bucket.GranularityHour, firstLabel: "9 Aug 2026"},
		{name: "fifteen days daily", first: hm(0), last: hm(15 * 24), gran: bucket.GranularityDay, firstLabel: "9 Aug 2026"},
		{name: "two years daily", first: hm(0), last: hm(2 * 365 * 24), gran: bucket.GranularityDay, firstLabel: "Aug 2026"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			labels := buildAxisLabels(axisFor(tt.first, tt.last, tt.gran))
			if len(labels) == 0 {
				t.Fatal("axis carries no labels at all")
			}
			// The first label dates the axis; the rest may be bare.
			if labels[0].Text != tt.firstLabel {
				t.Errorf("first label = %q, want %q", labels[0].Text, tt.firstLabel)
			}

			claimedTo := -1.0
			for _, l := range labels {
				x := mustFloat(t, l.X)
				if x < claimedTo {
					t.Errorf("label %q at %.2f runs into the one before it, which claimed to %.2f",
						l.Text, x, claimedTo)
				}
				if end := x + labelWidth(l.Text); end > chartWidth {
					t.Errorf("label %q ends at %.2f, past the %d-unit viewBox", l.Text, end, chartWidth)
				} else {
					claimedTo = end
				}
			}
		})
	}
}

// Under hourly bucketing a single afternoon is several buckets, which must not
// read as a range of dates.
func TestTileSpanNamesOneDayOnce(t *testing.T) {
	got := renderGraphAt(t, commitsAt(t,
		time.Date(2026, 8, 9, 17, 22, 0, 0, time.UTC),
		time.Date(2026, 8, 9, 18, 4, 0, 0, time.UTC),
		time.Date(2026, 8, 9, 19, 26, 0, 0, time.UTC),
	), bucket.GranularityHour)

	if !strings.Contains(got, ">9 Aug 2026<") {
		t.Error("the Commits tile does not name the day the history covers")
	}
	if strings.Contains(got, "9 Aug 2026 – 9 Aug 2026") {
		t.Error("a single day is reported as a range of itself")
	}
}

// The page takes its unit from the buckets it was handed, so it cannot name one
// width while charting another.
func TestGraphNamesTheGranularityItWasHanded(t *testing.T) {
	records := commitsAt(t,
		time.Date(2026, 3, 3, 9, 15, 0, 0, time.UTC),
		time.Date(2026, 3, 3, 10, 40, 0, 0, time.UTC),
		time.Date(2026, 3, 3, 13, 5, 0, 0, time.UTC),
	)

	got := renderGraphAt(t, records, 4)
	for _, want := range []string{
		"Net lines of code each 4 hours",
		"Table view — by 4 hours",
		"<th>Bucket start</th>",
		"<td>2026-03-03 08:00</td>",
		"<td>2026-03-03 12:00</td>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("page is missing %q", want)
		}
	}
	// Three commits, two 4-hour columns — one per chart.
	if hits := strings.Count(got, `class="hit"`); hits != 4 {
		t.Errorf("rendered %d hit targets, want 4 (two buckets × two charts)", hits)
	}
}

// An empty history has no bucket to read the unit off, and hour is the default
// everywhere else too.
func TestGraphWithNoBucketsStillLabelsItselfHourly(t *testing.T) {
	got := renderGraph(t, nil)

	for _, want := range []string{"Table view — by hour", "<th>Hour</th>"} {
		if !strings.Contains(got, want) {
			t.Errorf("empty page is missing %q", want)
		}
	}
}

// commitsAt builds a growing history landing at the given times, so a test can
// say when the commits happened and ignore what they contained.
func commitsAt(t *testing.T, at ...time.Time) []report.Record {
	t.Helper()
	var out []report.Record
	prev := 0
	for i, ts := range at {
		rec := report.Record{
			Short:     fmt.Sprintf("c%06d", i),
			Timestamp: ts,
			Subject:   "feat: work",
			Product:   report.Count{Files: 1, Code: 100 * (i + 1)},
		}
		rec.Finalize(prev)
		prev = rec.TotalCode
		out = append(out, rec)
	}
	return out
}

// hm is a time h hours after a fixed origin, for spans stated in hours.
func hm(h int) time.Time {
	return time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC).Add(time.Duration(h) * time.Hour)
}

// axisFor is what build() computes, for a test that only wants the geometry.
func axisFor(first, last time.Time, gran bucket.Granularity) axis {
	ax := axis{
		first: gran.Truncate(first),
		gran:  gran,
		yMax:  1,
	}
	ax.span = axisSpan(ax.first, gran.Truncate(last), gran)
	ax.pitch = float64(plotWidth) / float64(ax.span)
	return ax
}

func mustFloat(t *testing.T, s string) float64 {
	t.Helper()
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("parse coordinate %q: %v", s, err)
	}
	return v
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
	for _, b := range bucketsOf(t, bucket.GranularityHour, graphFixture()...) {
		if err := g.Write(b); err != nil {
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
