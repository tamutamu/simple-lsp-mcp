# FastAPI Codex A/B: MCP payload audit (2026-10-25)

Scope: inspect raw Codex JSONL tool results from the 36-run benchmark (18 MCP-enabled sessions). These are payload size measurements, not new coding-agent A/B timings.

| Tool | Calls | Total response bytes | Mean bytes |
| --- | ---: | ---: | ---: |
| get_document_symbols | 20 | 196,189 | 9,809 |
| get_diagnostics | 14 | 25,556 | 1,825 |
| find_references | 2 | 3,354 | 1,677 |
| search_symbols | 1 | 78 | 78 |
| rename_symbol | 11 attempts, 0 completed | 0 | — |
| **Total** | **48 attempts; 37 completed** | **225,177** | — |

**87.1%** of successful MCP text response bytes came from document symbols. Largest response: 18,425 bytes for 64 symbols. Representative FastAPI route: five functions, one module-level variable and 27 function-local variables/parameters. Each symbol repeated the path, language, full range, selection range, and identifier.

A targeted declaration-line grep of that source was 223 bytes. The original LSP response was 9,800 bytes; it contained structural metadata, not function bodies, and the agent often still read source afterward. Removing redundant fields but retaining all symbols could yield 3,077 bytes; removing function-local symbols in the default outline could yield approximately 570 bytes before adding a shared path.

Codex trace events contain both textual content and structured content with the same JSON, totaling a second 225,177 bytes in raw **log representation**. This does not establish that both copies enter the model context. The original 55% additional input tokens in MCP-enabled runs include repeated context, cached prompt tokens and extra agent turns; they cannot be attributed solely to the 225 KB of MCP responses.

## Implementation, directly verified with real language servers

| File | Legacy complete view | New default outline | Reduction |
| --- | ---: | ---: | ---: |
| Python FastAPI items.py | 9,800 B / 33 symbols | 611 B / 6 symbols | 93.8% |
| TypeScript items.tsx | 4,733 B / 15 symbols | 731 B / 7 symbols | 84.6% |

By default, get_document_symbols returns one shared file path and each visible symbol's name, kind, one-based line and symbol_id. It keeps module-level declarations, nested functions/types, and class/interface members, omitting function-local variables. With detail="full", the previous complete nested output including ranges and locals remains available. There are no new public MCP tools. All underlying symbols are still registered; the returned Python symbol_id successfully resolved in a subsequent get_hover.

Go unit tests, whole suite, race checks, go vet and real language-server tests pass. These are response-size and functional checks **only**, not evidence that agent coding is faster. Another independently graded agent A/B must be run before making productivity claims.

Other observed issues are distinct: Pyright benchmark diagnostics included missing-import messages when the LSP did not use the project virtualenv; 11 rename_symbol calls were cancelled by noninteractive Codex approval (including ten previews). Compact output does not resolve those integration problems.

Sources: [36-run Codex A/B report](fastapi-codex-lean-2026-10-25.md), [36 measurements](fastapi-codex-lean-2026-10-25.csv), and the saved local raw JSONL tool traces.
