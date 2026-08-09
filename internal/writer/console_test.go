package writer

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mcklmo/loc-history/internal/report"
)

func rec(day int, short, subject string, product, test, prevTotal int) report.Record {
	r := report.Record{
		SHA:       short + strings.Repeat("0", 33),
		Short:     short,
		Timestamp: time.Date(2026, 8, day, 12, 0, 0, 0, time.UTC),
		Author:    "mcklmo",
		Subject:   subject,
		Product:   report.Count{Code: product},
		Test:      report.Count{Code: test},
	}
	r.Finalize(prevTotal)
	return r
}

func TestConsoleWritesHeaderThenAlignedRows(t *testing.T) {
	var sb strings.Builder
	c := NewConsole(&sb)

	if err := c.Write(rec(6, "08ab753", "first commit", 412, 0, 0)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := c.Write(rec(6, "d251527", "git add init", 488, 0, 412)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := c.Write(rec(7, "9a9dab4", "refactor: extract ActivityRow", 3120, 1804, 4643)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	want := strings.Join([]string{
		"DATE        SHA      PRODUCT    TEST     TOTAL       Δ  SUBJECT",
		"2026-08-06  08ab753      412       0       412    +412  first commit",
		"2026-08-06  d251527      488       0       488     +76  git add init",
		"2026-08-07  9a9dab4     3120    1804      4924    +281  refactor: extract ActivityRow",
		"",
	}, "\n")

	if got := sb.String(); got != want {
		t.Errorf("console output mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestConsoleRendersNetDeletionWithMinusSign(t *testing.T) {
	var sb strings.Builder
	c := NewConsole(&sb)
	if err := c.Write(rec(8, "abc1234", "refactor: delete dead code", 900, 100, 1500)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	if !strings.Contains(sb.String(), "-500") {
		t.Errorf("a net deletion must render as -500, got:\n%s", sb.String())
	}
}

// A skipped commit is not the same as a commit whose source folder is empty;
// dashes keep the two distinguishable at a glance.
func TestConsoleMarksSkippedCommitsWithDashes(t *testing.T) {
	var sb strings.Builder
	c := NewConsole(&sb)
	r := rec(5, "0000001", "chore: repo init", 0, 0, 0)
	r.Skipped = true
	if err := c.Write(r); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	line := strings.Split(sb.String(), "\n")[1]
	want := "2026-08-05  0000001        -       -         -      +0  chore: repo init"
	if line != want {
		t.Errorf("skipped row =\n%q\nwant\n%q", line, want)
	}
}

func TestConsoleTruncatesLongSubjects(t *testing.T) {
	var sb strings.Builder
	c := NewConsole(&sb)
	long := "feat: " + strings.Repeat("very long subject ", 10)
	if err := c.Write(rec(6, "abc1234", long, 10, 0, 0)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	line := strings.Split(sb.String(), "\n")[1]
	subject := line[strings.LastIndex(line, "  ")+2:]
	if len([]rune(subject)) != subjectWidth {
		t.Errorf("subject rendered %d runes, want %d: %q", len([]rune(subject)), subjectWidth, subject)
	}
	if !strings.HasSuffix(subject, "…") {
		t.Errorf("truncated subject %q should end in an ellipsis", subject)
	}
}

func TestConsoleWritesNothingWhenNoRecords(t *testing.T) {
	var sb strings.Builder
	c := NewConsole(&sb)
	if err := c.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if sb.String() != "" {
		t.Errorf("a run with no records printed %q, want nothing", sb.String())
	}
}

type failingWriter struct{ err error }

func (f failingWriter) Write([]byte) (int, error) { return 0, f.err }

func TestConsoleSurfacesUnderlyingWriteErrors(t *testing.T) {
	boom := errors.New("broken pipe")
	c := NewConsole(failingWriter{boom})

	if err := c.Write(rec(6, "abc1234", "x", 1, 0, 0)); !errors.Is(err, boom) {
		t.Errorf("Write() error = %v, want it to wrap %v", err, boom)
	}
}
