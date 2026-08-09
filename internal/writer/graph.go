package writer

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mcklmo/loc-history/internal/report"
)

// Calendar geometry, in SVG user units.
const (
	cellSize   = 13
	cellGap    = 3
	cellPitch  = cellSize + cellGap
	gutterLeft = 32 // weekday labels
	gutterTop  = 20 // month labels
	// monthLabelGap is the minimum number of week columns between two month
	// labels, so a short history does not stack them on top of each other.
	monthLabelGap = 3
)

// GraphOptions labels the page.
type GraphOptions struct {
	Title    string
	Subtitle string
}

// Graph renders a calendar heat map of daily net change to a self-contained
// HTML file.
//
// It buffers: the colour scale is quantised over the whole history, so no cell
// can be painted until the last record has arrived. A few thousand records is
// nothing to hold in memory.
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

	Tiles  []tile
	Cells  []cell
	Months []monthLabel
	Days   []dayLabel
	Legend legend
	Rows   []tableRow

	Width  int
	Height int
}

type tile struct {
	Label string
	Value string
	Note  string
}

type cell struct {
	X, Y  int
	Class string
	Title string
}

type monthLabel struct {
	X    int
	Text string
}

type dayLabel struct {
	Y    int
	Text string
}

type legendSwatch struct {
	Class string
	Label string
}

type legend struct {
	Swatches []legendSwatch
	Low      string
	High     string
	Note     string
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
	delta    int
	commits  int
	subjects []string
}

func (g *Graph) build() pageData {
	p := pageData{Title: g.opts.Title, Subtitle: g.opts.Subtitle}

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

	first := weekStart(order[0])
	last := weekStart(order[len(order)-1])
	weeks := int(last.Sub(first).Hours()/24)/7 + 1

	p.Width = gutterLeft + weeks*cellPitch
	p.Height = gutterTop + 7*cellPitch

	thresholds := quantiles(days)
	p.Legend = buildLegend(days, thresholds)

	for w := range weeks {
		monday := first.AddDate(0, 0, w*7)
		for d := range 7 {
			date := monday.AddDate(0, 0, d)
			p.Cells = append(p.Cells, buildCell(date, days[dateKey(date)], w, d, thresholds))
		}
	}
	p.Months = buildMonthLabels(first, weeks)
	// Mon, Wed, Fri only — labelling all seven crowds an 11px row.
	for _, d := range []struct {
		row  int
		text string
	}{{0, "Mon"}, {2, "Wed"}, {4, "Fri"}} {
		p.Days = append(p.Days, dayLabel{
			Y:    gutterTop + d.row*cellPitch + cellSize - 2,
			Text: d.text,
		})
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

// groupByDay sums the deltas of every commit sharing a calendar date, and
// returns the dates in chronological order.
func groupByDay(records []report.Record) (map[string]*day, []time.Time) {
	days := make(map[string]*day, len(records))
	var order []time.Time

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
		d.delta += r.Delta
		d.commits++
		d.subjects = append(d.subjects, r.Subject)
	}

	sort.Slice(order, func(i, j int) bool { return order[i].Before(order[j]) })
	return days, order
}

// weekStart returns the Monday of t's week: columns are ISO weeks.
func weekStart(t time.Time) time.Time {
	offset := (int(t.Weekday()) + 6) % 7 // Monday = 0
	return t.AddDate(0, 0, -offset)
}

func buildCell(date time.Time, d *day, week, row int, th thresholds) cell {
	c := cell{
		X: gutterLeft + week*cellPitch,
		Y: gutterTop + row*cellPitch,
	}

	if d == nil {
		c.Class = "cell none"
		c.Title = date.Format("Mon 2006-01-02") + " · no commits"
		return c
	}

	c.Class = "cell " + scaleClass(d.delta, th)
	c.Title = fmt.Sprintf("%s · %+d lines · %s\n%s",
		date.Format("Mon 2006-01-02"), d.delta, plural(d.commits, "commit"),
		strings.Join(d.subjects, "\n"))
	return c
}

// thresholds are the quartile boundaries of daily magnitude.
type thresholds struct {
	q [3]int
	// max is the largest magnitude in either direction, for the legend.
	maxPos, maxNeg int
}

// quantiles buckets by quartile rather than linearly: one enormous initial
// commit would otherwise flatten every later day to the palest step.
func quantiles(days map[string]*day) thresholds {
	var mags []int
	var th thresholds
	for _, d := range days {
		if d.delta > th.maxPos {
			th.maxPos = d.delta
		}
		if d.delta < th.maxNeg {
			th.maxNeg = d.delta
		}
		if d.delta != 0 {
			mags = append(mags, abs(d.delta))
		}
	}
	if len(mags) == 0 {
		return th
	}
	sort.Ints(mags)
	for i, frac := range []float64{0.25, 0.5, 0.75} {
		th.q[i] = mags[int(float64(len(mags)-1)*frac)]
	}
	return th
}

// scaleClass maps a signed daily change onto the diverging ramp: a neutral
// midpoint for "nothing changed", four steps out along each arm.
func scaleClass(delta int, th thresholds) string {
	if delta == 0 {
		return "zero"
	}
	prefix := "pos"
	if delta < 0 {
		prefix = "neg"
	}

	mag := abs(delta)
	step := 4
	switch {
	case mag <= th.q[0]:
		step = 1
	case mag <= th.q[1]:
		step = 2
	case mag <= th.q[2]:
		step = 3
	}
	return fmt.Sprintf("%s%d", prefix, step)
}

func buildLegend(days map[string]*day, th thresholds) legend {
	l := legend{
		Low:  fmt.Sprintf("%d removed", th.maxNeg),
		High: fmt.Sprintf("+%d added", th.maxPos),
		Note: "Intensity steps at the quartiles of daily magnitude" +
			fmt.Sprintf(" (%d, %d, %d lines).", th.q[0], th.q[1], th.q[2]),
	}
	for _, s := range []struct{ class, label string }{
		{"neg4", "largest net deletion"},
		{"neg3", ""}, {"neg2", ""}, {"neg1", ""},
		{"zero", "no net change"},
		{"pos1", ""}, {"pos2", ""}, {"pos3", ""},
		{"pos4", "largest net addition"},
	} {
		l.Swatches = append(l.Swatches, legendSwatch{Class: s.class, Label: s.label})
	}
	_ = days
	return l
}

func buildMonthLabels(first time.Time, weeks int) []monthLabel {
	var out []monthLabel
	lastLabelled := -monthLabelGap
	prevMonth := time.Month(0)

	for w := range weeks {
		monday := first.AddDate(0, 0, w*7)
		if monday.Month() == prevMonth {
			continue
		}
		prevMonth = monday.Month()
		if w-lastLabelled < monthLabelGap {
			continue
		}
		lastLabelled = w

		text := monday.Format("Jan")
		if monday.Month() == time.January || w == 0 {
			text = monday.Format("Jan 2006")
		}
		out = append(out, monthLabel{X: gutterLeft + w*cellPitch, Text: text})
	}
	return out
}

func buildTiles(records []report.Record, order []time.Time) []tile {
	last := records[len(records)-1]

	var added, removed int
	for _, r := range records {
		if r.Delta > 0 {
			added += r.Delta
		} else {
			removed -= r.Delta
		}
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
			Label: "Written / removed",
			Value: fmt.Sprintf("+%d / −%d", added, removed),
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

// itoa is a package-level helper so the template can embed geometry constants
// at build time rather than passing them through every cell.
func itoa(n int) string { return fmt.Sprintf("%d", n) }
