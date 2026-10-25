# Codex A/B evaluation: Simple LSP MCP (18 tools)

**Date:** 2026-10-25 (JST). **Status: completed; 36/36 independent coding-agent runs graded.**

## Verdict

The mandatory-use 18-tool Simple LSP MCP **did not improve accepted-code quality** on these six official FastAPI-template tasks: both arms passed 18/18 independent acceptance graders. Its average completion time was **5.95% longer** and its reported **input-token usage 54.98% higher**.

This is evidence about this configuration and workload, **not proof that LSP has no value** in larger or semantically ambiguous repositories. The difference in task-weighted mean elapsed time was +3.93 s; an approximate t-based 95% interval across six task means is **−3.19 to +11.06 s**, so the duration effect is not estimated precisely.

## Controlled setup

- **Project:** official fastapi/full-stack-fastapi-template, git commit b1125d8839482cb7bbae603ddc6d0081bf87532e.
- **Agent:** Codex CLI 0.147.0, model gpt-5.6-sol, low reasoning effort, ephemeral noninteractive execution, workspace-write sandbox.
- **Treatments:** standard Codex alone vs the same Codex with Simple LSP MCP (18 focused tools, including the corrected position-based cross-file reference warm-up).
- **Instructions:** byte-identical AGENTS.md in both worktrees. It requires at least one task-relevant Simple LSP call **when connected**; otherwise work normally. Same user task and suffix in both arms. Any other user-level MCP server and Codex user configuration were disabled.
- **Replication:** six pre-registered tasks × three fresh source copies × two configurations = **36** runs, alternating assignment order by repetition. Maximum two simultaneous agents.
- **Acceptance:** the existing held-out fastapi-template-grader.py (HTTP behaviors, AST imports, cross-file rename invariants), plus Python compilation and git diff --check. Summary tests used only a dedicated disposable localhost PostgreSQL 16 container, not any user database.
- **Measurements:** actual Codex JSONL completion events, input/cached/output token usage, tool-call results, completion time, change list, independent grader outcome. USD costs are not available from the Codex CLI and **were not fabricated**.
- **Privacy:** raw agent traces and local isolated worktrees remain on the evaluation host; the attached CSV has only benchmark measurements.

## Per-task results

Each cell represents the **mean across three independent runs**. All six trials for every task passed the held-out grader.

| Task | Accepted, no MCP / MCP | Seconds, no MCP | Seconds, MCP | MCP delta | Input tokens, no MCP | Input tokens, MCP | MCP calls (succeeded) |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Search endpoint | 3/3 / 3/3 | 83.26 | 75.21 | −9.67% | 331,382 | 524,221 | 6 (6) |
| Pagination validation | 3/3 / 3/3 | 58.68 | 62.45 | +6.41% | 341,937 | 495,834 | 11 (11) |
| Authorization helper | 3/3 / 3/3 | 57.37 | 59.01 | +2.85% | 310,586 | 400,306 | 5 (5) |
| SQL summary endpoint | 3/3 / 3/3 | 82.21 | 93.71 | +13.98% | 371,126 | 627,783 | 6 (6) |
| Rename user lookup | 3/3 / 3/3 | 59.96 | 67.96 | +13.34% | 266,828 | 432,707 | 12 (6) |
| Rename password helper | 3/3 / 3/3 | 55.00 | 61.75 | +12.26% | 225,196 | 381,786 | 8 (3) |
| **Equal task weight** | **18/18 / 18/18** | **66.08** | **70.01** | **+5.95%** | **307,843** | **477,106** | **48 (37)** |

Totals across 18 runs per arm: wall time 1,189.48 s (no MCP) vs 1,260.25 s (MCP), input tokens 5,541,166 vs 8,587,911 (+55.0%), cached input tokens 5,119,232 vs 7,998,208; noncached input tokens 421,934 vs 589,703 (+39.8%). Output tokens 58,572 vs 59,791 (+2.1%). Token counts are not costs, especially with caching.

Among the 18 paired repetitions, MCP took longer in 12 and less time in six. A single run could reverse the three-run average: search was faster with MCP on average even though a separate preliminary single-pair smoke test was slower.

## Integration defects exposed by actual agent behavior

**1. The rename preview was unusable in noninteractive Codex.** All **11 failed MCP calls** were rename_symbol. Codex returned **“user cancelled MCP tool call”** for ten preview requests (apply=false) and one apply=true request. The MCP catalog marks rename_symbol as destructive even for a preview, which plausibly triggers the approval limitation; verify authorization behavior before changing annotations, because apply=true really writes files. Other 37 MCP tool invocations completed. Since Codex fell back to its normal file-editing tools, **successful acceptance tests do not establish that Simple LSP's rename feature improved the outcome**.

**2. One treatment run never used MCP.** The summary endpoint trial #3 had zero MCP calls; the agent stated that the MCP was not connected. This is a protocol deviation even though the code passed the grader. Keep it in the intention-to-treat arm for transparency; do not relabel it as a successfully MCP-assisted run.

**3. Tool invocation increased prompt footprint.** The metadata-only catalog had already fallen from 24,656 to 17,350 bytes after the 26→18-tool cleanup, but the observed repeated input-token count still increased by 55% when MCP was available. The actual agent traces show both useful semantic navigation and repeated diagnostics/failed rename attempts. Eliminating wrappers was not, by itself, enough to produce a measured coding productivity gain.

## Decision and next quality gate

- **Do not advertise coding speed, cost or acceptance improvements.** The current A/B evidence does not support any such claim.
- Fix **Codex-safe semantic rename preview/apply**, with an independently permission-tested design. Never mislabel the apply-capable tool as read-only. Repeat the same benchmark after that change, keeping this result frozen for historical comparison.
- Add harder semantic-identity work to the next suite: overloaded symbols, interface implementations, multi-module references, and realistic rename collisions. Trivial string search tasks do not demonstrate the true edge of an LSP.
- Consider paired token/latency metrics and tool necessity as release gates, not just compilation or tests.

## Reproduction and artifacts

- [Pre-registered task prompts](fastapi-template-tasks.json).
- [Held-out acceptance grader](fastapi-template-grader.py).
- [Codex A/B runner](run_fastapi_codex_ab.py).
- [36 sanitized per-run measurements](fastapi-codex-lean-2026-10-25.csv).
- [Old Claude Code + 26-tool A/B report](fastapi-template-2026-10-24.md); **not directly comparable**, because the model, client, instructions and tool surface differ.
- [Direct MCP/LSP functional test](fastapi-template-lean-direct-2026-10-24.md).

**Methodology limitation:** three repetitions per task are insufficient to claim a stable universal speed difference, and focused task-specific acceptance checks cannot establish broad repository maintainability or production correctness. The 11 denied rename calls and the disconnected run limit interpretation of the "MCP-assisted" condition.
