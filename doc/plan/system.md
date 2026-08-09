# `loc-history` — System Description

> This document described the system before it was built; it now describes the system as
> built. Where reality contradicted the original plan, the contradiction is recorded rather
> than quietly edited away — sections marked **WAS ASSUMED** were guesses in the plan that
> turned out to be wrong, and knowing *how* they were wrong is worth more than a clean page.
>
> Build progress and the running list of deviations live in [TODO.md](TODO.md).

---

## 1. What this is

A standalone **Go CLI** called `loc-history` that answers: *how did this codebase's product
code and test code grow, commit by commit, over the life of a branch?*

For every commit on a branch, oldest to newest, it:

1. Materialises that commit's source folder into a scratch directory, without touching the
   repository or the working tree.
2. Runs `cloc` twice inside Docker — once **excluding** test files, once counting **only**
   test files.
3. Emits one record (commit, timestamp, product LOC, test LOC, total, signed delta against
   the previous commit) to a pluggable sink.

Three sinks ship: a console table, a file (CSV/NDJSON), and a self-contained HTML calendar
heat map. Counted commits are cached, so re-running after a few new commits is instant.

The tool is **general-purpose** — it defaults to the current directory and runs against any
repository via `--repo`.

---

## 2. Environment

**Machine:** macOS (Darwin 25.6.0), zsh, Apple silicon.

| Tool | State |
|---|---|
| `go` | **1.26.5**, installed from the official go.dev tarball to `/usr/local/go`; `PATH` exported in `~/.zshrc`. Not brew, not a version manager — a deliberate choice by the user. |
| `docker` | 29.6.2, Docker Desktop. |
| `aldanial/cloc` | **cloc 1.98.** The image is **amd64-only**, so on Apple silicon every container runs under emulation. Container startup dominates runtime accordingly. |

**Dependencies: none.** Standard library only — `go.mod` has no `require` block.

---

## 3. Decisions made by the user

These were explicitly chosen from presented alternatives. **Do not re-open them.**

| # | Decision | Chosen | Rejected |
|---|---|---|---|
| 1 | Language | **Go** (separate module) | Node ESM script; Python 3 |
| 2 | Sink interface shape | **Push-based `Writer`**: `Write(Record) error` + `Close() error` | Pull-based consumer of an iterator; the name `Reader` |
| 3 | What the two cloc queries measure | **Product vs. test code** — the same regex used both ways, so the two are exact complements | Source vs. generated/vendored; two independent regexes |
| 4 | Heat map output | **Self-contained HTML file** (inlined CSS/SVG, no network) | ANSI blocks in the terminal; a standalone `.svg` |
| 5 | Go installation | Official tarball to `/usr/local/go` | Homebrew; a version manager |
| 6 | Test corpus | This repository and synthetic fixtures | A neighbouring `timeseries-visualizer` project, later ruled out of scope |

On decision 2: the original request said output is "written to a `Reader` interface", but all
three implementations are writers. The user resolved the contradiction in favour of the
conventional name.

Decision 3 survived contact with reality; only the flag name in the plan was wrong (§5).

---

## 4. Verified facts

**The product/test split is exact.** `--not-match-f` and `--match-f` over the same regex and
the same tree are complements. Confirmed on a four-file probe (product 5 + test 5 = total 10
code; 2 + 2 = 4 files) and again on this repository at every commit.

**Correctness anchor.** At commit `19c88c7`, counting `internal/` with `--test-regex='_test\.go$'`:

```console
$ loc-history --repo=. --folder=internal --test-regex='_test\.go$' --limit=1
2026-08-09  19c88c7     1933    2162      4095   +4095  feat(writer): self-contained HTML …
```

Independently confirmed by extracting the tree by hand and running the three cloc queries
directly: product 1933, test 2162, unfiltered total 4095. `1933 + 2162 = 4095`.

**The log format parses cleanly.** `%x1f` is the ASCII unit separator; it cannot appear in a
commit subject, so the format is injection-proof where a comma or a tab would not be. A
subject containing commas, tabs, quotes, pipes and backslashes round-trips intact.

**`git archive` leaves the working tree untouched**, including uncommitted edits and
untracked files. There is a test that makes an uncommitted edit, extracts an older commit,
and asserts the edit survives byte for byte.

**Extraction reproduces the path prefix**, so content lands at `<dest>/<folder>`, not
`<dest>/`.

---

