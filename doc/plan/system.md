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
   the previous commit), which is folded into its `--granularity` time bucket and handed to a
   pluggable sink.

Three sinks ship: a console table, a file (CSV/NDJSON), and a self-contained HTML page that
charts net change per time bucket for product and test code. Counted commits are cached, so re-running
after a few new commits is instant.

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
| 4 | Graph output | **Self-contained HTML file** (inlined CSS/SVG, no network) | ANSI blocks in the terminal; a standalone `.svg` |
| 5 | Go installation | Official tarball to `/usr/local/go` | Homebrew; a version manager |
| 6 | Test corpus | This repository and synthetic fixtures | A neighbouring `timeseries-visualizer` project, later ruled out of scope |
| 7 | Graph form | **Two step-area charts of the running total over time** — product and test, one shared y scale standing on zero | A GitHub-contributions calendar heat map (shipped first, superseded); diverging column charts of the per-bucket delta (superseded in turn); two-sided add/remove bars; a dual axis |
| 8 | Graph time bucket | **`--granularity=hour` (default), `day`, or a width in hours like `4h`**, with a floored hit-target width and an adaptive x-axis unit | A wider viewBox with horizontal scrolling; month labels for `day` and a special case for `hour`; `hour:4` and `"hour 4"` spellings; accepting widths that do not divide 24 |

On decision 2: the original request said output is "written to a `Reader` interface", but all
three implementations are writers. The user resolved the contradiction in favour of the
conventional name.

Decision 3 survived contact with reality; only the flag name in the plan was wrong (§5).

On decision 7: the first shipped graph *was* the calendar heat map — ISO-week columns,
weekday rows, one tinted cell per day. It answered "how often" well and "how much" badly: a
quartile-bucketed tint cannot show that one day is ten times another, and it merged product
and test into a single number. The user asked for the GitHub *code-frequency* shape instead.
The heat map is gone, not deprecated. Where the numbers come from did **not** change — still
the cloc snapshots — so nothing upstream of `internal/writer` moved.

That code-frequency shape was superseded in turn. Charting each bucket's *delta* answered "how
much changed" but never "how big is this now": a history of uniformly positive deltas drew a row
of similar columns rather than a curve that climbs. The user asked for the **level** instead —
"the graph should display the total rows, not the total added rows" — so the two small multiples
now plot the running total as step areas. The deltas did not become less true, only less
charted: they still carry the tables, the CSV and the bucket tooltip. Again nothing upstream of
`internal/writer` moved, because the running total was already on every bucket.

On decision 8: one column per calendar day broke short histories. This repository's own `main`
at the time was 14 commits across three hours of one afternoon, which day-wide bucketing draws
as a single step under a lone `Mar 2026` label. Hourly is therefore the default. Two
consequences were chosen deliberately. Sub-unit slots are **floored, not scrolled**: growing
the viewBox and wrapping the SVG in `overflow-x: auto` would reverse decision 7's "the whole
history fits the card" — instead a hit target is never drawn under 4 units, so every bucket
stays hoverable however dense the stretch. (The column floor this once also needed went with
the columns; the step area has no minimum width to hold.) And
the x-axis unit **adapts for both granularities** rather than special-casing `hour`, which does
change the daily rendering: a fortnight now carries day labels instead of one bare `Mar 2026`.

> **WAS ASSUMED — granularity is a graph concern only.** This document previously recorded that
> "the console and file sinks emit one row per commit and have no notion of buckets". That
> stopped being true: the user asked for `--granularity` to govern **every** sink. Bucketing
> moved out of `internal/writer/graph.go` into a new package, `internal/bucket`, and now happens
> **above** the `Writer` interface — so no sink derives it and none of the three can disagree
> about what a row is. Writers render what they are handed; none of them truncates a timestamp
> or groups a record. Three decisions came with it, all the user's: console and CSV emit one row
> per bucket keeping the bucket's **last** commit as its identity plus a commit count; NDJSON
> emits one bucket object per line with its records **nested**, so it stays lossless at any
> width; and there is **no per-commit escape hatch** — no `--granularity=commit`, one
> vocabulary, `hour` still the default. Nothing about how a bucket is computed changed, which is
> why all three golden pages still match byte for byte after the move.

