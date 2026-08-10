package writer

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/mcklmo/loc-history/internal/bucket"
)

// subjectWidth caps the last column so a verbose commit message cannot wrap the
// table on an 80-column terminal.
const subjectWidth = 60

// rowFmt keeps every cell a string so skipped buckets can render a dash where a
// number would otherwise go. Widths are tuned to the header labels; the first
// field is 16 wide because that is what a bucket start costs (2026-03-04 12:00).
const rowFmt = "%-16s  %-7s  %7s  %7s  %6s  %8s  %6s  %s\n"

// Console streams one line per time bucket to a terminal as the walk
// progresses, rather than buffering until the end.
type Console struct {
	w             io.Writer
	headerWritten bool
}

// NewConsole returns a Console that streams to w.
func NewConsole(w io.Writer) Writer {
	return &Console{w: w}
}

func (c *Console) Write(b bucket.Bucket) error {
	// The header is written lazily, on the first bucket, which is what lets it
	// name the unit the run actually bucketed by.
	if !c.headerWritten {
		if _, err := fmt.Fprintf(c.w, rowFmt,
			strings.ToUpper(b.Gran.Column()), "SHA", "COMMITS",
			"PRODUCT", "TEST", "TOTAL", "Δ", "SUBJECT"); err != nil {
			return fmt.Errorf("console: write header: %w", err)
		}
		c.headerWritten = true
	}

	// The bucket keeps its last commit's identity: one row, but still a commit
	// you can go and look at.
	last := b.Last()

	product, test, total := "-", "-", "-"
	if !b.Skipped {
		product = strconv.Itoa(b.Product.Code)
		test = strconv.Itoa(b.Test.Code)
		total = strconv.Itoa(b.TotalCode)
	}

	_, err := fmt.Fprintf(c.w, rowFmt,
		b.Start.Format(b.Gran.RowFormat()),
		last.Short,
		strconv.Itoa(b.Commits),
		product,
		test,
		total,
		fmt.Sprintf("%+d", b.Delta),
		truncate(last.Subject, subjectWidth),
	)
	if err != nil {
		return fmt.Errorf("console: write bucket %s: %w", b.Start.Format(b.Gran.RowFormat()), err)
	}
	return nil
}

// Close is a no-op: Console owns no resources and has already streamed
// everything it was given.
func (c *Console) Close() error { return nil }

func truncate(s string, width int) string {
	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	return string(runes[:width-1]) + "…"
}
