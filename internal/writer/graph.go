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
	// axisBand is deep enough that the x labels read as their own band rather
	// than running on from the bottom tick label.
	axisBand = 22

	plotWidth  = chartWidth - gutterLeft - gutterRight
	plotHeight = chartHeight - gutterTop - axisBand
	plotRight  = chartWidth - gutterRight
	// arm is half the plot: the axis is symmetric about zero, so a -500 bucket
	// is exactly as tall as a +500 bucket.
	arm        = plotHeight / 2
	zeroY      = gutterTop + arm
	tickLabelX = gutterLeft - 8
	xLabelY    = chartHeight - 5

	// barGap is the surface gap that separates adjacent columns. Below
	// minGapPitch it is dropped, so a dense history reads as a continuous
	// silhouette rather than dissolving into the gaps.
	barGap      = 2
	minGapPitch = 4
	maxBarWidth = 24 // a column never fills its slot; the leftover is air

	// An hourly year is 8,760 slots across a 898-unit plot, which puts a column
	// well under a pixel. These floors keep a sparse history visible and
	// hoverable at any span; a genuinely dense stretch merges into a silhouette,
	// which is the honest reading of it.
	minBarWidth = 1
	minHitWidth = 4

	// maxAxisLabels is how many x labels the axis will consider before dropping
	// to a coarser unit. Claiming thins whatever survives, so this is a bound on
	// candidates rather than on labels drawn.
	maxAxisLabels = 20
)

// Granularity is the slice of time one column covers, counted in hours.
//
// It must divide 24. That is what keeps every bucket on a wall-clock boundary
// and, more importantly, keeps the x axis an evenly spaced lattice: the
// geometry below reads slot i as first + i×step, so a bucket landing between
// two slots would simply never be drawn. A 5-hour bucket restarts at midnight
// and does exactly that.
type Granularity int

const (
	// GranularityHour buckets commits by the hour they landed. It is the
	// default: a day-wide bucket collapses a whole afternoon of work into a
	// single column, which on a young repo is the whole history.
	GranularityHour Granularity = 1
	// GranularityDay is the 24-hour bucket anchored at midnight, which is
	// exactly a calendar day — so one hour count expresses both.
	GranularityDay Granularity = 24
)

// ParseGranularity converts a --granularity value: the words hour and day, or a
// bucket width in whole hours, like 4h.
func ParseGranularity(s string) (Granularity, error) {
	raw := strings.ToLower(strings.TrimSpace(s))
	switch raw {
	case "hour":
		return GranularityHour, nil
	case "day":
		return GranularityDay, nil
	}

	digits, ok := strings.CutSuffix(raw, "h")
	if !ok {
		return 0, fmt.Errorf("unknown granularity %q; want hour, day, or a bucket width like 4h", s)
	}
	n, err := strconv.Atoi(digits)
	if err != nil {
		return 0, fmt.Errorf("unknown granularity %q; want hour, day, or a bucket width like 4h", s)
	}
	if g := Granularity(n); g.valid() {
		return g, nil
	}
	return 0, fmt.Errorf("bucket %q does not divide the day; want 1, 2, 3, 4, 6, 8, 12 or 24 hours", s)
}

// valid reports whether buckets this wide tile a day exactly.
func (g Granularity) valid() bool { return g >= 1 && g <= 24 && 24%int(g) == 0 }

// step is how much time one slot on the x axis covers.
func (g Granularity) step() time.Duration { return time.Duration(g) * time.Hour }

// truncate is the single point at which a timestamp becomes a bucket. It reads
// the wall clock in the commit's own zone — git hands back %cI with its offset
// intact — and relabels that as UTC. So a commit at 23:00+02:00 buckets on its
// author's own evening, and because every bucket time is UTC, the arithmetic
// downstream is exact: no DST discontinuity can shorten a step.
//
// The hour is floored to a multiple of the bucket width, counting from
// midnight, so a 4-hour axis runs 00:00, 04:00, … and a 24-hour one is the
// calendar day.
func (g Granularity) truncate(t time.Time) time.Time {
	hour := t.Hour() - t.Hour()%int(g)
	return time.Date(t.Year(), t.Month(), t.Day(), hour, 0, 0, 0, time.UTC)
}

