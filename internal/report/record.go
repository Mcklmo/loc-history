// Package report holds the domain types that flow through the pipeline: the
// commits read out of git, the line counts read out of cloc, and the records
// handed to the sinks.
package report

import "time"

// shortLen is the number of SHA characters git itself abbreviates to by default.
const shortLen = 7

// Commit is one entry in a branch's history, as enumerated by the gitlog package.
type Commit struct {
	SHA       string
	Timestamp time.Time // committer date; see gitlog for why not the author date
	Author    string
	Subject   string
}

// Count is one cloc result: how many files matched and what was in them.
type Count struct {
	Files   int `json:"files"`
	Code    int `json:"code"`
	Comment int `json:"comment"`
	Blank   int `json:"blank"`
}

// Add returns the field-wise sum of two counts. The receiver is unchanged.
func (c Count) Add(o Count) Count {
	return Count{
		Files:   c.Files + o.Files,
		Code:    c.Code + o.Code,
		Comment: c.Comment + o.Comment,
		Blank:   c.Blank + o.Blank,
	}
}

// Record is one row of output: a commit and the size of the tree it produced.
type Record struct {
	SHA       string    `json:"sha"`
	Short     string    `json:"short"`
	Timestamp time.Time `json:"timestamp"`
	Author    string    `json:"author"`
	Subject   string    `json:"subject"`

	Product Count `json:"product"` // counted with --not-match-f=<testRegex>
	Test    Count `json:"test"`    // counted with --only-match-f=<testRegex>

	TotalCode int  `json:"total_code"` // Product.Code + Test.Code
	Delta     int  `json:"delta"`      // signed change against the previous record
	Skipped   bool `json:"skipped"`    // source folder absent at this commit
}

// AverageDelta is the mean of a bucket's records, used to draw the graph's lines. It
// is not a record itself, so it is not in the Records slice. It could be placed in the bottom of a csv output
type AverageDelta struct {
	Product   Count `json:"product_average_delta"`
	Test      Count `json:"test_average_delta"`
	TotalCode int   `json:"total_code_average_delta"` // Product.Code + Test.Code
}

func Average(records []Record) AverageDelta {
	var avg AverageDelta

	if len(records) == 0 {
		return avg
	}

	for _, r := range records {
		avg.Product = avg.Product.Add(r.Product)
		avg.Test = avg.Test.Add(r.Test)
		avg.TotalCode += r.TotalCode
	}

	n := len(records)
	avg.Product.Files /= n
	avg.Product.Code /= n
	avg.Product.Comment /= n
	avg.Product.Blank /= n

	avg.Test.Files /= n
	avg.Test.Code /= n
	avg.Test.Comment /= n
	avg.Test.Blank /= n

	avg.TotalCode /= n

	return avg
}

// NewRecord starts a record from a commit. Counts are filled in later by the
// pipeline, and TotalCode and Delta by Finalize.
func NewRecord(c Commit) Record {
	return Record{
		SHA:       c.SHA,
		Short:     ShortSHA(c.SHA),
		Timestamp: c.Timestamp,
		Author:    c.Author,
		Subject:   c.Subject,
	}
}

// Finalize computes the tree size after this commit and the signed change
// against the previous record's total. Deletions stay negative: a refactor that
// removes more than it adds is real history, not something to clamp away.
//
// Callers must pass the immediately preceding record's TotalCode, which is why
// this runs at emit time in commit order rather than inside a worker.
func (r *Record) Finalize(prevTotal int) {
	r.TotalCode = r.Product.Code + r.Test.Code
	r.Delta = r.TotalCode - prevTotal
}

// ShortSHA abbreviates a commit hash the way git does.
func ShortSHA(sha string) string {
	if len(sha) <= shortLen {
		return sha
	}
	return sha[:shortLen]
}
