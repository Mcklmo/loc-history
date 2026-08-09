# `loc-history` — Implementation Handover Brief

> **Read this top to bottom before writing code.** It is written to be self-contained: every
> fact was established during a planning session and is recorded here with the command that
> produced it. You should not need to re-derive anything. Sections marked **ASSUMED** were *not*
> empirically verified and are called out explicitly.

---

## 1. What you are building

A standalone **Go CLI** called `loc-history` that answers: *how did this codebase's product code
and test code grow, commit by commit, over the life of a branch?*

For every commit on a branch, oldest to newest, it:

1. Materialises that commit's source folder into a scratch directory (without touching the
   working tree).
2. Runs `cloc` twice inside Docker — once **excluding** test files, once counting **only** test
   files.
3. Emits one record (commit, timestamp, product LOC, test LOC, total, delta vs. previous commit)
   to a pluggable sink.

Three sinks ship in v1: a console table, a file (CSV/NDJSON), and a self-contained HTML heat map
of lines-of-code changed over time.

The tool is **general-purpose** — it defaults to the current directory but runs against any repo
via `--repo`. The JS project it happens to live next to is its first test subject, nothing more.
**You are not modifying that JS project.** You are adding a new, independent Go module.

---

## 2. Environment

**Machine:** macOS (Darwin 25.6.0), zsh, user `moritzmarcushonscheidt`.

**Host repo used for testing** (the first subject, not the thing being modified):
`/Users/moritzmarcushonscheidt/Projects/personal/timeseries-visualizer/timeseries-visualizer`

It is a Vite + React + Cloudflare Workers app: ESM JavaScript with JSDoc types, vitest for tests,
`src/` as its source root. Its contents are irrelevant to the tool's design — it is only the
corpus you count.

### Toolchain status — checked with `which go python3 node docker`

| Tool | Status | Action needed |
|---|---|---|
| `docker` | Installed at `/usr/local/bin/docker` | **Daemon is NOT running.** Start Docker Desktop. |
| `go` | **NOT INSTALLED** — `which go` → not found | **`brew install go` before step 1.** |
| `node` | Installed (via fnm) | Not needed by this tool. |
| `python3` | Installed | Not needed by this tool. |

The exact daemon error observed, so you recognise it:
```
Cannot connect to the Docker daemon at unix:///Users/…/.docker/run/docker.sock.
Is the docker daemon running?
```

The `aldanial/cloc` image has **not** been pulled. First run will pull it (~50MB).

Go was chosen by the user *after* being told it was not installed. That is a settled decision —
do not revisit it or propose a rewrite in another language.

---

## 3. Decisions already made by the user

These four were explicitly chosen from presented alternatives. **Do not re-open them.** The
rejected alternatives are listed only so you recognise them as already-considered.

| # | Decision | Chosen | Rejected alternatives |
|---|---|---|---|
| 1 | Language | **Go** (separate module) | Node ESM script; Python 3 |
| 2 | Sink interface shape | **Push-based `Writer`**: `Write(Record) error` + `Close() error` | Pull-based consumer of an iterator; keeping the name `Reader` |
| 3 | What the two cloc queries measure | **Product vs. test code** — same regex, used as `--not-match-f` then `--only-match-f`, so the two are exact complements | Source vs. generated/vendored; two fully independent user-supplied regexes |
| 4 | Heat map output | **Self-contained HTML file** (inlined CSS/SVG, no network) | ANSI blocks in terminal; standalone `.svg` |

**On decision 2 specifically:** the original request said output is "written to a `Reader`
interface", but all three implementations (file, graph, console) are writers. This contradiction
was raised and the user resolved it in favour of the conventional name. The interface is called
`Writer`. If you see `Reader` referenced anywhere in older notes, it means this interface.

---

## 4. Facts verified during planning

Each of these was run against the host repo. Trust them; re-running is optional.

**History is short and perfectly linear** — no merge handling complexity is needed, though
`--first-parent` stays in for other repos:
```console
$ git rev-list --count main                  → 77
$ git rev-list --count --first-parent main   → 77
$ git rev-list --count --merges main         → 0
```

