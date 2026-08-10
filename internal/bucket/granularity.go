package bucket

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Granularity is the slice of time one bucket covers, counted in hours.
//
// It must divide 24. That is what keeps every bucket on a wall-clock boundary
// and, more importantly, keeps the graph's x axis an evenly spaced lattice: its
// geometry reads slot i as first + i×step, so a bucket landing between two
// slots would simply never be drawn. A 5-hour bucket restarts at midnight and
// does exactly that.
type Granularity int

const (
	// GranularityHour buckets commits by the hour they landed. It is the
	// default: a day-wide bucket collapses a whole afternoon of work into a
	// single row, which on a young repo is the whole history.
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

	const (
		hourSuffix = "h"
		daySuffix  = "d"
	)

	if strings.HasSuffix(raw, hourSuffix) {
		return parseGranularity(raw, hourSuffix, 1)
	}

	if strings.HasSuffix(raw, daySuffix) {
		return parseGranularity(raw, daySuffix, 24)
	}

	return 0, fmt.Errorf("unknown granularity %q; want hour, day, or a bucket width like 4h or 2d", raw)
}

func parseGranularity(
	raw string,
	suffixIdentifier string,
	multiplier int,
) (Granularity, error) {
	digits, ok := strings.CutSuffix(raw, suffixIdentifier)
	if !ok {
		return 0, fmt.Errorf("parseGranularity unknown granularity %q; want hour, day, or a bucket width like 4h or 2d", raw)
	}

	n, err := strconv.Atoi(digits)
	if err != nil {
		return 0, fmt.Errorf("unexpected int parse error %q: %w", digits, err)
	}

	g := Granularity(n * multiplier)
	if err := g.Valid(); err != nil {
		return 0, err
	}

	return g, nil
}

// Valid reports whether buckets this wide tile a day exactly.
func (g Granularity) Valid() error {
	if g < 1 {
		return fmt.Errorf("granularity %d is not positive", g)
	}
	if !(24%int(g) == 0 || int(g)%24 == 0) {
		return fmt.Errorf("granularity %d does not divide the day, expected a divisor of 24 or a multiple of 24", g)
	}

	return nil
}

// Step is how much time one bucket covers.
func (g Granularity) Step() time.Duration { return time.Duration(g) * time.Hour }

// Truncate is the single point at which a timestamp becomes a bucket. It reads
// the wall clock in the commit's own zone — git hands back %cI with its offset
// intact — and relabels that as UTC. So a commit at 23:00+02:00 buckets on its
// author's own evening, and because every bucket time is UTC, the arithmetic
// downstream is exact: no DST discontinuity can shorten a step.
//
// The hour is floored to a multiple of the bucket width, counting from
// midnight, so a 4-hour axis runs 00:00, 04:00, … and a 24-hour one is the
// calendar day.
func (g Granularity) Truncate(t time.Time) time.Time {
	hour := t.Hour() - t.Hour()%int(g)
	return time.Date(t.Year(), t.Month(), t.Day(), hour, 0, 0, 0, time.UTC)
}

// Noun names a bucket in prose, Column heads its table.
func (g Granularity) Noun() string {
	switch g {
	case GranularityHour:
		return "hour"
	case GranularityDay:
		return "day"
	}
	return fmt.Sprintf("%d hours", int(g))
}

func (g Granularity) Column() string {
	switch g {
	case GranularityHour:
		return "Hour"
	case GranularityDay:
		return "Date"
	}
	return "Bucket start"
}

// TitleFormat stamps a tooltip, RowFormat a table cell.
func (g Granularity) TitleFormat() string {
	if g == GranularityDay {
		return "Mon 2006-01-02"
	}
	return "Mon 2006-01-02 15:04"
}

func (g Granularity) RowFormat() string {
	if g == GranularityDay {
		return "2006-01-02"
	}
	return "2006-01-02 15:04"
}
