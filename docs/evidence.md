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