## 5. What the plan assumed, and what was actually true

The Docker daemon was down during planning, so nothing involving a running container had been
confirmed. Every one of these was checked before the code that depends on it was written.

- **WAS ASSUMED — `--only-match-f` selects test files.** *It does not exist.* cloc 1.98 offers
  `--match-f` / `--not-match-f`. Decision 3 is intact — one regex, used both ways — but the
  flag name in the plan was wrong. Caught by running the command before writing the parser.

- **WAS ASSUMED — cloc 2.02 and its JSON shape.** The version is **1.98**. The document shape
  the parser targets (`SUM` for totals, `header.cloc_version` for the cache key) is correct.
  Real captured output lives in `internal/cloc/testdata/` and the parser is written against
  those bytes.

- **WAS ASSUMED — an empty result prints nothing.** It prints a bare **`{}`** with exit 0.
  Both blank output and `{}` are handled as a legitimate zero.

- **WAS ASSUMED — an unshared `/var/folders` scratch dir mounts empty on macOS.**
  *It did not reproduce.* Docker Desktop 29.6.2 mounts a `/var/folders/…` temp directory
  correctly; cloc counted through it. The guard was kept anyway — see below.

- **CONFIRMED, and worse than described — a nonexistent path also returns `{}` with exit 0.**
  cloc's answer for "the mount is empty" is byte-identical to its answer for "nothing
  matched". This is why the mount check cannot be a per-commit heuristic: a `src/` folder
  containing only images legitimately counts zero, so "zero from a non-empty tree" would
  produce false alarms. Instead `cloc.VerifyMount` runs **one canary container at startup**
  with a known answer. It costs ~0.4s and rules out an entire class of silently-wrong output,
  which is the failure mode worth paying for even though the trap did not reproduce here.

- **WAS ASSUMED — mount the scratch dir at `/tmp` in the container.** Changed to **`/loc`**.
  Mounting over the container's own temp directory leaves cloc nowhere to write. The
  in-container path is always absolute, so nothing depends on the image's `WORKDIR`.

---

## 6. Architecture

```
                  ┌──────────────────────────────────────────┐
   git repo ─────►│ gitlog.Commits(Options)                  │
                  │   git log --reverse [--first-parent]     │
                  └───────────────┬──────────────────────────┘
                                  │ []report.Commit (chronological)
                                  ▼
             ┌───────────────────────────────────────────────┐
             │ cloc.VerifyMount — one canary container,      │
             │ returns the cloc version that keys the cache  │
             └───────────────┬───────────────────────────────┘
                             ▼
                  ┌──────────────────────────────────────────┐
                  │ pipeline.Run — N workers + reorder buffer │
                  └───────────────┬──────────────────────────┘
                                  │  per commit, in parallel:
              ┌───────────────────┼───────────────────┐
              ▼                   ▼                   ▼
      ┌───────────────┐   ┌───────────────┐   ┌───────────────┐
      │ cache.Get     │──►│ tree.Extract  │──►│ cloc.Runner   │
      │  hit → skip   │   │ git archive   │   │ 2× docker run │
      │  the rest     │   │  → scratch    │   │  --json       │
      └───────────────┘   └───────────────┘   └───────┬───────┘
                                                      │ Output{Count,Version}
                                  ┌───────────────────┘
                                  ▼  reordered to commit order,
                                     delta computed at emit time
                  ┌──────────────────────────────────────────┐
                  │ report.Record  {sha,time,product,test,Δ} │
                  └───────────────┬──────────────────────────┘
                                  ▼
                  ┌──────────────────────────────────────────┐
                  │ writer.Writer   Write(Record) / Close()  │
                  └──┬──────────────┬─────────────────┬──────┘
                     ▼              ▼                 ▼
                  Console          File             Graph
                  (streams)      (csv/ndjson)   (buffers → HTML)
                     └──────── MultiWriter ────────────┘
```

Everything above `Writer` is orchestration; everything below is rendering. That seam is the
extension point — a fourth sink is purely additive: implement `Writer`, add its name to
`knownSinks` in `main.go`, and construct it in the switch in `execute`.

---

## 7. Package layout

