# Evidence

This page separates what `simple-lsp-mcp` can currently prove from what is still a hypothesis. The goal is to make performance and usefulness claims reproducible rather than promotional.

## Proven in automated tests

### Real language servers, not protocol mocks

CI launches `gopls`, TypeScript Language Server, and Pyright as real subprocesses. The integration suite verifies document symbols, symbol lookup, stale-symbol handling, monorepo search, semantic slices, workspace enumeration, semantic rename, formatting, and WorkspaceEdit-backed code actions.

### One MCP engine can serve a mixed React + FastAPI monorepo

[`examples/react-fastapi-monorepo`](../examples/react-fastapi-monorepo/) configures two independent project roots:

- `apps/web` -> TypeScript Language Server for `.ts` / `.tsx`
- `apps/api` -> Pyright for `.py`

`TestRealReactFastAPIMonorepoExample` starts both real LSPs from one `Engine` and verifies:

- `find_symbol(path=..., symbol_path=...)` infers TypeScript React and Python without a caller-supplied language,
- `list_workspace_symbols` returns frontend and backend symbols from one workspace traversal,
- `get_semantic_slice` follows `loadUsers -> fetchUsers` across TypeScript files,
- `get_semantic_slice` follows `list_users -> get_users` across Python files, and
- the same public MCP tool surface is used for both languages.

### LSP configuration is forwarded, not reduced to fixed presets

The example uses project-specific `env`, `settings`, and `initialization_options`. `TestSessionPassesConfiguredEnvSettingsAndInitializationOptions` uses a real child process speaking LSP framing and verifies that:

- configured environment variables reach the language-server process,
- `initialization_options` are present in the LSP `initialize` request, and
- `settings` are sent via `workspace/didChangeConfiguration`.

This matters for language servers that need project-specific memory limits, analysis modes, interpreter paths, preferences, or vendor-specific initialization data.

### Search completeness fails closed

Workspace-wide symbol resolution searches every configured LSP instance for a language. If a bounded search cannot finish, the server reports `INCOMPLETE_SEARCH` instead of turning a partial search into a false not-found or false unique result.

### Full API retained within a bounded MCP catalog

The public MCP surface contains 26 tools after adding `verify_change`, including all exact definition/reference/call/type primitives. High-level semantic workflows are listed first, but low-level primitives are not removed or deprecated.

`TestToolCatalogStaysWithinPromptBudget` serializes the same tool metadata exposed to MCP clients and enforces a 25 KiB upper bound. The current catalog is 24,639 bytes after adding `verify_change`. This keeps the complete API while limiting model-visible tool-definition overhead.

### Semantic Change Guard catches incomplete changes

`TestRealSemanticChangeGuard` creates a committed Go baseline, changes a function signature without updating its caller or test, and runs `verify_change(base="HEAD")`. The guard maps the changed line to the `Add` function, finds the untouched caller and related test through LSP impact analysis, retrieves gopls `WrongArgCount` diagnostics, and returns `FAIL`.

`TestRealSemanticChangeGuardFailsOnChangedLineDiagnostic` changes only a function body to introduce a type error and verifies that a **new** LSP error on the changed line produces `FAIL`. `TestRealSemanticChangeGuardIgnoresPreexistingChangedLineError` commits the same type error first, then changes whitespace on that diagnostic line; `verify_change` launches an isolated gopls over the committed snapshot, classifies the error as unchanged, and returns `PASS` instead of a false regression. These tests also exposed and fixed an existing diagnostics bug: diagnostic ranges were being converted with absolute document paths instead of workspace-relative paths.

`TestRealSemanticChangeGuardReturnsRepairCandidate` introduces an unused Go import. `verify_change` identifies the diagnostic as new and returns `FAIL`, then asks gopls for code actions and returns a WorkspaceEdit-backed repair candidate with a stable `action_id` plus the exact `apply_input` needed by `apply_code_action`. The test applies that candidate, verifies the import is actually removed, and runs `verify_change` again to prove the loop ends at `PASS`. The guard itself performs no write.

The base snapshot is extracted with strict path/link checks and file/byte budgets. It is created only when the current verification scope actually contains LSP errors, avoiding a second LSP startup on clean changes.

`TestRealSemanticChangeGuardCrossLanguageDiagnostics` verifies the same new-error/base-comparison path against TypeScript Language Server and Pyright. This check exposed a missing `textDocument.publishDiagnostics` client capability: TypeScript diagnostics were previously silent even for a clear TS2322 error. The capability is now advertised and covered by a session-level regression test.

