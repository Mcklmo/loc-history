// Package writer holds the output sinks. Everything above this package is
// orchestration; everything in it is rendering. Adding a fourth sink is purely
// additive — implement Writer and wire it up in main.
//
// Sinks render what they are handed. Granularity is interpreted once, upstream
// in internal/bucket, so no sink here truncates a timestamp or groups a record.
package writer

import (
	"errors"

	"github.com/mcklmo/loc-history/internal/bucket"
	"github.com/mcklmo/loc-history/internal/report"
)

// Writer consumes buckets in chronological order, oldest first, and is closed
// exactly once when the walk ends — including on error paths, so an aborted run
// still leaves a partial artifact behind.
type Writer interface {
	Write(b bucket.Bucket) error
	// Summary is the run-level footer, computed upstream in internal/bucket and
	// offered once after the last bucket. A run with no rows never sends one,
	// and a sink is free to drop it.
	Summary(avg report.AverageDelta) error
	Close() error
}

// MultiWriter fans each bucket out to several sinks, mirroring io.MultiWriter.
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

// Write offers the bucket to every sink even after one fails — a broken console
// should not cost you the HTML report — and returns the first error seen.
func (m multi) Write(b bucket.Bucket) error {
	var first error
	for _, w := range m {
		if err := w.Write(b); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Summary offers the footer to every sink even after one fails, and returns the
// first error, exactly like Write.
func (m multi) Summary(avg report.AverageDelta) error {
	var first error
	for _, w := range m {
		if err := w.Summary(avg); err != nil && first == nil {
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

func (discard) Write(bucket.Bucket) error         { return nil }
func (discard) Summary(report.AverageDelta) error { return nil }
func (discard) Close() error                      { return nil }