**The log format parses cleanly.** `%x1f` is the ASCII unit separator; it cannot appear in a
commit subject, so this is injection-proof where a comma or tab would not be:
```console
$ git log --first-parent --reverse --format='%H%x1f%cI%x1f%an%x1f%s' main | head -3
08ab753d…<US>2026-08-06T21:23:18+02:00<US>mcklmo<US>first commit
d251527e…<US>2026-08-06T22:13:14+02:00<US>mcklmo<US>git add init
64294609…<US>2026-08-06T22:22:36+02:00<US>mcklmo<US>slop
```
Oldest commit `08ab753` (2026-08-06); newest `55bd3cc` ("fix: map background never painted").

**Subtree extraction works and leaves the working tree untouched:**
```console
$ D=$(mktemp -d /tmp/locprobe.XXXX)
$ git archive 55bd3cc -- src | tar -x -C "$D"
$ find "$D" -maxdepth 2
/tmp/locprobe.QSgy
/tmp/locprobe.QSgy/src
/tmp/locprobe.QSgy/src/metrics
/tmp/locprobe.QSgy/src/ui
…
```
Note the shape: extraction reproduces the `src/` prefix, so the folder lands at
`<dest>/<folder>`, not `<dest>/`.

**Folder-existence probing works** (needed because early commits in an arbitrary repo may
predate the source folder):
```console
$ git cat-file -e 08ab753:src  → exit 0 (exists at the very first commit)
$ git cat-file -e 55bd3cc:src  → exit 0 (exists at HEAD)
```
In *this* repo `src/` exists at every commit, so the skip path will not be exercised naturally —
you must unit-test it with a synthetic repo.

**The product/test split is substantial and worth graphing** — at HEAD, under `src/`, using
regex `\.(test|spec)\.[mc]?[jt]sx?$`:
```console
$ git ls-tree -r --name-only 55bd3cc -- src | grep -cE  '\.(test|spec)\.[mc]?[jt]sx?$'   → 89
$ git ls-tree -r --name-only 55bd3cc -- src | grep -cvE '\.(test|spec)\.[mc]?[jt]sx?$'   → 102
```
**These two numbers are your end-to-end correctness anchor.** A full run's final record must
report 102 product files and 89 test files.

---

## 5. Assumptions NOT verified — check these first

The Docker daemon was down during planning, so nothing involving a running container was
confirmed. Treat the following as designed-for but unproven.

- **ASSUMED — `cloc --json` output shape.** Expected structure, which `parse.go` targets:
  ```json
  {
    "header": { "cloc_version": "2.02", "elapsed_seconds": 0.4, "n_files": 102 },
    "JavaScript": { "nFiles": 88, "blank": 900, "comment": 400, "code": 5200 },
    "SUM":        { "nFiles": 102, "blank": 1010, "comment": 460, "code": 6100 }
  }
  ```
  Read `.SUM` for totals and `.header.cloc_version` for the cache key. **Verify by hand before
  writing the parser** — run one container, capture real output into
  `internal/cloc/testdata/`, and write the parser against the actual bytes.

- **ASSUMED — empty-result behaviour.** When no files match the filter, cloc is expected to print
  *nothing at all* (exit 0, empty stdout) rather than a JSON document with a zeroed `SUM`. Handle
  empty stdout + exit 0 as a legitimate zero count, never as a parse error. Confirm with:
  `docker run --rm -v $PWD:/tmp aldanial/cloc --json --quiet --only-match-f='zzz' /tmp/src`

- **ASSUMED — the image's `WORKDIR`.** Unknown, so the design never relies on it: always pass the
  **absolute in-container path** `/tmp/<folder>`, never a relative one.

