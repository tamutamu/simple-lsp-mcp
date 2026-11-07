# Full TypeScript LSP reference workflow A/B — 2026-11-07

## Scope and correctness

Compare the unchanged implementation at commit `788f7c715022f62952db27a9a3b2012b3ee2511f` with the optimization branch on the *same GitHub Actions runner*, using the **identical benchmark fixture**. The command starts a real `typescript-language-server` for each sample and performs an untimed warm-up, then measures eight actual `find_references` requests per sample. The operation includes source/symbol resolution, candidate walking and synchronization, TypeScript semantic priming, language-server reference lookup, and location decoding.

The fixture has 100 unrelated TypeScript sources containing `grid` but not the identifier `id`, 8 importers with a call each, and 1 target declaration. Both implementations returned exactly **17 semantic locations** (1 declaration + 8 import bindings + 8 calls) in all successful trials.

## Measurements

[Actions run 42344302641](https://github.com/tamutamu/simple-lsp-mcp/actions/runs/42344302641), Ubuntu amd64, `go test ./internal/tools -run '^$' -bench '^BenchmarkRealTypeScriptReferenceWorkflow$' -benchmem -benchtime=8x -count=3`.

| Full operation | Three runs (ns/op) | Median (ms/op) |
| --- | --- | --- |
| Main baseline | 8,510,728 / 6,916,373 / 6,938,188 | 6.938 |
| Improved | 5,541,567 / 5,805,199 / 5,276,981 | 5.542 |

**Measured ratio:** 1.25× faster, or 20.1% less elapsed time on the median full reference operation. These are raw CI-run medians, not statistically validated generalized claims.

## Supporting isolated candidate-scan measurement

[Actions run 42344129805](https://github.com/tamutamu/simple-lsp-mcp/actions/runs/42344129805): repeated lexical filtering for the short name `id` on 100 files, 3 samples × 50 iterations, reduced from 828,704 ns/op median to 319,036 ns/op median (**2.60×**); absent-name and hit scenarios without repeated boundary matches remained roughly neutral. The accelerated path caches *only* the last lexical candidate answer of an unchanged file; LSP remains the authority for semantic identity.

## Limitations

- This is a **warm**, repeated-reference workload on a synthetic (though actual LSP) project. Cold setup and editor/agent interactions are not included.
- This does not show that LSP is faster than `rg` at bare text matching. `rg` does not implement the same semantic query.
- No Codex A/B data has been collected for this revision, and no token saving is proven by these micro-/integration benchmarks.
- The index retains a full filesystem walk and stat per query; further work should target those costs without stale results.
- Exact production latency and statistical significance require more repositories and repeated randomized runs.

## Reproduce

CI runs benchmark against both the merge-base checkout and optimized branch with the same benchmark source: see `.github/workflows/ci.yml` jobs `warm-candidate-performance` and `real-reference-performance`.
