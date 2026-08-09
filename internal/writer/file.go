package writer

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mcklmo/loc-history/internal/bucket"
)

// Format selects how File serialises buckets.
type Format int

const (
	// FormatCSV writes a header row and one line per bucket: a flat
	// projection meant for a spreadsheet.
	FormatCSV Format = iota + 1
	// FormatNDJSON writes the whole Bucket per line, its records nested, for
	// piping to jq. That is what keeps the file lossless at any granularity.
	FormatNDJSON
)

// ParseFormat converts a --file-format value.
func ParseFormat(s string) (Format, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "csv":
		return FormatCSV, nil
	case "ndjson":
		return FormatNDJSON, nil
	default:
		return 0, fmt.Errorf("unknown file format %q; want csv or ndjson", s)
	}
}

// csvHeader is the documented column list. It is fixed rather than derived from
// the granularity, because it goes out before the first bucket exists and so
// cannot name the unit; `bucket_start` is RFC 3339 at every width instead.
//
// The `last_*` prefixes say what the identity columns now mean: one row is a
// slice of time, and the commit named in it is the last one that landed in the
// slice. `product_delta`/`test_delta` let a reader reproduce the two charts.
//
// The trailing `skipped` column cannot be inferred from the counts before it:
// they cannot express the difference between "the folder was not there" and
// "the folder was there and empty" — both read as zero.
var csvHeader = []string{
	"bucket_start", "commits",
	"last_sha", "last_short", "last_author", "last_subject",
	"product_code", "test_code", "total_code",
	"product_delta", "test_delta", "delta", "skipped",
}

// File streams buckets to disk as CSV or NDJSON.
type File struct {
	path string
	f    *os.File
	buf  *bufio.Writer
	csv  *csv.Writer
	enc  *json.Encoder
}

// NewFile opens path for writing, creating parent directories as needed.
func NewFile(path string, format Format) (*File, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create output directory %s: %w", dir, err)
		}
	}

	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", path, err)
	}

	w := &File{path: path, f: f, buf: bufio.NewWriter(f)}
	switch format {
	case FormatCSV:
		w.csv = csv.NewWriter(w.buf)
		// The header goes out immediately, so even a run that produces no
		// records leaves a structurally valid file.
		if err := w.csv.Write(csvHeader); err != nil {
			f.Close()
			return nil, err
		}
	case FormatNDJSON:
		w.enc = json.NewEncoder(w.buf)
	default:
		f.Close()
		return nil, fmt.Errorf("unknown file format %d", format)
	}
	return w, nil
}

func (w *File) Write(b bucket.Bucket) error {
	if w.csv != nil {
		last := b.Last()
		return w.csv.Write([]string{
			b.Start.Format(time.RFC3339),
			strconv.Itoa(b.Commits),
			last.SHA,
			last.Short,
			last.Author,
			last.Subject,
			strconv.Itoa(b.Product.Code),
			strconv.Itoa(b.Test.Code),
			strconv.Itoa(b.TotalCode),
			strconv.Itoa(b.ProductDelta),
			strconv.Itoa(b.TestDelta),
			strconv.Itoa(b.Delta),
			strconv.FormatBool(b.Skipped),
		})
	}
	return w.enc.Encode(b)
}

// Close flushes buffered rows and releases the file. Skipping it would lose
// whatever is still in the buffer.
func (w *File) Close() error {
	var errs []error
	if w.csv != nil {
		w.csv.Flush()
		errs = append(errs, w.csv.Error())
	}
	errs = append(errs, w.buf.Flush(), w.f.Close())

	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("close %s: %w", w.path, err)
	}
	return nil
}

// interface check
var _ io.Closer = (*File)(nil)
