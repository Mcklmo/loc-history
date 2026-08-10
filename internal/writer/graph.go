package writer

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mcklmo/loc-history/internal/bucket"
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
	// baseY is the floor of the plot. A line count is never negative, so the
	// series sits on the bottom and the whole plot height is signal — twice the
	// vertical resolution a symmetric axis would have.
	baseY      = gutterTop + plotHeight
	tickLabelX = gutterLeft - 8
	xLabelY    = chartHeight - 5

	// An hourly year is 8,760 slots across a 898-unit plot, which puts a slot
	// well under a pixel. The floor keeps every commit-bearing bucket hoverable
	// at any span.
	minHitWidth = 4

	// maxAxisLabels is how many x labels the axis will consider before dropping
	// to a coarser unit. Claiming thins whatever survives, so this is a bound on
	// candidates rather than on labels drawn.
	maxAxisLabels = 20
)

// GraphOptions labels the page. How wide one bucket is comes off the buckets
// themselves — see build.
type GraphOptions struct {
	Title    string
	Subtitle string
}

// Graph renders the running total of lines of code over time — one area chart
// for product files, one for test files — to a self-contained HTML file.
//
// It buffers: the two charts share one y scale computed over the whole history,
// so nothing can be drawn until the last bucket has arrived. A few thousand
// buckets is nothing to hold in memory.
type Graph struct {
	path    string
	opts    GraphOptions
	buckets []bucket.Bucket
}

// NewGraph prepares a graph sink writing to path.
func NewGraph(path string, opts GraphOptions) (Writer, error) {
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

func (g *Graph) Write(b bucket.Bucket) error {
	g.buckets = append(g.buckets, b)
	return nil
}

// Summary is dropped on purpose: the charts plot the level the tree stood at,
// and a mean of the row deltas has nowhere to sit on that axis.
func (g *Graph) Summary(report.AverageDelta) error { return nil }

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
	Area      area
	Hits      []hit
	YTicks    []yTick
	XLabels   []xLabel
}

// area is the running total as two SVG paths sharing one point list: Fill is
// closed down to the baseline, Line traces the top edge alone so the series
// stays crisp where the fill is only a tint.
type area struct {
	Fill string
	Line string
}

// hit is a transparent full-height target over one commit-bearing bucket. Both
// charts carry the same title text, so either one gives the whole bucket's
// context.
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

