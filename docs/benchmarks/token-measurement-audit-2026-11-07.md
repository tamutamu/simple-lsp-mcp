# Codex token and LSP-use measurement audit (2026-11-07)

## Finding
The 2026-11-01 report compares Codex-reported **cumulative input tokens**.
The original runner reads `turn.completed.usage.input_tokens`, and additionally
records `cached_input_tokens`. Its published compact CSV does not preserve
the per-trial cache accounting or the underlying JSONL traces.

A difference in total input is not necessarily a difference in fresh context,
billed cost, LSP payload or time spent searching. These are different measures.

## Correct measurement
1. Report total, cached and uncached input separately; use null for missing data.
2. Record output/reasoning usage separately.
3. Deduplicate MCP lifecycle events by item ID and distinguish attempts from valid semantic results.
4. Report separate elapsed time for agent startup, LSP initialization, semantic discovery, editing and tests **only when timestamps/markers actually exist**.
5. Keep A: no MCP, B: naturally chosen MCP, C: relevant LSP use required; do not claim LSP gains from B trials where LSP had zero calls.
6. Report retrieval precision (relevant symbols/returned symbols), missing references and unnecessary file reads alongside task acceptance.
7. Pin model, agent, task, grader, working tree, instructions and LSP binary; predeclare repeats.
8. Do not infer token usage from tool-schema byte length.

## How to audit existing runs

```bash
python3 docs/benchmarks/audit_codex_trace.py path/to/runs/*/trace.jsonl > audit.json
python3 -m unittest discover -s docs/benchmarks -p test_audit_codex_trace.py
```

The trace audit can reconstruct *reported* usage for each completed turn.
It cannot recover per-tool token counts, billable totals, timings or successful
semantic results that were never captured. For those, extend the runner's
instrumentation and keep the raw trace.
