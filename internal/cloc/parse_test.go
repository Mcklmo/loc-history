package cloc

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mcklmo/loc-history/internal/report"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return b
}

func TestParseReadsTheSumBlock(t *testing.T) {
	tests := []struct {
		file string
		want report.Count
	}{
		{"product.json", report.Count{Files: 2, Code: 5, Comment: 1, Blank: 1}},
		{"test.json", report.Count{Files: 2, Code: 5, Comment: 0, Blank: 1}},
		{"total.json", report.Count{Files: 4, Code: 10, Comment: 1, Blank: 2}},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			got, err := Parse(fixture(t, tt.file))
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if got.Count != tt.want {
				t.Errorf("Count = %+v, want %+v", got.Count, tt.want)
			}
		})
	}
}

// --not-match-f and --match-f over the same regex and the same tree are exact
// complements. If that ever stops holding, the product/test split is a lie.
func TestProductAndTestSumToTheUnfilteredTotal(t *testing.T) {
	product, err := Parse(fixture(t, "product.json"))
	if err != nil {
		t.Fatal(err)
	}
	test, err := Parse(fixture(t, "test.json"))
	if err != nil {
		t.Fatal(err)
	}
	total, err := Parse(fixture(t, "total.json"))
	if err != nil {
		t.Fatal(err)
	}

	if got := product.Count.Add(test.Count); got != total.Count {
		t.Errorf("product+test = %+v, want the unfiltered total %+v", got, total.Count)
	}
}

func TestParseReadsTheClocVersionForCacheKeying(t *testing.T) {
	got, err := Parse(fixture(t, "product.json"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got.Version != "1.98" {
		t.Errorf("Version = %q, want %q", got.Version, "1.98")
	}
}

// cloc emits a bare {} when nothing matched. That is a legitimate zero, not a
// parse failure — and not an empty stdout, as was originally assumed.
func TestParseTreatsEmptyObjectAsZero(t *testing.T) {
	got, err := Parse(fixture(t, "empty.json"))
	if err != nil {
		t.Fatalf("Parse() error = %v, want a clean zero count", err)
	}
	if got.Count != (report.Count{}) {
		t.Errorf("Count = %+v, want all zeroes", got.Count)
	}
	if !got.Empty {
		t.Error("Empty = false, want true so callers can tell a zero from a real count")
	}
}

func TestParseTreatsBlankOutputAsZero(t *testing.T) {
	for _, in := range []string{"", "\n", "   \n"} {
		got, err := Parse([]byte(in))
		if err != nil {
			t.Fatalf("Parse(%q) error = %v", in, err)
		}
		if got.Count != (report.Count{}) || !got.Empty {
			t.Errorf("Parse(%q) = %+v, want an empty zero count", in, got)
		}
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	for _, in := range []string{"not json at all", `{"SUM": `, "<html>error</html>"} {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("Parse(%q) returned no error", in)
		}
	}
}

// A non-empty result with no SUM block would mean the output shape changed
// under us; silently reporting zero would poison every downstream number.
func TestParseRejectsOutputWithoutASumBlock(t *testing.T) {
	in := `{"header": {"cloc_version": "1.98"}, "JavaScript": {"nFiles": 2, "code": 5}}`
	if _, err := Parse([]byte(in)); err == nil {
		t.Error("Parse() accepted cloc output with no SUM block")
	}
}
