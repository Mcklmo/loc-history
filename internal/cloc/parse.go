package cloc

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mcklmo/loc-history/internal/report"
)

// Output is one parsed cloc run.
type Output struct {
	Count   report.Count
	Version string // cloc's own version, part of the cache key
	Empty   bool   // cloc matched nothing at all
}

// clocJSON mirrors the parts of cloc's --json document this tool reads. The
// per-language blocks are deliberately ignored; a language breakdown is a
// widening of Count, not something to half-collect now.
type clocJSON struct {
	Header struct {
		Version string `json:"cloc_version"`
		NFiles  int    `json:"n_files"`
	} `json:"header"`
	SUM *struct {
		NFiles  int `json:"nFiles"`
		Blank   int `json:"blank"`
		Comment int `json:"comment"`
		Code    int `json:"code"`
	} `json:"SUM"`
}

// Parse converts cloc's JSON document into a Count.
//
// Verified against cloc 1.98: when nothing matches, cloc prints a bare `{}`
// and exits 0 — including when the path does not exist. That is a legitimate
// zero here, but it is also indistinguishable from a scratch directory that
// mounted empty inside the container, which is why the caller cross-checks the
// count against the number of files it actually extracted.
func Parse(stdout []byte) (Output, error) {
	if strings.TrimSpace(string(stdout)) == "" {
		return Output{Empty: true}, nil
	}

	var doc clocJSON
	if err := json.Unmarshal(stdout, &doc); err != nil {
		return Output{}, fmt.Errorf("parse cloc json: %w: %s", err, excerpt(stdout))
	}

	if doc.SUM == nil {
		// A bare {} is cloc's "nothing matched". Anything else without a SUM
		// block means the output shape changed and the numbers cannot be trusted.
		if strings.TrimSpace(string(stdout)) == "{}" {
			return Output{Empty: true}, nil
		}
		return Output{}, fmt.Errorf("cloc output has no SUM block: %s", excerpt(stdout))
	}

	return Output{
		Count: report.Count{
			Files:   doc.SUM.NFiles,
			Code:    doc.SUM.Code,
			Comment: doc.SUM.Comment,
			Blank:   doc.SUM.Blank,
		},
		Version: doc.Header.Version,
	}, nil
}

func excerpt(b []byte) string {
	const max = 200
	s := strings.TrimSpace(string(b))
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
