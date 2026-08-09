# loc-history

Chart how a codebase's **product code** and **test code** grew, commit by commit, over the
life of a branch.

For every commit on a branch, oldest to newest, `loc-history` materialises that commit's
source folder into a scratch directory, runs `cloc` twice inside Docker — once excluding test
files, once counting only test files — and emits one row per `--granularity` time bucket to a
console table, a CSV/NDJSON file, or a self-contained HTML page charting the two over time.

```console
$ loc-history --repo=~/code/my-app --folder=src --branch=main
HOUR              SHA      COMMITS  PRODUCT    TEST     TOTAL       Δ  SUBJECT
2026-06-22 14:00  3a20694        1      887       0       887    +887  udpate io
2026-06-24 09:00  25eca54        2      895       0       895      +8  fix: use comma in csv o…
5 commits, 3 skipped, 0 failed in 790ms
```

It **never touches the target repository**. Trees are materialised with `git archive`, so
uncommitted edits, untracked files and the current checkout all survive a run untouched.

---

## Requirements

| | |
|---|---|
| `go` | 1.26 or newer — only to build. The binary has no runtime Go dependency. |
| `docker` | Must be **running**. `cloc` executes in a container; nothing is installed on your machine. |
| `git` | Any recent version, on `PATH`. |

The tool itself has **zero Go dependencies** — standard library only.

On the first run Docker pulls `aldanial/cloc` (~50 MB). That image is amd64-only, so on Apple
silicon every container runs under emulation and container startup dominates the runtime.

---

## Install

This module has no public remote, so install it from a local clone:

```bash
git clone <this-repo> ~/Projects/personal/loc-history
cd ~/Projects/personal/loc-history
go install .
```

`go install .` puts the binary in `$(go env GOPATH)/bin` — `~/go/bin` by default. Make sure
that is on your `PATH`:

```bash
echo 'export PATH="$PATH:$(go env GOPATH)/bin"' >> ~/.zshrc && source ~/.zshrc
loc-history --help
```

Prefer not to install globally? Build it anywhere and call it by path:

```bash
go build -o /tmp/loc-history .
/tmp/loc-history --repo=~/code/my-app
```

---

## Running it against a different repository

This is the main use case: `loc-history` is general-purpose and expects to be pointed at
somebody else's tree.

### Two equivalent ways

```bash
# 1. Point at it from anywhere — the CLI's own cwd is irrelevant.
loc-history --repo=~/code/my-app --folder=src --branch=main

# 2. Or cd into the target repo and let --repo default to "."
cd ~/code/my-app
loc-history --folder=src
```

Output files (`--file-out`, `--graph-out`) are written **relative to your current directory**,
not to the target repo — so nothing lands inside the repository you are measuring unless you
ask for it.

### The three flags that matter per repository

Everything else has a sane default. These three depend on the repo you are pointing at, and
getting them wrong is the cause of nearly every surprising result:

| Flag | Default | Set it when |
|---|---|---|
| `--branch` | `main` | The repo's trunk is `master`, `develop`, … A wrong name is a hard error, not a silent empty run. |
| `--folder` | `src` | The code does not live in `src/`. Pass `--folder=` (empty) to count the **whole tree**. |
| `--test-regex` | `\.(test\|spec)\.[mc]?[jt]sx?$` | The project is not JS/TS. The regex is matched against the **basename**. |

`--test-regex` is used **both ways over the same tree** — as `--not-match-f` for product code
and `--match-f` for test code — so the two halves are exact complements and always sum to the
total.

### `--test-regex` recipes

| Ecosystem | Flag |
|---|---|
| JS / TS (default) | `--test-regex='\.(test\|spec)\.[mc]?[jt]sx?$'` |
| Go | `--test-regex='_test\.go$'` |
| Python (pytest) | `--test-regex='(test_.*\|.*_test)\.py$'` |
| Java / Kotlin | `--test-regex='(Test\|Tests\|Spec)\.(java\|kt)$'` |
| Ruby (RSpec) | `--test-regex='_spec\.rb$'` |
| C# | `--test-regex='(Test\|Tests)\.cs$'` |

Quote it in single quotes so the shell does not eat the backslashes.

**Limitation — the regex sees basenames, not paths.** Projects that separate tests by
*directory* rather than by filename (Rust's `tests/`, Java's `src/test/java`, a top-level
`spec/`) cannot be split this way. Two options: run the tool twice with `--folder` pointing at
each tree in turn, or rely on a naming convention if the project also has one. Rust unit tests
written as inline `#[cfg(test)]` modules cannot be separated at all — `cloc` counts whole files.

### Worked example

```bash
cd ~/anywhere

loc-history \
  --repo=~/code/my-app \
  --branch=main \
  --folder=src \
  --test-regex='\.(test|spec)\.[mc]?[jt]sx?$' \
  --out=console,file,graph \
  --file-out=./my-app.csv \
  --graph-out=./my-app.html

open ./my-app.html
```

Start with `--limit=5` on an unfamiliar repository to confirm the folder and regex are right
before committing to a full history walk:

```bash
loc-history --repo=~/code/my-app --folder=src --limit=5
```

---

## Output sinks

`--out` takes a comma-separated list; one walk can feed several sinks at once
(`--out=console,file,graph`).

Every sink is fed the same buckets, so the three can never disagree about what a row is:
`--granularity` is applied once, above them all (see below).

**`console`** (default) — streams a table, one line per bucket, as the walk progresses. The
first column is headed by the unit in play (`HOUR`, `DATE`, or `BUCKET START` for a width like
`4h`) and `COMMITS` says how many commits merged into the row; the SHA and subject are the
bucket's **last** commit, so the row still names something you can go and look at. A dash
rather than a zero means the source folder did not exist at that commit: zero lines and no
folder are different facts.

**`file`** — `--file-format=csv` (default) or `ndjson`, written to `--file-out`.
CSV columns: `bucket_start,commits,last_sha,last_short,last_author,last_subject,product_code,test_code,total_code,product_delta,test_delta,delta,skipped`.
`bucket_start` is RFC 3339 at every granularity; the `last_*` columns are the bucket's last
commit; `product_code` and `test_code` are what let a spreadsheet reproduce the two charts, and
`product_delta` and `test_delta` the change behind them.
NDJSON emits one bucket per line **with its records nested**, so it stays lossless at any
granularity and keeps the file/comment/blank counts the CSV projection drops.

**`graph`** — one self-contained HTML file at `--graph-out`. Inlined CSS and SVG, no scripts,
no external URLs, so it opens straight from disk and survives being emailed.

Two area charts, one for product files and one for test files. Time runs along the x axis, one
slot per `--granularity` bucket; the series is the **running total** — the lines of code
standing at the end of each bucket. It is drawn as a **smooth line through every commit-bearing
bucket**, so a repo that only grows shows a curve that only rises. Only those buckets are
measured: between two of them the curve is interpolation, and a quiet stretch reads as a
gradual climb towards the next commit rather than as a claim about the days it crosses. The
interpolation is monotone cubic, so the curve passes through every point and can never bulge
past a peak or dip below the point before it.
Both charts sit on **one shared y scale** standing on zero, so a 2,000-line product tree is
visibly ten times a 200-line test tree and the two are read against each other rather than side
by side.

### `--granularity`

`--granularity` takes `hour` (the default), `day`, or a bucket width in whole hours like `4h`,
and it governs **every sink**, not just the graph: commits sharing a bucket merge into one
console row, one CSV row, and one column. The bucketing happens once, between the walk and the
sinks, so nothing downstream reinterprets it. There is no per-commit escape hatch — one
vocabulary, and NDJSON is where the individual commits survive.

The default is hourly because a day-wide bucket collapses an afternoon of work into a single
row, which on a young repo is the whole history. In the graph, the x axis labels itself in
whatever unit the span calls for — hours, days, or months — so three hours of work reads as
three hours.

A bucket has to **divide the day**: `1, 2, 3, 4, 6, 8, 12` or `24` hours, so `hour` is `1h` and
`day` is `24h`. Buckets are anchored at midnight — a `4h` axis runs `00:00, 04:00, …` — which
is what keeps the slots evenly spaced. `5h` would restart at every midnight and is refused.

Those values are **counts taken from the cloc snapshot of each commit, not diff line counts**: a
commit that rewrites 100 lines in place leaves the total where it was. A bucket whose folder was
absent counts 0, so the area genuinely drops to the floor there. Every commit-bearing bucket
carries a tooltip on hover and on keyboard focus, and two `<details>` tables — one by bucket,
one by commit — list every charted number alongside the per-bucket deltas the chart no longer
draws, so nothing is reachable only by hovering.

The whole history always fits the card; there is no horizontal scrolling. A history dense enough
to put a bucket under a unit wide gets a 4-unit floor on its hit target, so every bucket stays
hoverable at any span.