The guard's current evidence is intentionally narrower than a correctness proof: semantic impact still comes from the current working tree. The base snapshot currently supplies diagnostic regression comparison, not a complete before/after semantic relation graph or business-level invariants.

### Semantic edits are executed by real language servers

The edit integration tests verify that source changes come from LSP edit protocols rather than string replacement:

- `TestRealLSPEditing` uses `gopls` to preview and apply a cross-file rename, preview/apply document formatting, and apply a `source.organizeImports` code action backed by `WorkspaceEdit`. It also verifies that an unrelated identifier containing the old symbol text is not renamed.
- `TestRealTypeScriptRangeFormatting` exercises `textDocument/rangeFormatting` against TypeScript Language Server.
- `TestRealSemanticRenameAcrossLanguages` runs the same `rename_symbol` operation against TypeScript Language Server and Pyright and verifies that definition and reference files are both updated.

TypeScript/Python semantic operations also exercise the bounded internal project warm-up used when project-based servers have not yet attached unopened reference files. TypeScript additionally performs a semantic call-hierarchy synchronization point because `didOpen` alone can leave path-alias consumers temporarily outside tsserver's project graph. The real-LSP suite verifies cross-file references, incoming callers, impact-analysis affected files, path-alias references, and cross-file rename; the MCP still applies only the `WorkspaceEdit` returned by the language server.

## Manual external-repository check

A local, read-only evaluation was also run against `fastapi/full-stack-fastapi-template` pinned to commit `cb740b656d7a0a6c5e12c7bf8e50343ec94ee9c7` (43 Python files and 109 TypeScript/TSX files in the checkout). This is not part of CI and is reported as an external validation, not a benchmark claim.

Representative results from fresh MCP/LSP processes:

- pathless `find_symbol(symbol_path="authenticate", language="python")` resolved `backend/app/crud.py` without first knowing the file,
- pathless `find_symbol(symbol_path="create_user", language="python")` returned three distinct candidates instead of pretending the name was unique,
- `get_semantic_slice` for frontend `useAuth` returned the root plus `useCustomToast`, `isLoggedIn`, generated client methods, and ten direct dependents in about 6.5 KB of structured output,
- `impact_analysis` for backend `authenticate` found the login route plus three test callers/references after project warm-up,
- `rename_symbol(authenticate -> authenticate_user)` previewed five LSP-computed edits across the definition, login route, and tests, and
- a TypeScript path-alias case (`useTheme`) exposed an initialization gap: low-level references initially saw only the declaration. The resulting semantic-prime fix made standalone references return eight locations and rename preview eight edits across four files; a real-LSP regression test now covers this pattern.

Literal `rg` searches on this repository were effectively instantaneous, so `simple-lsp-mcp` should not claim to beat grep for raw string lookup. Its demonstrated value is semantic disambiguation, bounded context, call/impact relationships, and LSP-calculated changes. The timings above include fresh MCP and language-server startup and therefore are not interactive-session latency measurements.

## Reproduce the mixed-language proof

Install the TypeScript Language Server, TypeScript 6, and Pyright, then run:

```sh
SIMPLE_LSP_REAL_LSP=1 go test ./internal/tools \
  -run '^TestRealReactFastAPIMonorepoExample$' -count=1 -v
```

The same test runs in GitHub Actions.

## Non-callable symbol impact via LSP references (2026-10-23)

A deterministic, real-LSP regression test exposed an `impact_analysis` failure: on a Go constant `TaxRate` used by both `invoice.go` and `order.go`, the old implementation always called `textDocument/prepareCallHierarchy` and gopls returned `lsp error 0: TaxRate is not a function`. Yet `textDocument/references` can locate both affected files.

Instead of introducing another public MCP tool, `impact_analysis` now uses a symbol's LSP `documentSymbol` **kind** to determine whether Call Hierarchy is applicable. Functions/methods/constructors retain the existing incoming-call traversal. Constants/fields/types (or servers that do not advertise Call Hierarchy) use **LSP references + implementations**, preserve affected-file reasons, and include `analysis_mode: "references_only"` and `meta.complete=false` with a warning because transitive callers were *not* analyzed. Missing LSP data is never synthesized from grep. Unexpected Call Hierarchy request errors are still surfaced, not silently converted to success.