- **ASSUMED — the macOS bind-mount trap.** Docker Desktop shares a fixed set of host paths
  (`/Users`, `/Volumes`, `/private`, `/tmp` by default). Go's `os.MkdirTemp("", …)` on macOS
  returns a path under `/var/folders/…`, which is believed **not** to be shared — the mount then
  yields an *empty* directory inside the container and cloc reports zero lines for every commit,
  silently. This was not reproduced. Guard against it regardless: default the scratch root to
  `/tmp` (which resolves to `/private/tmp`, shared), expose `--work-dir` to override, and
  **detect the failure** rather than trusting the guess — after extraction, assert the scratch
  dir is non-empty, and treat a zero count from a non-empty tree as a hard error with a message
  naming the mount as the likely cause.

---

## 6. Architecture

```
                  ┌──────────────────────────────────────────┐
   git repo ─────►│ gitlog.Commits(repo, branch)             │
                  │   git log --first-parent --reverse       │
                  └───────────────┬──────────────────────────┘
                                  │ []Commit (chronological)
                                  ▼
                  ┌──────────────────────────────────────────┐
                  │ pipeline.Run  — N workers + reorder buf   │
                  └───────────────┬──────────────────────────┘
                                  │  per commit, in parallel:
              ┌───────────────────┼───────────────────┐
              ▼                   ▼                   ▼
      ┌───────────────┐   ┌───────────────┐   ┌───────────────┐
      │ cache.Get(sha)│──►│ tree.Extract  │──►│ cloc.Runner   │
      │  hit → skip   │   │ git archive   │   │ 2× docker run │
      └───────────────┘   │  → /tmp/xxx   │   │  --json       │
                          └───────────────┘   └───────┬───────┘
                                                      │ Counts{product,test}
                                  ┌───────────────────┘
                                  ▼  reordered to commit order
                  ┌──────────────────────────────────────────┐
                  │ report.Record  {sha,time,product,test,Δ} │
                  └───────────────┬──────────────────────────┘
                                  ▼
                  ┌──────────────────────────────────────────┐
                  │ writer.Writer   Write(Record) / Close()  │
                  └──┬──────────────┬─────────────────┬──────┘
                     ▼              ▼                 ▼
              ConsoleWriter    FileWriter        GraphWriter
              (streams)        (csv/ndjson)      (buffers → HTML)
                     └──────── MultiWriter ────────────┘
```

Everything above `Writer` is orchestration; everything below is rendering. That seam is the
extension point — a fourth sink is purely additive.

---

## 7. Package layout

Create as a **separate Go module** so it does not entangle the host JS project's toolchain.
Recommended location: `tools/loc-history/` inside the host repo, or any standalone directory —
the tool takes `--repo`, so it does not care where it lives.

```
tools/loc-history/
  go.mod                    module github.com/mcklmo/loc-history   (Go 1.22+)
  main.go                   flag parsing → wiring → pipeline.Run
  internal/
    gitlog/
      gitlog.go             Commits(repo, branch, firstParent, limit) ([]Commit, error)
      gitlog_test.go        builds a fixture repo in t.TempDir()
    tree/
      extract.go            Extract(repo, sha, folder, dest) (found bool, err error)
      extract_test.go
    cloc/
      runner.go             Runner interface + Options
      docker.go             DockerRunner — the `docker run` implementation
      parse.go              cloc --json → Count
      fake.go               FakeRunner for tests (canned Count per sha)
      parse_test.go
      testdata/             REAL captured cloc JSON — see §5
    cache/
      cache.go              content-addressed by (sha, folder, regex, clocVersion)
    report/
      record.go             Count, Record, delta computation
    pipeline/
      pipeline.go           worker pool + reorder buffer
      pipeline_test.go      drives FakeRunner; asserts order + deltas
    writer/
      writer.go             Writer interface + MultiWriter
      console.go
      file.go
      graph.go
      graph_test.go         golden-file comparison
      testdata/golden.html
```

Standard library only, plus `golang.org/x/sync/errgroup` if you want it for the worker pool
(a hand-rolled `sync.WaitGroup` + semaphore channel is equally fine and drops the dependency).

---

## 8. Core types and the `Writer` interface

