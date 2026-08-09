package writer

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mcklmo/loc-history/internal/bucket"
	"github.com/mcklmo/loc-history/internal/report"
)

// writeAll streams records through the granularity gate into w, at the default
// hourly width unless a test says otherwise.
func writeAll(t *testing.T, w Writer, records ...report.Record) {
	t.Helper()
	writeAllAt(t, w, bucket.GranularityHour, records...)
}

func writeAllAt(t *testing.T, w Writer, gran bucket.Granularity, records ...report.Record) {
	t.Helper()
	for _, b := range bucketsOf(t, gran, records...) {
		if err := w.Write(b); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseFormat(t *testing.T) {
	tests := []struct {
		in      string
		want    Format
		wantErr bool
	}{
		{"csv", FormatCSV, false},
		{"ndjson", FormatNDJSON, false},
		{"CSV", FormatCSV, false},
		{"", 0, true},
		{"yaml", 0, true},
	}
	for _, tt := range tests {
		got, err := ParseFormat(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParseFormat(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			continue
		}
		if err == nil && got != tt.want {
			t.Errorf("ParseFormat(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestParseFormatErrorNamesTheAlternatives(t *testing.T) {
	_, err := ParseFormat("yaml")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"csv", "ndjson"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should offer %q", err, want)
		}
	}
}

func TestCSVWritesAHeaderThenOneRowPerBucket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.csv")
	w, err := NewFile(path, FormatCSV)
	if err != nil {
		t.Fatal(err)
	}
	writeAll(t, w,
		rec(6, "08ab753", "first commit", 412, 0, 0),
		rec(7, "d251527", "git add init", 488, 100, 412),
	)

	rows, err := csv.NewReader(strings.NewReader(readFile(t, path))).ReadAll()
	if err != nil {
		t.Fatalf("output is not valid CSV: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want a header plus 2 buckets", len(rows))
	}

	wantHeader := []string{
		"bucket_start", "commits",
		"last_sha", "last_short", "last_author", "last_subject",
		"product_code", "test_code", "total_code",
		"product_delta", "test_delta", "delta", "skipped",
	}
	if strings.Join(rows[0], ",") != strings.Join(wantHeader, ",") {
		t.Errorf("header = %v, want %v", rows[0], wantHeader)
	}

	want := []string{
		"2026-08-06T12:00:00Z", "1",
		"08ab753" + strings.Repeat("0", 33), "08ab753", "mcklmo", "first commit",
		"412", "0", "412", "412", "0", "412", "false",
	}
	if strings.Join(rows[1], ",") != strings.Join(want, ",") {
		t.Errorf("row = %v\nwant %v", rows[1], want)
	}
	// The two category deltas are what let a reader reproduce the charts.
	if rows[2][9] != "76" || rows[2][10] != "100" || rows[2][11] != "176" {
		t.Errorf("second row deltas = product %q, test %q, total %q; want 76, 100, 176",
			rows[2][9], rows[2][10], rows[2][11])
	}
}

// Commits sharing a slice of time are one row, and the row says how many.
func TestCSVCollapsesABucketToOneRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.csv")
	w, err := NewFile(path, FormatCSV)
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC)
	writeAll(t, w,
		recAt(day.Add(9*time.Hour), "08ab753", "feat: first", 100, 0, 0),
		recAt(day.Add(9*time.Hour+30*time.Minute), "d251527", "test: cover it", 100, 80, 100),
	)

	rows, err := csv.NewReader(strings.NewReader(readFile(t, path))).ReadAll()
	if err != nil {
		t.Fatalf("output is not valid CSV: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want a header plus 1 bucket", len(rows))
	}
	want := []string{
		"2026-08-06T09:00:00Z", "2",
		"d251527" + strings.Repeat("0", 33), "d251527", "mcklmo", "test: cover it",
		"100", "80", "180", "100", "80", "180", "false",
	}
	if strings.Join(rows[1], ",") != strings.Join(want, ",") {
		t.Errorf("row = %v\nwant %v", rows[1], want)
	}
}

// A subject full of commas and quotes is exactly what CSV quoting is for.
func TestCSVQuotesHostileSubjects(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.csv")
	w, err := NewFile(path, FormatCSV)
	if err != nil {
		t.Fatal(err)
	}
	hostile := `fix: a,b "quoted", and more`
	writeAll(t, w, rec(6, "abc1234", hostile, 10, 0, 0))

	rows, err := csv.NewReader(strings.NewReader(readFile(t, path))).ReadAll()
	if err != nil {
		t.Fatalf("output is not valid CSV: %v", err)
	}
	if rows[1][5] != hostile {
		t.Errorf("last_subject = %q, want %q", rows[1][5], hostile)
	}
}

func TestCSVRendersNegativeDeltas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.csv")
	w, err := NewFile(path, FormatCSV)
	if err != nil {
		t.Fatal(err)
	}
	writeAll(t, w, rec(6, "abc1234", "refactor", 100, 0, 500))

	rows, _ := csv.NewReader(strings.NewReader(readFile(t, path))).ReadAll()
	if rows[1][11] != "-400" {
		t.Errorf("delta = %q, want -400", rows[1][11])
	}
}

