package writer

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mcklmo/loc-history/internal/report"
)

// Chart geometry, in SVG user units. The viewBox is fixed and the element is
// sized at 100% width, so the whole history always fits the card: no horizontal
// scrolling, and the two charts stay time-aligned by construction.
const (
	chartWidth  = 952
	chartHeight = 214

	gutterLeft  = 46 // y tick labels
	gutterRight = 8
	gutterTop   = 10
	// axisBand is deep enough that the month labels read as their own band
	// rather than running on from the bottom tick label.
	axisBand = 22

	plotWidth  = chartWidth - gutterLeft - gutterRight
	plotHeight = chartHeight - gutterTop - axisBand
	plotRight  = chartWidth - gutterRight
	// arm is half the plot: the axis is symmetric about zero, so a -500 day is
	// exactly as tall as a +500 day.
	arm         = plotHeight / 2
	zeroY       = gutterTop + arm
	tickLabelX  = gutterLeft - 8
	monthLabelY = chartHeight - 5

	// barGap is the surface gap that separates adjacent columns. Below
	// minGapPitch it is dropped, so a dense history reads as a continuous
	// silhouette rather than dissolving into the gaps.
	barGap      = 2
	minGapPitch = 4
	maxBarWidth = 24 // a column never fills its slot; the leftover is air

	// A month label claims the room its own text needs, in user units at 10px,
	// so a wide "Jan 2006" does not crowd the "Feb" behind it.
	monthGap     = 34
	yearMonthGap = 60
)

// GraphOptions labels the page.
type GraphOptions struct {
	Title    string
	Subtitle string
}

// Graph renders daily net change in lines of code — one diverging column chart
// for product files, one for test files — to a self-contained HTML file.
//
// It buffers: the two charts share one y scale computed over the whole history,
// so no column can be drawn until the last record has arrived. A few thousand
// records is nothing to hold in memory.
type Graph struct {
	path    string
	opts    GraphOptions
	records []report.Record
}

// NewGraph prepares a graph sink writing to path.
func NewGraph(path string, opts GraphOptions) (*Graph, error) {
	if opts.Title == "" {
		opts.Title = "loc-history"
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create output directory %s: %w", dir, err)
		}
	}
	// Fail now rather than after a full walk.
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return &Graph{path: path, opts: opts}, nil
}

func (g *Graph) Write(r report.Record) error {
	g.records = append(g.records, r)
	return nil
}