Arbitrary widths (`4h`) came later. Two things fell out of the constraint that a bucket must
**divide 24**. First, `day` is exactly the 24-hour bucket anchored at midnight, so the enum is
just an hour count and `hour`/`day` are names for `1h`/`24h` — one vocabulary, not two. Second,
widths like `5h` are refused rather than supported: truncation floors the hour from midnight,
so a 5-hour bucket would restart every night and leave bucket starts *between* the axis slots,
where they would be looked up, missed, and silently never drawn. `bucket.NewAggregator` enforces the same rule as the flag
layer, because a library caller can name a bucket the CLI would have rejected. The rejected
alternative was anchoring the lattice on the epoch, which admits any width but detaches buckets
from the clock.

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
                  │ bucket.Aggregator — THE GRANULARITY GATE │
                  │  truncate → fold → flush on rollover     │
                  └───────────────┬──────────────────────────┘
                                  │ bucket.Bucket {start,gran,commits,Δs,records}
                                  ▼
                  ┌──────────────────────────────────────────┐
                  │ writer.Writer   Write(Bucket) / Close()  │
                  └──┬──────────────┬─────────────────┬──────┘
                     ▼              ▼                 ▼
                  Console          File             Graph
                  (streams)      (csv/ndjson)   (buffers → HTML)
                     └──────── MultiWriter ────────────┘
```

Everything above `Writer` is orchestration; everything below is rendering. That seam is the
extension point — a fourth sink is purely additive: implement `Writer`, add its name to
`knownSinks` in `main.go`, and construct it in the switch in `execute`.

The aggregator sits **above** that seam, which is what makes granularity one decision rather
than three: it is the only place a timestamp becomes a bucket, and every sink renders what it
is handed. It **streams** — records arrive oldest first, so a bucket is flushed as soon as a
record with a later start turns up, and the open one on `Close` — so the console still prints
as the walk progresses, one bucket behind, rather than buffering the whole history. `pipeline`
therefore no longer imports `writer` at all; it takes a local `Sink` interface over
`report.Record`, and `bucket.Aggregator` is what `main` hands it.

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
    pipeline/               worker pool + reorder buffer; local Sink interface
    bucket/
      granularity.go        Granularity: parse, Valid, Step, Truncate, and the label vocabulary
      bucket.go             Bucket, Sink, streaming Aggregator — the granularity gate
    writer/
      writer.go             Writer interface + MultiWriter
      console.go            streaming table
      file.go               csv / ndjson
      graph.go              chart geometry + view model
      graph_template.go     the self-contained page
      testdata/golden*.html
    gittest/                throwaway repositories for tests
```

**166 test functions.** Everything except the `cloc` container tests runs without Docker and
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
deletes more than it adds yields a negative value, and the graph renders that honestly rather
than clamping to zero.

`Finalize` takes the previous total explicitly because only the emitter knows commit order.

```go
// internal/bucket

type Granularity int // hours per bucket; must divide 24
const (
    GranularityHour Granularity = 1
    GranularityDay  Granularity = 24
)
func ParseGranularity(s string) (Granularity, error) // "hour" | "day" | "4h"

type Bucket struct {
    Start   time.Time   // floored commit time, relabelled UTC
    Gran    Granularity // self-describing: the sinks read the width here
    Commits int

    Product   report.Count // the tree at the bucket's LAST commit
    Test      report.Count
    TotalCode int

    ProductDelta int // summed net change across the bucket's commits;
    TestDelta    int // ProductDelta + TestDelta == Delta
    Delta        int

    Skipped bool // the folder was absent at the bucket's last commit

    Records []report.Record
}
func (b Bucket) Last() report.Record // never empty by construction

type Sink interface {                 // writer.Writer satisfies this
    Write(Bucket) error               // structurally, so bucket never
    Close() error                     // imports writer and there is no cycle
}

func NewAggregator(g Granularity, sink Sink) (*Aggregator, error)
func (a *Aggregator) Write(r report.Record) error
func (a *Aggregator) Close() error
```

A bucket keeps its **last** commit's identity because a row still has to name something you can
go and look at, and the snapshot columns describe the tree as it stood when the slice of time
ended. `Records` is what keeps NDJSON lossless at any width.

The aggregator rolls over only on a **strictly later** start. `pipeline` emits oldest-first,
but `git log --reverse` follows the commit graph rather than committer dates, so a record can
in principle arrive with an earlier timestamp; folding it into the open bucket keeps one bucket
per lattice slot, where opening a second with an already-emitted start would give the graph two
rows for one slot and its lookup would silently drop one. `Close` flushes the open bucket and
then closes the sink **regardless** of whether the flush failed, joining both — which is what
preserves the close-exactly-once guarantee through the new layer.

```go
// internal/writer

type Writer interface {
    Write(b bucket.Bucket) error
    Close() error
}

func MultiWriter(ws ...Writer) Writer
```

`MultiWriter` offers each bucket to **every** sink even after one fails, returning the first
error — a broken console should not cost you the HTML report. `Close` closes all of them
regardless, joining errors with `errors.Join`, so a failing file sink cannot leak the graph
sink's file handle. `pipeline.Run` calls `Close` exactly once via `defer`, **including on
every error path**, so an aborted run still leaves a partial artifact.