```go
// internal/report/record.go

type Count struct {
    Files   int `json:"files"`
    Code    int `json:"code"`
    Comment int `json:"comment"`
    Blank   int `json:"blank"`
}

type Record struct {
    SHA       string    // full 40-char
    Short     string    // first 7 chars
    Timestamp time.Time // COMMITTER date (%cI) — see rationale in §9
    Author    string
    Subject   string

    Product Count // cloc --not-match-f=<testRegex>
    Test    Count // cloc --only-match-f=<testRegex>

    TotalCode int  // Product.Code + Test.Code — the tree's size after this commit
    Delta     int  // TotalCode - previous.TotalCode; NEGATIVE on a net deletion
    Skipped   bool // source folder absent at this commit; counts are zero
}
```

`Delta` is what the original request called "lines added". **It is signed.** A refactor that
deletes more than it adds yields a negative value, and the heat map must render that honestly
rather than clamping to zero.

```go
// internal/writer/writer.go

type Writer interface {
    Write(r report.Record) error
    Close() error
}

// MultiWriter fans one record out to several sinks — mirrors io.MultiWriter.
// Write returns the first error encountered.
// Close closes ALL sinks even if an earlier one fails, joining errors with
// errors.Join, so a broken file sink cannot leak the graph sink's file handle.
func MultiWriter(ws ...Writer) Writer
```

The pipeline calls `Close()` exactly once via `defer`, **including on error paths**, so an
aborted run still produces a partial artifact.

### `ConsoleWriter` — streams, one line per commit

```
DATE        SHA      PRODUCT   TEST    TOTAL     Δ  SUBJECT
2026-08-06  08ab753      412      0      412  +412  first commit
2026-08-06  d251527      488      0      488   +76  git add init
2026-08-07  9a9dab4     3120   1804     4924  +281  refactor: introduce ActivityRow…
```

### `FileWriter` — `--file-format=csv` (default) or `ndjson`

CSV gets a header row then one line per commit:
`sha,short,timestamp,author,subject,product_code,test_code,total_code,delta`.
NDJSON emits the `Record` struct verbatim, one JSON object per line, for piping to `jq`.

### `GraphWriter` — buffers, renders on `Close()`

One self-contained `.html` file: inlined CSS and SVG, **no network fetches**, opens straight from
disk. 77 records is nothing to hold in memory.

Layout is a GitHub-style calendar heat map — columns are ISO weeks, rows are weekdays, one cell
per calendar day. **Cell value is the summed `Delta` of every commit that day.**

Because that value is signed, the colour scale must be **diverging**, not sequential: distinct
hues either side of a neutral zero, magnitude driving intensity. Cell `<title>` elements carry
the day's commit subjects for hover detail. Include a legend anchoring both ends. Make the page
theme-aware via `prefers-color-scheme`.

> **Before writing `graph.go`, load the `dataviz` skill.** It carries the validated diverging
> palette and the accessibility rules for exactly this chart type. Do not invent colours.

---

## 9. Component specifications

### `gitlog` — enumerate commits

```
git -C <repo> log --first-parent --reverse --format=%H%x1f%cI%x1f%an%x1f%s <branch>
```

- `--reverse` gives chronological order, so `Delta` is a simple running difference.
- `--first-parent` is a no-op on the host repo (0 merges) but keeps the series meaningful
  elsewhere: you get the trunk's size over time rather than an interleaving of branch-internal
  states.
- **Committer date (`%cI`), not author date.** Author dates run backwards after a rebase, which
  would scramble the time axis and produce nonsense deltas.
- Split fields on `\x1f`, records on `\n`. Verified format — see §4.

### `tree` — materialise one commit

```go
// Returns (false, nil) when the folder does not exist at this commit — that is not an error.
func Extract(repo, sha, folder, dest string) (found bool, err error)
```

Implementation: probe with `git cat-file -e <sha>:<folder>`; if present, run
`git archive <sha> -- <folder>` and pipe into `tar -x -C <dest>`. Content lands at
`<dest>/<folder>` (verified, §4).

