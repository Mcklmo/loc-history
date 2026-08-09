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

	"github.com/mcklmo/loc-history/internal/report"
)

// Format selects how File serialises records.
type Format int

const (
	// FormatCSV writes a header row and one line per commit: a flat
	// projection meant for a spreadsheet.
	FormatCSV Format = iota + 1
	// FormatNDJSON writes the whole Record per line, for piping to jq.
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

// csvHeader is the documented column list.
//
// The trailing `skipped` column is not in the original specification but the
// nine columns before it cannot express the difference between "the folder was
// not there" and "the folder was there and empty" — both read as zero.
var csvHeader = []string{
	"sha", "short", "timestamp", "author", "subject",
	"product_code", "test_code", "total_code", "delta", "skipped",
}

// File streams records to disk as CSV or NDJSON.
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

func (w *File) Write(r report.Record) error {
	if w.csv != nil {
		return w.csv.Write([]string{
			r.SHA,
			r.Short,
			r.Timestamp.Format(time.RFC3339),
			r.Author,
			r.Subject,
			strconv.Itoa(r.Product.Code),
			strconv.Itoa(r.Test.Code),
			strconv.Itoa(r.TotalCode),
			strconv.Itoa(r.Delta),
			strconv.FormatBool(r.Skipped),
		})
	}
	return w.enc.Encode(r)
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
