package bucket

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mcklmo/loc-history/internal/report"
)

// sink collects what an Aggregator hands it and can be told to fail.
type sink struct {
	buckets  []Bucket
	closed   int
	writeErr error
	closeErr error
}

func (s *sink) Write(b Bucket) error {
	s.buckets = append(s.buckets, b)
	return s.writeErr
}

func (s *sink) Close() error {
	s.closed++
	return s.closeErr
}

// aggregate runs records through an Aggregator, which is what the pipeline does
// before any sink sees anything.
func aggregate(t *testing.T, g Granularity, records ...report.Record) *sink {
	t.Helper()
	out := &sink{}
	a, err := NewAggregator(g, out)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range records {
		if err := a.Write(r); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}
	if err := a.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return out
}

// commitsAt builds a growing history landing at the given times, so a test can
// say when the commits happened and ignore what they contained.
func commitsAt(t *testing.T, at ...time.Time) []report.Record {
	t.Helper()
	var out []report.Record
	prev := 0
	for i, ts := range at {
		rec := report.Record{
			Short:     fmt.Sprintf("c%06d", i),
			Timestamp: ts,
			Subject:   "feat: work",
			Product:   report.Count{Files: 1, Code: 100 * (i + 1)},
		}
		rec.Finalize(prev)
		prev = rec.TotalCode
		out = append(out, rec)
	}
	return out
}

// history is a fixed run: a commit before the source folder existed, growth, a
// refactor that deletes more than it adds, and a documentation-only commit.
func history() []report.Record {
	type row struct {
		day     int
		short   string
		subject string
		product int
		test    int
		skipped bool
	}
	rows := []row{
		{3, "08ab753", "chore: repo init", 0, 0, true},
		{3, "d251527", "feat: first module", 412, 0, false},
		{4, "9a9dab4", "feat: add the panel", 620, 140, false},
		{4, "1c2b3a4", "test: cover the panel", 620, 380, false},
		{6, "5e6f7a8", "refactor: collapse three modules into one", 300, 380, false},
		{9, "b1c2d3e", "docs: comments only", 300, 380, false},
		{11, "f4a5b6c", "feat: the big one", 1400, 900, false},
		{17, "0d1e2f3", "fix: trim dead code", 1310, 900, false},
	}

	var out []report.Record
	prev := 0
	for _, r := range rows {
		rec := report.Record{
			SHA:       r.short + strings.Repeat("0", 33),
			Short:     r.short,
			Timestamp: time.Date(2026, 3, r.day, 14, 30, 0, 0, time.UTC),
			Author:    "mcklmo",
			Subject:   r.subject,
			Product:   report.Count{Files: 3, Code: r.product},
			Test:      report.Count{Files: 2, Code: r.test},
			Skipped:   r.skipped,
		}
		rec.Finalize(prev)
		prev = rec.TotalCode
		out = append(out, rec)
	}
	return out
}

// The point of the hour bucket: an afternoon of work is several buckets, not
// one.
func TestHourlyBucketingSplitsWithinOneDay(t *testing.T) {
	records := commitsAt(t,
		time.Date(2026, 3, 3, 9, 15, 0, 0, time.UTC),
		time.Date(2026, 3, 3, 9, 45, 0, 0, time.UTC),
		time.Date(2026, 3, 3, 11, 20, 0, 0, time.UTC),
	)

	if got := aggregate(t, GranularityHour, records...).buckets; len(got) != 2 {
		t.Errorf("got %d hourly buckets, want 2 (09:00 and 11:00)", len(got))
	}
	if got := aggregate(t, GranularityDay, records...).buckets; len(got) != 1 {
		t.Errorf("got %d daily buckets, want 1", len(got))
	}
}

// The whole point of a wider bucket: commits an hour apart merge into one.
func TestAMultiHourBucketMergesTheCommitsInsideIt(t *testing.T) {
	records := commitsAt(t,
		time.Date(2026, 3, 3, 9, 15, 0, 0, time.UTC),
		time.Date(2026, 3, 3, 10, 40, 0, 0, time.UTC),
		time.Date(2026, 3, 3, 13, 5, 0, 0, time.UTC),
	)

	got := aggregate(t, 4, records...).buckets
	if len(got) != 2 {
		t.Fatalf("got %d 4-hour buckets, want 2 (08:00 and 12:00)", len(got))
	}
	if got[0].Commits != 2 || got[1].Commits != 1 {
		t.Errorf("commit counts = %d and %d, want 2 and 1", got[0].Commits, got[1].Commits)
	}
	if n := len(aggregate(t, GranularityHour, records...).buckets); n != 3 {
		t.Errorf("got %d hourly buckets, want 3", n)
	}
}

