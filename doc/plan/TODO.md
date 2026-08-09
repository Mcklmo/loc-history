# loc-history — progress

Build order follows `system.md` §11. Every feature is TDD: test first, then implementation,
then a commit.

## Status

- [x] **0. Toolchain** — go1.26.5 installed from the official go.dev tarball to `/usr/local/go`
      (not brew, not a version manager); `PATH` exported in `~/.zshrc`. Git repo initialised,
      module `github.com/mcklmo/loc-history`.
- [x] **1. `report` + `writer`** — `Commit`, `Count`, `Record`, signed `Delta`, `Writer`,
      `MultiWriter` (fans out past a failing sink, joins Close errors), streaming `ConsoleWriter`
      with dash cells for skipped commits. 15 tests.
- [x] **2. `gitlog`** — `Commits(Options)` walking `--reverse` (+ optional `--first-parent`),
      `%x1f`-delimited format, committer dates, `--limit` keeping the most recent N in
      chronological order. 10 tests against fixture repos in `t.TempDir()`.
- [x] **3. `tree`** — `Extract` via `git archive` into a stdlib tar reader; absent folder is
      `Found=false, nil`; working tree provably untouched; path-traversal and escaping-symlink
      guards. 10 tests. Shared `internal/gittest` fixture helper extracted here.
- [x] **4. `cloc`** — `Runner`/`Options`, parser written against captured cloc 1.98 bytes,
      `DockerRunner`, `VerifyMount` preflight canary, `FakeRunner` (a real local counter).
      21 tests, container ones skipped under `-short` or a stopped daemon.
- [x] **5. `pipeline`** — worker pool, reorder buffer, emit-time deltas, per-commit scratch
      dirs, tolerant error policy with `--fail-fast` opt-in, sink closed exactly once on every
      path. 14 tests; the ordering test was mutation-checked (removing the buffer fails it) and
      passes under `-race`.
- [x] **6. `main.go`** — flag parsing with validation, sink wiring, `VerifyMount` preflight,
      signal-cancellable walk, run summary. 13 tests. First real containerised run done.
- [x] **7. `cache`** — content-addressed by (sha, folder, regex, cloc version) plus a schema
      tag, sharded, atomic renames, corrupt entries degrade to a recount. Consulted before
      extraction, so a hit skips the tar too. 12 cache tests + 5 pipeline tests + 3 CLI tests.
      Measured on this repo: 1.885s cold, 0s warm, byte-identical output.
- [x] **8. `FileWriter`** — csv (via `encoding/csv`, so hostile subjects quote correctly) and
      ndjson (whole `Record`, keeping files/comment/blank the CSV projection drops). Buffered,
      flushed on `Close`; parent directories created. 12 tests + 4 CLI tests.
- [x] **9. `GraphWriter`** — self-contained HTML calendar heat map: no script, link, image or
      external URL, so it opens from disk. Diverging blue↔red scale with a neutral midpoint,
      quartile intensity steps, per-cell `<title>`, legend anchoring both ends, stat tiles,
      `<details>` table view, theme-aware via `prefers-color-scheme` **and** a `data-theme`
      override. Golden-file test + 12 behavioural tests + 2 CLI tests. Rendered and eyeballed
      in both schemes.
- [x] **10. End-to-end verification** — every check in `system.md` §11 run against real
      containers. Anchor matches three independent cloc queries; 2.776s cold / 0s warm with
      byte-identical output; working tree clean; all four failure modes exercised.

**Done.** 131 test functions, `go vet` and `go test ./... -race` clean, zero dependencies.

## Findings that contradicted the brief

Verified against the real image, not assumed:

- **`--only-match-f` does not exist.** cloc 1.98 offers `--match-f` / `--not-match-f`. The
  brief's decision #3 (one regex, used both ways) is intact — the flag name was wrong. The
  complement invariant was then confirmed empirically: 5 + 5 = 10 code, 2 + 2 = 4 files.
- **cloc version is 1.98**, not the 2.02 the sample JSON assumed. The `SUM` and
  `header.cloc_version` shape the parser targets is otherwise correct.
- **An empty result is `{}` with exit 0**, not empty stdout as assumed. Both are handled.
- **A nonexistent path returns that same `{}` with exit 0.** So does an empty bind mount —
  which is exactly the macOS trap in §5, and why the mount check cannot be a per-commit
  heuristic. It is a startup canary with a known answer instead (`VerifyMount`).
- **The macOS bind-mount trap did not reproduce.** Docker Desktop 29.6.2 mounts a
  `/var/folders/…` temp directory correctly — cloc counted through it. The `VerifyMount`
  canary is kept anyway: it costs one container at startup and it guards against a
  *silently wrong answer*, which is the failure mode worth paying for.
- **The image is amd64-only**; on Apple silicon every container runs under emulation, so the
  per-container cost the brief budgets for is if anything understated.
- **Mount point is `/loc`, not `/tmp`.** Mounting over the container's own temp directory
  leaves cloc nowhere to write.

## Deviations from the original brief

- **Test subject.** The brief nominated a neighbouring `timeseries-visualizer` repo as the
  first corpus, with "102 product files / 89 test files at HEAD" as the end-to-end correctness
  anchor. The user has since ruled that repo out of scope. Correctness is therefore anchored on
  **synthetic fixture repos built in `t.TempDir()`**, where the expected counts are constructed
  rather than observed, plus smoke runs against this repo itself.
- **New correctness anchor.** At `f73ea3b`, `--folder=internal --test-regex='_test\.go$'`
  reports 935 product / 1392 test / 2327 total. Independently confirmed by extracting the
  tree by hand and running the three cloc queries directly: 935 + 1392 = 2327.
- **Location.** Standalone module at the repo root, not `tools/loc-history/` inside a host repo.
- **The diverging ramp's red arm is computed.** The `dataviz` reference palette documents a
  full blue sequential ramp but no red one, and the documented diverging pair is blue↔red.
  Each red step therefore takes its blue counterpart's OKLCH lightness at the documented
  red's hue, chroma scaled by the ratio between the two documented poles. The gate for a
  diverging ramp is lightness monotonicity, not the categorical adjacency checks (running the
  categorical validator on a ramp fails by design) — verified: `|L − L_mid|` rises
  monotonically along each arm in both modes, four steps per arm. The innermost steps sit
  below 3:1 on purpose, which the table view and per-cell titles relieve.
- **CSV has a tenth column, `skipped`.** The nine specified columns cannot distinguish "the
  folder was absent" from "the folder was present and empty" — both are zero. NDJSON carries
  it already, since it emits the whole `Record`.
