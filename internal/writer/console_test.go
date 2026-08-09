package writer

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mcklmo/loc-history/internal/bucket"
	"github.com/mcklmo/loc-history/internal/report"
)

func rec(day int, short, subject string, product, test, prevTotal int) report.Record {
	return recAt(time.Date(2026, 8, day, 12, 0, 0, 0, time.UTC), short, subject, product, test, prevTotal)
}

// recAt pins the clock, for the tests that care which bucket a commit lands in.
func recAt(at time.Time, short, subject string, product, test, prevTotal int) report.Record {
	r := report.Record{
		SHA:       short + strings.Repeat("0", 33),
		Short:     short,
		Timestamp: at,
		Author:    "mcklmo",
		Subject:   subject,
		Product:   report.Count{Code: product},
		Test:      report.Count{Code: test},
	}
	r.Finalize(prevTotal)
	return r
}

// writeConsole streams records through the granularity gate into a Console.
func writeConsole(t *testing.T, gran bucket.Granularity, records ...report.Record) string {
	t.Helper()
	var sb strings.Builder
	c := NewConsole(&sb)
	for _, b := range bucketsOf(t, gran, records...) {
		if err := c.Write(b); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return sb.String()
}

func TestConsoleWritesHeaderThenAlignedRows(t *testing.T) {
	got := writeConsole(t, bucket.GranularityHour,
		rec(6, "08ab753", "first commit", 412, 0, 0),
		rec(7, "d251527", "git add init", 488, 0, 412),
		rec(8, "9a9dab4", "refactor: extract ActivityRow", 3120, 1804, 4643),
	)

	want := strings.Join([]string{
		"HOUR              SHA      COMMITS  PRODUCT    TEST     TOTAL       Δ  SUBJECT",
		"2026-08-06 12:00  08ab753        1      412       0       412    +412  first commit",
		"2026-08-07 12:00  d251527        1      488       0       488     +76  git add init",
		"2026-08-08 12:00  9a9dab4        1     3120    1804      4924    +281  refactor: extract ActivityRow",
		"",
	}, "\n")

	if got != want {
		t.Errorf("console output mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

// The header names the unit the run was actually bucketed by, which is why it
// is written lazily rather than at construction.
func TestConsoleHeaderNamesTheGranularity(t *testing.T) {
	for _, tt := range []struct {
		gran bucket.Granularity
		want string
	}{
		{bucket.GranularityHour, "HOUR"},
		{bucket.GranularityDay, "DATE"},
		{4, "BUCKET START"},
	} {
		t.Run(tt.gran.Noun(), func(t *testing.T) {
			got := writeConsole(t, tt.gran, rec(6, "08ab753", "first commit", 412, 0, 0))
			if header := strings.SplitN(got, "\n", 2)[0]; !strings.HasPrefix(header, tt.want) {
				t.Errorf("header = %q, want it to start with %q", header, tt.want)
			}
		})
	}
}

// One row per bucket, not per commit: the commits that share a slice of time
// merge, and the row keeps the last one's identity so it can still be found.
func TestConsoleCollapsesABucketToOneRow(t *testing.T) {
	day := time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC)
	got := writeConsole(t, bucket.GranularityHour,
		recAt(day.Add(9*time.Hour+15*time.Minute), "08ab753", "feat: first", 100, 0, 0),
		recAt(day.Add(9*time.Hour+50*time.Minute), "d251527", "test: cover it", 100, 80, 100),
		recAt(day.Add(11*time.Hour), "9a9dab4", "refactor: shrink", 40, 80, 180),
	)

	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want a header plus 2 buckets:\n%s", len(lines), got)
	}
	// 09:00 holds two commits, ends on d251527, and nets +180.
	want := "2026-08-06 09:00  d251527        2      100      80       180    +180  test: cover it"
	if lines[1] != want {
		t.Errorf("merged row =\n%q\nwant\n%q", lines[1], want)
	}
	if !strings.HasSuffix(lines[2], "refactor: shrink") || !strings.Contains(lines[2], "-60") {
		t.Errorf("second bucket = %q, want the shrinking commit with a negative delta", lines[2])
	}
}

func TestConsoleRendersNetDeletionWithMinusSign(t *testing.T) {
	got := writeConsole(t, bucket.GranularityHour,
		rec(8, "abc1234", "refactor: delete dead code", 900, 100, 1500))

	if !strings.Contains(got, "-500") {
		t.Errorf("a net deletion must render as -500, got:\n%s", got)
	}
}

// A bucket whose last commit predates the source folder is not the same as one
// whose folder is empty; dashes keep the two distinguishable at a glance.
func TestConsoleMarksSkippedBucketsWithDashes(t *testing.T) {
	r := rec(5, "0000001", "chore: repo init", 0, 0, 0)
	r.Skipped = true
	got := writeConsole(t, bucket.GranularityHour, r)

	line := strings.Split(got, "\n")[1]
	want := "2026-08-05 12:00  0000001        1        -       -         -      +0  chore: repo init"
	if line != want {
		t.Errorf("skipped row =\n%q\nwant\n%q", line, want)
	}
}

func TestConsoleTruncatesLongSubjects(t *testing.T) {
	long := "feat: " + strings.Repeat("very long subject ", 10)
	got := writeConsole(t, bucket.GranularityHour, rec(6, "abc1234", long, 10, 0, 0))

	line := strings.Split(got, "\n")[1]
	subject := line[strings.LastIndex(line, "  ")+2:]
	if len([]rune(subject)) != subjectWidth {
		t.Errorf("subject rendered %d runes, want %d: %q", len([]rune(subject)), subjectWidth, subject)
	}
	if !strings.HasSuffix(subject, "…") {
		t.Errorf("truncated subject %q should end in an ellipsis", subject)
	}
}

func TestConsoleWritesNothingWhenNoRecords(t *testing.T) {
	if got := writeConsole(t, bucket.GranularityHour); got != "" {
		t.Errorf("a run with no records printed %q, want nothing", got)
	}
}

type failingWriter struct{ err error }

func (f failingWriter) Write([]byte) (int, error) { return 0, f.err }

func TestConsoleSurfacesUnderlyingWriteErrors(t *testing.T) {
	boom := errors.New("broken pipe")
	c := NewConsole(failingWriter{boom})

	b := bucketsOf(t, bucket.GranularityHour, rec(6, "abc1234", "x", 1, 0, 0))[0]
	if err := c.Write(b); !errors.Is(err, boom) {
		t.Errorf("Write() error = %v, want it to wrap %v", err, boom)
	}
}
