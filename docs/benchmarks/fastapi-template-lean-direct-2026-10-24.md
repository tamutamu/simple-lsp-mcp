# FastAPI template: lean LSP bridge verification (2026-10-24)

**Scope: direct MCP/LSP operation comparison, not coding-agent A/B.** This verifies the 18-tool refactor and corrects a semantic regression discovered while preparing to re-run the previous [36-session coding-agent evaluation](fastapi-template-2026-10-24.md). It does not demonstrate a change in Claude Code's development speed, cost, or completion rate.

## Reproducible setup

- Code under analysis: official `fastapi/full-stack-fastapi-template`, pinned checkout `b1125d8` (same revision as the prior agent evaluation).
- Control: locally compiled 26-tool `simple-lsp-mcp v0.10.2+dirty` binary preserved from the prior evaluation.
- Treatment: locally compiled lean 18-tool implementation based on GitHub main commit `9f5a3e0`, with the new path/position reference-warm-up fix.
- Backends: installed Pyright (Python) and TypeScript Language Server (TypeScript); no Claude Code LSP plugin or native LSP.
- Exactly three fresh independent MCP/LSP processes for each binary; alternated execution order between trial pairs.
- Identical `tools/list` and LSP requests against unchanged files; no source modifications. Source paths: `backend/app/crud.py`, `frontend/src/routes/_layout/items.tsx`.
- `find_references` used `path=backend/app/crud.py`, `line=34`, `column=6` (inside `get_user_by_email`). The rename call used `new_name=find_user_by_email` **without** `apply` and therefore generated only a preview.

## Results (means of three trials unless specified)

| Observation | Old 26 tools | Lean 18 tools + reference fix |
| --- | ---: | ---: |
| MCP tool catalog bytes (compact UTF-8 JSON) | 24,656 | 17,350 |
| Catalog reduction | — | **29.6%** |
| `get_document_symbols` Python | 6 symbols (3/3) | 6 symbols (3/3) |
| `find_references`, **first call** | **1 reference** (3/3) | **13 references** (3/3) |
| `find_references`, after rename preview | 13 references (3/3) | 13 references (3/3) |
| `get_definition` | 1 location (3/3) | 1 location (3/3) |
| `rename_symbol` preview | 5 files / 14 edits (3/3) | 5 files / 14 edits (3/3) |
| Python LSP diagnostic result | 1 diagnostic (3/3) | 1 diagnostic (3/3) |
| TypeScript `get_document_symbols` | 5 symbols (3/3) | 5 symbols (3/3) |
| First reference call elapsed time | 5.3 ms | 162.7 ms |
| Whole MCP process + eight tool calls, average | 1,286 ms | 1,371 ms |

**Why the first reference result matters:** prior behavior was *incomplete but marked `complete=true`*. Python references searched only the opened definition file when the request supplied `path/line/column` but no `symbol_path`. Calling rename later warmed other project files and exposed twelve previously missing references. The fix uses a read-only LSP `textDocument/prepareRename` request to determine the precise identifier before reusing the existing bounded project-file synchronization. This restores correctness **without adding a public MCP tool or introducing an agent-level impact-analysis engine**. TypeScript and Python real-LSP regression tests now cover this initial position-based reference query.

**Trade-off:** the correct initial reference query must synchronize additional candidate files. Consequently that individual query takes longer, and the complete sequence was about 6.6% slower in this run. Smaller tool metadata and accurate symbol results do not by themselves prove faster agent development.

## What remains unmeasured

The requested **Claude Code MCP-off vs lean-MCP-on** rerun of all six tasks × three trials was **not completed**. The current local execution sandbox could not complete an authenticated Claude Code CLI request: a minimal one-turn request timed out after 25 seconds. The previous 36 trials were against the **old** MCP and cannot be relabeled as evidence for the lean version. No new coding-agent times, costs, or success rates are claimed.

This direct-protocol measurement is an intermediate engineering regression check, **not** a substitute for the agent A/B study. Once the agent execution environment permits outbound authenticated requests, rerun the same six pre-defined tasks with the same model, identical checkout/prompt and independent graders, in three fresh trials per condition, capturing actual MCP use, quality, wall time and API cost. Keep the negative old-version results visible rather than extrapolating from this direct-tool test.

## Agent A/B follow-up (2026-10-25)

A repeatable [coding-agent A/B runner](run_fastapi_agent_ab.py) now accepts the pinned official template, the compiled 18-tool MCP, and the same [six predefined tasks](fastapi-template-tasks.json). It creates independent checkouts, gives **identical neutral CLAUDE.md instructions** to both conditions (no mandatory MCP calls), disables Claude's native LSP, records actual MCP invocations and reported cost, and applies the [held-out grader](fastapi-template-grader.py) only after agent work. The plan covers six tasks × three repeats × two conditions = **36 independent agent runs**. Summary grading requires a disposable local PostgreSQL 16 instance. Raw CLI traces remain local rather than being published.

**Execution blocked:** in the current read-only/network-restricted local session, resolving `api.anthropic.com` failed and a 16-second minimal Claude Code command timed out. A smoke pair (`search`, one baseline plus one lean-MCP) also timed out after 12 seconds in both conditions, with **zero MCP calls and no completed agent result**. These two incomplete smoke attempts are *not* included in any performance or quality statistics. Neither the historical v0.10.2 results nor the direct-LSP measurements can substitute for a completed agent A/B study.

Run on a host permitting outbound authenticated Claude Code requests and writes to the scratch workspace. For example (replace the placeholders with absolute paths and use a disposable PostgreSQL database named `bench`):

```sh
python3 docs/benchmarks/run_fastapi_agent_ab.py \
  --source /path/to/pinned/full-stack-fastapi-template \
  --mcp /path/to/simple-lsp-mcp \
  --python /path/to/full-stack-fastapi-template/.venv/bin/python \
  --node-bin /path/to/node/bin \
  --lsp-bin /path/to/language-servers/bin \
  --postgres-url postgresql+psycopg://postgres:password@127.0.0.1:55439/bench \
  --output /tmp/simple-lsp-lean-agent-ab
```

The runner fails fast when the normal Claude API hostname cannot resolve; `--skip-network-check` is only for hosts intentionally configured with a different working API endpoint. Preserve all 36 trial results and verify treatment MCP calls before reporting any effect.
