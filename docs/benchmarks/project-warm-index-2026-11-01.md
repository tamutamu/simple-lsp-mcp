# Project semantic warm-up index: controlled CI results (2026-11-01)

## Scope

This change removes repeated full-source **reads** from `internal/tools/warm.go`; it does **not** eliminate directory traversal, file metadata checks, TypeScript semantic priming, or language-server work.

A bounded 2-Kbit trigram filter per source rejects impossible substring matches. Candidates are still checked with `bytes.Contains` and synchronized to the real LSP. Positive source content is cached up to 8 MiB per project snapshot; at most four snapshots are retained. Every search rebuilds the path inventory and validates file identity and file metadata before using a previous filter or byte buffer. Incomplete/failed traversals are not published.

## Controlled benchmark

GitHub Actions [run 154](https://github.com/tamutamu/simple-lsp-mcp/actions/runs/41294077833), Ubuntu hosted runner, 100 TypeScript files with 100 repeated lookups, three repetitions. Each source has approximately 1.9 KiB; hit workload has exactly one matching file. Compare the same source content against a full-file read baseline that also stats each source. The new index is primed before timing.

```sh
go test ./internal/tools -run '^$' -bench '^BenchmarkProjectWarm(LegacyFullScan(Miss|Hit)|Index(Miss|Hit))$' -benchmem -benchtime=100x -count=3
```

| Scenario | Legacy ns/op (3 runs) | Indexed ns/op (3 runs) | Legacy / indexed file reads per op |
| --- | --- | --- | --- |
| Absent identifier | 1,252,228 / 1,191,052 / 1,190,475 | 494,436 / 492,666 / 493,118 | 100 / 0 |
| One matching file | 1,187,475 / 1,174,575 / 1,188,801 | 696,525 / 567,415 / 497,109 | 100 / 0 |

**Observed:** repeated warm-up source scanning is ~59% faster for misses and ~50% faster for one-hit searches, comparing the arithmetic mean of the three timings. All values are limited to this local benchmark. End-to-end Codex/Claude task-time, token usage, and spontaneous tool-selection rate were **not** measured and remain unproven.

## Independent cold-start audit (2026-11-01, follow-up)

A second CI benchmark explicitly measures the **first** project scan, which was missing from the initial report. [CI run 165](https://github.com/tamutamu/simple-lsp-mcp/actions/runs/41296058642) (100 TypeScript sources, 100 operations per measurement, three repetitions):

| 100-file source search | Mean ns/op | Reads/op | Compared with legacy |
| --- | ---: | ---: | ---: |
| Legacy full source scan (miss) | 1,172,599 | 100 | baseline |
| Indexed cold first scan (miss) | 2,143,507 | 100 | **83% slower** |
| Indexed warm repeated scan (miss) | 519,953 | 0 | 56% faster |
| Legacy full source scan (one match) | 1,184,036 | 100 | baseline |
| Indexed warm scan (one match) | 502,611 | 0 | 58% faster |

For the one-name miss workload above, three sequential lookups in the same engine are needed to amortize the cold build cost (approximately 9.5% faster over three scans); one or two scans are slower. This is *not* a prediction for full agent tasks: LSP requests, model decisions and file change frequency are excluded. High-churn workspaces may repeatedly pay the cold-build or invalidation costs.

**Revised judgment:** Warm-index optimization is not unconditionally faster. Keep the cold-vs-warm distinction in all performance claims and prioritize measurement of real agent task patterns.

## Correctness gates

The tests cover:
- First read and subsequent unchanged misses / positive matches.
- File changes, additions, deletions and replacement preserving size and modification time.
- Failed LSP synchronization must not publish an incomplete cache.
- Exhaustive byte-substring checks on sample source text (including multibyte UTF-8).
- Existing real Go, TypeScript and Pyright LSP integration suite, plus Go race detector and vet.

**Limitations:** This is metadata-based invalidation. On filesystems that hide change time, deliberate in-place rewrites with spoofed timestamps can invalidate the correctness assumption. Initial cold indexing adds ~83% to this isolated 100-source scan (see follow-up above); real workspaces can have different candidate density and filesystem characteristics. The maximum 10,000 files / 128 MiB / 512 candidate-files guard remains intact. Do not interpret this isolated test as proof of product efficiency.

## Next gates

1. Measure cold-start cost and multiple independent real repositories with mutation during long-lived sessions.
2. Reduce the remaining O(files) metadata traversal and duplicate TypeScript priming safely.
3. Run preregistered paired Codex A/B tests on >= 20 independent tasks, reporting successful completion, total elapsed time, total and uncached tokens, and spontaneous tool-selection frequency.
4. Do not claim the 25% time, 30% token, or 80% spontaneous-use product targets until independently demonstrated.
