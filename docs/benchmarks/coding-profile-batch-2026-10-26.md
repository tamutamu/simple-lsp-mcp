# Coding-agent tool-surface and batch-access evaluation — 2026-10-26

## Goal and rationale

Following the [39 coding actions and information-needs matrix](../agent-coding-information-needs.md), reduce unnecessary **model-to-MCP turns** and **advertised tool schema size** while preserving accurate LSP identity, source text, and optional full functionality. This is not a generic benchmark claiming that MCP beats literal `rg`.

## Iteration 1 — batch several known functions

`inspect_symbol` now accepts `targets: [{path, symbol_path}, ...]` together with `view=edit|source|signature|location` and `max_lines`. A regular single symbol request is backward compatible and returns its original object; a batch returns a `results` array in the same order. Maximum eight, 32-KiB total JSON output. All selectors are validated before any LSP call. Duplicate selectors, invalid/missing/ambiguous/stale symbols, and oversized results fail explicitly rather than inventing complete code.

Real FastAPI template (`b1125d8839482cb7bbae603ddc6d0081bf87532e`) with Pyright on the same files: `read_items`, `read_item`, `update_item`.

| Comparison | 3 separate MCP calls | 1 batched call |
| --- | ---: | ---: |
| Number of MCP invocations | 3 | 1 |
| JSON content (bytes) | 4,117 | 4,133 |
| Warm LSP mean (five matched rounds, ms) | **12.37** | **12.75** |
| Exact code bodies | 3, verified identical | 3, verified identical |

Before warming, an initial individual call can take about 500 ms for language-server startup. The batched call in the same session reused that already warmed server and must **not** be compared to that cold call. Batch does not cache/reuse the underlying symbol queries yet, so warm backend latency was not reduced. Its possible value is fewer external tool invocations or agent decision turns; Codex may already issue several calls in parallel.

## Iteration 2 — opt-in smaller tool catalog

Introduce `--tool-profile coding` exposing 11 read-only operations: `search_symbols`, `list_workspace_symbols`, `get_document_symbols`, `inspect_symbol`, `get_hover`, `get_definition`, `find_references`, `find_implementations`, `get_diagnostics`, `get_incoming_calls`, `get_outgoing_calls`. The existing default `--tool-profile full` keeps all **19** public operations, including semantic rename, formatting, code actions, type-definition/declaration and subtype/supertype navigation.

| Actual MCP `tools/list` wire payload | Full profile | Coding profile |
| --- | ---: | ---: |
| Number of tools | 19 | 11 |
| UTF-8 JSON bytes | 20,319 | **11,507** |
| Reduction | — | **43.4%** |

These are complete JSON-RPC tool metadata, not tokens billed or processed by the LLM. If the client sends all tool definitions on each model turn, a smaller catalog *could* save context; how Codex represents/caches tool schemas needs separate attribution. `coding` is deliberately opt-in so previously exposed operations are not silently removed from user setups.

On the same pinned FastAPI/Pyright file, the coding profile also passed batched result equality and error-boundary checks. A repeated warm run measured separate 3-call time **11.17 ms** versus batched time **11.70 ms**, again not a backend speedup.

## Verification

- `go test ./...` with real Go/Python/TypeScript LSP integration: **passed**.
- `go test -race ./...`: **passed**.
- `go vet ./...`: **passed**.
- `git diff --check`: **passed**.
- Direct MCP protocol tests for the complete full and coding tool catalogs, exact source equality, invalid selectors and oversized target arrays: **passed**.
- Checked that each LSP-bound identity belongs to the workspace and rejected invalid batches without claiming partial completion.

## Coding-agent A/B status: BLOCKED — do not claim results

Prepared [the paired Codex A/B runner](run_fastapi_codex_coding_profile_ab.py) on the same pinned FastAPI tasks. Attempted to run it in this turn. The current local MCP session permits a writable workspace only but **does not permit creating new directories under the benchmark's `/tmp`** (`OSError: [Errno 30] Read-only file system`). A separate Codex launch inside the workspace also failed before model execution: `failed to initialize in-process app-server client: Read-only file system`. The host-permission mode was unavailable (`session ... is not running; Connection refused`). **No new Codex coding-agent task ran, no new task-level input-token comparison exists, and no claims of throughput or coding-quality improvements are warranted**.

The previous [12-run edit-context study](fastapi-codex-edit-context-2026-10-25.md) found increases in input tokens despite small LSP payloads. It used a different interface and configuration and cannot be counted as evidence for these new batching/profile optimizations.

## Decision and next controlled evaluation

Retain both modifications as measured, backward-compatible, opt-in optimizations for controlled evaluation. The initial direct measurements alone do not justify making coding profile the default. To establish effectiveness, restore a fully writable Codex execution environment with identical verified test dependencies in both arms and run paired repetitions on semantically ambiguous multi-file tasks. Track acceptance, `input_tokens`, `cached_input_tokens`, noncached tokens, model/tool turns, repeated file reads, wall-clock time, and exact MCP error/usage counts. If batching or profile reductions fail this acceptance-and-efficiency gate, remove or revise them rather than adding further unmeasured tools.