// Close renders the page.
func (g *Graph) Close() error {
	page := g.build()

	f, err := os.Create(g.path)
	if err != nil {
		return fmt.Errorf("create %s: %w", g.path, err)
	}
	if err := pageTemplate.Execute(f, page); err != nil {
		f.Close()
		return fmt.Errorf("render %s: %w", g.path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", g.path, err)
	}
	return nil
}

// --- view model -------------------------------------------------------------

type pageData struct {
	Title    string
	Subtitle string
	Empty    bool

	Frame   frame
	Tiles   []tile
	Charts  []chart
	Key     []legendSwatch
	DayRows []dayRow
	Rows    []tableRow
}

// frame is the fixed SVG geometry, handed to the template so the markup does
// not repeat the constants above.
type frame struct {
	Width, Height int
	PlotLeft      int
	PlotRight     int
	PlotTop       int
	PlotHeight    int
	TickLabelX    int
	MonthLabelY   int
}

type tile struct {
	Label string
	Value string
	Note  string
}

// chart is one small multiple: one series, one y scale shared with its sibling.
type chart struct {
	Label     string
	AriaLabel string
	Bars      []bar
	Hits      []hit
	YTicks    []yTick
	Months    []monthLabel
}

// bar is one day's net change, drawn up from the zero line where the tree grew
// and down where it shrank.
//
// The skill's 4px rounded data-end is deliberately dropped: a dense history
// puts columns well under 4 units wide, and a corner radius wider than the bar
// distorts the value the bar encodes.
type bar struct {
	Class string // "bar up" | "bar down"
	X     string
	Y     string
	W     string
	H     string
}

// hit is a transparent full-height target over one commit-bearing day. It is
// wider than the column it covers, and both charts carry the same title text,
// so either one gives the whole day's context.
type hit struct {
	X     string
	W     string
	Title string
}

type yTick struct {
	Y      string
	LabelY string
	Class  string // "grid" | "zero"
	Text   string
}

type monthLabel struct {
	X    string
	Text string
}

type legendSwatch struct {
	Class string
	Label string
}

type dayRow struct {
	Date    string
	Commits string
	Product string
	Test    string
	Total   string
}

type tableRow struct {
	Date    string
	Short   string
	Subject string
	Product string
	Test    string
	Total   string
	Delta   string
}

// day aggregates every commit landing on one calendar date.
type day struct {
	date     time.Time
	product  int
	test     int
	total    int
	commits  int
	subjects []string
}

func (g *Graph) build() pageData {
	p := pageData{
		Title:    g.opts.Title,
		Subtitle: g.opts.Subtitle,
		Frame: frame{
			Width: chartWidth, Height: chartHeight,
			PlotLeft: gutterLeft, PlotRight: plotRight,
			PlotTop: gutterTop, PlotHeight: plotHeight,
			TickLabelX: tickLabelX, MonthLabelY: monthLabelY,
		},
	}

	for _, r := range g.records {
		p.Rows = append(p.Rows, tableRow{
			Date:    r.Timestamp.Format("2006-01-02"),
			Short:   r.Short,
			Subject: r.Subject,
			Product: countCell(r, r.Product.Code),
			Test:    countCell(r, r.Test.Code),
			Total:   countCell(r, r.TotalCode),
			Delta:   fmt.Sprintf("%+d", r.Delta),
		})
	}

	if len(g.records) == 0 {
		p.Empty = true
		return p
	}

	days, order := groupByDay(g.records)
	p.Tiles = buildTiles(g.records, order)
	p.DayRows = buildDayRows(days, order)

	first := order[0]
	span := axisSpan(first, order[len(order)-1])

	// One scale for both charts, so a +2000 product day is visibly ten times a
	// +200 test day.
	var peak int
	for _, d := range days {
		peak = max(peak, abs(d.product), abs(d.test))
	}
	yMax := niceMax(peak)

	p.Charts = []chart{
		buildChart("Product files",
			"Column chart of the net change in product lines of code each day. The same values are listed in the by-day table below.",
			days, first, span, yMax, func(d *day) int { return d.product }),
		buildChart("Test files",
			"Column chart of the net change in test lines of code each day, on the same scale as the product chart. The same values are listed in the by-day table below.",
			days, first, span, yMax, func(d *day) int { return d.test }),
	}
	p.Key = []legendSwatch{
		{Class: "added", Label: "added"},
		{Class: "removed", Label: "removed"},
	}

	return p
}

// countCell renders a number, or a dash where the folder was not there at all
// — zero and absent are different facts.
func countCell(r report.Record, n int) string {
	if r.Skipped {
		return "—"
	}
	return fmt.Sprintf("%d", n)
}

func dateKey(t time.Time) string { return t.Format("2006-01-02") }

// groupByDay sums the per-category deltas of every commit sharing a calendar
// date, and returns the dates in chronological order.
//
// The deltas come from the cloc snapshots the pipeline already produced, not
// from a diff. prevProduct/prevTest start at zero — including across Skipped
// commits, whose counts are zero — mirroring Finalize's prevTotal convention,
// which is what makes productΔ + testΔ == Delta hold for every record.
func groupByDay(records []report.Record) (map[string]*day, []time.Time) {
	days := make(map[string]*day, len(records))
	var order []time.Time
	prevProduct, prevTest := 0, 0

	for _, r := range records {
		date := time.Date(r.Timestamp.Year(), r.Timestamp.Month(), r.Timestamp.Day(),
			0, 0, 0, 0, time.UTC)
		key := dateKey(date)
		d, ok := days[key]
		if !ok {
			d = &day{date: date}
			days[key] = d
			order = append(order, date)
		}
		d.product += r.Product.Code - prevProduct
		d.test += r.Test.Code - prevTest
		d.total += r.Delta
		d.commits++
		d.subjects = append(d.subjects, r.Subject)

		prevProduct, prevTest = r.Product.Code, r.Test.Code
	}

	sort.Slice(order, func(i, j int) bool { return order[i].Before(order[j]) })
	return days, order
}

// axisSpan is the number of calendar days the x axis covers, first and last
// commit day inclusive. Both are UTC midnight, so the division is exact.
func axisSpan(first, last time.Time) int {
	return int(last.Sub(first).Hours()/24) + 1
}

// niceMax rounds n up to the next 1, 2 or 5 × 10ⁿ so the axis reads in round
// numbers. The floor of 1 keeps a flat history from dividing by zero.
func niceMax(n int) int {
	if n <= 1 {
		return 1
	}
	pow := 1
	for pow*10 <= n {
		pow *= 10
	}
	for _, m := range []int{1, 2, 5} {
		if n <= m*pow {
			return m * pow
		}
	}
	return 10 * pow
}

func buildChart(label, aria string, days map[string]*day, first time.Time, span, yMax int, pick func(*day) int) chart {
	c := chart{
		Label:     label,
		AriaLabel: aria,
		YTicks:    buildYTicks(yMax),
	}

	pitch := float64(plotWidth) / float64(span)
	gap := float64(barGap)
	if pitch <= minGapPitch {
		gap = 0
	}
	width := min(pitch-gap, maxBarWidth)

	for i := range span {
		date := first.AddDate(0, 0, i)
		d := days[dateKey(date)]
		if d == nil {
			continue
		}

		slotX := gutterLeft + float64(i)*pitch
		// The hit target is the whole slot, gap included: columns get narrow.
		c.Hits = append(c.Hits, hit{X: fnum(slotX), W: fnum(pitch), Title: dayTitle(d)})

		v := pick(d)
		if v == 0 {
			continue
		}
		height := float64(abs(v)) / float64(yMax) * arm
		b := bar{
			Class: "bar up",
			X:     fnum(slotX + (pitch-width)/2),
			Y:     fnum(zeroY - height),
			W:     fnum(width),
			H:     fnum(height),
		}
		if v < 0 {
			b.Class, b.Y = "bar down", fnum(zeroY)
		}
		c.Bars = append(c.Bars, b)
	}

	c.Months = buildMonthLabels(first, span, pitch)
	return c
}

func dayTitle(d *day) string {
	return fmt.Sprintf("%s · product %+d · test %+d · %s\n%s",
		d.date.Format("Mon 2006-01-02"), d.product, d.test,
		plural(d.commits, "commit"), strings.Join(d.subjects, "\n"))
}

// buildYTicks lays out the gridlines: hairlines at ±yMax and ±yMax/2, a
// slightly stronger one at zero. An odd axis maximum has no clean half step, so
// it gets three lines rather than a tick reading "2.5".
func buildYTicks(yMax int) []yTick {
	fracs := []float64{1, 0.5, 0, -0.5, -1}
	if yMax%2 != 0 {
		fracs = []float64{1, 0, -1}
	}

	var out []yTick
	for _, f := range fracs {
		y := zeroY - f*arm
		t := yTick{
			Y:      fnum(y),
			LabelY: fnum(y + 3), // 10px text, centred on its line
			Class:  "grid",
			Text:   axisLabel(int(f * float64(yMax))),
		}
		if f == 0 {
			t.Class = "zero"
		}
		out = append(out, t)
	}
	return out
}

// axisLabel writes a tick in round, thousands-separated numbers.
func axisLabel(n int) string {
	switch {
	case n > 0:
		return "+" + commas(n)
	case n < 0:
		return "−" + commas(-n)
	}
	return "0"
}

func commas(n int) string {
	s := itoa(n)
	head := len(s) % 3
	if head == 0 {
		head = 3
	}
	out := s[:head]
	for i := head; i < len(s); i += 3 {
		out += "," + s[i:i+3]
	}
	return out
}

// buildMonthLabels walks the day axis and labels each month it enters. Every
// label reserves the span it occupies; a month whose column falls inside the
// previous label's span, or whose own text would run off the right edge, is
// skipped rather than drawn on top of its neighbour.
func buildMonthLabels(first time.Time, span int, pitch float64) []monthLabel {
	var out []monthLabel
	claimedTo := -1.0
	prevMonth := time.Month(0)

	for i := range span {
		date := first.AddDate(0, 0, i)
		if date.Month() == prevMonth {
			continue
		}
		prevMonth = date.Month()

		text, width := date.Format("Jan"), float64(monthGap)
		if date.Month() == time.January || len(out) == 0 {
			text, width = date.Format("Jan 2006"), yearMonthGap
		}

		x := gutterLeft + float64(i)*pitch
		if x < claimedTo || x+width > chartWidth {
			continue
		}
		claimedTo = x + width
		out = append(out, monthLabel{X: fnum(x), Text: text})
	}
	return out
}

func buildDayRows(days map[string]*day, order []time.Time) []dayRow {
	var out []dayRow
	for _, date := range order {
		d := days[dateKey(date)]
		out = append(out, dayRow{
			Date:    date.Format("2006-01-02"),
			Commits: itoa(d.commits),
			Product: fmt.Sprintf("%+d", d.product),
			Test:    fmt.Sprintf("%+d", d.test),
			Total:   fmt.Sprintf("%+d", d.total),
		})
	}
	return out
}

func buildTiles(records []report.Record, order []time.Time) []tile {
	last := records[len(records)-1]

	var productAdded, productRemoved, testAdded, testRemoved int
	prevProduct, prevTest := 0, 0
	for _, r := range records {
		if d := r.Product.Code - prevProduct; d > 0 {
			productAdded += d
		} else {
			productRemoved -= d
		}
		if d := r.Test.Code - prevTest; d > 0 {
			testAdded += d
		} else {
			testRemoved -= d
		}
		prevProduct, prevTest = r.Product.Code, r.Test.Code
	}

	span := order[0].Format("2 Jan 2006")
	if len(order) > 1 {
		span += " – " + order[len(order)-1].Format("2 Jan 2006")
	}

	return []tile{
		{
			Label: "Lines today",
			Value: fmt.Sprintf("%d", last.TotalCode),
			Note:  fmt.Sprintf("%d product · %d test", last.Product.Code, last.Test.Code),
		},
		{
			Label: "Product",
			Value: fmt.Sprintf("+%d / −%d", productAdded, productRemoved),
			Note:  "summed over every commit",
		},
		{
			Label: "Test",
			Value: fmt.Sprintf("+%d / −%d", testAdded, testRemoved),
			Note:  "summed over every commit",
		},
		{
			Label: "Commits",
			Value: fmt.Sprintf("%d", len(records)),
			Note:  span,
		},
	}
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }

// fnum formats a coordinate to two decimals, so the rendered page is
// byte-identical across runs and platforms.
func fnum(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }
