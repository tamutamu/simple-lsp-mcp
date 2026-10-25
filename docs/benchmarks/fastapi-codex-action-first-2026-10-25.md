# Agent-action-first LSP evaluation — 2026-10-25

## Design premise

Inventory **39 coding-agent actions** first, assign each the minimum evidence it needs, then select whether Simple LSP MCP is the appropriate evidence provider. [Full Japanese action-to-information matrix](../agent-coding-information-needs.md).

The resulting public surface contains 19 focused operations, not 39 wrappers: ordinary filename/text searches, editors, Git, builds and real test execution remain ordinary tools. Semantic discovery now defaults to concise handles. `inspect_symbol(view=source|signature|location)` returns the needed representation of one **actual LSP-identified declaration**; `find_references(relation_view=lines)` and the corresponding location tools add real source lines only upon request. A known path and symbol can be inspected in a single MCP call without fetching an outline first.

## Direct MCP measurements on pinned FastAPI template

Template pinned to `b1125d8839482cb7bbae603ddc6d0081bf87532e`. One real Pyright MCP server session, initial call sequence then warmed reads. These are **tool response bytes**, not model tokens or agent wall time.

| Agent information request | Response | UTF-8 JSON bytes |
| --- | --- | ---: |
| What declarations does `crud.py` contain? | `get_document_symbols` outline (six symbols) | 612 |
| What is `get_user_by_email` doing? | `inspect_symbol(view=source)`, actual implementation | 421 |
| What is its API signature? | `inspect_symbol(view=signature)` | 290 |
| Where is its declaration? | `inspect_symbol(view=location)` | 164 |
| Find symbol by name | `search_symbols` compact / full | 206 / 368 |
| Enumerate eight workspace symbols | `list_workspace_symbols` compact / full | **1,649 / 3,062** |
| Find exact usages | `find_references` locations: 14 | 1,677 |
| Inspect actual usage lines | `find_references(relation_view=lines)`: 14 | 2,835 |

Compact workspace results decreased by **46.1%** while preserving `symbol_id`, stable symbol paths where available, pagination cursor and completeness indicators. More expensive source/text representations are deliberately larger **only when requested**. The original verbose document-symbol response in the prior study was 9,800 bytes on a representative FastAPI route; the compact outline there was 611 bytes. These requests do **not** prove faster AI coding on their own.

## Small, independently graded Codex A/B

After the prior 36-trial follow-up was blocked by Codex usage quotas (four valid search completions), the usage window became available and a **new, limited two-task paired study** was run. Same official FastAPI template, Codex CLI 0.147.0 `gpt-5.6-sol` (low effort), task prompt, pinned code, standard Codex sandbox and byte-identical `AGENTS.md` (conditional MCP-use guidance). One fresh checkout per arm/task. Codex user MCP configuration was ignored; treatment contained only this Simple LSP server. **This is an exploratory sample of two task pairs, not a complete replication or statistical demonstration.**

| Coding task | Codex alone | Codex + action-first LSP | Independent acceptance |
| --- | ---: | ---: | --- |
| Pagination fix, seconds | 60.53 | **49.29** | both pass |
| Authorization refactor, seconds | 60.88 | **47.98** | both pass |
| Pagination, input tokens | 346,241 | 349,712 | +1.0% with MCP |
| Authorization, input tokens | 251,286 | 295,824 | +17.7% with MCP |

MCP treatment invoked **nine real MCP tools (9/9 completed)** across the two tasks; pagination: two `inspect_symbol(view=source)` and two diagnostics; authorization: `get_document_symbols`, three `inspect_symbol(view=source)` and one diagnostics. The agent accessed real implementations rather than treating symbol positions as a substitute for source code. It completed both tasks faster in this small sample, **but consumed more input tokens**. No cost claims: Codex traces give token counts but not audited per-task USD charges.

[Sanitized four-row CSV](fastapi-codex-action-first-2026-10-25.csv) contains the per-trial results. Raw worktrees and JSONL traces remain in the local benchmark environment and are not public.

### Interpretation and limitations

- The two paired task timings favor the action-first MCP, **but a single trial per task cannot establish a durable speed advantage**. Do not extrapolate to the six-task benchmark.
- The previous full 36-trial result (18/18 accepted in both arms; +5.95% mean time and +55% mean input tokens with the older MCP design) remains valid **for its older interface, instructions and trial configuration** and must not be overwritten by this new exploratory sample.
- A separate four-completion search study (two pairs) also passed all acceptance checks but is not merged here; it had a different MCP binary/instruction snapshot. Remaining sessions terminated at Codex usage limit, and are **not** scored as task failures.
- The client previously cancelled 11 `rename_symbol` calls. Action-based response shaping does **not** fix the write-approval issue.
- The new two-task Codex study called `get_diagnostics` three times; LSP environment diagnostics can be noisy, so native test results remain the acceptance gate.

## Verified quality checks

`go test ./...` (including actual LSP integration with Go, Pyright and TypeScript Language Server), `go test -race ./...`, `go vet ./...` and `git diff --check` passed. A direct MCP JSON-RPC script also checked compact/full discovery, source/signature/location modes, handle reuse, reference completeness and the real source-line extension.

**Release gate:** repeat matched coding tasks on larger/semantically ambiguous repositories with enough replications and independently graded acceptance, not just tool-response sizes. Keep invalid/quota-blocked trials separate and record actual MCP usage and retries.
