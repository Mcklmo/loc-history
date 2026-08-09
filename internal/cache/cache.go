// Package cache stores line counts on disk so a repeated walk is cheap.
//
// A commit tree is immutable, so counting one is a pure function of the commit,
// the folder, the regex that splits it, and the version of cloc doing the
// counting. That makes the answer permanently valid: re-running after a few new
// commits costs a few containers instead of a few hundred.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mcklmo/loc-history/internal/report"
)

// schema is mixed into every key so that changing Entry's shape invalidates
// what is already on disk instead of misreading it.
const schema = "v1"

// Key identifies one cached answer. Every field is an input that can change
// the numbers.
type Key struct {
	SHA         string
	Folder      string
	TestRegex   string
	ClocVersion string
}

// Entry is what a commit cost to compute: both counts, or the fact that the
// folder was not there at all.
type Entry struct {
	Product report.Count `json:"product"`
	Test    report.Count `json:"test"`
	Skipped bool         `json:"skipped"`
}

// Cache is a content-addressed store under a directory.
//
// It needs no lock: entries are immutable, writes are atomic renames, and two
// processes computing the same key necessarily compute the same bytes.
type Cache struct {
	dir string
}

// New prepares a cache rooted at dir, creating it if needed.
func New(dir string) (*Cache, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create cache directory %s: %w", dir, err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("cache directory %s is not a directory", dir)
	}
	return &Cache{dir: dir}, nil
}

// Get returns the stored entry for k, if there is a readable one.
//
// Any problem — missing, truncated, hand-edited — is a miss. Recounting is
// cheap next to reporting a number that came from a damaged file.
func (c *Cache) Get(k Key) (Entry, bool) {
	b, err := os.ReadFile(c.pathFor(k))
	if err != nil {
		return Entry{}, false
	}
	var e Entry
	if err := json.Unmarshal(b, &e); err != nil {
		return Entry{}, false
	}
	return e, true
}

// Put stores an entry, replacing any existing one atomically.
func (c *Cache) Put(k Key, e Entry) error {
	path := c.pathFor(k)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create cache shard: %w", err)
	}

	b, err := json.Marshal(e)
	if err != nil {
		return err
	}

	// Write beside the target and rename, so a reader never sees half a file
	// and an interrupted run leaves nothing behind to misread later.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("create cache entry: %w", err)
	}
	defer os.Remove(tmp.Name()) // no-op once the rename succeeds

	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("install cache entry: %w", err)
	}
	return nil
}

// pathFor maps a key to its file, sharded by the first two hash characters so
// no single directory collects every commit of every repository.
func (c *Cache) pathFor(k Key) string {
	sum := sha256.Sum256([]byte(strings.Join(
		[]string{schema, k.SHA, k.Folder, k.TestRegex, k.ClocVersion}, "\x1f")))
	name := hex.EncodeToString(sum[:])
	return filepath.Join(c.dir, name[:2], name[2:]+".json")
}
