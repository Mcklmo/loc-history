// Package bucket turns a stream of per-commit records into fixed slices of
// time. It is the single place granularity is interpreted; the sinks below it
// render what they are handed and none of them truncates a timestamp or groups
// a record.
package bucket

import (
	"errors"
	"fmt"
	"time"

	"github.com/mcklmo/loc-history/internal/report"
)

// Bucket is every commit landing in one slice of time, as the sinks receive it.
type Bucket struct {
	Start   time.Time   `json:"bucket_start"`
	Gran    Granularity `json:"bucket_hours"` // self-describing: the graph reads its lattice width here
	Commits int         `json:"commits"`

	// Tree size at the end of the bucket — the last commit's snapshot.
	Product   report.Count `json:"product"`
	Test      report.Count `json:"test"`
	TotalCode int          `json:"total_code"`

	// Summed net change across the bucket's commits.
	// ProductDelta + TestDelta == Delta, for the same reason it holds per
	// record: both are differences of the cloc snapshots the pipeline already
	// produced, taken in commit order.
	ProductDelta int `json:"product_delta"`
	TestDelta    int `json:"test_delta"`
	Delta        int `json:"delta"`

	// Skipped: the source folder was absent at the bucket's *last* commit, so
	// the snapshot columns have nothing to report and render as dashes. Zero
	// and absent are different facts — that is why this field exists.
	Skipped bool `json:"skipped"`

	Records []report.Record `json:"records"`
}

// Last is the commit a bucket takes its identity from. A bucket only exists
// because a record opened it, so there is always one.
func (b Bucket) Last() report.Record { return b.Records[len(b.Records)-1] }

// Sink is what an Aggregator feeds. writer.Writer satisfies it structurally, so
// this package never imports writer and there is no cycle.
type Sink interface {
	Write(Bucket) error
	Close() error
}

// Aggregator is the granularity gate: records go in, buckets come out.
//
// It streams rather than buffering the walk. Records arrive oldest first, so a
// bucket is complete as soon as a record with a later start turns up, and the
// console keeps printing as the walk progresses — one bucket behind.
type Aggregator struct {
	gran Granularity
	sink Sink

	open *Bucket

	// The per-category deltas are differences against the previous record's
	// snapshot, running across bucket boundaries. Both start at zero —
	// including across Skipped commits, whose counts are zero — mirroring
	// Record.Finalize's prevTotal convention, which is what makes
	// ProductDelta + TestDelta == Delta hold.
	prevProduct, prevTest int
}

// NewAggregator returns an Aggregator feeding sink.
func NewAggregator(g Granularity, sink Sink) (*Aggregator, error) {
	// A bucket that does not tile the day would leave columns off the lattice
	// the graph's axis is drawn on, and they would vanish rather than misdraw.
	if !g.Valid() {
		return nil, fmt.Errorf("granularity: a %d-hour bucket does not divide the day", g)
	}
	return &Aggregator{gran: g, sink: sink}, nil
}

// Write folds one record into its bucket, flushing the previous one first.
//
// A sink error is returned as-is: the pipeline treats a failed write as fatal,
// which is the existing policy.
func (a *Aggregator) Write(r report.Record) error {
	start := a.gran.Truncate(r.Timestamp)

	// Roll over only on a *strictly later* start. The pipeline emits oldest
	// first, but `git log --reverse` follows the commit graph rather than
	// committer dates, so a record can in principle arrive with an earlier
	// timestamp. Folding it into the open bucket keeps the output well-formed;
	// opening a second bucket with a start already emitted would give the graph
	// two rows for one lattice slot, and its slot lookup would silently drop
	// one of them.
	if a.open != nil && start.After(a.open.Start) {
		if err := a.flush(); err != nil {
			return err
		}
	}
	if a.open == nil {
		a.open = &Bucket{Start: start, Gran: a.gran}
	}

	b := a.open
	b.ProductDelta += r.Product.Code - a.prevProduct
	b.TestDelta += r.Test.Code - a.prevTest
	b.Delta += r.Delta
	b.Commits++
	b.Records = append(b.Records, r)

	// The snapshot always describes the last commit seen, so the bucket
	// reports the tree as it stood when the slice of time ended.
	b.Product, b.Test, b.TotalCode, b.Skipped = r.Product, r.Test, r.TotalCode, r.Skipped

	a.prevProduct, a.prevTest = r.Product.Code, r.Test.Code
	return nil
}

// Close flushes the open bucket and then closes the sink *regardless* of
// whether that flush failed, so the close-exactly-once guarantee the pipeline
// relies on survives a broken sink. An empty run flushes nothing and still
// closes.
func (a *Aggregator) Close() error {
	flushErr := a.flush()
	closeErr := a.sink.Close()
	return errors.Join(flushErr, closeErr)
}

func (a *Aggregator) flush() error {
	if a.open == nil {
		return nil
	}
	b := *a.open
	a.open = nil
	return a.sink.Write(b)
}