// The charts and the tables are views of the same numbers; this is the
// invariant that keeps them from disagreeing.
func TestBucketDeltasSumToTheRecordDelta(t *testing.T) {
	records := history()

	prevProduct, prevTest := 0, 0
	for _, r := range records {
		productDelta := r.Product.Code - prevProduct
		testDelta := r.Test.Code - prevTest
		if productDelta+testDelta != r.Delta {
			t.Errorf("%s: product %+d + test %+d = %+d, want Delta %+d",
				r.Short, productDelta, testDelta, productDelta+testDelta, r.Delta)
		}
		prevProduct, prevTest = r.Product.Code, r.Test.Code
	}

	for _, gran := range []Granularity{GranularityHour, 4, GranularityDay} {
		t.Run(gran.Noun(), func(t *testing.T) {
			for _, b := range aggregate(t, gran, records...).buckets {
				if b.ProductDelta+b.TestDelta != b.Delta {
					t.Errorf("%s: product %+d + test %+d = %+d, want delta %+d",
						b.Start, b.ProductDelta, b.TestDelta, b.ProductDelta+b.TestDelta, b.Delta)
				}
			}
		})
	}
}

// Streaming is the whole design: the console prints as the walk progresses
// rather than waiting for it to end. A bucket is therefore handed over as soon
// as — but not before — a later record proves nothing else can join it.
func TestBucketsReachTheSinkOnlyOnceNoLaterRecordCanJoinThem(t *testing.T) {
	out := &sink{}
	a, err := NewAggregator(GranularityHour, out)
	if err != nil {
		t.Fatal(err)
	}

	records := commitsAt(t,
		time.Date(2026, 3, 3, 9, 15, 0, 0, time.UTC),
		time.Date(2026, 3, 3, 9, 45, 0, 0, time.UTC),
		time.Date(2026, 3, 3, 11, 20, 0, 0, time.UTC),
	)

	for i, r := range records {
		if err := a.Write(r); err != nil {
			t.Fatal(err)
		}
		// Nothing is emitted until the third record opens a later bucket.
		want := 0
		if i == 2 {
			want = 1
		}
		if len(out.buckets) != want {
			t.Fatalf("after record %d the sink holds %d buckets, want %d", i, len(out.buckets), want)
		}
	}

	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if len(out.buckets) != 2 {
		t.Fatalf("got %d buckets, want the open one flushed on Close", len(out.buckets))
	}
	if !out.buckets[0].Start.Before(out.buckets[1].Start) {
		t.Errorf("buckets arrived out of order: %s then %s", out.buckets[0].Start, out.buckets[1].Start)
	}
}

// git log --reverse follows the commit graph, not committer dates, so a record
// can land earlier than the one before it. Merging it into the open bucket
// keeps one bucket per slot; a second bucket with an already-emitted start
// would be looked up once and silently drop one of the two.
func TestARecordLandingBeforeTheOpenBucketFoldsIntoIt(t *testing.T) {
	records := commitsAt(t,
		time.Date(2026, 3, 3, 11, 20, 0, 0, time.UTC),
		time.Date(2026, 3, 3, 9, 15, 0, 0, time.UTC),
		time.Date(2026, 3, 3, 11, 40, 0, 0, time.UTC),
	)

	got := aggregate(t, GranularityHour, records...).buckets
	if len(got) != 1 {
		t.Fatalf("got %d buckets, want 1 — the late record must not open a duplicate", len(got))
	}
	if got[0].Commits != 3 {
		t.Errorf("bucket holds %d commits, want all 3", got[0].Commits)
	}
	if want := time.Date(2026, 3, 3, 11, 0, 0, 0, time.UTC); !got[0].Start.Equal(want) {
		t.Errorf("bucket start = %s, want the open bucket's %s", got[0].Start, want)
	}
}

