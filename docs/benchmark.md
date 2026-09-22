# Context-efficiency benchmark

The goal of this benchmark is to measure what `simple-lsp-mcp` actually optimizes: how many agent-visible tool calls and how much returned context are needed to understand one symbol.

The benchmark intentionally does **not** publish made-up universal numbers. Results depend on the repository, language server, symbol fan-out, machine, and warm/cold LSP state. Run it against a representative symbol in your own repository.

## Run

```sh
go run ./cmd/simple-lsp-bench \
  --workspace . \
  --symbol 'UserService/createUser' \
  --depth 2 \
  --max-bytes 24576 \
  --warmups 1 \
  --runs 6
```

If the symbol name is ambiguous, add `--path`. `--language` is optional and should normally be unnecessary.

The command compares three approaches on the same target:

| Measurement | Agent-visible calls | What it gathers |
| --- | ---: | --- |
| `separate_navigation_calls` | 5 | symbol/source + callers + callees + references + implementations |
| `get_symbol_context` | 1 | the same neighborhood through one aggregate tool |
| `get_semantic_slice` | 1 | root source + bounded callee source + type definitions + reference-backed test candidates + compact dependents + implementations |

It reports per-run elapsed time, serialized response bytes, partial/failed results,
plus median values only for valid runs. `max_bytes` is a strict JSON byte cap,
not a token estimate. An incomplete result or failed operation sets
`summary[].comparable=false` and causes a nonzero exit status. Do not compare
medians from partial samples.

The runner warms each method up, then rotates the measurement order to reduce
first-run cache bias. All methods use one shared warmed LSP; this **does not**
represent independent cold-start measurements. For cold-start evaluation, run
each method in a fresh process and explicitly label those results separately.

## Methodology notes

- Run multiple warmed trials and inspect every failure/partial result. Measure cold starts separately in fresh processes; never mix them into warmed medians.
- Use the same repository revision and language-server version for every comparison.
- Prefer a symbol with non-trivial callers and callees; tiny leaf functions are not representative.
- Do not compare only latency. A single aggregate call may do several LSP round trips internally; the primary metrics are agent-visible calls and returned context size.
- The five-call baseline requests implementations even for ordinary functions; some LSP servers reject this as inapplicable. Such a run is **not comparable** to an aggregate tool that returns partial results. Choose tasks supported by all five methods or report these cases as limitations.
- Keep raw output when publishing benchmark claims so results are reproducible.

## Evaluation of actual coding outcomes

This microbenchmark reports tool calls, latency and JSON bytes, **not** successful changes or proof that an agent uses fewer model tokens. For a controlled baseline-vs-MCP coding evaluation, follow [agent-evaluation.md](agent-evaluation.md). Never publish numbers without the repository revision, language-server versions and raw measurements.
