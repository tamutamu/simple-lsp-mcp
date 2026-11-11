# simple-lsp-mcp release candidate verification — 2026-11-11

**Status: NOT RELEASED.** These are verifiable implementation and test facts, not a declaration that all 56 release tasks are complete.

## Source of truth and isolation

- Candidate branch: `agent/simple-lsp-release-20261111`, based on GitHub `origin/main` commit `4eb07a98d3be7d197286db5d73fe476c2f563092`.
- The earlier, diverged worktree and its uncommitted files remain preserved separately. No CI files were modified in the candidate branch.
- The original 20-tool scope in `product-shipping-backlog.md` predates two additional upstream operations. The current candidate exposes 22 tools; the 20 original tools are retained. Do not silently drop upstream tools to hit an outdated count.

## Product improvements implemented

- MCP initialization advertises a human-readable server title, website, and a title for every tool. Stable tool names are unchanged.
- `--result-format readable` renders source code and hover/semantic-relation context directly, falling back to JSON text for errors and unsupported shapes. Existing `both` and `text` remain compatible.
- Codex setup registers the smaller coding tool profile with readable output; plugin `.mcp.json` opts in to readable output. Default server format remains `both` for older integrations.
- The explicit requirement to make at least one LSP call regardless of information need was removed from active AGENTS/CLAUDE/setup guidance. LSP selection depends on actual semantic need, not a call quota.
- CLI/README catalog counts were corrected: the current full profile has 22 operations. The 12-tool coding profile is a subset, not a replacement.

## Reproduction — independently run against Go, TypeScript, and Pyright

With Go 1.26.4, gopls, `typescript-language-server`, and `pyright-langserver` installed on PATH:

```sh
SIMPLE_LSP_REAL_LSP=1 go test ./... -count=1 -timeout 8m
go test -race ./internal/tools ./internal/mcpserver -count=1
go vet ./...
git diff --check
python3 -m unittest discover -s docs/benchmarks -p 'test_audit_product_ab.py'
```

All five checks passed on the candidate's latest implementation. Codex app-server discovery and direct `get_document_symbols` / `inspect_symbol` calls passed using `docs/benchmarks/run_codex_mcp_discovery_smoke.py`, with 22 tools advertised and readable Go source returned. These are **direct tool smoke tests**; they do not prove natural agent adoption.

Build caveat: this isolated Git worktree is nested under another Git repository. The Go VCS embed may pick up the outer repository's commit. For local binary tests use `go build -buildvcs=false`; published release builds must set an explicit verified version rather than use the nested-worktree pseudo-version.

## Agent A/B evidence — fail closed

Trace-derived audit: `python3 docs/benchmarks/audit_product_ab.py .scratch/product-ab-evaluation-release-20261111`.

- Predeclared 15 tasks × 2 repeats × 2 arms = **60 attempted**; 14 runs genuinely completed and passed, while **46** ended with Codex `usageLimitExceeded` and are **not** counted as coding failures or accepted runs.
- Seven pairs had independently accepted, *completed* runs on both arms. **Zero** of those seven pairs invoked the LSP MCP. Time and token differences cannot be attributed to LSP.
- An additional same-instruction, non-mandatory semantic-rename study produced one completed/accepted baseline run, followed by three credit-limited runs. It therefore has **zero qualifying comparison pairs**.
- The old evaluator bug counted a passing source-tree grader as success even when `turn/completed.status=failed`. The new trace auditor rejects that case, and `test_audit_product_ab.py` locks the check down.
- Nothing here shows a verified agent productivity gain. Previous direct LSP completeness/response-quality tests are separate evidence and must not be relabeled as task-level A/B wins.

## Remaining release blockers

The 56-task inventory in `product-shipping-backlog.md` is not complete. In particular, a complete fresh, independently graded multi-language agent A/B (T33–T47), comprehensive task-by-task objective evidence, Claude Code and Codex distribution acceptance, and versioned publication (T53–T56) remain unverified. A candidate branch is not a GitHub release. Do not tag, merge, or publish this build as production-complete until the evidence exists. Existing user changes and unrelated CI must remain untouched.

