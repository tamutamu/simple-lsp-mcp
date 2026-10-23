# Claude Code agent evaluation — full-stack-fastapi-template

- **Date:** 2026-10-23 (Asia/Tokyo)
- **Status:** Exploratory, **one run per task and condition**, not a statistically significant benchmark.
- **Upstream:** [`fastapi/full-stack-fastapi-template`](https://github.com/fastapi/full-stack-fastapi-template), pinned at `b1125d8839482cb7bbae603ddc6d0081bf87532e` (2026-10-15).
- **simple-lsp-mcp:** local HEAD `c98a23a` (built from checkout, not the installed release).
- **Agent:** Claude Code CLI 2.1.220, Sonnet, medium effort, `--max-turns 16`, `--max-budget-usd 0.55`, `--no-session-persistence`, `--strict-mcp-config`, `--permission-mode dontAsk`.
- **Allowed tools:** Read, Glob, Grep, Edit, Write, Bash and `mcp__simple-lsp__*` (the latter was only available in MCP arms).
**Environment:** each run used a separate clean clone of the exact upstream commit. No upstream code was pushed or committed.

## Question

Does telling Claude Code to use `simple-lsp-mcp` improve a real, cross-file code-change task over its built-in tools? In particular, does LSP-based semantic rename increase correctness, reduce time or cost, or make the completion check more reliable?

## Task definitions and objective oracle

**Python:** Rename the module-level `authenticate` function in `backend/app/crud.py` to `authenticate_user` and update *actual callers*, including tests. Preserve endpoints, other functions, behavior and generated code. Expected: definition + 1 login caller + 3 test callers; exactly **3 modified files**.

**TypeScript:** Rename only the default-exported `useAuth` hook from `frontend/src/hooks/useAuth.ts` to `useSessionAuth`. Update default import bindings and usages, **keep the filename and named `isLoggedIn` export unchanged**. Expected: hook file + 10 consumers; exactly **11 modified files**.

All conditions received the **same task-specific prompt**. All used the same common `CLAUDE.md` guidance: make minimal behavior-preserving changes inside the checkout; do not modify generated client code, commit or push; report unavailable tests accurately.

- **Standard:** no MCP server configured and no extra tool-use guidance.
- **MCP / full:** configured `simple-lsp-mcp`, plus `CLAUDE.md` explicitly encouraging semantic discovery/refactoring with `find_symbol`, `get_symbol_context`/`impact_analysis`, `rename_symbol(apply=true)` and follow-up diagnostics. Claude also called `verify_change`.
- **MCP / rename only:** configured the same MCP, with a focused `CLAUDE.md` instruction to use `find_symbol` / `rename_symbol` where supported. Explicitly disabled `verify_change` in this arm to isolate rename cost after the full arms revealed a hang. This is a diagnostic follow-up, **not** an unbiased replacement for the full arm.

Each MCP workspace used a project-local `.simple-lsp.yaml` with `backend` routed to `pyright-langserver --stdio` and `frontend` routed to `typescript-language-server --stdio`. All CLI invocations used isolated `--mcp-config` and `--strict-mcp-config`, so the standard arm could not silently access simple-lsp.

## Results

| Task / condition | Diff correctness | Files | MCP rename calls | End-to-end | CLI-reported estimated API cost | Status |
| --- | --- | ---: | ---: | ---: | ---: | --- |
| Python / standard | Correct | 3 | 0 | 14.160 s | $0.1153702 | Success |
| Python / MCP, rename only | Correct | 3 | 2 | 20.710 s | $0.0904965 | Success |
| Python / MCP, full | Correct diff | 3 | 1 | Timeout at 170 s | Not reported | No final result |
| TypeScript / standard | Correct | 11 | 0 | 14.986 s | $0.0808924 | Success |
| TypeScript / MCP, rename only | Correct | 11 | 2 | 24.214 s | $0.0995600 | Success |
| TypeScript / MCP, full | Correct diff | 11 | 2 | Timeout at 140 s | Not reported | No final result |

All three conditions in each task produced an **identical binary Git patch** (`git diff --binary` SHA-256):

- Python: `b977af1c7c7795fd` (prefix)
- TypeScript: `0a19de668eabf0f0` (prefix)

This proves equality of the resulting edits for these specific prompts, **not behavioral equivalence across all possible runtimes**.

### Independent validation

- Python: enumerated the exact three touched files, confirmed `authenticate_user` declaration and call sites, no stale `crud.authenticate(`, and parsed all three changed files using `ast.parse`: **pass** in all arms.
- TypeScript: enumerated the exact 11 touched files, checked definition, default export and all consumers, no stale identifier outside preserved import paths, and parsed the 11 touched `.ts`/`.tsx` files with TypeScript `createSourceFile`: **pass** in all arms.
- Full backend pytest and frontend typecheck/build were **not run**: dependencies were not installed into these trial clones. The upstream snapshot also contains Python 3.14 syntax in `backend/app/api/deps.py` that fails a repository-wide compile on the host's Python 3.10.12. Do not interpret the static checks as proof of full test-suite success.

### `verify_change` on a real repository

The full MCP arms both invoked `verify_change` after a correct semantic rename. They failed to produce a final answer before their respective agent CLI timeouts. This is a **real usability failure**, not a failed refactor.

The same existing working trees were independently probed by calling `Engine.VerifyChange` with a 20-second context. Both probes reached that deadline and returned:

```text
verdict: WARNING
meta.complete: false
baseline diagnostics unavailable: INCOMPLETE_SEARCH:
  base snapshot contains symlinks or hard links;
  baseline diagnostics cannot be verified safely
```

The upstream revision tracks four symlinks:

```text
.agents/skills/fastapi
.agents/skills/sqlmodel
.claude/skills/fastapi
.claude/skills/sqlmodel
```

`internal/tools/baseline.go` currently rejects symlink entries when extracting `git archive` snapshots. This **directly explains the incomplete baseline comparison**. It does not, by itself, prove the exact cause of the long-running calls: `verify_change` also follows multiple changed symbols/references and current LSP diagnostics; performance needs separate profiling.

### What we can conclude

1. **`CLAUDE.md` prompting works:** Claude used `rename_symbol` for the actual edits, and those edits were correct.
2. **No correctness lift was observed:** built-in Grep/edit produced byte-identical patches on both tasks.
3. **No repeatable speed or cost benefit was proven:** on these two single trials, rename-only MCP was slower; the Python run reported a lower API cost and the TypeScript run a higher API cost. Variance and the different `CLAUDE.md` contents prevent causal claims.
4. **The full `verify_change` workflow is not ready for this real repository:** it timed out and cannot complete a baseline comparison when tracked links are present.
5. **Do not add more public tools on this evidence.** Fix and bound known reliability issues first, then run repeated, varied agent tasks with an objective outcome oracle and compare against built-in and official LSP features.

## Reproduction without adding a benchmark framework

Clone the pinned upstream commit into separate disposable folders. For both conditions, use the same common `CLAUDE.md` rules. For the MCP condition add:

```md
## Semantic development tools
For symbol discovery and refactoring, prefer the configured simple-lsp MCP tools over raw text replacement.
Use `find_symbol` or `search_symbols` to identify the exact symbol, `get_symbol_context`/`impact_analysis` to inspect references as needed, and `rename_symbol` with `apply=true` for a semantic rename when supported. If the LSP fails, fall back to standard editing and state the failure. After edits, check related diagnostics where feasible.
```

Configure MCP only in the enabled arm and verify registration with Claude. Use exactly the task descriptions above as prompts. Capture CLI JSONL events (tool names, result, duration, cost); independently inspect `git diff --binary` and run the listed checks. Preserve timed-out logs and **do not count a timed-out agent as a completed successful workflow**, even if it made the correct edits before stalling.
