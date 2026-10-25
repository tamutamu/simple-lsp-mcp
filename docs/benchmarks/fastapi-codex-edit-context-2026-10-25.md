# Simple LSP MCP: edit-context design and Codex A/B (2026-10-25)

## Decision

**Information-level improvement verified; coding productivity improvement NOT established.** A coding-agent action matrix identified a gap: a function body alone often omitted decorators, referenced imports and module-level configuration, causing the agent to reread the same file. `inspect_symbol(view="edit")` now returns this nearby edit evidence from current workspace source, without sending unrelated code or a whole file.

The MCP still cannot determine every dynamically resolved, transitive or cross-file dependency. Module import selection is lexical and bounded; don't mistake it for a full semantic dependency closure. Existing source, signature and location modes are unchanged.

## Workflow and output contract

| Coding action | Ask for | Output |
| --- | --- | --- |
| Determine file shape | get_document_symbols | Compact symbol names, kinds, lines, handles |
| Inspect algorithm only | inspect_symbol(view=source) | Exact LSP-identified body |
| **Edit a function** | **inspect_symbol(view=edit)** | Actual body + leading decorators + referenced import statements and simple root bindings |
| Check signature / edit position | inspect_symbol(view=signature / location) | Bounded header / precise range |
| Examine exact cross-file references | find_references(relation_view=lines) | Real use-site source line with semantic location |
| Read a test or run a build | normal file/test commands | Tests, stdout, exit status; not duplicated in MCP |

The `edit` view keeps the normal 16 KiB source bound and adds no more than 4 KiB of selected local module context; `meta.truncated` reports when limits constrain the answer. `code_start_line` is included when the code snippet starts on a decorator preceding the declared LSP range. Binding imports are selected after direct bindings: for FastAPI `@router.get`, the selected `router = APIRouter(...)` additionally leads to its `APIRouter` import. No code or import is synthesized.

## Real LSP protocol size checks

Using actual Pyright and TypeScript language servers against the official FastAPI template at pinned commit b1125d8839482cb7bbae603ddc6d0081bf87532e:

| Tool question | Result size |
| --- | ---: |
| Which symbols in backend/app/crud.py? | 612 B |
| Source of get_user_by_email? | 421 B |
| Edit context of get_user_by_email? | 594 B |
| Edit context of read_items with decorators/router/imports (after binding-import fix)? | **1,763 B** |
| Edit context of TypeScript ItemsTable? | 524 B |
| Workspace list 8 symbol identities, compact vs full? | 1,649 B vs 3,062 B (46.1% smaller) |
| Find 14 semantic references, locations vs with actual source lines? | 1,677 B vs 2,835 B |

These numbers are JSON response sizes, **not** model token costs or task timing. They establish that MCP can return useful editing context in short, exact-source answers.

## Independently graded coding-agent A/B

**Agent:** Codex CLI 0.147.0, gpt-5.6-sol, low reasoning; same pinned FastAPI source, prompts, sandbox and byte-identical AGENTS.md per treatment/baseline trial. Codex configured with no user MCP integrations; treatment received only the tested Simple LSP binary. **Acceptance:** existing held-out independent grader plus Python compilation and diff check. No contaminated runs from quota failures are counted. Tokens are Codex JSONL cumulative input, with cached tokens reported separately, **not billable cost**.

### First edit-context implementation — 8 successful trials

Two tasks, two runs per condition. All eight passed independent acceptance. The two MCP runs for pagination called inspect_symbol(view=edit) twice each; authorization runs called edit mode on three target functions (one also inspected a compact document outline).

| Task | Baseline seconds | MCP seconds | Baseline input tokens | MCP input tokens | Pass rates |
| --- | ---: | ---: | ---: | ---: | --- |
| Pagination (two runs each) | 56.16 | 56.83 | 297,190 | 422,770 | 2/2 both |
| Authorization refactor (two runs each) | 53.03 | 56.93 | 304,371 | 385,040 | 2/2 both |
| **Average across four runs/arm** | **54.59** | **56.88** | **300,780** | **403,905** | **4/4 both** |

Average noncached input tokens were 19,884 baseline versus 28,865 MCP. In other words **duration +4.2%, cumulative input +34.3%, uncached input +45.2%**, not a productivity win.

### After correcting indirect module-binding imports — 4 more successful trials

The second binary corrected context closure for simple module bindings (e.g. including the APIRouter import required by a relevant `router` binding) and excluded prior function-local imports. One repeat per task/condition; all four passed.

| Task | Baseline seconds | MCP seconds | Baseline input tokens | MCP input tokens | Pass rates |
| --- | ---: | ---: | ---: | ---: | --- |
| Pagination | 58.19 | 58.69 | 310,733 | 426,923 | 1/1 both |
| Authorization refactor | 54.73 | 48.62 | 366,909 | 389,827 | 1/1 both |
| **Average across two runs/arm** | **56.46** | **53.66** | **338,821** | **408,375** | **2/2 both** |

Average noncached input tokens: 25,349 baseline vs 32,439 MCP. The faster authorization outcome (+6.11 seconds saved) is **one run**, insufficient to generalize.

The two versions are separated in [all 12 sanitized per-run measurements](fastapi-codex-edit-context-2026-10-25.csv). Do not pool them as six repetitions of one binary. Raw trace/worktree paths are local-only and no sensitive content is published.

## Why token usage did not improve

Tool trace inspection exposed a crucial distinction between **MCP response size** and **the cumulative tokens used by the coding agent**:

- The two initial pagination MCP runs returned only about 3.15 KiB of MCP text each, yet one spent far more time probing an absent pytest/uv environment. No LSP response-shaping optimization can correct those unrelated repeated execution attempts.
- One authorization MCP run emitted roughly 4.6 KiB of MCP text but also received around 80 KiB of shell output from other broad searches; removing full function reads alone does not prevent a separate broad repository search.
- In the first edit-context benchmark, MCP often eliminated whole-file sed reads, but cumulative model input still increased. Repeated model requests, prompt-cache accounting, tool catalog schema and extra shell results remain contributors; their individual causal shares are **not isolated** in these trials.
- The coding-agent runner lacked a ready pytest environment inside fresh isolated worktrees; acceptance used the separate grader's installed FastAPI environment. That setup led to retry noise in Codex traces and should be fixed before estimating reliable wall-clock improvements.

These findings do not justify marketing a throughput or token-cost advantage. They justify tightening the task-scoped context contract, improving test fixture readiness, and measuring search duplication separately.

## Quality checks and reproducibility

- Full Go suite with real Go/Pyright/TypeScript LSP, Go race tests, go vet, and git diff --check.
- Explicit unit tests for Python FastAPI decorators, multiline import statements, module-binding import closure, local-scope exclusion, TS imports, Go imports, bounded source, stable semantic handles and real JSON-RPC calls.
- [Runnable benchmark harness](run_fastapi_codex_edit_ab.py): fresh independent checkouts, conditional identical project instructions, Codex token and MCP trace collection, independent acceptance test and incomplete-run exclusion.
- [Prior 36-run full baseline](fastapi-codex-lean-2026-10-25.md) remains separately documented (both groups 18/18 accepted, MCP +5.95% average time, +55% cumulative input on the older tool shape).

**Next quality gate:** restore a deterministic, preinstalled test environment to both coding-agent arms, benchmark semantically difficult multi-module tasks with adequate repeated trials, and reject any claim of efficiency until accepted code quality, time, prompt tokens and redundant reads are measured simultaneously.
