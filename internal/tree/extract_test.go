package tree

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mcklmo/loc-history/internal/gittest"
)

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// relFiles lists every regular file under root, repository-relative and sorted.
func relFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

// Extraction reproduces the folder prefix, so content lands at <dest>/<folder>.
func TestExtractMaterialisesFolderUnderItsOwnPrefix(t *testing.T) {
	r := gittest.New(t)
	r.Write("src/app.js", "export const a = 1\n")
	r.Write("src/ui/panel.js", "export const p = 2\n")
	r.Write("README.md", "# not counted\n")
	sha := r.Commit("first commit")

	dest := t.TempDir()
	got, err := Extract(r.Dir, sha, "src", dest)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}

	if !got.Found {
		t.Error("Found = false, want true")
	}
	want := []string{"src/app.js", "src/ui/panel.js"}
	if diff := strings.Join(relFiles(t, dest), ","); diff != strings.Join(want, ",") {
		t.Errorf("extracted %v, want %v (README.md must stay outside the pathspec)", relFiles(t, dest), want)
	}
	if got.Files != 2 {
		t.Errorf("Files = %d, want 2", got.Files)
	}
	if c := read(t, filepath.Join(dest, "src", "app.js")); c != "export const a = 1\n" {
		t.Errorf("content = %q", c)
	}
}

// The whole point of walking history: an old SHA must yield the old content.
func TestExtractReturnsTheContentOfThatCommitNotHead(t *testing.T) {
	r := gittest.New(t)
	r.Write("src/app.js", "old\n")
	old := r.Commit("first commit")
	r.Write("src/app.js", "new and much longer\n")
	r.Write("src/extra.js", "added later\n")
	r.Commit("second commit")

	dest := t.TempDir()
	if _, err := Extract(r.Dir, old, "src", dest); err != nil {
		t.Fatalf("Extract() error = %v", err)
	}

	if c := read(t, filepath.Join(dest, "src", "app.js")); c != "old\n" {
		t.Errorf("app.js = %q, want the content at the old commit", c)
	}
	if _, err := os.Stat(filepath.Join(dest, "src", "extra.js")); !os.IsNotExist(err) {
		t.Error("a file added after this commit leaked into the extraction")
	}
}

// Early commits in an arbitrary repo may predate the source folder. That is
// ordinary history, not an error.
func TestExtractReportsAbsentFolderWithoutError(t *testing.T) {
	r := gittest.New(t)
	r.Write("README.md", "# no source yet\n")
	sha := r.Commit("chore: repo init")

	dest := t.TempDir()
	got, err := Extract(r.Dir, sha, "src", dest)

	if err != nil {
		t.Fatalf("Extract() error = %v, want nil for a folder that simply does not exist yet", err)
	}
	if got.Found {
		t.Error("Found = true, want false")
	}
	if got.Files != 0 {
		t.Errorf("Files = %d, want 0", got.Files)
	}
	if files := relFiles(t, dest); len(files) != 0 {
		t.Errorf("destination is not empty: %v", files)
	}
}

func TestExtractHandlesNestedFolders(t *testing.T) {
	r := gittest.New(t)
	r.Write("packages/web/src/app.ts", "const a = 1\n")
	r.Write("packages/api/src/main.ts", "const b = 2\n")
	sha := r.Commit("monorepo")

	dest := t.TempDir()
	got, err := Extract(r.Dir, sha, "packages/web/src", dest)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}

	if !got.Found || got.Files != 1 {
		t.Errorf("got %+v, want Found=true Files=1", got)
	}
	if _, err := os.Stat(filepath.Join(dest, "packages", "web", "src", "app.ts")); err != nil {
		t.Errorf("nested folder did not land under its full prefix: %v", err)
	}
}

// An empty folder means "count the whole repository".
func TestExtractWithEmptyFolderTakesTheWholeTree(t *testing.T) {
	r := gittest.New(t)
	r.Write("src/app.js", "a\n")
	r.Write("README.md", "b\n")
	sha := r.Commit("first commit")

	dest := t.TempDir()
	got, err := Extract(r.Dir, sha, "", dest)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}

	if !got.Found || got.Files != 2 {
		t.Errorf("got %+v, want Found=true Files=2", got)
	}
}

// git archive is a read-only pipe; checkout would have destroyed the edit below.
func TestExtractLeavesTheWorkingTreeUntouched(t *testing.T) {
	r := gittest.New(t)
	r.Write("src/app.js", "committed\n")
	old := r.Commit("first commit")
	r.Write("src/app.js", "uncommitted work in progress\n")
	r.Write("src/scratch.js", "untracked\n")

	before := r.Git("status", "--porcelain")
	if _, err := Extract(r.Dir, old, "src", t.TempDir()); err != nil {
		t.Fatalf("Extract() error = %v", err)
	}

	if after := r.Git("status", "--porcelain"); after != before {
		t.Errorf("working tree changed\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if c := read(t, filepath.Join(r.Dir, "src", "app.js")); c != "uncommitted work in progress\n" {
		t.Errorf("uncommitted edit was clobbered: %q", c)
	}
}

func TestExtractPreservesTheExecutableBit(t *testing.T) {
	r := gittest.New(t)
	r.Write("src/build.sh", "#!/bin/sh\necho hi\n")
	if err := os.Chmod(filepath.Join(r.Dir, "src", "build.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	sha := r.Commit("add script")

	dest := t.TempDir()
	if _, err := Extract(r.Dir, sha, "src", dest); err != nil {
		t.Fatalf("Extract() error = %v", err)
	}

	info, err := os.Stat(filepath.Join(dest, "src", "build.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o100 == 0 {
		t.Errorf("mode = %s, want the executable bit preserved", info.Mode())
	}
}

func TestExtractErrorsOnUnknownRevision(t *testing.T) {
	r := gittest.New(t)
	r.Write("src/app.js", "a\n")
	r.Commit("first commit")

	_, err := Extract(r.Dir, "0000000000000000000000000000000000000000", "src", t.TempDir())
	if err == nil {
		t.Fatal("Extract() on an unknown revision returned no error")
	}
}

func TestExtractErrorsOnNonRepository(t *testing.T) {
	if _, err := Extract(t.TempDir(), "HEAD", "src", t.TempDir()); err == nil {
		t.Fatal("Extract() outside a repository returned no error")
	}
}

// A path that is a file rather than a directory still counts as found.
func TestExtractOfASingleFilePath(t *testing.T) {
	r := gittest.New(t)
	r.Write("src/app.js", "a\n")
	sha := r.Commit("first commit")

	dest := t.TempDir()
	got, err := Extract(r.Dir, sha, "src/app.js", dest)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if !got.Found || got.Files != 1 {
		t.Errorf("got %+v, want Found=true Files=1", got)
	}
}