// Without this column a CSV reader cannot tell an absent folder from an
// empty one, and both look like zero.
func TestCSVMarksSkippedBuckets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.csv")
	w, err := NewFile(path, FormatCSV)
	if err != nil {
		t.Fatal(err)
	}
	r := rec(6, "abc1234", "chore: init", 0, 0, 0)
	r.Skipped = true
	writeAll(t, w, r)

	rows, _ := csv.NewReader(strings.NewReader(readFile(t, path))).ReadAll()
	if rows[1][12] != "true" {
		t.Errorf("skipped = %q, want true", rows[1][12])
	}
}

func TestCSVWithNoRecordsStillHasItsHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.csv")
	w, err := NewFile(path, FormatCSV)
	if err != nil {
		t.Fatal(err)
	}
	writeAll(t, w)

	if got := readFile(t, path); !strings.HasPrefix(got, "bucket_start,commits,") {
		t.Errorf("empty run produced %q, want a header row", got)
	}
}

func TestNDJSONEmitsOneObjectPerLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.ndjson")
	w, err := NewFile(path, FormatNDJSON)
	if err != nil {
		t.Fatal(err)
	}
	writeAll(t, w,
		rec(6, "08ab753", "first commit", 412, 0, 0),
		rec(7, "d251527", "git add init", 488, 100, 412),
	)

	lines := strings.Split(strings.TrimSuffix(readFile(t, path), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}

	var got bucket.Bucket
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("line 0 is not valid JSON: %v", err)
	}
	if got.Last().Short != "08ab753" || got.TotalCode != 412 || got.Delta != 412 {
		t.Errorf("decoded %+v, want the first bucket", got)
	}
	if !got.Start.Equal(time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("bucket start = %s", got.Start)
	}
	if got.Gran != bucket.GranularityHour {
		t.Errorf("bucket_hours = %d, want the width it was cut at", got.Gran)
	}
}

// NDJSON stays lossless at any granularity: the records nest inside their
// bucket, so nothing a wider bucket merges is actually thrown away.
func TestNDJSONNestsEveryRecordOfTheBucket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.ndjson")
	w, err := NewFile(path, FormatNDJSON)
	if err != nil {
		t.Fatal(err)
	}
	writeAllAt(t, w, bucket.GranularityDay,
		rec(6, "08ab753", "first commit", 412, 0, 0),
		rec(6, "d251527", "git add init", 488, 100, 412),
	)

	lines := strings.Split(strings.TrimSuffix(readFile(t, path), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want the day to be one object", len(lines))
	}

	var got bucket.Bucket
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Records) != 2 {
		t.Fatalf("bucket nests %d records, want both commits", len(got.Records))
	}
	for i, want := range []string{"08ab753", "d251527"} {
		if got.Records[i].Short != want {
			t.Errorf("record %d = %s, want %s", i, got.Records[i].Short, want)
		}
	}
	if got.ProductDelta+got.TestDelta != got.Delta {
		t.Errorf("product %+d + test %+d != delta %+d", got.ProductDelta, got.TestDelta, got.Delta)
	}
}

// The full Count structs survive, so a jq user can reach files and comments
// even though the CSV projection drops them.
func TestNDJSONKeepsTheFullCounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.ndjson")
	w, err := NewFile(path, FormatNDJSON)
	if err != nil {
		t.Fatal(err)
	}
	r := rec(6, "abc1234", "x", 100, 20, 0)
	r.Product = report.Count{Files: 3, Code: 100, Comment: 9, Blank: 4}
	r.Test = report.Count{Files: 1, Code: 20, Comment: 1, Blank: 2}
	writeAll(t, w, r)

	var got bucket.Bucket
	if err := json.Unmarshal([]byte(strings.TrimSpace(readFile(t, path))), &got); err != nil {
		t.Fatal(err)
	}
	if got.Product.Files != 3 || got.Product.Comment != 9 || got.Test.Blank != 2 {
		t.Errorf("bucket counts did not survive: %+v", got)
	}
	if rec := got.Last(); rec.Product.Comment != 9 || rec.Test.Blank != 2 {
		t.Errorf("nested record counts did not survive: %+v", rec)
	}
}

func TestNewFileCreatesMissingParentDirectories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reports", "2026", "out.csv")
	w, err := NewFile(path, FormatCSV)
	if err != nil {
		t.Fatalf("NewFile() error = %v", err)
	}
	writeAll(t, w, rec(6, "abc1234", "x", 1, 0, 0))

	if _, err := os.Stat(path); err != nil {
		t.Errorf("file was not created: %v", err)
	}
}

func TestNewFileReportsAnUnwritablePath(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := NewFile(filepath.Join(blocker, "out.csv"), FormatCSV); err == nil {
		t.Error("NewFile() accepted a path underneath a regular file")
	}
}

// Buffered rows must reach disk, so Close cannot be a no-op here.
func TestCloseFlushesAndReleasesTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.csv")
	w, err := NewFile(path, FormatCSV)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC)
	var records []report.Record
	for i := range 500 {
		// An hour apart, so this is 500 buckets rather than one fat one.
		records = append(records,
			recAt(start.Add(time.Duration(i)*time.Hour), "abc1234", strings.Repeat("x", i%40+1), i, 0, 0))
	}
	writeAll(t, w, records...)

	rows, err := csv.NewReader(strings.NewReader(readFile(t, path))).ReadAll()
	if err != nil {
		t.Fatalf("output is not valid CSV: %v", err)
	}
	if len(rows) != 501 {
		t.Errorf("got %d rows, want 501; buffered rows were lost", len(rows))
	}
}