Verified against real language servers (rather than mock LSP JSON):

- **Go/gopls:** a constant declaration and two cross-file references; tested by `symbol_id`, `symbol_path`, and selecting an interior character of the constant name. A struct field (`Pricing/Discount`) also returns reference-only impact. Prior version failed for the constant; updated tests pass.
- **Python/Pyright:** data symbol in `rates.py` used in `invoice.py` and `order.py`; both affected files returned.
- **TypeScript/TypeScript Language Server:** exported const in `rates.ts` imported by `invoice.ts` and `order.ts`; both affected files returned.
- **No regression for functions:** `TestImpactAnalysisWithGopls` still returns `analysis_mode: call_hierarchy` and nonempty direct callers.

**Limit:** `references_only` mode does not claim to know which call chains are impacted transitively, and a position targeting a *use-site* instead of a declaration can still require the caller to resolve the definition first. The follow-up usage-graph enhancement below addresses a subset of transitive paths, but does not prove completeness or whether an edit is required.

## Composed LSP usage graph (2026-10-23)

**Previous behavior:** for a non-callable symbol, `impact_analysis` returned its definition and raw reference file/line locations. To discover which functions use a changed field/constant, an agent had to open each file and manually interpret the location. To find an upstream handler that never mentions the constant, the agent then had to identify each referencing function and follow its callers separately.

**New behavior:** the **existing** `impact_analysis` now composes three authoritative LSP operations without a new public MCP tool:

1. `textDocument/references` finds exact references to the selected constant/type/field (not occurrences of the same spelling).
2. `textDocument/documentSymbol` finds the **innermost containing declaration** at each reference. `direct.dependent_symbols` includes its `symbol_id`, `symbol_path`, reference lines/count and `test_candidate` when the referencing file has a conventional test filename.
3. With `depth >= 2`, `textDocument/prepareCallHierarchy` and `callHierarchy/incomingCalls` follow those referencing functions to their callers, which appear under `indirect.callers` with the intermediate function shown as `via.name`. TypeScript's callable `const`/arrow functions are included.

Example proven using **real gopls**, not a mock: `TaxRate` in `config.go` has LSP references inside `Invoice` (`invoice.go`), `Order` (`order.go`) and `TestTaxRate` (`taxrate_test.go`). `HandleInvoice` (`handler.go`) calls `Invoice` **without referring to `TaxRate` directly**. A single `impact_analysis` request with `depth:2` returns these exact owners, flags `TestTaxRate` as a *candidate* test, and returns `HandleInvoice` as an indirect caller via `Invoice`. The old `find_references` primitive only returns direct source locations. The same two-hop graph is independently verified with **real Pyright** (`TAX_RATE → invoice → handle_invoice`) and **real TypeScript Language Server** (`taxRate → invoice → handleInvoice`). Existing function-call analysis still passes unchanged.

The implementation is bounded to 128 reference locations, 16 distinct ownership-inspection files, 24 output owner symbols (ranked by reference count), 12 caller-expansion roots, and the existing overall 64-LSP-request/60-second limits. Results are marked partial or truncated when limits/capabilities prevent coverage. `analysis_mode:"reference_usage_graph"` indicates that an indirect caller was actually found; `references_only` is preserved if none was found. No static call graph is ever invented from text matching, and a `test_candidate` is not a claim of test coverage. **This validates richer semantic data in one model-visible call, not an A/B-proven improvement in agent editing success.**

Runnable tests: `TestRealGoImpactForNonCallable`, `TestRealImpactForDataSymbolsAcrossLanguages`, and the existing callable `TestImpactAnalysisWithGopls`, with `SIMPLE_LSP_REAL_LSP=1`.

### Real application verification (not only synthetic fixtures)

On a fresh, unmodified checkout of `fastapi/full-stack-fastapi-template` pinned to `b1125d8` (2026-10-15), `impact_analysis` was run against the actual module-level `settings` declaration in `backend/app/core/config.py`, with `depth:2,limit:100` and real Pyright. The result contained **100 displayed direct references, 63 identified referencing declarations (24 displayed under a hard output cap), 8 indirect callers and 17 affected-file candidates**. Among the returned LSP-verified edges were `settings → send_email → recover_password` and `settings → send_email → create_user`. The tool completed in **3,521 ms** on this single measured run; `meta.complete=false` and `meta.truncated=true` correctly disclosed the 24-of-63 ownership cap, 12-of-63 expansion cap, and the unsupported implementation capability. No project sources were modified. This is objective evidence of multi-hop semantic navigation on an existing production template, **not** proof of lower agent edit error rates or a reproducible latency improvement.