```
loc-history/
  go.mod                    module github.com/mcklmo/loc-history (go 1.26.5, no dependencies)
  main.go                   flags → validation → wiring → pipeline.Run
  main_test.go
  doc/plan/                 system.md (this file), TODO.md
  internal/
    report/                 Commit, Count, Record, Finalize, ShortSHA
    gitlog/                 Commits(Options) — enumerate a branch
    tree/                   Extract — materialise one commit
    cloc/
      runner.go             Runner interface, Options, defaults
      docker.go             DockerRunner, error classification, VerifyMount
      parse.go              cloc --json → Output
      fake.go               FakeRunner — a real local line counter
      testdata/             REAL captured cloc 1.98 output
    cache/                  content-addressed counts on disk
    pipeline/               worker pool + reorder buffer
    writer/
      writer.go             Writer interface + MultiWriter
      console.go            streaming table
      file.go               csv / ndjson
      graph.go              calendar heat map view model
      graph_template.go     the self-contained page
      testdata/golden.html
    gittest/                throwaway repositories for tests
```

**131 test functions.** Everything except the `cloc` container tests runs without Docker and
without a network; the container tests skip themselves under `-short` or a stopped daemon.

---

## 8. Core types

```go
// internal/report

type Commit struct {
    SHA       string
    Timestamp time.Time // COMMITTER date — see §9
    Author    string
    Subject   string
}

type Count struct {
    Files, Code, Comment, Blank int
}
func (c Count) Add(o Count) Count

type Record struct {
    SHA, Short string
    Timestamp  time.Time
    Author     string
    Subject    string

    Product Count // cloc --not-match-f=<testRegex>
    Test    Count // cloc --match-f=<testRegex>

    TotalCode int  // Product.Code + Test.Code
    Delta     int  // signed change against the previous record
    Skipped   bool // source folder absent at this commit
}
func (r *Record) Finalize(prevTotal int)
```

`Commit` lives in `report`, not `gitlog`, so the domain types sit in one package and nothing
imports a producer for a type alone. `gitlog.Commit` is an alias.

`Delta` is what the original request called "lines added". **It is signed.** A refactor that
deletes more than it adds yields a negative value, and the heat map renders that honestly
rather than clamping to zero.

`Finalize` takes the previous total explicitly because only the emitter knows commit order.

```go
// internal/writer

type Writer interface {
    Write(r report.Record) error
    Close() error
}

func MultiWriter(ws ...Writer) Writer
```

`MultiWriter` offers each record to **every** sink even after one fails, returning the first
error — a broken console should not cost you the HTML report. `Close` closes all of them
regardless, joining errors with `errors.Join`, so a failing file sink cannot leak the graph
sink's file handle. `pipeline.Run` calls `Close` exactly once via `defer`, **including on
every error path**, so an aborted run still leaves a partial artifact.

### `Console` — streams, one line per commit

```
DATE        SHA      PRODUCT    TEST     TOTAL       Δ  SUBJECT
2026-08-09  fb31941        -       -         -      +0  chore: scaffold Go module, git repo…
2026-08-09  f9d02a4      145     320       465    +465  feat(report,writer): domain types, …
2026-08-09  bfb7e01      225     536       761    +296  feat(gitlog): enumerate branch comm…
```

Dashes, not zeroes, where the source folder was absent: zero and absent are different facts.

### `File` — `--file-format=csv` (default) or `ndjson`

CSV columns: `sha,short,timestamp,author,subject,product_code,test_code,total_code,delta,skipped`.
The tenth column is beyond the original nine because without it an absent folder and an empty
one both read as zero. NDJSON emits the whole `Record` per line, keeping the file/comment/blank
counts the CSV projection drops.

### `Graph` — buffers, renders on `Close`

One self-contained HTML file: inlined CSS and SVG, no `<script>`, `<link>`, `<img>` or
external URL anywhere, so it opens straight from disk. A test asserts exactly that.

A GitHub-style calendar heat map — columns are ISO weeks, rows are weekdays, one cell per
calendar day, **cell value is the summed `Delta` of every commit that day**.

Because that value is signed the scale is **diverging**: blue for growth, red for deletion, a
neutral gray midpoint, four steps per arm. A day with no commits is a fourth, distinct colour
from a day that netted zero. Intensity steps sit at the **quartiles of daily magnitude**, not
linear bands — one enormous initial commit would otherwise flatten the whole rest of the
history to the palest step.

Colours are CSS custom properties rather than SVG `fill` attributes, which is what lets one
document serve both colour schemes: `prefers-color-scheme` plus a `data-theme` override, with
dark mode a *selected* set of steps from the same ramps rather than an inversion.

