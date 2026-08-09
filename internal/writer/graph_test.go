package writer

import (
	"flag"
	"fmt"
	"html/template"
	"math"
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

func TestGraphTooltipsCarryTheirBucketAndSubjects(t *testing.T) {
	got := renderGraph(t, graphFixture())

	if !strings.Contains(got, "feat: add the panel") {
		t.Error("commit subjects are missing from the bucket tooltips")
	}
	if !strings.Contains(got, "2026-03-04") {
		t.Error("bucket tooltips do not name their bucket")
	}
}

// The chart is the level the tree stands at, not the change that got it there:
// the last point has to be the fixture's final 1,310 product lines, not the −90
// its last commit contributed.
func TestGraphPlotsTheRunningTotal(t *testing.T) {
	got := renderGraph(t, graphFixture())

	for _, want := range []string{`class="area"`, `class="area-line"`} {
		if !strings.Contains(got, want) {
			t.Errorf("no path rendered with %q", want)
		}
	}
	if strings.Contains(got, `class="bar`) {
		t.Error("a column was drawn; the chart is an area of the running total")
	}

	pts := areaLinePoints(t, got, 0)
	last := pts[len(pts)-1]
	if want := yFor(1310, 2000); !near(last.y, want) {
		t.Errorf("product series ends at y=%.2f, want %.2f (1,310 lines standing, not the last delta)", last.y, want)
	}
}

type point struct{ x, y float64 }

// near compares coordinates that have been through fnum's two decimals and back.
func near(a, b float64) bool { return math.Abs(a-b) < 0.01 }

// segment is one drawn piece of the top edge: where it ends up, and — for a
// cubic — the two control points that shape it on the way. A straight L carries
// none.
type segment struct {
	end   point
	ctrl  []point
	cubic bool
}

// areaLinePoints parses the top edge of the i-th chart back out of the page, so
// a test can assert on the shape that was actually rendered. It returns the
// on-curve anchors alone — the points the data actually put there.
func areaLinePoints(t *testing.T, page string, i int) []point {
	t.Helper()
	var out []point
	for _, s := range areaLineCurve(t, page, i) {
		out = append(out, s.end)
	}
	if len(out) == 0 {
		t.Fatal("area line carries no points")
	}
	return out
}

// areaLineCurve parses the top edge into its segments, control points and all.
// Only a test about the shape between two anchors needs those; everything else
// wants areaLinePoints.
//
// The leading M is returned as the first segment, so segment k always ends at
// anchor k.
func areaLineCurve(t *testing.T, page string, i int) []segment {
	t.Helper()
	found := regexp.MustCompile(`<path class="area-line" d="M([^"]+)"`).FindAllStringSubmatch(page, -1)
	if len(found) <= i {
		t.Fatalf("page carries %d area lines, want more than %d", len(found), i)
	}

	// The body is "x,y" followed by any number of L and C commands; a C carries
	// three space-separated pairs, of which the last is the anchor.
	body := found[i][1]
	cmds := regexp.MustCompile(`[LC]`).Split(body, -1)
	kinds := regexp.MustCompile(`[LC]`).FindAllString(body, -1)

	out := []segment{{end: parsePoint(t, cmds[0])}}
	for j, raw := range cmds[1:] {
		pairs := strings.Fields(raw)
		s := segment{cubic: kinds[j] == "C"}
		switch {
		case s.cubic && len(pairs) == 3:
			s.ctrl = []point{parsePoint(t, pairs[0]), parsePoint(t, pairs[1])}
			s.end = parsePoint(t, pairs[2])
		case !s.cubic && len(pairs) == 1:
			s.end = parsePoint(t, pairs[0])
		default:
			t.Fatalf("malformed %s command %q", kinds[j], raw)
		}
		out = append(out, s)
	}
	return out
}

func parsePoint(t *testing.T, pair string) point {
	t.Helper()
	xy := strings.Split(strings.TrimSpace(pair), ",")
	if len(xy) != 2 {
		t.Fatalf("malformed path point %q", pair)
	}
	return point{mustFloat(t, xy[0]), mustFloat(t, xy[1])}
}

// The user's requirement in one assertion: if every bucket adds lines, the graph
// rises. On an SVG canvas y grows downwards, so rising is non-increasing y.
func TestGraphRisesWithAGrowingHistory(t *testing.T) {
	var at []time.Time
	start := time.Date(2026, 3, 3, 9, 0, 0, 0, time.UTC)
	for i := range 6 {
		at = append(at, start.Add(time.Duration(i*5)*time.Hour))
	}
	got := renderGraphAt(t, commitsAt(t, at...), bucket.GranularityHour)

	pts := areaLinePoints(t, got, 0)
	if len(pts) < 6 {
		t.Fatalf("series has %d points for six growing buckets, want a point per bucket", len(pts))
	}
	for i, p := range pts[1:] {
		if p.y > pts[i].y {
			t.Errorf("point %d drops to y=%.2f from %.2f; a history that only grows must only rise",
				i+1, p.y, pts[i].y)
		}
	}
	if pts[len(pts)-1].y >= pts[0].y {
		t.Error("the series ends no higher than it started, so the growth is not visible at all")
	}
}

// A quiet stretch is not an absence of code: the level the last commit left
// stands until another one moves it, rather than falling away to the baseline.
func TestGraphCarriesTheTotalAcrossQuietBuckets(t *testing.T) {
	// The fixture stands at 300 product lines from the 6th of March to the
	// 11th: the 7th, 8th and 10th carry no commits at all, and the 9th's is
	// docs-only, so nothing moves the level in between.
	got := renderGraphAt(t, graphFixture(), bucket.GranularityDay)

	ax := axisFor(
		time.Date(2026, 3, 3, 14, 30, 0, 0, time.UTC),
		time.Date(2026, 3, 17, 14, 30, 0, 0, time.UTC),
		bucket.GranularityDay,
	)
	xOf := func(day int) float64 { return gutterLeft + float64(day-3)*ax.pitch }

	held := yFor(300, 2000)
	segs := areaLineCurve(t, got, 0)

	// Only a commit puts an anchor on the axis; a day nothing happened on is
	// not a measurement the curve may claim to have taken.
	for _, quiet := range []int{7, 8, 10} {
		if i := slices.IndexFunc(segs, func(s segment) bool { return near(s.end.x, xOf(quiet)) }); i >= 0 {
			t.Errorf("the series carries an anchor on the %dth of March, a day with no commits", quiet)
		}
	}

	// What it carries across is the standing total, not the floor: the 6th's
	// commit and the docs-only one on the 9th are both worth 300 lines.
	for _, day := range []int{6, 9} {
		i := slices.IndexFunc(segs, func(s segment) bool { return near(s.end.x, xOf(day)) })
		if i < 0 {
			t.Fatalf("no anchor on the %dth of March, where a commit landed", day)
		}
		if !near(segs[i].end.y, held) {
			t.Errorf("the %dth sits at y=%.2f, want %.2f (300 lines standing)", day, segs[i].end.y, held)
		}
		if near(segs[i].end.y, baseY) {
			t.Errorf("the series fell to the baseline on the %dth; a quiet %s is not an empty tree",
				day, bucket.GranularityDay.Noun())
		}
	}

	// And it carries it flat. Both control points of the 6th→9th segment sit at
	// the held level, so the docs-only commit did not bow the curve over three
	// days on which the tree did not change size.
	i := slices.IndexFunc(segs, func(s segment) bool { return near(s.end.x, xOf(9)) })
	if !segs[i].cubic {
		t.Fatalf("the 6th→9th stretch is not a curve segment; nothing to check for a bow")
	}
	for _, c := range segs[i].ctrl {
		if !near(c.y, held) {
			t.Errorf("the 6th→9th stretch bows to y=%.2f, want a flat %.2f — nothing moved the level",
				c.y, held)
		}
	}
}

// The curve is interpolation, but it may not be invention: a spline that bulges
// past a peak or dips below the point before it draws line counts the repository
// never had. Monotone cubic is chosen precisely because it cannot, and this is
// the assertion that keeps a prettier spline from being swapped in quietly.
func TestGraphCurveNeverOvershootsItsPoints(t *testing.T) {
	// A flat run, then a cliff, then a flat run: the shape a smoothing spline
	// overshoots on, either side of the jump.
	var spiky []report.Record
	prev := 0
	for i, code := range []int{300, 300, 9000, 9000} {
		rec := report.Record{
			Short:     fmt.Sprintf("s%06d", i),
			Timestamp: time.Date(2026, 3, 3+i, 9, 0, 0, 0, time.UTC),
			Subject:   "feat: work",
			Product:   report.Count{Files: 1, Code: code},
		}
		rec.Finalize(prev)
		prev = rec.TotalCode
		spiky = append(spiky, rec)
	}

	for _, tt := range []struct {
		name    string
		records []report.Record
	}{
		{"fixture", graphFixture()},
		{"a cliff between two flat runs", spiky},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := renderGraphAt(t, tt.records, bucket.GranularityDay)

			for chart := range 2 {
				segs := areaLineCurve(t, got, chart)
				for i, s := range segs[1:] {
					lo, hi := min(segs[i].end.y, s.end.y), max(segs[i].end.y, s.end.y)
					for _, c := range s.ctrl {
						if c.y < lo-0.01 || c.y > hi+0.01 {
							t.Errorf("chart %d segment %d is steered to y=%.2f, outside the %.2f–%.2f its own endpoints span — the curve leaves the data",
								chart, i, c.y, lo, hi)
						}
					}
				}
				// And nothing, anchor or control point, leaves the plot.
				for _, s := range segs {
					for _, p := range append([]point{s.end}, s.ctrl...) {
						if p.y < gutterTop-0.01 || p.y > baseY+0.01 {
							t.Errorf("chart %d carries y=%.2f, outside the plot %d–%d", chart, p.y, gutterTop, baseY)
						}
					}
				}
			}
		})
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
		"running total of product lines of code",
		"running total of test lines of code",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output is missing %q", want)
		}
	}
}