## Zero-config startup and managed LSPs

`TestRealAutoDetectedLanguageServers` creates a Go + FastAPI/Python + TypeScript workspace **without `.simple-lsp.yaml`**, calls `config.Load`, and then requests real `documentSymbol` results through gopls, Pyright and TypeScript Language Server. It verifies that all three project roots are selected, no config is written and no package is downloaded. CI includes this test in the opt-in real-LSP suite.

The CLI was also tested manually against an isolated real [`fastapi/full-stack-fastapi-template`](https://github.com/fastapi/full-stack-fastapi-template) checkout at `b1125d8` **without YAML**. `doctor --auto-install-lsp` found the Python, TypeScript, HTML and CSS profiles in their project roots, and `doctor --probe` returned `probe_ok=true` for `backend/app/crud.py` and `frontend/src/hooks/useAuth.ts`. Missing optional HTML/CSS executables were reported as missing without opt-in; with opt-in, the doctor selected managed `npx` launchers instead.

To verify the actual download path rather than merely previewing the command, the LSP executables were **deliberately hidden from PATH** and managed mode was used to fetch and start TypeScript Language Server (`typescript@5.9.3` / `typescript-language-server@5.3.0`), Pyright (`pyright@1.1.413`), and HTML/CSS Language Servers (`vscode-langservers-extracted@4.10.0`) via `npx`. gopls was also hidden from PATH and started through pinned `go run golang.org/x/tools/gopls@v0.24.0 serve`. All five language profiles returned a successful real `doctor --probe`; the no-network-download preview returned correctly without executing a package manager.

### Private, versioned LSP cache (2026-10-23)

The optional managed mode was revised from `npx`/`go run` launchers to a separate `internal/managedlsp` installer. This is a **distribution improvement**, not a new MCP tool: each pinned language server is installed once into the user's own versioned cache using `npm install --prefix ... --ignore-scripts` or `go install` with a private `GOBIN`; the managed launcher subsequently starts the cached file using its absolute path. The managed configuration does **not** inspect or require language-server executables on PATH. Go and Node toolchains remain prerequisites.

An isolated five-language workspace was used with `SIMPLE_LSP_CACHE_DIR` redirected to a disposable test directory. After downloading and installing all pinned packages, `doctor --auto-install-lsp --probe <source>` passed for Go, Python, TypeScript, HTML and CSS. The tested cached executables were:

- `go/0.24.0/gopls`
- `python/1.1.413/node_modules/pyright/langserver.index.js`
- `typescript-javascript/5.3.0/node_modules/typescript-language-server/lib/cli.mjs`
- `html-css/4.10.0/node_modules/vscode-langservers-extracted/bin/{vscode-html-language-server,vscode-css-language-server}`

Then **every LSP executable was removed from PATH**, `npm_config_offline=true` was set, and all five `doctor --probe` requests succeeded again using only the managed cache. Go itself remained on PATH for gopls workspace analysis: removing the Go toolchain produced `lsp error 0: no views`, confirming that bundling the LSP does not eliminate the language toolchain prerequisite. The probes did not modify source files or create `.simple-lsp.yaml`.

Unit tests additionally use an isolated fake `npm` / `go` installer to verify absolute-path launch commands, cached reuse after removing npm, concurrent requests causing a single installation, PATH independence, correct `GOBIN` override, and HTML/CSS package sharing. The entire install path is opt-in via `--auto-install-lsp` and the plugin's `.mcp.json`; the un-managed default still uses installed LSP commands.

These are runtime setup smoke tests, **not** proof that simple-lsp improves an agent's development success rate.

## Exploratory Claude Code comparison (2026-10-23)

One **read-only code-review task** asked Claude Code (Sonnet, medium effort) to trace the public WorkspaceEdit write paths and review stale-file, symlink, and partial-write protection. The same prompt and 12-turn limit were used with and without this MCP; both sessions permitted Read, Glob, and Grep and denied Bash/Edit/Write. MCP configuration was isolated between runs.

| Metric | Without MCP | With MCP available |
| --- | ---: | ---: |
| Successful review | Yes | Yes |
| Read/Grep calls | 8 | 5 |
| Elapsed time, as reported by CLI | 28.7 seconds | 18.4 seconds |
| Estimated API cost, as reported by CLI | $0.153 | $0.125 |
| Calls to simple-lsp-mcp | n/a | **0** |

**This does not establish an MCP speed or cost benefit.** Claude did not call the MCP in the enabled condition, and a single stochastic run per condition cannot isolate model variance. The initial trial was discarded because permissions differed and MCP calls were denied. Both valid-condition reviews independently identified the direct `os.WriteFile` implementation, best-effort multi-file rollback, and residual race/crash risks. The review is useful as a correctness investigation, not product-impact evidence.

A separate, **prompted real-repository** experiment against `fastapi/full-stack-fastapi-template` is documented in [the 2026-10-23 agent evaluation](agent-evaluation-2026-10-23.md): Claude did use `rename_symbol`, both renames matched the built-in-tool diff byte-for-byte, and the full MCP arms timed out on `verify_change`. No user-value advantage is claimed from those results.

## Not proven yet

The next evidence layer is agent-level A/B testing. We still need controlled tasks comparing Claude Code and Codex with and without `simple-lsp-mcp`, measuring at least:

- task success / correctness,
- agent-visible tool calls,
- source bytes returned to the model,
- elapsed time, and
- whether the agent touched the correct files and symbols.

Until those experiments exist, the project should claim **verified semantic navigation/edit capability and bounded context**, not a percentage improvement in agent accuracy or speed.

## Coding-agent outcome A/B pilot (2026-10-24)

**Question:** does giving a *coding agent* simple-lsp-mcp produce better actual code changes, not just better LSP responses? Unlike the real-LSP integration tests above, the following trials used Claude Code to **edit separate, identical source checkouts** and then independently inspected the produced Git diffs and ran acceptance checks. In each pair the agent was given the same task and model; the baseline had no external MCP server, and the treatment had this project's MCP server and short semantic-tool guidance. The setup is therefore a comparison of the *usable integration*, not an isolation of tool access from tool instructions.

| Actual source and task | Agent outcome, no MCP | Agent outcome, simple-lsp-mcp | Interpretation |
| --- | --- | --- | --- |
| `fastapi/full-stack-fastapi-template` at `f45203ff`: rename `app.crud.authenticate` to `authenticate_user` and update real Python callers | Success; 3 changed files, 6 tool calls, 16 s, estimated $0.118694 | Success; **identical Git diff**, 4 tool calls, 13 s, estimated $0.121009 | Fewer calls and seconds in this run, but **higher API cost**; no correctness win |
| This repository at `8d76608`: rename only `(*symbol.Registry).Register` to `Store` and update Go callers and tests | Success; 4 changed files, 18 tool calls, 32 s, estimated $0.134215 | Success; **identical Git diff**, 4 tool calls, 22 s, estimated $0.075861 | In this run: 78% fewer tool calls, 31% faster, 43% cheaper; no correctness win |

### Go task repeatability check

To see whether the faster Go result was a one-off, we repeated that *same* task in **three independent pairs** (six fresh Claude sessions, including the first pair). The second pair ran MCP first; the first and third ran control first. All six edits passed the same acceptance check, all six `git diff` outputs were **byte-for-byte identical** (SHA-256 prefix `4c078e56abca`), and all three MCP sessions invoked `rename_symbol` rather than merely receiving its tool schema.

| Trial | No MCP: tool calls / s / USD | MCP: tool calls / s / USD | Result |
| --- | --- | --- | --- |
| 1 | 18 / 32 / $0.134215 | 4 / 22 / $0.075861 | Identical passing diff |
| 2 | 19 / 27 / $0.136258 | 4 / 19 / $0.072796 | Identical passing diff |
| 3 | 18 / 26 / $0.134302 | 4 / 22 / $0.076570 | Identical passing diff |
| **Median** | **18 / 27 / $0.134302** | **4 / 22 / $0.075861** | **78% fewer calls, 19% less time, 44% lower reported cost** |

This is repeatability **for one Go refactoring task**, not an unbiased estimate across development tasks. No claim of statistical significance, improved functional correctness or general coding effectiveness follows from three repeated pairs. It does show a reproducible reduction in agent tool round trips for this particular LSP-suitable change. The earlier Python task remains a counterexample to a blanket claim about cost: its MCP run cost slightly more.

### Exact agent task prompts

The text below is the original English user prompt for each pair, not a retrospectively simplified task description. The same prompt text was used in both conditions; only the additional availability/configuration of the MCP and a brief treatment-side semantic-tool suggestion changed.

**Python task:**

> In this FastAPI backend repository, rename the Python function app.crud.authenticate to authenticate_user, updating every real call site in backend/app and backend/tests. Preserve behavior and avoid changing unrelated symbols. Do not add aliases, compatibility wrappers, dependencies, or unnecessary edits. If practical, verify syntax. Finish with a brief description of what changed.

**Go task (all three pairs):**

> Refactor the Go codebase: rename only the method `(*symbol.Registry).Register` in `internal/symbol/registry.go` to `Store` and update all its real callers across the project, including tests. Avoid changing any unrelated `Register` methods or SDK names. Do not add wrappers, aliases, dependencies, or unrelated edits. Run `go test ./...` when possible and report the result.

The MCP-specific system prompt appended only for treatment was: *When useful, use the simple-lsp MCP for semantic navigation and rename, and confirm its actual results before finishing.* We used `claude -p` with `--model sonnet --effort low --strict-mcp-config --no-session-persistence --output-format stream-json --verbose --permission-mode dontAsk`, `--max-turns 14` for Python / `16` for Go, and the same basic editing and shell-tool permissions. A fresh temporary `--mcp-config` pointed the treatment at the repository's locally built `simple-lsp-mcp` binary; the control ran with **no MCP config**. The Go task used the source at the `8d76608` revision, independent of later release metadata edits. Repeat the trials on clean identical Git source snapshots; never run them in a user's working tree.

### How success was checked

- Python task: independently parse the files with Python's AST parser; confirm the old declaration has disappeared, `authenticate_user` exists, all three `crud.authenticate_user` call sites remain (and no `crud.authenticate` calls), only the three expected files changed, and `python3 -I -m compileall -q backend/app backend/tests` succeeds. No end-to-end HTTP test/database integration was exercised.
- Go task: check that `(*Registry).Store` exists in `internal/symbol/registry.go`, no old `Register` method remains, compare the two complete `git diff` outputs byte-for-byte, run `go test ./...` **independently of the agent**, and check `git diff --check`. The changes affected the registry, its tests, and callers in `internal/tools/helpers.go` and `internal/tools/hierarchy.go`.
- For both tasks the *actual MCP invocation* `mcp__simple-lsp__rename_symbol` appears in the agent's tool-use trace, and changes were applied, not merely previewed. Control traces instead show `Grep`/`Edit` calls. The two groups used the same `claude-sonnet-5-5` model (Claude Code 2.1.301), low effort, an identical user task, independent initial checkouts, `--strict-mcp-config`, `--no-session-persistence`, and the same basic file/shell tools. Each agent process started fresh.
- Wall time is process elapsed time, not cumulative tool execution time; API costs are Claude's own reported estimated `total_cost_usd`. Tool-call count includes discovery tool calls such as `ToolSearch`, not just writes. These are observed figures, not statistical estimates.

### Interpretation and acceptance criteria

**This pilot does not establish that coding agents generally become more correct, faster, or cheaper.** Both succeeded, two tasks are both symbol renames, and a single session can vary with prompt caching, model sampling, host load and LSP warm-up. An identical passing diff is useful evidence that the LSP shortcut does not degrade correctness *in these cases*, not a universal correctness guarantee. It would be misleading to present these timings as a product-wide benchmark or claim a statistically significant improvement.

For a general performance claim, preregister representative tasks with independent acceptance tests **before** comparing agents: multi-file code changes, symbol-name collisions, debugging, regression repair, impact analysis, and at least one substantial TypeScript, Python, and Go repository. Use equal model/effort/tool permissions, separate identical worktrees, alternate or randomize treatment order, capture JSON tool traces and actual code diffs, and repeat each task enough times to estimate variation. A favorable signal requires **no drop in passing acceptance checks**, no increase in irrelevant edits, and a meaningful reproducible reduction in median cost/time or tool calls with uncertainty bounds that exclude zero. Failures, skips, and timeouts must stay in the denominator. Report each task's results (including baseline wins) rather than only the best cases. Keep this as transparent evidence, **not another MCP tool or synthetic token-count benchmark**.
