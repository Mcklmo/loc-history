// Package writer holds the output sinks. Everything above this package is
// orchestration; everything in it is rendering. Adding a fourth sink is purely
// additive — implement Writer and wire it up in main.
package writer

import (
	"errors"

	"github.com/mcklmo/loc-history/internal/report"
)

// Writer consumes records in commit order, oldest first, and is closed exactly
// once when the walk ends — including on error paths, so an aborted run still
// leaves a partial artifact behind.
type Writer interface {
	Write(r report.Record) error
	Close() error
}

// MultiWriter fans each record out to several sinks, mirroring io.MultiWriter.
func MultiWriter(ws ...Writer) Writer {
	switch len(ws) {
	case 0:
		return discard{}
	case 1:
		return ws[0]
	default:
		return multi(ws)
	}
}

type multi []Writer

// Write offers the record to every sink even after one fails — a broken console
// should not cost you the HTML report — and returns the first error seen.
func (m multi) Write(r report.Record) error {
	var first error
	for _, w := range m {
		if err := w.Write(r); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Close closes every sink even if an earlier one fails, so a broken file sink
// cannot leak the graph sink's file handle.
func (m multi) Close() error {
	errs := make([]error, 0, len(m))
	for _, w := range m {
		errs = append(errs, w.Close())
	}
	return errors.Join(errs...)
}

type discard struct{}

func (discard) Write(report.Record) error { return nil }
func (discard) Close() error              { return nil }