// noun names a bucket in prose, column heads its table.
func (g Granularity) noun() string {
	switch g {
	case GranularityHour:
		return "hour"
	case GranularityDay:
		return "day"
	}
	return fmt.Sprintf("%d hours", int(g))
}

func (g Granularity) column() string {
	switch g {
	case GranularityHour:
		return "Hour"
	case GranularityDay:
		return "Date"
	}
	return "Bucket start"
}

// titleFormat stamps a tooltip, rowFormat a table cell.
func (g Granularity) titleFormat() string {
	if g == GranularityDay {
		return "Mon 2006-01-02"
	}
	return "Mon 2006-01-02 15:04"
}

func (g Granularity) rowFormat() string {
	if g == GranularityDay {
		return "2006-01-02"
	}
	return "2006-01-02 15:04"
}

// GraphOptions labels the page and picks how wide one column is.
type GraphOptions struct {
	Title       string
	Subtitle    string
	Granularity Granularity
}

// Graph renders net change in lines of code per time bucket — one diverging
// column chart for product files, one for test files — to a self-contained
// HTML file.
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
	if opts.Granularity == 0 {
		opts.Granularity = GranularityHour
	}
	// A bucket that does not tile the day would leave columns off the lattice
	// the axis is drawn on, and they would vanish rather than misdraw.
	if !opts.Granularity.valid() {
		return nil, fmt.Errorf("granularity: a %d-hour bucket does not divide the day", opts.Granularity)
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

	// BucketNoun and BucketColumn name the time slice in prose and in the
	// table head. Both tables render outside the empty-history branch, so both
	// are set before build can return early.
	BucketNoun   string
	BucketColumn string

	Frame      frame
	Tiles      []tile
	Charts     []chart
	Key        []legendSwatch
	BucketRows []bucketRow
	Rows       []tableRow
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
	XLabelY       int
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
	XLabels   []xLabel
}

// bar is one bucket's net change, drawn up from the zero line where the tree
// grew and down where it shrank.
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

// hit is a transparent full-height target over one commit-bearing bucket. It is
// wider than the column it covers, and both charts carry the same title text,
// so either one gives the whole bucket's context.
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

type xLabel struct {
	X    string
	Text string
}

type legendSwatch struct {
	Class string
	Label string
}

