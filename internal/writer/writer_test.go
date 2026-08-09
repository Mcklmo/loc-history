package writer

import (
	"errors"
	"strings"
	"testing"

	"github.com/mcklmo/loc-history/internal/report"
)

// recorder is a Writer that remembers what it was given and can be told to fail.
type recorder struct {
	records  []report.Record
	closed   int
	writeErr error
	closeErr error
}

func (r *recorder) Write(rec report.Record) error {
	r.records = append(r.records, rec)
	return r.writeErr
}

func (r *recorder) Close() error {
	r.closed++
	return r.closeErr
}

func TestMultiWriterFansOutToEverySink(t *testing.T) {
	a, b, c := &recorder{}, &recorder{}, &recorder{}
	mw := MultiWriter(a, b, c)

	rec := report.Record{SHA: "abc", Short: "abc"}
	if err := mw.Write(rec); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	for i, r := range []*recorder{a, b, c} {
		if len(r.records) != 1 || r.records[0].SHA != "abc" {
			t.Errorf("sink %d got %+v, want one record with SHA abc", i, r.records)
		}
	}
}

func TestMultiWriterWriteReachesLaterSinksDespiteEarlierError(t *testing.T) {
	boom := errors.New("boom")
	bad, good := &recorder{writeErr: boom}, &recorder{}
	mw := MultiWriter(bad, good)

	err := mw.Write(report.Record{SHA: "abc"})

	if !errors.Is(err, boom) {
		t.Errorf("Write() error = %v, want it to wrap %v", err, boom)
	}
	if len(good.records) != 1 {
		t.Error("a failing sink stopped a healthy later sink from receiving the record")
	}
}

func TestMultiWriterWriteReturnsFirstError(t *testing.T) {
	first, second := errors.New("first"), errors.New("second")
	mw := MultiWriter(&recorder{writeErr: first}, &recorder{writeErr: second})

	err := mw.Write(report.Record{})

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
	if err := mw.Write(report.Record{}); err != nil {
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