Every cell carries a `<title>` with its date, net change, commit count and subjects; a
`<details>` table view lists every commit, so no value is reachable only by hovering.

> The palette comes from the `dataviz` skill's reference instance. Its documented blue
> sequential ramp is the growth arm; it documents no red ramp, so the deletion arm is
> **computed** — each step takes its blue counterpart's OKLCH lightness at the documented
> red's hue, chroma scaled by the ratio between the two documented poles. The gate for a
> diverging ramp is lightness monotonicity (running the *categorical* validator on a ramp
> fails by design); verified `|L − L_mid|` rises monotonically along each arm in both modes.

---

## 9. Component notes

### `gitlog`

```
git [-C <repo>] log --reverse --format=%H%x1f%cI%x1f%an%x1f%s [--first-parent] [-n N] [<branch>]
```

- `--reverse` gives chronological order, so `Delta` is a simple running difference.
- **Committer date (`%cI`), not author date.** Author dates run backwards after a rebase,
  which would scramble the time axis and produce nonsense deltas.
- `-n` is passed alongside `--reverse`; git applies it first, so `--limit` means "the most
  recent N commits", still oldest-first.

### `tree`

```go
type Result struct {
    Found bool // the folder exists at this commit
    Files int  // regular files written
}
func Extract(repo, sha, folder, dest string) (Result, error)
```

`git archive` rather than `checkout` or `worktree`: it is the only one of the three that
never mutates anything. `checkout` would destroy uncommitted work; `worktree add` leaves
state under `.git` that leaks if the process dies. Restricting it to a pathspec keeps each
extraction to the source folder rather than the whole repository.

The stream is read with `archive/tar` from the standard library instead of piping to `tar`:
one less external dependency, plus the file count comes free and archive entries that would
escape the destination can be rejected.

Revision validity is checked with `rev-parse --verify` *before* probing the path, because git
reports a missing 40-character SHA and a missing path with the **identical** message
(`fatal: path 'src' does not exist in '<sha>'`). Splitting the question in two means the path
probe's exit status can be trusted on its own, with no error-message matching.

A folder absent at a commit returns `Found=false` and no error — early commits legitimately
predate the source directory.

### `cloc`

```go
type Runner interface {
    Count(ctx context.Context, hostDir, folder string, opts Options) (Output, error)
}
type Options struct {
    TestRegex string // "" means no filter
    OnlyTests bool   // selects --match-f over --not-match-f
    Image     string
}
type Output struct {
    Count   report.Count
    Version string // keys the cache
    Empty   bool   // cloc matched nothing
}
```

```
docker run --rm -v <hostDir>:/loc <image> --json --quiet --not-match-f=<re> /loc/<folder>
docker run --rm -v <hostDir>:/loc <image> --json --quiet     --match-f=<re> /loc/<folder>
```

Default `--test-regex`: `\.(test|spec)\.[mc]?[jt]sx?$`. cloc matches it against the basename.

A stopped daemon is classified into `ErrDaemonUnavailable` with an actionable message rather
than surfacing as a JSON parse failure three layers down.

`FakeRunner` is a **working local line counter**, not a table of canned answers: given a real
extracted tree it returns real numbers with the same product/test split, so pipeline tests
exercise git extraction and ordering together and still finish in milliseconds.

### `pipeline`

Container startup dominates runtime, so `--jobs` commits are processed concurrently (default
4). Results therefore complete **out of order** while `Delta` is a running difference and
every sink expects oldest-first. A **reorder buffer** reconciles the two: completed results
wait in a map keyed by commit index and are released only as the next expected index becomes
available. **`Delta` is computed at emit time, never inside a worker** — a worker has no
reliable view of its predecessor.

> The ordering test is the load-bearing one, and it was mutation-checked: replacing the
> reorder buffer with emit-on-arrival makes it fail. Without randomised `FakeRunner` delays
> it would pass vacuously.

Each commit gets **its own** scratch directory under `--work-dir`, not one per worker. A
reused directory would keep files that the next commit deleted, silently flattening every
deletion; there is a test that would catch it.

Error policy: a failed commit does **not** abort the run — it is reported on stderr and
skipped, because one bad commit should not cost a multi-minute rebuild. `--fail-fast` opts
into cancelling the context on the first error. A *write* failure does abort: the sink is the
point of the exercise.

### `cache`