**Why `git archive` and not `git worktree` or `git checkout`:** it is the only option that never
mutates the repository or the user's working tree. `checkout` would destroy uncommitted work.
`worktree add` writes into `.git/worktrees` and needs cleanup that leaks if the process crashes.
`git archive` is a read-only pipe. Restricting it to `-- <folder>` also means extracting a few
hundred KB per commit instead of the whole tree including a 197KB `package-lock.json`.

Commits where the folder is absent produce a zeroed `Record` with `Skipped: true` — never an
aborted run.

### `cloc` — the Docker runner

```go
type Runner interface {
    Count(ctx context.Context, hostDir, folder string, opts Options) (report.Count, error)
}

type Options struct {
    TestRegex string // --not-match-f for product, --only-match-f for test
    Image     string // default "aldanial/cloc"
    OnlyTests bool   // selects which of the two flags to emit
}
```

Two invocations per commit, following the command shape given in the original request:

```
docker run --rm -v <hostDir>:/tmp aldanial/cloc --json --quiet \
    --not-match-f=<testRegex>  /tmp/<folder>      # product

docker run --rm -v <hostDir>:/tmp aldanial/cloc --json --quiet \
    --only-match-f=<testRegex> /tmp/<folder>      # test
```

Default `--test-regex`: `\.(test|spec)\.[mc]?[jt]sx?$` — matches `.test.js`, `.spec.ts`,
`.test.tsx`, `.test.mjs`, `.spec.cjs`.

Because the two flags use the *same* regex over the *same* folder, they are exact complements:
`product.Code + test.Code` is the true total. **Assert that invariant in a test.**

Notes that matter:
- `--json --quiet` makes output machine-parseable; `--quiet` suppresses the progress header.
- Pass the **absolute** in-container path so the design does not depend on the image's `WORKDIR`.
- `Runner` is an interface **specifically** so `pipeline` and the writers are testable via
  `FakeRunner` — no Docker, no network, sub-second unit tests. Build `FakeRunner` early.

### Scratch directories

Each concurrent worker gets its own scratch dir created under the `--work-dir` root, default
**`/tmp`** — *not* a bare `os.MkdirTemp("", …)`. See §5 for the reasoning and the required
guard. Clean up with `defer os.RemoveAll`.

### `pipeline` — parallel execution, ordered output

`docker run` costs roughly 0.3–1s of startup regardless of workload. At 77 commits × 2 queries
that is ~154 container starts and it dominates runtime, so workers process `--jobs` commits
concurrently (default 4).

Results therefore complete **out of order**, but `Delta` is a running difference and every sink
expects chronological order. Solve with a **reorder buffer**: each result carries its commit
index, completed results land in a map, and a single emitter goroutine releases them in index
order as the next-expected index becomes available. **Compute `Delta` at emit time, never inside
a worker** — a worker has no reliable view of its predecessor.

Error policy: a failed commit does **not** abort the run by default — record it, report on
stderr, continue. One bad commit should not cost a three-minute rebuild. `--fail-fast` opts into
cancelling the context on first error.

### `cache`

A commit tree is immutable, so `(sha, folder, testRegex, clocVersion)` → `Count` is a pure
function and permanently cacheable. Store as JSON under `--cache-dir` (default
`~/.cache/loc-history/`), keyed by a SHA-256 of those four inputs. This is what makes the tool a
repeatable command rather than a one-off: re-running after a few new commits goes from minutes to
near-instant. `--no-cache` bypasses.

Build this **after** the uncached path is proven correct — a cache over a buggy counter just
persists the bug.

---

## 10. CLI surface

```
loc-history [flags]

  --repo string          repository path (default ".")
  --branch string        branch to walk (default "main")
  --folder string        source folder to count (default "src")
  --test-regex string    (default `\.(test|spec)\.[mc]?[jt]sx?$`)

  --out string           sinks, comma-separated: console,file,graph (default "console")
  --file-out string      (default "loc-history.csv")
  --file-format string   csv | ndjson (default "csv")
  --graph-out string     (default "loc-history.html")

  --jobs int             concurrent commits (default 4)
  --work-dir string      scratch root (default "/tmp")  — see §5
  --cache-dir string     (default "~/.cache/loc-history")
  --no-cache             recompute everything
  --first-parent         follow trunk only (default true)
  --limit int            most recent N commits, 0 = all
  --fail-fast            abort on first commit error
  --image string         (default "aldanial/cloc")
```

