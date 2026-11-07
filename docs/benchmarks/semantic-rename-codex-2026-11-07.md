# Semantic rename A/B — root cause and repaired evaluation (2026-11-07)

## Root cause

The prior Codex `rename_user` / `rename_password` trials **did not test semantic rename**:

1. The runner launched `simple-lsp-mcp --tool-profile coding`; its 12-tool catalog omitted both `preview_rename` and `rename_symbol`.
2. Its identical conditional `AGENTS.md` required `inspect_symbol` before editing. Codex used source inspection plus ordinary search and patching.
3. Older Codex records (2026-10-25) had **11 cancelled `rename_symbol` calls** due to noninteractive approval, including attempted read-only previews. A distinct, non-destructive `preview_rename` tool now exists.
4. The resulting token measurements cannot be attributed to semantic rename performance.

## Product change

- `coding` now exposes **14** tools including `preview_rename` (read-only) and `rename_symbol` (destructive, explicit approval).
- For a user-authorized, unambiguous rename, `rename_symbol(apply=true)` performs all LSP WorkspaceEdit validations, applies every edit and verifies changed files in **one MCP tool call**. Preview is available when desired, but is not compulsory.
- Under `--result-format text`, a successful applied rename returns only `applied`, `file_count`, `edit_count`, `new_name`, `changed_files` and optional warnings. There is no repeated list of every edit in the model context. Full Engine / dual-format contract is preserved.
- An **optional** `--tool-profile rename` advertises only the two semantic rename tools for tasks needing a minimal catalog. The standard `coding` and `full` surfaces remain available.
- The benchmark runner's original required-inspection policy is preserved by default. `--semantic-rename` opts into one-call rename guidance and Codex's documented `--approve-for-me` authorization (without its incompatible `--sandbox` flag), applying the same conditional instructions to both arms.

## Direct LSP proof

Pinned repo: `fastapi/full-stack-fastapi-template@b1125d8839482cb7bbae603ddc6d0081bf87532e`, rename `backend/app/crud.py::get_user_by_email` to `find_user_by_email`.

| Method | Measurements |
| --- | --- |
| Preview then apply (real Pyright) | 693.78 ms preview + 20.66 ms apply; 5 files, 14 edits |
| **One write call** (real Pyright) | **646.64 ms** including cold semantic resolution, 5 files, 14 edits |
| One-call MCP response | **354 bytes**, vs 2,305-byte detailed applied response |
| Git difference | SHA-256 `fbb8cc01bdbfcf12600207d8801af8413887f56ede40e66ad6e94fb61cca5cde`, **identical to the Codex result** |
| Correctness checks | `git diff --check` passed; no old identifier remained |

The numbers are **not** a Codex task duration. No model inference is needed in this direct MCP experiment.

## Codex paired trials

All arms use Codex CLI `0.167.0`, model `gpt-5.6-sol`, low thinking, same pinned FastAPI tree, same task and matching conditional `AGENTS.md`. Each row represents **one exploratory run**, not a statistically significant study. The treatment arms actually called `rename_symbol(apply=true)` once and successfully applied all changes.

### One-call with normal 14-tool coding profile

| Case | Arm | Seconds | Input total | Cached input | Fresh input | Shell calls | LSP calls |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| rename_user | Baseline | 53.85 | 112,117 | 103,168 | 8,949 | 7 | 0 |
| rename_user | Coding LSP | **32.65** | 113,322 | 99,072 | 14,250 | 6 | 1 successful |
| rename_password | Baseline | 38.72 | 87,715 | 80,128 | 7,587 | 5 | 0 |
| rename_password | Coding LSP | **26.82** | 107,429 | 94,720 | 12,709 | 2 | 1 successful |

**Observed time improvements:** 39.4% and 30.7%. Fresh input **increased** in both cases. Tool discovery and stochastic agent follow-up are part of the Codex cost.

### One-call with optional 2-tool rename profile

| Case | Arm | Seconds | Input total | Cached input | Fresh input | Shell calls | LSP calls |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| rename_user | Baseline | 42.19 | 110,087 | 101,504 | 8,583 | 7 | 0 |
| rename_user | Rename LSP | **36.84** | **93,341** | 76,800 | 16,541 | 5 | 1 successful |
| rename_password | Baseline | 39.28 | 107,187 | 99,200 | 7,987 | 7 | 0 |
| rename_password | Rename LSP | **26.05** | **90,663** | 82,304 | 8,359 | 2 | 1 successful |

**Observed total-input improvements:** 15.2% and 15.4%; elapsed time improved 12.7% and 33.7%. Fresh input did **not** decrease. One paired run per case is insufficient to attribute the difference causally to catalog size.

### Tool disclosure size

- `coding`: **14 tools / 14,222 JSON bytes**.
- `rename`: **2 tools / 2,364 JSON bytes**, a reduction of **83.4%** in serialized catalog bytes.
- Bytes of tool schema are **not** token usage. The actual fresh-token deltas do not reliably follow the schema reduction because Codex caching, tool choices and reasoning vary across runs.
- The applied result itself is ~350 bytes and is no longer the likely source of the high model input.

All four one-call Codex runs produced identical Git diffs to their corresponding baselines:

- `rename_user`: `fbb8cc01bdbfcf12600207d8801af8413887f56ede40e66ad6e94fb61cca5cde`
- `rename_password`: `60ec03049ac13386c39cd9e4f435c45d738645d7d223976eb1ea3b29214d27a2`

## Grading and interpretation

The independent FastAPI runtime grader could not execute because the provided Python environment lacked `fastapi` (and the pinned application targets Python 3.14). Thus `accepted=false` on its own is **not** a failure of the modifications, and we cannot claim full runtime acceptance. We did confirm identical diffs, direct semantic edit counts and the absence of stale old names in the direct probe.

**Conclusion:** Once the tool is exposed and authorized, one-call LSP rename is accurate and visibly saves Codex wall time on these two tasks. A smaller tool catalog reduced **reported total input** in the two new trials. **No consistent fresh-token or billed-cost reduction has yet been established.**

Further evidence requires multiple alternated trials with independent runtime grading, semantic complexity beyond a simple rename, and source-level code navigation tasks. Never infer model efficiency from isolated LSP microbenchmarks alone.
