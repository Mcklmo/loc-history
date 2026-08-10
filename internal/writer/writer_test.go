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
	buckets    []bucket.Bucket
	summaries  []report.AverageDelta
	closed     int
	writeErr   error
	summaryErr error
	closeErr   error
}

func (r *recorder) Write(b bucket.Bucket) error {
	r.buckets = append(r.buckets, b)
	return r.writeErr
}

func (r *recorder) Summary(avg report.AverageDelta) error {
	r.summaries = append(r.summaries, avg)
	return r.summaryErr
}

func (r *recorder) Close() error {
	r.closed++
	return r.closeErr
}

// runOf streams records through an aggregator, which is what the pipeline does
// before any sink sees anything, and keeps everything the sink was handed: the
// buckets and, when the run produced rows, the run-level summary. Every sink
// test starts from records for that reason: a hand-built bucket could hold a
// combination the gate never produces.
func runOf(t *testing.T, gran bucket.Granularity, records ...report.Record) *recorder {
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
	return out
}

func bucketsOf(t *testing.T, gran bucket.Granularity, records ...report.Record) []bucket.Bucket {
	t.Helper()
	return runOf(t, gran, records...).buckets
}

// replay hands w exactly what the aggregator handed the recorder, in the same
// order, so a sink test exercises the real sequence: buckets, then the summary,
// then Close.
func replay(t *testing.T, w Writer, run *recorder) {
	t.Helper()
	for _, b := range run.buckets {
		if err := w.Write(b); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}
	for _, avg := range run.summaries {
		if err := w.Summary(avg); err != nil {
			t.Fatalf("Summary() error = %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
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

func TestMultiWriterFansTheSummaryOutToEverySink(t *testing.T) {
	a, b, c := &recorder{}, &recorder{}, &recorder{}
	mw := MultiWriter(a, b, c)

	avg := report.AverageDelta{Product: 244, Test: 50, TotalCode: 294}
	if err := mw.Summary(avg); err != nil {
		t.Fatalf("Summary() error = %v", err)
	}

	for i, r := range []*recorder{a, b, c} {
		if len(r.summaries) != 1 || r.summaries[0] != avg {
			t.Errorf("sink %d got %+v, want one summary %+v", i, r.summaries, avg)
		}
	}
}

func TestMultiWriterSummaryReachesLaterSinksDespiteEarlierError(t *testing.T) {
	boom := errors.New("boom")
	bad, good := &recorder{summaryErr: boom}, &recorder{}
	mw := MultiWriter(bad, good)

	err := mw.Summary(report.AverageDelta{})

	if !errors.Is(err, boom) {
		t.Errorf("Summary() error = %v, want it to wrap %v", err, boom)
	}
	if len(good.summaries) != 1 {
		t.Error("a failing sink stopped a healthy later sink from receiving the summary")
	}
}

func TestMultiWriterSummaryReturnsFirstError(t *testing.T) {
	first, second := errors.New("first"), errors.New("second")
	mw := MultiWriter(&recorder{summaryErr: first}, &recorder{summaryErr: second})

	err := mw.Summary(report.AverageDelta{})

	if !errors.Is(err, first) {
		t.Errorf("Summary() error = %v, want the first error %v", err, first)
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
	if err := mw.Summary(report.AverageDelta{}); err != nil {
		t.Errorf("Summary() error = %v, want nil", err)
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
