# loc-history — progress

Build order follows `system.md` §11. Every feature is TDD: test first, then implementation,
then a commit.

## Status

- [x] **0. Toolchain** — go1.26.5 installed from the official go.dev tarball to `/usr/local/go`
      (not brew, not a version manager); `PATH` exported in `~/.zshrc`. Git repo initialised,
      module `github.com/mcklmo/loc-history`.
- [ ] 1. `report` + `writer` — `Count`, `Record`, `Writer`, `ConsoleWriter`, `MultiWriter`
- [ ] 2. `gitlog` — commit enumeration
- [ ] 3. `tree` — `git archive` extraction, absent-folder path
- [ ] 4. `cloc` — `Runner`, `parse.go`, `DockerRunner`, `FakeRunner`
- [ ] 5. `pipeline` — worker pool + reorder buffer
- [ ] 6. `main.go` — flags and wiring, first real run
- [ ] 7. `cache`
- [ ] 8. `FileWriter`
- [ ] 9. `GraphWriter`
- [ ] 10. End-to-end verification

## Deviations from the original brief

- **Test subject.** The brief nominated a neighbouring `timeseries-visualizer` repo as the
  first corpus, with "102 product files / 89 test files at HEAD" as the end-to-end correctness
  anchor. The user has since ruled that repo out of scope. Correctness is therefore anchored on
  **synthetic fixture repos built in `t.TempDir()`**, where the expected counts are constructed
  rather than observed, plus smoke runs against this repo itself.
- **Location.** Standalone module at the repo root, not `tools/loc-history/` inside a host repo.