`--out=console,graph` composes via `MultiWriter` — one walk, two artifacts.

---

## 11. Build order

Ordered so the bulk of the logic is under test before the first container ever starts.

0. **`brew install go`**; start Docker Desktop; capture real `cloc --json` output into
   `internal/cloc/testdata/` (§5) so the parser is written against actual bytes.
1. **`report` + `writer`** — types, the `Writer` interface, `ConsoleWriter`, `MultiWriter`.
   Testable immediately against hand-built `Record` values; no Git, no Docker.
2. **`gitlog`** — commit enumeration, tested against a throwaway repo built in `t.TempDir()`.
3. **`tree`** — `git archive` extraction, including the absent-folder path.
4. **`cloc`** — `Runner` interface, then `parse.go` against the captured fixtures, then
   `DockerRunner`. `FakeRunner` lands here.
5. **`pipeline`** — worker pool and reorder buffer, tested end-to-end with `FakeRunner`.
6. **`main.go`** — flags and wiring. First real run against the host repo.
7. **`cache`**.
8. **`FileWriter`**, then **`GraphWriter`** (load the `dataviz` skill first; golden-file test).

---

## 12. Verification

### Unit — `go test ./...`

The load-bearing assertions:
- `product.Code + test.Code == total.Code` for a tree counted three ways.
- **Records reach the `Writer` in strict chronological order under `--jobs=8` with a `FakeRunner`
  that sleeps for randomised durations.** This is the test that actually proves the reorder
  buffer; without randomised delays it passes vacuously.
- Deltas sum to the final `TotalCode`.
- Absent source folder yields `Skipped: true`, not an error. (Must be synthetic — the host repo
  has `src/` at every commit, §4.)
- Graph HTML matches `testdata/golden.html`.

### End-to-end — Docker Desktop running

```bash
cd tools/loc-history && go build -o /tmp/loc-history .
REPO=/Users/moritzmarcushonscheidt/Projects/personal/timeseries-visualizer/timeseries-visualizer

# 1. Smoke: 5 commits, console only. Expect 5 rows, ascending dates.
/tmp/loc-history --repo=$REPO --limit=5

# 2. CORRECTNESS ANCHOR: the final record must report exactly
#    102 product files and 89 test files (independently confirmed, §4).
/tmp/loc-history --repo=$REPO --limit=1

# 3. Full run, all three sinks. ~77 commits × 2 containers.
/tmp/loc-history --repo=$REPO --out=console,file,graph

# 4. Cache proof: second run dramatically faster AND byte-identical.
time /tmp/loc-history --repo=$REPO --out=file --file-out=/tmp/a.csv
time /tmp/loc-history --repo=$REPO --out=file --file-out=/tmp/b.csv
diff /tmp/a.csv /tmp/b.csv && echo "cache is consistent"

# 5. Open the heat map; confirm it renders offline, in light AND dark scheme.
open loc-history.html

# 6. The working tree must be untouched by the whole exercise.
git -C $REPO status --porcelain   # expect empty
```

### Failure modes to exercise by hand

- **Docker daemon stopped** → must fail with a clear, actionable message, not a JSON parse error.
- **`--folder=nope`** → every commit skipped; empty but structurally valid output.
- **`--work-dir` set to an unmountable path** (e.g. a `/var/folders/…` temp dir) → must **not**
  silently report zero lines for all 77 commits. This is the §5 trap; the guard belongs here.

---

## 13. Deliberately out of scope for v1

Per-language breakdown (cloc already returns it; `Count` would just need widening), incremental
append to an existing CSV, and any sink beyond the three specified. The `Writer` seam means each
is purely additive later.