type bucketRow struct {
	When    string
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

// bucket aggregates every commit landing in one slice of time.
type bucket struct {
	start    time.Time
	product  int
	test     int
	total    int
	commits  int
	subjects []string
}

// axis is the x geometry the two charts share. Sharing it is what keeps the
// small multiples comparable: same first slot, same pitch, same y scale.
type axis struct {
	first time.Time
	span  int
	pitch float64
	yMax  int
	gran  Granularity
}

// at is the start of the i-th slot. Every bucket time is UTC, so this is exact.
func (a axis) at(i int) time.Time {
	return a.first.Add(time.Duration(i) * a.gran.step())
}

func (g *Graph) build() pageData {
	gran := g.opts.Granularity
	p := pageData{
		Title:        g.opts.Title,
		Subtitle:     g.opts.Subtitle,
		BucketNoun:   gran.noun(),
		BucketColumn: gran.column(),
		Frame: frame{
			Width: chartWidth, Height: chartHeight,
			PlotLeft: gutterLeft, PlotRight: plotRight,
			PlotTop: gutterTop, PlotHeight: plotHeight,
			TickLabelX: tickLabelX, XLabelY: xLabelY,
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

	buckets, order := groupByBucket(g.records, gran)
	p.Tiles = buildTiles(g.records, order)
	p.BucketRows = buildBucketRows(buckets, order, gran)

	// One scale for both charts, so a +2000 product bucket is visibly ten times
	// a +200 test bucket.
	var peak int
	for _, b := range buckets {
		peak = max(peak, abs(b.product), abs(b.test))
	}

	ax := axis{
		first: order[0],
		span:  axisSpan(order[0], order[len(order)-1], gran),
		yMax:  niceMax(peak),
		gran:  gran,
	}
	ax.pitch = float64(plotWidth) / float64(ax.span)

	noun := gran.noun()
	p.Charts = []chart{
		buildChart("Product files",
			fmt.Sprintf("Column chart of the net change in product lines of code each %s. The same values are listed in the table below.", noun),
			buckets, ax, func(b *bucket) int { return b.product }),
		buildChart("Test files",
			fmt.Sprintf("Column chart of the net change in test lines of code each %s, on the same scale as the product chart. The same values are listed in the table below.", noun),
			buckets, ax, func(b *bucket) int { return b.test }),
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

// bucketKey identifies a bucket by its start. Truncation has already chosen the
// bucket, so one format serves every granularity.
func bucketKey(t time.Time) string { return t.Format(time.RFC3339) }

// groupByBucket sums the per-category deltas of every commit sharing a bucket,
// and returns the bucket starts in chronological order.
//
// The deltas come from the cloc snapshots the pipeline already produced, not
// from a diff. prevProduct/prevTest start at zero — including across Skipped
// commits, whose counts are zero — mirroring Finalize's prevTotal convention,
// which is what makes productΔ + testΔ == Delta hold for every record.
func groupByBucket(records []report.Record, g Granularity) (map[string]*bucket, []time.Time) {
	buckets := make(map[string]*bucket, len(records))
	var order []time.Time
	prevProduct, prevTest := 0, 0

	for _, r := range records {
		start := g.truncate(r.Timestamp)
		key := bucketKey(start)
		b, ok := buckets[key]
		if !ok {
			b = &bucket{start: start}
			buckets[key] = b
			order = append(order, start)
		}
		b.product += r.Product.Code - prevProduct
		b.test += r.Test.Code - prevTest
		b.total += r.Delta
		b.commits++
		b.subjects = append(b.subjects, r.Subject)

		prevProduct, prevTest = r.Product.Code, r.Test.Code
	}

	sort.Slice(order, func(i, j int) bool { return order[i].Before(order[j]) })
	return buckets, order
}

// axisSpan is the number of buckets the x axis covers, first and last
// inclusive. Both are UTC, so the division is exact.
func axisSpan(first, last time.Time, g Granularity) int {
	return int(last.Sub(first)/g.step()) + 1
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

func buildChart(label, aria string, buckets map[string]*bucket, ax axis, pick func(*bucket) int) chart {
	c := chart{
		Label:     label,
		AriaLabel: aria,
		YTicks:    buildYTicks(ax.yMax),
	}

	gap := float64(barGap)
	if ax.pitch <= minGapPitch {
		gap = 0
	}
	width := max(min(ax.pitch-gap, maxBarWidth), minBarWidth)
	hitWidth := max(ax.pitch, minHitWidth)

	for i := range ax.span {
		b := buckets[bucketKey(ax.at(i))]
		if b == nil {
			continue
		}

		slotX := gutterLeft + float64(i)*ax.pitch
		// The hit target is the whole slot, gap included: columns get narrow.
		c.Hits = append(c.Hits, hit{
			X:     fnum(slotSpan(slotX, ax.pitch, hitWidth)),
			W:     fnum(hitWidth),
			Title: bucketTitle(b, ax.gran),
		})

		v := pick(b)
		if v == 0 {
			continue
		}
		height := float64(abs(v)) / float64(ax.yMax) * arm
		r := bar{
			Class: "bar up",
			X:     fnum(slotSpan(slotX, ax.pitch, width)),
			Y:     fnum(zeroY - height),
			W:     fnum(width),
			H:     fnum(height),
		}
		if v < 0 {
			r.Class, r.Y = "bar down", fnum(zeroY)
		}
		c.Bars = append(c.Bars, r)
	}

	c.XLabels = buildAxisLabels(ax)
	return c
}

// slotSpan centres something w wide on the slot at slotX, keeping it inside the
// plot — w has floors, so it can come out wider than the slot itself. Whenever
// w fits its slot this is the plain centring it replaces, to the byte.
func slotSpan(slotX, pitch, w float64) float64 {
	return max(gutterLeft, min(slotX+(pitch-w)/2, plotRight-w))
}

func bucketTitle(b *bucket, g Granularity) string {
	return fmt.Sprintf("%s · product %+d · test %+d · %s\n%s",
		b.start.Format(g.titleFormat()), b.product, b.test,
		plural(b.commits, "commit"), strings.Join(b.subjects, "\n"))
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

// labelUnit is the unit the x axis is labelled in. It is chosen from the span,
// not from the granularity alone: an hourly axis over a fortnight wants days.
type labelUnit int

const (
	unitHour labelUnit = iota
	unitDay
	unitMonth
)

// pickLabelUnit takes the finest unit that does not flood the axis. A unit
// finer than the bucket says nothing — every day bucket starts at 00:00 — and
// month is the floor: past a couple of dozen months the claiming below thins
// them, which is the right reading of a long history.
func pickLabelUnit(ax axis) labelUnit {
	if ax.gran < GranularityDay && ax.span <= maxAxisLabels {
		return unitHour
	}
	last := ax.at(ax.span - 1)
	midnight := GranularityDay.truncate(ax.first)
	if days := int(last.Sub(midnight)/(24*time.Hour)) + 1; days <= maxAxisLabels {
		return unitDay
	}
	return unitMonth
}

// id is what makes two slots share a label: consecutive slots carrying the same
// id belong to one labelled unit, and only the first of them is a candidate.
func (u labelUnit) id(t time.Time) string {
	switch u {
	case unitHour:
		return t.Format("2006-01-02T15")
	case unitDay:
		return t.Format("2006-01-02")
	}
	return t.Format("2006-01")
}

// text writes the label. Day and month labels carry the year on the first label
// and wherever the year rolls over, so the axis dates itself; an hour label does
// not need to, because the "Commits" tile already carries the date.
func (u labelUnit) text(t time.Time, first bool) string {
	switch u {
	case unitHour:
		return t.Format("15:04")
	case unitDay:
		if first || (t.Month() == time.January && t.Day() == 1) {
			return t.Format("2 Jan 2006")
		}
		return t.Format("2 Jan")
	}
	if first || t.Month() == time.January {
		return t.Format("Jan 2006")
	}
	return t.Format("Jan")
}

// labelWidth is the room a label claims, in user units at 10px — generous, so a
// wide "Jan 2006" does not crowd the label behind it.
func labelWidth(text string) float64 { return float64(len(text))*6 + 10 }

// buildAxisLabels walks the slots and labels each new unit the axis enters.
// Every label reserves the room its text needs; one whose slot falls inside the
// previous label's claim, or whose own text would run off the right edge, is
// dropped rather than drawn on top of its neighbour. A dropped label is not
// retried at the next slot of the same unit — that would only shift the
// collision along.
func buildAxisLabels(ax axis) []xLabel {
	unit := pickLabelUnit(ax)

	var out []xLabel
	claimedTo := -1.0
	prevID := ""

	for i := range ax.span {
		t := ax.at(i)
		id := unit.id(t)
		if id == prevID {
			continue
		}
		prevID = id

		text := unit.text(t, len(out) == 0)
		width := labelWidth(text)

		x := gutterLeft + float64(i)*ax.pitch
		if x < claimedTo || x+width > chartWidth {
			continue
		}
		claimedTo = x + width
		out = append(out, xLabel{X: fnum(x), Text: text})
	}
	return out
}

func buildBucketRows(buckets map[string]*bucket, order []time.Time, g Granularity) []bucketRow {
	var out []bucketRow
	for _, start := range order {
		b := buckets[bucketKey(start)]
		out = append(out, bucketRow{
			When:    start.Format(g.rowFormat()),
			Commits: itoa(b.commits),
			Product: fmt.Sprintf("%+d", b.product),
			Test:    fmt.Sprintf("%+d", b.test),
			Total:   fmt.Sprintf("%+d", b.total),
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

	// Compared as formatted text, not as bucket count: an hourly history can
	// run to several buckets inside one day, which is not a span of days.
	span := order[0].Format("2 Jan 2006")
	if end := order[len(order)-1].Format("2 Jan 2006"); end != span {
		span += " – " + end
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
