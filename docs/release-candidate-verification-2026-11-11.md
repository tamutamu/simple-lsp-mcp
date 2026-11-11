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