// bucketRow carries both readings of a bucket: the level the tree stood at when
// it closed, and the net change that got it there. The chart draws the level, so
// the deltas would otherwise be reachable only from a tooltip.
type bucketRow struct {
	When         string
	Commits      string
	Product      string
	Test         string
	Total        string
	ProductDelta string
	TestDelta    string
	Delta        string
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

// axis is the x geometry the two charts share. Sharing it is what keeps the
// small multiples comparable: same first slot, same pitch, same y scale.
type axis struct {
	first time.Time
	span  int
	pitch float64
	yMax  int
	gran  bucket.Granularity
}

// at is the start of the i-th slot. Every bucket time is UTC, so this is exact.
func (a axis) at(i int) time.Time {
	return a.first.Add(time.Duration(i) * a.gran.Step())
}

func (g *Graph) build() pageData {
	// The buckets are self-describing, so the page cannot disagree with what
	// was actually aggregated. An empty history has nothing to read it off, and
	// hour is the default everywhere else too.
	gran := bucket.GranularityHour
	if len(g.buckets) > 0 {
		gran = g.buckets[0].Gran
	}

	p := pageData{
		Title:        g.opts.Title,
		Subtitle:     g.opts.Subtitle,
		BucketNoun:   gran.Noun(),
		BucketColumn: gran.Column(),
		Frame: frame{
			Width: chartWidth, Height: chartHeight,
			PlotLeft: gutterLeft, PlotRight: plotRight,
			PlotTop: gutterTop, PlotHeight: plotHeight,
			TickLabelX: tickLabelX, XLabelY: xLabelY,
		},
	}

	for _, b := range g.buckets {
		for _, r := range b.Records {
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
	}

	if len(g.buckets) == 0 {
		p.Empty = true
		return p
	}

	buckets, order := indexBuckets(g.buckets)
	p.Tiles = buildTiles(g.buckets, order)
	p.BucketRows = buildBucketRows(buckets, order, gran)

	// One scale for both charts, so a 2,000-line product tree is visibly ten
	// times a 200-line test tree.
	var peak int
	for _, b := range buckets {
		peak = max(peak, b.Product.Code, b.Test.Code)
	}

	ax := axis{
		first: order[0],
		span:  axisSpan(order[0], order[len(order)-1], gran),
		yMax:  niceMax(peak),
		gran:  gran,
	}
	ax.pitch = float64(plotWidth) / float64(ax.span)

	noun := gran.Noun()
	p.Charts = []chart{
		buildChart("Product files",
			fmt.Sprintf("Line chart of the running total of product lines of code, plotted at the end of each %s that carries a commit and joined by a smooth curve. The same values are listed in the table below.", noun),
			buckets, ax, func(b *bucket.Bucket) int { return b.Product.Code }),
		buildChart("Test files",
			fmt.Sprintf("Line chart of the running total of test lines of code, plotted at the end of each %s that carries a commit and joined by a smooth curve, on the same scale as the product chart. The same values are listed in the table below.", noun),
			buckets, ax, func(b *bucket.Bucket) int { return b.Test.Code }),
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

// indexBuckets puts the buffered buckets where the lattice can find them: the
// axis walks every slot between the first and the last, including the quiet
// ones, and looks each up by its start. The aggregator already emitted them
// chronologically, so the order is simply the order they arrived in.
func indexBuckets(buckets []bucket.Bucket) (map[string]*bucket.Bucket, []time.Time) {
	byStart := make(map[string]*bucket.Bucket, len(buckets))
	order := make([]time.Time, 0, len(buckets))
	for i := range buckets {
		b := &buckets[i]
		byStart[bucketKey(b.Start)] = b
		order = append(order, b.Start)
	}
	return byStart, order
}

// axisSpan is the number of buckets the x axis covers, first and last
// inclusive. Both are UTC, so the division is exact.
func axisSpan(first, last time.Time, g bucket.Granularity) int {
	return int(last.Sub(first)/g.Step()) + 1
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

func buildChart(label, aria string, buckets map[string]*bucket.Bucket, ax axis, pick func(*bucket.Bucket) int) chart {
	c := chart{
		Label:     label,
		AriaLabel: aria,
		YTicks:    buildYTicks(ax.yMax),
	}

	hitWidth := max(ax.pitch, minHitWidth)
	for i := range ax.span {
		b := buckets[bucketKey(ax.at(i))]
		if b == nil {
			continue
		}
		slotX := gutterLeft + float64(i)*ax.pitch
		c.Hits = append(c.Hits, hit{
			X:     fnum(slotSpan(slotX, ax.pitch, hitWidth)),
			W:     fnum(hitWidth),
			Title: bucketTitle(b, ax.gran),
		})
	}

	c.Area = buildCurve(buckets, ax, pick)
	c.XLabels = buildAxisLabels(ax)
	return c
}

// yFor maps a line count onto the plot. The series stands on baseY and grows
// upwards, so a total of yMax reaches the top gridline.
func yFor(v, yMax int) float64 {
	return baseY - float64(v)/float64(yMax)*float64(plotHeight)
}

// pt is one on-curve anchor: where a commit-bearing bucket's slot sits, and the
// level the tree stood at when it closed.
type pt struct{ x, y float64 }

// buildCurve traces the running total as a smooth line through every
// commit-bearing bucket. Only those buckets are anchors; a quiet slot is not a
// measurement, so the curve travels over it and a long quiet stretch reads as a
// gradual climb towards the next commit rather than as a staircase. What the
// curve draws between two anchors is interpolation, not observation.
//
// An anchor is kept even where its total equals its predecessor's. That gives
// the segment a zero secant, which the monotone filter turns into zero tangents
// at both ends — a genuinely flat stretch. Dropping it would let the earlier
// tangent aim at the next commit and bow the curve upwards across a stretch
// where nothing changed.
//
// The path is still sized by the commits rather than by the span: an hourly
// year is 8,760 slots but only as many anchors as there are commit-bearing
// buckets.
//
// A Skipped bucket carries zero counts because the folder was absent, so the
// curve genuinely falls to the floor there. That is what the snapshot says.
func buildCurve(buckets map[string]*bucket.Bucket, ax axis, pick func(*bucket.Bucket) int) area {
	at := func(x, y float64) string { return fnum(x) + "," + fnum(y) }

	ps := make([]pt, 0, len(buckets))
	for i := range ax.span {
		b := buckets[bucketKey(ax.at(i))]
		if b == nil {
			continue
		}
		ps = append(ps, pt{gutterLeft + float64(i)*ax.pitch, yFor(pick(b), ax.yMax)})
	}
	if len(ps) == 0 {
		// build returns early on an empty history, so this is unreachable —
		// but the emit below indexes, and an empty path is the honest answer.
		return area{}
	}

	// Distinct slots at a positive pitch, so x strictly increases and no
	// segment can have zero width.
	var segs strings.Builder
	if len(ps) > 1 {
		m := monotoneTangents(ps)
		for i, p := range ps[:len(ps)-1] {
			next := ps[i+1]
			h := next.x - p.x
			segs.WriteString("C" + at(p.x+h/3, p.y+m[i]*h/3) +
				" " + at(next.x-h/3, next.y-m[i+1]*h/3) +
				" " + at(next.x, next.y))
		}
	}

	// The last anchor sits at plotRight-pitch, and the level it stands at is
	// still standing at the end of the axis, so the line runs straight out.
	last := ps[len(ps)-1]
	top := at(ps[0].x, ps[0].y) + segs.String() + "L" + at(plotRight, last.y)
	return area{
		Line: "M" + top,
		Fill: "M" + at(gutterLeft, baseY) + "L" + top + "L" + at(plotRight, baseY) + "Z",
	}
}

// monotoneTangents fits one Fritsch–Carlson tangent per anchor, given anchors
// whose x strictly increases. Cubic Hermite segments built on these tangents
// pass through every anchor and provably cannot overshoot it: the filter below
// confines each segment's control points to the box between its two endpoints,
// so a rising run never draws a dip, a peak is never exceeded, and the curve
// cannot leave the plot. Working in inverted SVG y is fine — it is sign
// agnostic.
func monotoneTangents(ps []pt) []float64 {
	n := len(ps)

	// Secants: the straight-line slope of each segment.
	d := make([]float64, n-1)
	for i, p := range ps[:n-1] {
		d[i] = (ps[i+1].y - p.y) / (ps[i+1].x - p.x)
	}

	// A first guess: average the two secants meeting at each interior anchor.
	m := make([]float64, n)
	m[0], m[n-1] = d[0], d[n-2]
	for i := 1; i < n-1; i++ {
		m[i] = (d[i-1] + d[i]) / 2
	}

	for i, di := range d {
		if di == 0 {
			// The segment is flat, so it must leave flat and arrive flat.
			m[i], m[i+1] = 0, 0
			continue
		}
		// Tangents measured in units of the secant. Negative means the tangent
		// leans against the segment's direction; past a radius of 3 the cubic
		// bulges outside its endpoints.
		a, b := m[i]/di, m[i+1]/di
		if a < 0 {
			m[i], a = 0, 0
		}
		if b < 0 {
			m[i+1], b = 0, 0
		}
		if r := a*a + b*b; r > 9 {
			t := 3 / math.Sqrt(r)
			m[i], m[i+1] = t*a*di, t*b*di
		}
	}
	return m
}

// slotSpan centres something w wide on the slot at slotX, keeping it inside the
// plot — w has floors, so it can come out wider than the slot itself. Whenever
// w fits its slot this is the plain centring it replaces, to the byte.
func slotSpan(slotX, pitch, w float64) float64 {
	return max(gutterLeft, min(slotX+(pitch-w)/2, plotRight-w))
}

// bucketTitle leads with the levels the chart draws and carries the change that
// got there, so the tooltip reads the same way the chart does.
func bucketTitle(b *bucket.Bucket, g bucket.Granularity) string {
	subjects := make([]string, 0, len(b.Records))
	for _, r := range b.Records {
		subjects = append(subjects, r.Subject)
	}
	return fmt.Sprintf("%s · product %s · test %s · total %s (%s) · %s\n%s",
		b.Start.Format(g.TitleFormat()), commas(b.Product.Code), commas(b.Test.Code),
		commas(b.TotalCode), signedCommas(b.Delta),
		plural(b.Commits, "commit"), strings.Join(subjects, "\n"))
}

// buildYTicks lays out the gridlines: hairlines at yMax and yMax/2, a slightly
// stronger one on the zero baseline the series stands on. An odd axis maximum
// has no clean half step, so it gets two lines rather than a tick reading "2.5".
func buildYTicks(yMax int) []yTick {
	fracs := []float64{1, 0.5, 0}
	if yMax%2 != 0 {
		fracs = []float64{1, 0}
	}

	var out []yTick
	for _, f := range fracs {
		v := int(f * float64(yMax))
		y := yFor(v, yMax)
		t := yTick{
			Y:      fnum(y),
			LabelY: fnum(y + 3), // 10px text, centred on its line
			Class:  "grid",
			Text:   commas(v),
		}
		if f == 0 {
			t.Class = "zero"
		}
		out = append(out, t)
	}
	return out
}

// signedCommas writes a change the way the tooltip's levels are written, so one
// line does not mix separated and unseparated numbers. commas takes it unsigned
// because its grouping counts digits from the left.
func signedCommas(n int) string {
	if n < 0 {
		return "-" + commas(-n)
	}
	return "+" + commas(n)
}

// commas groups a non-negative number in thousands.
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
	if ax.gran < bucket.GranularityDay && ax.span <= maxAxisLabels {
		return unitHour
	}
	last := ax.at(ax.span - 1)
	midnight := bucket.GranularityDay.Truncate(ax.first)
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

func buildBucketRows(buckets map[string]*bucket.Bucket, order []time.Time, g bucket.Granularity) []bucketRow {
	var out []bucketRow
	for _, start := range order {
		b := buckets[bucketKey(start)]
		out = append(out, bucketRow{
			When:         start.Format(g.RowFormat()),
			Commits:      itoa(b.Commits),
			Product:      itoa(b.Product.Code),
			Test:         itoa(b.Test.Code),
			Total:        itoa(b.TotalCode),
			ProductDelta: fmt.Sprintf("%+d", b.ProductDelta),
			TestDelta:    fmt.Sprintf("%+d", b.TestDelta),
			Delta:        fmt.Sprintf("%+d", b.Delta),
		})
	}
	return out
}

func buildTiles(buckets []bucket.Bucket, order []time.Time) []tile {
	// The tree as it stood at the end of the history is the last bucket's
	// snapshot, which is its last commit's.
	last := buckets[len(buckets)-1]

	var productAdded, productRemoved, testAdded, testRemoved, commits int
	prevProduct, prevTest := 0, 0
	for _, b := range buckets {
		commits += b.Commits
		for _, r := range b.Records {
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
			Value: fmt.Sprintf("%d", commits),
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

func itoa(n int) string { return fmt.Sprintf("%d", n) }

// fnum formats a coordinate to two decimals, so the rendered page is
// byte-identical across runs and platforms.
func fnum(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }
