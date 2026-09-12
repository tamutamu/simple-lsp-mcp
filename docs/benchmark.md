# Context-efficiency benchmark

The goal of this benchmark is to measure what `simple-lsp-mcp` actually optimizes: how many agent-visible tool calls and how much returned context are needed to understand one symbol.

The benchmark intentionally does **not** publish made-up universal numbers. Results depend on the repository, language server, symbol fan-out, machine, and warm/cold LSP state. Run it against a representative symbol in your own repository.

## Run

```sh
go run ./cmd/simple-lsp-bench \
  --workspace . \
  --symbol 'UserService/createUser' \
  --depth 2 \
  --max-tokens 6000
```

If the symbol name is ambiguous, add `--path`. `--language` is optional and should normally be unnecessary.

The command compares three approaches on the same target:

| Measurement | Agent-visible calls | What it gathers |
| --- | ---: | --- |
| `separate_navigation_calls` | 5 | symbol/source + callers + callees + references + implementations |
| `get_symbol_context` | 1 | the same neighborhood through one aggregate tool |
| `get_semantic_slice` | 1 | root source + bounded callee source + compact dependents + implementations |

It reports elapsed time, serialized response bytes, and an approximate token count (`JSON bytes / 4`). The token estimate is for relative comparison only; it is not a billing-token measurement.

## Methodology notes

- Run once cold and several times warm; report both when publishing results.
- Use the same repository revision and language-server version for every comparison.
- Prefer a symbol with non-trivial callers and callees; tiny leaf functions are not representative.
- Do not compare only latency. A single aggregate call may do several LSP round trips internally; the primary metrics are agent-visible calls and returned context size.
- Keep raw output when publishing benchmark claims so results are reproducible.