---

## Flag reference

```
loc-history [flags]

  --repo string          repository path (default ".")
  --branch string        branch to walk (default "main")
  --folder string        source folder to count (default "src"; empty = whole tree)
  --test-regex string    splits test from product, matched against the basename
                         (default `\.(test|spec)\.[mc]?[jt]sx?$`)

  --out string           sinks, comma-separated: console, file, graph (default "console")
  --file-out string      path for the file sink (default "loc-history.csv")
  --file-format string   csv | ndjson (default "csv")
  --graph-out string     path for the graph sink (default "loc-history.html")
  --granularity string   output time bucket: hour | day | Nh, e.g. 4h (default "hour")
                         N must divide 24: 1, 2, 3, 4, 6, 8, 12, 24
                         applies to every sink, not just the graph

  --jobs int             commits processed concurrently (default 4)
  --work-dir string      scratch root; must be a path Docker may bind-mount (default "/tmp")
  --cache-dir string     where counted commits are remembered
                         (default ~/Library/Caches/loc-history on macOS)
  --no-cache             recompute everything, ignoring stored counts
  --first-parent         follow the trunk only (default true)
  --limit int            most recent N commits; 0 means all
  --fail-fast            abort on the first commit that fails
  --image string         cloc container image (default "aldanial/cloc")
```

Every flag is validated **before any work starts**, so a typo in `--file-format` costs nothing.
Ctrl-C cancels the walk rather than killing it, so the sinks still close and a partial artifact
survives.

---

## Caching

A commit tree never changes, so `(sha, folder, test-regex, cloc version)` → counts is a pure
function and permanently valid. Results are cached under `--cache-dir` and looked up **before**
extraction, so a hit skips the `git archive` and both containers.

In practice: the first run over a repository costs a container round trip per commit; re-running
after a few new commits only pays for the new ones.

```console
$ time loc-history --repo=~/code/my-app --folder=src --limit=2   # cold
2 commits, 0 skipped, 0 failed in 834ms

$ time loc-history --repo=~/code/my-app --folder=src --limit=2   # warm
2 commits, 0 skipped, 0 failed in 0s
```

Change the folder or the regex and you are asking a different question, so the cache correctly
misses. `--no-cache` forces a full recount. Failed commits are never cached, and a corrupt entry
degrades to a recount rather than to a wrong number.

---

## Troubleshooting

| Symptom | Cause and fix |
|---|---|
| `docker daemon unavailable: start Docker Desktop and try again` | Docker is installed but not running. |
| `git log main in …: fatal: ambiguous argument 'main'` | The target repo's trunk is not `main`. Pass `--branch=master` (or whatever `git -C <repo> branch --show-current` reports). |
| Fewer rows than commits | Expected: a row is a `--granularity` bucket, and its `COMMITS` column says how many merged into it. Narrow the bucket, or use `--file-format=ndjson`, whose nested `records` keep every commit. |
| Every row is dashes, `N skipped` | `--folder` does not exist at those commits. Either the path is wrong (`--folder=src` is the default and many repos have no `src/`), or the early history genuinely predates the directory. Try `--folder=` to count the whole tree. |
| `TEST` is always `0` | `--test-regex` does not match this project's test files. See the recipes above; remember it matches the **basename**, not the path. |
| `scratch directory is not visible inside the container` | `--work-dir` points somewhere Docker Desktop does not share. Use the default `/tmp`, or add the path in Docker Desktop → Settings → Resources → File sharing. |
| Slow on Apple silicon | `aldanial/cloc` is amd64-only and runs emulated. Raise `--jobs` (container startup, not CPU, is the bottleneck) and rely on the cache for repeat runs. |
| A few commits fail | Failures are reported on stderr and skipped, so one bad commit does not cost a multi-minute walk. Use `--fail-fast` to abort on the first one instead. |

---

## Development

```bash
go test ./...          # 166 test functions
go test -race ./...    # also clean
go test -short ./...   # skips the container tests
```

Everything except the `cloc` container tests runs without Docker and without a network; the
container tests skip themselves under `-short` or a stopped daemon.

Adding a fourth sink is purely additive: implement `writer.Writer`, add its name to
`knownSinks` in [main.go](main.go), and construct it in the switch in `execute`.

The design, the decisions behind it, and the assumptions that turned out to be wrong are
recorded in [doc/plan/system.md](doc/plan/system.md).
