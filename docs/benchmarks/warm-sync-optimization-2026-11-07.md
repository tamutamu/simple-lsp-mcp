# Repeated document synchronization: 2026-11-07

## Change

`internal/document/store.go` now detects an unchanged **identical immutable backing buffer** already synchronized to the same language-server session generation. It can return the existing document without repeating UTF-8 validation and SHA-256. Changed buffers, changed language IDs, or restarted servers retain the normal validation/notification path. The caller's buffer-ownership contract explicitly forbids mutation after sync.

## Reproducible CI measurement

[GitHub Actions run 42342836300](https://github.com/tamutamu/simple-lsp-mcp/actions/runs/42342836300), Ubuntu amd64, Go 1.26, benchmark `BenchmarkWarmRepeatedSync`, 200 iterations × 3 samples.

| Case | ns/op (3 repetitions) | B/op | allocs/op |
| --- | --- | --- | --- |
| Fresh equal-content buffer (full hash path) | 116022 / 115066 / 115452 | 176 | 4 |
| Same immutable cached buffer (fast path) | 55.16 / 111.0 / 55.51 | 0 | 0 |

Median duration ratio: ~2080× for the isolated 172 KiB `SyncBytes` operation. Note this is **not** a 2080× improvement in LSP query time or coding-agent throughput. Filesystem traversal, language-server semantic processing, model inference, and network transport are excluded. The optimization only fires when the identical source buffer is reused during warm-up.

## Next product-level measurement

1. Record how many warm-up candidates actually reuse the same buffer and total warm-up duration.
2. Measure full `find_references`, `rename_symbol` and `inspect_symbol` calls over large repos, including cold/warm LSP states and changes.
3. Compare identical real Codex tasks with baseline / natural MCP / required LSP, preserving raw traces and non-cached token counts.

Until (2) and (3), this is evidence for an internal overhead reduction, not proof of overall product superiority.
