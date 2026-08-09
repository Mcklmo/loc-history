// Package gitlog enumerates the commits of a branch, oldest first.
package gitlog

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/mcklmo/loc-history/internal/report"
)

// Commit is re-exported so callers can say gitlog.Commit without importing
// report for the type alone. It is the same type the sinks receive.
type Commit = report.Commit

// unitSep is git's %x1f. It cannot occur in a commit subject, which makes this
// format injection-proof where a comma or a tab would not be.
const unitSep = "\x1f"

// logFormat is hash, committer date, author name, subject.
const logFormat = "%H%x1f%cI%x1f%an%x1f%s"

// Options selects which commits to enumerate.
type Options struct {
	Repo        string // repository path; empty means the current directory
	Branch      string // branch to walk; empty means the checked-out HEAD
	FirstParent bool   // follow the trunk only, ignoring merged-in history
	Limit       int    // most recent N commits; 0 means all
}

// Commits returns the branch's history in chronological order.
//
// Chronological order is what makes Delta a simple running difference, and the
// committer date is what keeps that order meaningful: author dates run
// backwards after a rebase and would scramble the time axis.
func Commits(o Options) ([]Commit, error) {
	args := []string{}
	if o.Repo != "" {
		args = append(args, "-C", o.Repo)
	}
	args = append(args, "log", "--reverse", "--format="+logFormat)
	if o.FirstParent {
		args = append(args, "--first-parent")
	}
	if o.Limit > 0 {
		// git applies -n before --reverse, so this keeps the most recent N
		// commits and still hands them back oldest first.
		args = append(args, "-n", strconv.Itoa(o.Limit))
	}
	if o.Branch != "" {
		args = append(args, o.Branch)
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.Command("git", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git log %s in %s: %w: %s",
			describeTarget(o), describeRepo(o.Repo), err, strings.TrimSpace(stderr.String()))
	}

	return parseLog(stdout.String())
}

func parseLog(out string) ([]Commit, error) {
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	commits := make([]Commit, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		fields := strings.Split(line, unitSep)
		if len(fields) != 4 {
			return nil, fmt.Errorf("git log line has %d fields, want 4: %q", len(fields), line)
		}
		ts, err := time.Parse(time.RFC3339, fields[1])
		if err != nil {
			return nil, fmt.Errorf("parse committer date %q: %w", fields[1], err)
		}
		commits = append(commits, Commit{
			SHA:       fields[0],
			Timestamp: ts,
			Author:    fields[2],
			Subject:   fields[3],
		})
	}
	return commits, nil
}

func describeTarget(o Options) string {
	if o.Branch == "" {
		return "HEAD"
	}
	return o.Branch
}

func describeRepo(repo string) string {
	if repo == "" {
		return "the current directory"
	}
	return repo
}