// Shared scale is the whole point of small multiples: a 2,000-line product tree
// has to be visibly ten times a 200-line test tree.
func TestGraphSharesOneYScaleAcrossBothCharts(t *testing.T) {
	got := renderGraph(t, graphFixture())

	figures := strings.Split(got, "<figure>")[1:]
	if len(figures) != 2 {
		t.Fatalf("got %d figures, want 2", len(figures))
	}
	// The fixture peaks at a 1,400-line product tree, so both axes top out at
	// 2,000 and stand on 0 — a count is never negative.
	for i, fig := range figures {
		for _, tick := range []string{"2,000", "0"} {
			want := ">" + escaped(tick) + "<"
			if !strings.Contains(fig, want) {
				t.Errorf("figure %d has no %q tick; the two charts are not on one scale", i, want)
			}
		}
		if strings.Contains(fig, escaped("−2,000")) {
			t.Errorf("figure %d carries a negative tick; the axis stands on zero", i)
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

	// 412 product lines, no test lines: both charts still draw their series,
	// the test one flat along the baseline.
	if areas := strings.Count(got, `class="area"`); areas != 2 {
		t.Errorf("rendered %d areas for a single commit, want 2", areas)
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
				// The levels are what the chart draws; the deltas are what it
				// no longer draws. Neither may be hover-only.
				for _, v := range []int{b.Product.Code, b.Test.Code, b.TotalCode} {
					want := fmt.Sprintf(`<td class="num">%d</td>`, v)
					if !strings.Contains(table, want) {
						t.Errorf("by-%s table is missing level %s for %s", gran.Noun(), want, when)
					}
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

// A year of hourly slots puts a slot at a tenth of a unit — far too thin to
// hover. The floor trades exact slot width for a chart that can still be used.
func TestGraphFloorsHitTargetWidth(t *testing.T) {
	// 40 commits spread over ~357 days: 8,581 hourly slots, pitch 0.10.
	var at []time.Time
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range 40 {
		at = append(at, start.Add(time.Duration(i*220)*time.Hour))
	}
	got := renderGraphAt(t, commitsAt(t, at...), bucket.GranularityHour)

	found := regexp.MustCompile(`<rect class="hit" x="([\d.]+)" y="\d+" width="([\d.]+)"`).FindAllStringSubmatch(got, -1)
	if len(found) == 0 {
		t.Fatal("no hit target rendered; the assertions below would be vacuous")
	}
	for _, m := range found {
		x, w := mustFloat(t, m[1]), mustFloat(t, m[2])
		if w < minHitWidth {
			t.Errorf("hit target is %.2f units wide, below the %d floor", w, minHitWidth)
		}
		// A floored width can outgrow its slot, so it has to be held inside
		// the plot rather than centred blindly.
		if x < gutterLeft || x+w > plotRight {
			t.Errorf("hit target spans %.2f–%.2f, outside the plot %d–%d", x, x+w, gutterLeft, plotRight)
		}
	}
	assertFinite(t, got)
}

// The path is sized by the commits, not by the span: an hourly year is 8,760
// slots, and a point per slot would put the whole lattice in the file.
func TestGraphPathIsSizedByCommitsNotBySpan(t *testing.T) {
	// 40 commits spread over ~357 days: 8,581 hourly slots.
	var at []time.Time
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range 40 {
		at = append(at, start.Add(time.Duration(i*220)*time.Hour))
	}
	got := renderGraphAt(t, commitsAt(t, at...), bucket.GranularityHour)

	// One anchor per commit-bearing bucket, plus the run out to the right edge.
	if n, ceiling := len(areaLinePoints(t, got, 0)), 40+1; n > ceiling {
		t.Errorf("the series carries %d points for 40 commits, want at most %d — quiet slots are being drawn",
			n, ceiling)
	}
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
		"at the end of each 4 hours",
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