### `Console` — streams, one line per bucket

```
HOUR              SHA      COMMITS  PRODUCT    TEST     TOTAL       Δ  SUBJECT
2026-08-09 15:00  fb31941        1        -       -         -      +0  chore: scaffold Go mod…
2026-08-09 16:00  f9d02a4        3      145     320       465    +465  feat(report,writer): d…
2026-08-09 20:00  bfb7e01        2      225     536       761    +296  feat(gitlog): enumerat…
```

The header is written lazily, on the first bucket, which is what lets its first column name the
unit actually in play — `Column()` yields `Hour` / `Date` / `Bucket start`. SHA and subject come
from `Last()`. Dashes, not zeroes, where the source folder was absent: zero and absent are
different facts.

### `File` — `--file-format=csv` (default) or `ndjson`

CSV columns:
`bucket_start,commits,last_sha,last_short,last_author,last_subject,product_code,test_code,total_code,product_delta,test_delta,delta,skipped`.

The header is fixed rather than derived from the granularity, because it goes out before the
first bucket exists and so cannot name the unit; `bucket_start` is RFC 3339 at every width
instead. The `last_*` prefixes are deliberate — the semantics changed, so the names should too,
rather than silently repurposing `sha`. `product_code`/`test_code` let a spreadsheet reproduce
the two charts, and `product_delta`/`test_delta` the change behind them. `skipped` is beyond
the original specification because without it an
absent folder and an empty one both read as zero.

NDJSON emits the whole `Bucket` per line with its `Records` **nested**, which is what keeps the
file lossless however wide the bucket, and keeps the file/comment/blank counts the CSV
projection drops.

### `Graph` — buffers, renders on `Close`

One self-contained HTML file: inlined CSS and SVG, no `<script>`, `<link>`, `<img>` or
external URL anywhere, so it opens straight from disk. A test asserts exactly that.

Two **step-area charts** as small multiples — *Product files* above, *Test files* below. The x
axis is a linear run of buckets from the first commit's bucket to the last, inclusive, so quiet
stretches take up room. Each series is the **running total**: the lines of code standing at the
end of each bucket, which is that bucket's last commit's snapshot.

It is drawn as a **step** — the level holds flat across a quiet stretch and steps where a commit
moved it — so a history of positive deltas *rises* rather than reading as a row of similar
columns, which is what the chart is for. Equal-valued runs collapse into a single segment, so
the path is sized by the commits rather than by the span: an hourly year is 8,760 slots but only
as many steps as there are commit-bearing buckets. Each chart is two `<path>`s sharing one point
list — a tinted fill closed down to the baseline, and the top edge alone, so the series stays
crisp where the fill is only a tint.

The graph **accumulates nothing**: `Bucket.Product`, `Bucket.Test` and `Bucket.TotalCode` are
already the end-of-bucket snapshot, so the change was geometry only. A `Skipped` bucket's counts
are genuinely 0 because the folder was absent at its last commit, so the area drops to the floor
there rather than being special-cased — that is what the snapshot says.

The graph **derives no bucketing of its own**. It buffers the `bucket.Bucket`s it is handed and
reads the width off the first of them — `GraphOptions` has no `Granularity` field, so the page
cannot name one unit while charting another. An empty history has nothing to read it off and
falls back to hourly.

A bucket is an **hour** by default, a **calendar day** under `--granularity=day`, or any width
in hours that divides 24 (`4h`). `bucket.Granularity` is simply that hour count:
`GranularityHour` is 1 and `GranularityDay` is 24, because a 24-hour bucket anchored at midnight
*is* a calendar day. It carries the rest of the difference as methods (`Step`, `Noun`, `Column`,
`TitleFormat`, `RowFormat`), so no call site branches on it.

`Granularity.Truncate` is the single point where a timestamp becomes a bucket: it reads the
wall clock in the commit's own zone — git hands back `%cI` with its offset intact — floors the
hour to a multiple of the bucket width counting from midnight, and relabels the result as UTC.
So a commit at `23:00+02:00` buckets on its author's own evening, and because every bucket time
is then UTC, all downstream arithmetic is exact and no DST discontinuity can shorten a step.

Dividing 24 is a **correctness** constraint, not a style rule. The axis reads slot *i* as
`first + i×step`; a width that does not tile the day (`5h`) would restart the sequence at every
midnight, putting bucket starts between slots where they would be looked up, missed, and never
drawn. `ParseGranularity` and `NewAggregator` both refuse those widths.

Per record, in the order the aggregator receives them:

```
productΔ = r.Product.Code − prevProduct
testΔ    = r.Test.Code    − prevTest
```