## 2026-11-11 evening continuation — genuine agent-use evidence

The prior 60 attempts and zero-MCP observations above remain valid for that *optional-selection* dataset. They must not be confused with the later, separate **task-conditional production-instruction** study. The paired study with a relevant semantic-use guide (identical text in both arms) is published in [`docs/benchmarks/product-ab-go-2026-11-11.md`](benchmarks/product-ab-go-2026-11-11.md):

- **T43, cross-file semantic rename:** 3/3 accepted solutions in *both* arms; six real, successful MCP invocations in the treatment (`preview_rename` and `rename_symbol` each used once per repetition). Average 132.314 s baseline versus 88.639 s MCP, i.e. 33.0% less measured task time. However non-cached input token totals increased 28.1%; one of three repetitions was slower with MCP.
- **T35, simple Go compiler type error:** 3/3 accepted in both arms, zero MCP calls; average 58.533 s baseline versus 67.464 s MCP-available. The time difference cannot be attributed to actually using LSP. This is a negative-control task, not proof of LSP slowdown.
- Every A/B pair started from the same frozen initial Git commit. MCP was warmed on an unrelated Go file **before** the measured coding turn (warm-up duration 0.33–0.87 s recorded separately), avoiding repeated language-server startup charges for each trial. The scripts include independent Go test/vet/diff checks and a fail-closed event-trace audit. Twelve source snapshots, outcome JSON files and traces are retained under the candidate's `.scratch/`; the reproducible runner and non-secret result CSV are in `docs/benchmarks/`.
- A *separate exploratory* 1-pair rename study saw baseline miss two test-string updates and fail a real gopls test, while treatment passed. It was not pooled into the prespecified 3 repetitions.
- A new portable run harness was smoke-tested on T33: both arms completed and independently passed. This is harness verification, **not** additional evidence of LSP effectiveness; the MCP arm made zero calls.
- Final quality checks: `SIMPLE_LSP_REAL_LSP=1 go test -race ./... -count=1 -timeout=8m` **passed** across all packages, followed by `go vet ./...`, the Python A/B audit unit tests and `git diff --check` (all passed).

### Staged distribution (not published)

- Candidate staged version: `0.11.0-rc.20261111`, built with explicit version ldflags (local testing only). SHA-256 of the staged Linux binary: `7666e31dde9b60976c23bd8aad138084903d6e9477b45fb7f989d6353bacdb41`.
- Both `setup codex` and `setup claude` commands generated plausible **preview-only** registration instructions without overwriting existing registrations. Codex setup uses `--result-format readable` and the `coding` profile.
- Codex app-server **direct** discovery and MCP invocation passed against the staged binary (22 advertised tools, readable `inspect_symbol`). A separate authenticated Codex agent also successfully invoked `get_document_symbols`, and T43 produced six additional genuine agent-initiated tool calls.
- Claude Code executable `2.1.220` is installed, but **the authenticated Claude Code model integration is blocked**: the CLI reported `Failed to authenticate: OAuth session expired and could not be refreshed`. The real Claude agent call cannot be declared complete on this host until its owner reauthenticates. The direct MCP protocol itself is working.

**Still NOT a final release:** This is two independently repeated Go tasks, not the 20+ heterogeneous tasks and full 56-task evidence required by the release criteria. The model-usage policy matters; do not rebrand conditional semantic-use guidance as unprompted free selection. Do not tag or publish an official release until the remaining gates are fulfilled. No CI files were changed, and the pre-existing original worktree remains intact.

### MCP contract snapshot

The 22 live MCP tool schemas, annotated read/write hints, titles and descriptions were captured from the candidate's actual Codex app-server session in [`docs/mcp-tool-contracts-2026-11-11.json`](mcp-tool-contracts-2026-11-11.json) and summarized in [`docs/mcp-tool-contracts-2026-11-11.md`](mcp-tool-contracts-2026-11-11.md). The direct smoke also verified that `preview_rename` with missing `new_name` is rejected with an MCP error. This documents the actual published input surface; the per-tool error/result combination matrix is **not** yet exhaustively certified.