A commit tree is immutable, so `(sha, folder, testRegex, clocVersion)` → counts is a pure
function and permanently valid. Entries are JSON under `--cache-dir`, keyed by a SHA-256 of
those four inputs plus a schema tag, sharded by the first two hash characters, written by
atomic rename. The lookup happens **before** extraction, so a hit skips the `git archive` as
well as both containers.

The cloc version comes from the preflight container, which was already being run to check the
bind mount — keying the cache costs no extra container. Failures are never stored; corrupt
entries degrade to a recount rather than to a wrong number; a cache that cannot be written is
reported but does not fail the run.

Default location is `os.UserCacheDir()/loc-history` — `~/Library/Caches/loc-history` on
macOS — rather than the plan's Linux-flavoured `~/.cache`.

---

## 10. CLI surface

```
loc-history [flags]

  --repo string          repository path (default ".")
  --branch string        branch to walk (default "main")
  --folder string        source folder to count (default "src"; empty = whole tree)
  --test-regex string    (default `\.(test|spec)\.[mc]?[jt]sx?$`)

  --out string           sinks, comma-separated: console,file,graph (default "console")
  --file-out string      (default "loc-history.csv")
  --file-format string   csv | ndjson (default "csv")
  --graph-out string     (default "loc-history.html")

  --jobs int             concurrent commits (default 4)
  --work-dir string      scratch root (default "/tmp")
  --cache-dir string     (default ~/Library/Caches/loc-history on macOS)
  --no-cache             recompute everything
  --first-parent         follow trunk only (default true)
  --limit int            most recent N commits, 0 = all
  --fail-fast            abort on first commit error
  --image string         (default "aldanial/cloc")
```

`--out=console,graph` composes via `MultiWriter` — one walk, two artifacts. Everything is
validated before any work starts, so a typo in `--file-format` costs nothing.

Ctrl-C cancels the context rather than killing the process, so the sinks still close.

---

## 11. Verification

### Unit — `go test ./...` (add `-race`; both are clean)

The load-bearing assertions:
- `product.Code + test.Code == total.Code` for a tree counted three ways — asserted against
  captured cloc fixtures, against real containers, and against the fake.
- **Records reach the `Writer` in strict chronological order under `--jobs=8` with a
  `FakeRunner` sleeping for randomised durations.** Mutation-checked.
- Deltas are running differences and sum to the final `TotalCode`, including a genuine net
  deletion.
- An absent source folder yields `Skipped: true`, not an error.
- Deleted files do not leak between commits.
- Graph HTML matches `testdata/golden.html`, contains no external reference, and escapes
  hostile commit subjects.

### End-to-end — with Docker running

```bash
go build -o /tmp/loc-history .
RE='_test\.go$'

# 1. Smoke.
/tmp/loc-history --repo=. --folder=internal --test-regex="$RE" --limit=3

# 2. Correctness anchor: 1933 product / 2162 test / 4095 total at 19c88c7.
/tmp/loc-history --repo=. --folder=internal --test-regex="$RE" --limit=1

# 3. Full run, all three sinks.
/tmp/loc-history --repo=. --folder=internal --test-regex="$RE" --out=console,file,graph

# 4. Cache proof.
time /tmp/loc-history … --out=file --file-out=/tmp/a.csv    # 2.776s cold
time /tmp/loc-history … --out=file --file-out=/tmp/b.csv    # 0s warm
diff /tmp/a.csv /tmp/b.csv

# 5. The working tree must be untouched.
git status --porcelain
```

All of the above were run. Results: the anchor matches three independent cloc queries; the
cold run took 2.776s and the warm run 0s with byte-identical output; the working tree was
clean afterwards.

### Failure modes exercised by hand

| Case | Behaviour |
|---|---|
| Docker daemon stopped | One actionable line: `docker daemon unavailable: start Docker Desktop and try again: …` |
| `--folder=nope` | Every commit skipped; structurally valid output with dashes, exit 0 |
| `--work-dir` on `/var/folders` | Counted correctly — the assumed macOS mount trap did not reproduce on Docker Desktop 29.6.2. The preflight canary stays as insurance. |
| Heat map in both colour schemes | Rendered and inspected; dark and light both correct, ramp direction inverts per surface |

---

## 12. Deliberately out of scope

Per-language breakdown (cloc already returns it; `Count` would just need widening),
incremental append to an existing CSV, and any sink beyond the three specified. The `Writer`
seam means each is purely additive later.
