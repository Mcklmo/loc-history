package writer

import (
	"fmt"
	"io"
	"strconv"

	"github.com/mcklmo/loc-history/internal/report"
)

// subjectWidth caps the last column so a verbose commit message cannot wrap the
// table on an 80-column terminal.
const subjectWidth = 60

// rowFmt keeps every cell a string so skipped commits can render a dash where a
// number would otherwise go. Widths are tuned to the header labels.
const rowFmt = "%-10s  %-7s  %7s  %6s  %8s  %6s  %s\n"

// Console streams one line per commit to a terminal as the walk progresses,
// rather than buffering until the end.
type Console struct {
	w             io.Writer
	headerWritten bool
}

// NewConsole returns a Console that streams to w.
func NewConsole(w io.Writer) *Console {
	return &Console{w: w}
}

func (c *Console) Write(r report.Record) error {
	if !c.headerWritten {
		if _, err := fmt.Fprintf(c.w, rowFmt,
			"DATE", "SHA", "PRODUCT", "TEST", "TOTAL", "Δ", "SUBJECT"); err != nil {
			return fmt.Errorf("console: write header: %w", err)
		}
		c.headerWritten = true
	}

	product, test, total := "-", "-", "-"
	if !r.Skipped {
		product = strconv.Itoa(r.Product.Code)
		test = strconv.Itoa(r.Test.Code)
		total = strconv.Itoa(r.TotalCode)
	}

	_, err := fmt.Fprintf(c.w, rowFmt,
		r.Timestamp.Format("2006-01-02"),
		r.Short,
		product,
		test,
		total,
		fmt.Sprintf("%+d", r.Delta),
		truncate(r.Subject, subjectWidth),
	)
	if err != nil {
		return fmt.Errorf("console: write record %s: %w", r.Short, err)
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