// The bucket takes its identity and its snapshot from its last commit: the row
// says how big the tree was when the slice of time ended.
func TestABucketDescribesItsLastCommit(t *testing.T) {
	got := aggregate(t, GranularityDay, history()...).buckets
	if len(got) != 6 {
		t.Fatalf("got %d daily buckets, want 6", len(got))
	}

	first := got[0]
	if first.Last().Short != "d251527" {
		t.Errorf("last commit = %s, want d251527", first.Last().Short)
	}
	if first.Product.Code != 412 || first.TotalCode != 412 {
		t.Errorf("snapshot = %d product / %d total, want the last commit's 412/412",
			first.Product.Code, first.TotalCode)
	}
	// The bucket opened on a skipped commit but did not end on one.
	if first.Skipped {
		t.Error("bucket reported as skipped although its last commit counted a real tree")
	}
	if first.Commits != 2 {
		t.Errorf("Commits = %d, want 2", first.Commits)
	}
}

// A bucket whose last commit predates the source folder has nothing to report,
// and the sinks render dashes rather than a zero.
func TestABucketEndingOnASkippedCommitIsSkipped(t *testing.T) {
	r := report.Record{
		Short:     "0000001",
		Timestamp: time.Date(2026, 3, 3, 9, 0, 0, 0, time.UTC),
		Subject:   "chore: repo init",
		Skipped:   true,
	}
	r.Finalize(0)

	got := aggregate(t, GranularityHour, r).buckets
	if len(got) != 1 || !got[0].Skipped {
		t.Errorf("got %+v, want one bucket marked skipped", got)
	}
}

func TestAnEmptyRunEmitsNoBucketsAndStillClosesTheSink(t *testing.T) {
	out := aggregate(t, GranularityHour)
	if len(out.buckets) != 0 {
		t.Errorf("got %d buckets from an empty run, want none", len(out.buckets))
	}
	if out.closed != 1 {
		t.Errorf("Close called %d times, want exactly 1", out.closed)
	}
}

// An aborted run still has to leave a partial artifact, so the sink is closed
// even when the final flush fails.
func TestCloseClosesTheSinkOnceAndJoinsAFailedFlush(t *testing.T) {
	boom, shut := errors.New("disk full"), errors.New("bad close")
	out := &sink{writeErr: boom, closeErr: shut}
	a, err := NewAggregator(GranularityHour, out)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Write(commitsAt(t, time.Date(2026, 3, 3, 9, 0, 0, 0, time.UTC))[0]); err != nil {
		t.Fatalf("Write() error = %v, want the failure to surface on the flush", err)
	}

	err = a.Close()

	if !errors.Is(err, boom) || !errors.Is(err, shut) {
		t.Errorf("Close() error = %v, want it to join %v and %v", err, boom, shut)
	}
	if out.closed != 1 {
		t.Errorf("Close called %d times, want exactly 1", out.closed)
	}
}

// A write failure is fatal upstream, so it must reach the pipeline unswallowed.
func TestWriteSurfacesASinkFailure(t *testing.T) {
	boom := errors.New("disk full")
	out := &sink{writeErr: boom}
	a, err := NewAggregator(GranularityHour, out)
	if err != nil {
		t.Fatal(err)
	}

	records := commitsAt(t,
		time.Date(2026, 3, 3, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 3, 10, 0, 0, 0, time.UTC),
	)
	if err := a.Write(records[0]); err != nil {
		t.Fatalf("Write() error = %v; nothing has been flushed yet", err)
	}
	if err := a.Write(records[1]); !errors.Is(err, boom) {
		t.Errorf("Write() error = %v, want it to wrap %v", err, boom)
	}
}

// A library caller can name a bucket the flag layer would have refused.
func TestNewAggregatorRejectsABucketThatDoesNotTileADay(t *testing.T) {
	if _, err := NewAggregator(5, &sink{}); err == nil {
		t.Fatal("NewAggregator() accepted a 5-hour bucket, whose columns would fall between slots")
	} else if !strings.Contains(err.Error(), "divide the day") {
		t.Errorf("error %q should say why 5 hours is refused", err)
	}
}

// Every bucket carries the width it was cut at, which is how the sinks below
// know what to call it without being told separately.
func TestBucketsCarryTheirOwnGranularity(t *testing.T) {
	for _, gran := range []Granularity{GranularityHour, 4, GranularityDay} {
		for _, b := range aggregate(t, gran, history()...).buckets {
			if b.Gran != gran {
				t.Errorf("bucket at %s reports %d hours, want %d", b.Start, b.Gran, gran)
			}
		}
	}
}
