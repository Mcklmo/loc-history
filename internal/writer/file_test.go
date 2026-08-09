package writer

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mcklmo/loc-history/internal/report"
)

func writeAll(t *testing.T, w Writer, records ...report.Record) {
	t.Helper()
	for _, r := range records {
		if err := w.Write(r); err != nil {
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

func TestCSVWritesAHeaderThenOneRowPerCommit(t *testing.T) {
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
		t.Fatalf("got %d rows, want a header plus 2 commits", len(rows))
	}

	wantHeader := []string{"sha", "short", "timestamp", "author", "subject",
		"product_code", "test_code", "total_code", "delta", "skipped"}
	if strings.Join(rows[0], ",") != strings.Join(wantHeader, ",") {
		t.Errorf("header = %v, want %v", rows[0], wantHeader)
	}

	want := []string{
		"08ab753" + strings.Repeat("0", 33), "08ab753", "2026-08-06T12:00:00Z",
		"mcklmo", "first commit", "412", "0", "412", "412", "false",
	}
	if strings.Join(rows[1], ",") != strings.Join(want, ",") {
		t.Errorf("row = %v\nwant %v", rows[1], want)
	}
	if rows[2][8] != "176" {
		t.Errorf("delta = %q, want 176", rows[2][8])
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
	if rows[1][4] != hostile {
		t.Errorf("subject = %q, want %q", rows[1][4], hostile)
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
	if rows[1][8] != "-400" {
		t.Errorf("delta = %q, want -400", rows[1][8])
	}
}

// Without this column a CSV reader cannot tell an absent folder from an
// empty one, and both look like zero.
func TestCSVMarksSkippedCommits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.csv")
	w, err := NewFile(path, FormatCSV)
	if err != nil {
		t.Fatal(err)
	}
	r := rec(6, "abc1234", "chore: init", 0, 0, 0)
	r.Skipped = true
	writeAll(t, w, r)

	rows, _ := csv.NewReader(strings.NewReader(readFile(t, path))).ReadAll()
	if rows[1][9] != "true" {
		t.Errorf("skipped = %q, want true", rows[1][9])
	}
}

func TestCSVWithNoRecordsStillHasItsHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.csv")
	w, err := NewFile(path, FormatCSV)
	if err != nil {
		t.Fatal(err)
	}
	writeAll(t, w)

	if got := readFile(t, path); !strings.HasPrefix(got, "sha,short,") {
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

	var got report.Record
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("line 0 is not valid JSON: %v", err)
	}
	if got.Short != "08ab753" || got.TotalCode != 412 || got.Delta != 412 {
		t.Errorf("decoded %+v, want the first record", got)
	}
	if !got.Timestamp.Equal(time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("timestamp = %s", got.Timestamp)
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

	var got report.Record
	if err := json.Unmarshal([]byte(strings.TrimSpace(readFile(t, path))), &got); err != nil {
		t.Fatal(err)
	}
	if got.Product.Files != 3 || got.Product.Comment != 9 || got.Test.Blank != 2 {
		t.Errorf("counts did not survive: %+v", got)
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
	for i := range 500 {
		if err := w.Write(rec(6, "abc1234", strings.Repeat("x", i%40+1), i, 0, 0)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	rows, err := csv.NewReader(strings.NewReader(readFile(t, path))).ReadAll()
	if err != nil {
		t.Fatalf("output is not valid CSV: %v", err)
	}
	if len(rows) != 501 {
		t.Errorf("got %d rows, want 501; buffered rows were lost", len(rows))
	}
}
