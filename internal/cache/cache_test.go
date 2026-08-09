package cache

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/mcklmo/loc-history/internal/report"
)

func testKey() Key {
	return Key{
		SHA:         "08ab753d1c4e5f6a7b8c9d0e1f2a3b4c5d6e7f80",
		Folder:      "src",
		TestRegex:   `\.(test|spec)\.[mc]?[jt]sx?$`,
		ClocVersion: "1.98",
	}
}

func testEntry() Entry {
	return Entry{
		Product: report.Count{Files: 102, Code: 6100, Comment: 460, Blank: 1010},
		Test:    report.Count{Files: 89, Code: 4200, Comment: 120, Blank: 700},
	}
}

func TestPutThenGetRoundTrips(t *testing.T) {
	c, err := New(filepath.Join(t.TempDir(), "cache"))
	if err != nil {
		t.Fatal(err)
	}

	if err := c.Put(testKey(), testEntry()); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	got, ok := c.Get(testKey())
	if !ok {
		t.Fatal("Get() reported a miss immediately after Put")
	}
	if got != testEntry() {
		t.Errorf("Get() = %+v, want %+v", got, testEntry())
	}
}

func TestGetOnAnEmptyCacheMisses(t *testing.T) {
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get(testKey()); ok {
		t.Error("Get() reported a hit on an empty cache")
	}
}

// A commit tree is immutable, but the question being asked about it is not.
// Every input that changes the answer must change the key.
func TestEveryKeyFieldChangesTheAnswer(t *testing.T) {
	base := testKey()
	variants := map[string]Key{
		"different commit":  {SHA: "ffff", Folder: base.Folder, TestRegex: base.TestRegex, ClocVersion: base.ClocVersion},
		"different folder":  {SHA: base.SHA, Folder: "lib", TestRegex: base.TestRegex, ClocVersion: base.ClocVersion},
		"different regex":   {SHA: base.SHA, Folder: base.Folder, TestRegex: `_test\.go$`, ClocVersion: base.ClocVersion},
		"different version": {SHA: base.SHA, Folder: base.Folder, TestRegex: base.TestRegex, ClocVersion: "2.02"},
	}

	for name, k := range variants {
		t.Run(name, func(t *testing.T) {
			c, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := c.Put(base, testEntry()); err != nil {
				t.Fatal(err)
			}
			if _, ok := c.Get(k); ok {
				t.Errorf("Get() hit despite a %s", name)
			}
		})
	}
}

func TestSkippedCommitsAreCached(t *testing.T) {
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := Entry{Skipped: true}

	if err := c.Put(testKey(), want); err != nil {
		t.Fatal(err)
	}
	got, ok := c.Get(testKey())
	if !ok {
		t.Fatal("Get() missed a cached skip")
	}
	if !got.Skipped {
		t.Errorf("Get() = %+v, want Skipped", got)
	}
}

// Re-running the tool is the whole point; entries must outlive the process.
func TestEntriesSurviveANewCacheOverTheSameDirectory(t *testing.T) {
	dir := t.TempDir()

	first, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Put(testKey(), testEntry()); err != nil {
		t.Fatal(err)
	}

	second, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := second.Get(testKey()); !ok {
		t.Error("a fresh cache over the same directory missed a stored entry")
	}
}

// A truncated or hand-edited file must degrade to a recount, never to a crash
// or, worse, a wrong number.
func TestCorruptEntriesAreTreatedAsMisses(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Put(testKey(), testEntry()); err != nil {
		t.Fatal(err)
	}

	path := pathFor(dir, testKey())
	if err := os.WriteFile(path, []byte(`{"product": {"code": `), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, ok := c.Get(testKey()); ok {
		t.Error("Get() returned a hit for a truncated entry")
	}
}

func TestPutIsAtomic(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Put(testKey(), testEntry()); err != nil {
		t.Fatal(err)
	}

	// No partially written temporary files may be left lying around.
	var leftovers []string
	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && filepath.Ext(path) != ".json" {
			leftovers = append(leftovers, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Errorf("temporary files left behind: %v", leftovers)
	}
}

func TestConcurrentUseIsSafe(t *testing.T) {
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			k := testKey()
			k.SHA = string(rune('a'+i%4)) + k.SHA
			if err := c.Put(k, testEntry()); err != nil {
				t.Errorf("Put() error = %v", err)
			}
			c.Get(k)
		}()
	}
	wg.Wait()
}

// One flat directory with tens of thousands of files is a filesystem the user
// has to live with; sharding by hash prefix keeps it navigable.
func TestEntriesAreShardedByHashPrefix(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Put(testKey(), testEntry()); err != nil {
		t.Fatal(err)
	}

	rel, err := filepath.Rel(dir, pathFor(dir, testKey()))
	if err != nil {
		t.Fatal(err)
	}
	if depth := len(filepath.SplitList(rel)); depth != 1 {
		t.Fatalf("unexpected path shape %q", rel)
	}
	if len(filepath.Dir(rel)) != 2 {
		t.Errorf("entry path %q should sit under a two-character shard directory", rel)
	}
}

func TestNewCreatesTheDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b", "c")
	if _, err := New(dir); err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("New() did not create %s: %v", dir, err)
	}
}

func TestNewRejectsAnUnusableDirectory(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(file); err == nil {
		t.Error("New() accepted a path that is a regular file")
	}
}