with `prevProduct`/`prevTest` starting at 0 and running *across* bucket boundaries — including
across `Skipped` commits, whose counts are 0 — mirroring `Finalize`'s `prevTotal` convention.
That makes `productΔ + testΔ == Delta` hold for every record and for every bucket, so the
charts, the two table views and the CSV can never disagree. A test asserts it. The deltas are no
longer *charted*, but they remain the spine of the tables, the CSV and the bucket tooltip.
**No `git diff --numstat` is involved**: these are counts read off tree snapshots, so a commit
that rewrites 100 lines in place leaves the total where it was. The figure caption says so.

The **y scale is shared by both charts and stands on zero**: `yMax` is the largest total across
*both* series, rounded up to 1/2/5 × 10ⁿ with a floor of 1 so a flat history cannot divide by
zero. A line count is never negative, so the series sits on the floor of the plot and the whole
plot height is signal — twice the vertical resolution a symmetric axis would have, and the axis
labels are unsigned. Same lines-per-pixel in both charts, so a 2,000-line product tree is
visibly ten times a 200-line test tree. Never a dual axis. The shared x geometry — first slot,
span, pitch, `yMax`, granularity — travels as one `axis` value, which is what keeps the small
multiples comparable.

The viewBox is fixed (952 × 214) and the SVG is sized at 100% width, so the whole history
always fits the card — no horizontal scrolling, and the two charts stay time-aligned by
construction. Because the SVG scales with the card, the series line is drawn with
`vector-effect: non-scaling-stroke`, so it stays a hairline on a wide screen rather than
thickening with the viewBox.

Because an hourly year is 8,760 slots across a 898-unit plot — a slot a tenth of a unit wide —
a hit target is never drawn under **4 units**. A floored width can outgrow its own slot, so it
goes through `slotSpan`, which centres it and holds it inside the plot; whenever the width fits
its slot that is the plain centring it replaces, to the byte.

The x axis labels itself in the finest unit that does not flood it — **hour → day → month**,
chosen from the span rather than the granularity, with month as the floor. Each label claims
the room its own text needs; one landing inside the previous claim, or running past the right
edge, is dropped rather than drawn on top of its neighbour, and is not retried at the next slot
of the same unit. Day and month labels carry the year on the first label and wherever the year
rolls over; hour labels do not, because the *Commits* tile already carries the date.

Colours are CSS custom properties rather than SVG `fill` attributes, which is what lets one
document serve both colour schemes: `prefers-color-scheme` plus a `data-theme` override, with
dark mode a *selected* set of steps rather than an inversion.

One transparent full-height hit target per commit-bearing bucket carries a `<title>` with the
bucket start, both categories' standing totals, the total and the net change that got there,
the commit count and the subjects — the same text on both charts, so either gives the whole
bucket. Hover and keyboard focus surface it alike. Two `<details>` table views, one by bucket
and one by commit, list every charted number; the by-bucket table carries the levels the chart
draws *and* the deltas it no longer draws, so neither reading is reachable only by hovering.

> The palette is three roles taken unchanged from the `dataviz` skill's reference instance: one
> series blue (categorical slot 1), plus the documented gridline and baseline greys. Both charts
> share the one accent deliberately — they are small multiples of the same measure on one scale,
> so varying colour between them would falsely imply the measure differs, and each series is
> already named by its `figcaption`. That is also why there is no legend: a two-swatch key would
> only repeat the captions. Validated against both surfaces with the skill's own script — every
> check passes, worst-pair CVD ΔE 21.6 light / 19.2 dark. The old eight-step ramps are gone,
> along with the red arm and the computed diverging pair they needed.

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

`pipeline` declares its own `Sink` interface over `report.Record` rather than importing
`writer`, so it does not know that buckets exist and cannot be broken by a change to them.
`bucket.Aggregator` is the shipped implementation `main` hands it.

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
  --granularity string   output time bucket: hour | day | Nh (default "hour")
                         N must divide 24: 1, 2, 3, 4, 6, 8, 12, 24
                         applies to every sink: one row per bucket, everywhere

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
- `productΔ + testΔ == Delta` per record *and* per bucket, at every granularity.
- A bucket reaches the sink only once no later record could still join it, the open one is
  flushed on `Close`, and the sink is closed exactly once even when that flush fails.
- Graph HTML matches `testdata/golden.html`, `golden-day.html` and `golden-4h.html` — the three
  survived the move of bucketing out of the graph **byte for byte, without `-update`**, which
  is the proof that the refactor changed no arithmetic. It contains no external reference and
  escapes hostile commit subjects.

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
| Graph in both colour schemes | Rendered and inspected; dark and light both correct, each a selected step rather than an inversion |

---

## 12. Deliberately out of scope

Per-language breakdown (cloc already returns it; `Count` would just need widening),
incremental append to an existing CSV, and any sink beyond the three specified. The `Writer`
seam means each is purely additive later.
