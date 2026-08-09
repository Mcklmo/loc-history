package writer

import (
	"errors"
	"strings"
	"testing"

	"github.com/mcklmo/loc-history/internal/bucket"
	"github.com/mcklmo/loc-history/internal/report"
)

// recorder is a Writer that remembers what it was given and can be told to fail.
type recorder struct {
	buckets  []bucket.Bucket
	closed   int
	writeErr error
	closeErr error
}

func (r *recorder) Write(b bucket.Bucket) error {
	r.buckets = append(r.buckets, b)
	return r.writeErr
}

func (r *recorder) Close() error {
	r.closed++
	return r.closeErr
}

// bucketsOf runs records through an aggregator, which is what the pipeline does
// before any sink sees anything. Every sink test starts from records for that
// reason: a hand-built bucket could hold a combination the gate never produces.
func bucketsOf(t *testing.T, gran bucket.Granularity, records ...report.Record) []bucket.Bucket {
	t.Helper()
	out := &recorder{}
	agg, err := bucket.NewAggregator(gran, out)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range records {
		if err := agg.Write(r); err != nil {
			t.Fatalf("aggregate %s: %v", r.Short, err)
		}
	}
	if err := agg.Close(); err != nil {
		t.Fatal(err)
	}
	return out.buckets
}

func TestMultiWriterFansOutToEverySink(t *testing.T) {
	a, b, c := &recorder{}, &recorder{}, &recorder{}
	mw := MultiWriter(a, b, c)

	if err := mw.Write(bucketsOf(t, bucket.GranularityHour, rec(6, "abc", "x", 1, 0, 0))[0]); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	for i, r := range []*recorder{a, b, c} {
		if len(r.buckets) != 1 || r.buckets[0].Last().Short != "abc" {
			t.Errorf("sink %d got %+v, want one bucket holding commit abc", i, r.buckets)
		}
	}
}

func TestMultiWriterWriteReachesLaterSinksDespiteEarlierError(t *testing.T) {
	boom := errors.New("boom")
	bad, good := &recorder{writeErr: boom}, &recorder{}
	mw := MultiWriter(bad, good)

	err := mw.Write(bucket.Bucket{})

	if !errors.Is(err, boom) {
		t.Errorf("Write() error = %v, want it to wrap %v", err, boom)
	}
	if len(good.buckets) != 1 {
		t.Error("a failing sink stopped a healthy later sink from receiving the bucket")
	}
}

func TestMultiWriterWriteReturnsFirstError(t *testing.T) {
	first, second := errors.New("first"), errors.New("second")
	mw := MultiWriter(&recorder{writeErr: first}, &recorder{writeErr: second})

	err := mw.Write(bucket.Bucket{})

	if !errors.Is(err, first) {
		t.Errorf("Write() error = %v, want the first error %v", err, first)
	}
}

// A broken file sink must not leak the graph sink's file handle.
func TestMultiWriterCloseClosesEverySinkAndJoinsErrors(t *testing.T) {
	e1, e3 := errors.New("e1"), errors.New("e3")
	a, b, c := &recorder{closeErr: e1}, &recorder{}, &recorder{closeErr: e3}
	mw := MultiWriter(a, b, c)

	err := mw.Close()

	for i, r := range []*recorder{a, b, c} {
		if r.closed != 1 {
			t.Errorf("sink %d closed %d times, want exactly 1", i, r.closed)
		}
	}
	if !errors.Is(err, e1) || !errors.Is(err, e3) {
		t.Errorf("Close() error = %v, want it to join both %v and %v", err, e1, e3)
	}
}

func TestMultiWriterCloseCleanReturnsNil(t *testing.T) {
	if err := MultiWriter(&recorder{}, &recorder{}).Close(); err != nil {
		t.Errorf("Close() error = %v, want nil", err)
	}
}

func TestMultiWriterWithNoSinksIsInert(t *testing.T) {
	mw := MultiWriter()
	if err := mw.Write(bucket.Bucket{}); err != nil {
		t.Errorf("Write() error = %v, want nil", err)
	}
	if err := mw.Close(); err != nil {
		t.Errorf("Close() error = %v, want nil", err)
	}
}

// A single sink should not be wrapped in ceremony; it is returned as-is.
func TestMultiWriterWithOneSinkReturnsIt(t *testing.T) {
	a := &recorder{}
	if got := MultiWriter(a); got != Writer(a) {
		t.Errorf("MultiWriter(one) = %T, want the sink itself", got)
	}
}

func TestWriterInterfaceIsSatisfiedByShippedSinks(t *testing.T) {
	var _ Writer = (*Console)(nil)
	var sb strings.Builder
	var _ Writer = NewConsole(&sb)
}
