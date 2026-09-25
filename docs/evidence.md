# Evidence

This page separates what `simple-lsp-mcp` can currently prove from what is still a hypothesis. The goal is to make performance and usefulness claims reproducible rather than promotional.

## Proven in automated tests

### Real language servers, not protocol mocks

CI launches `gopls`, TypeScript Language Server, and Pyright as real subprocesses. The integration suite verifies document symbols, symbol lookup, stale-symbol handling, monorepo search, semantic slices, and workspace enumeration.

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

## Reproduce the mixed-language proof

Install the TypeScript Language Server, TypeScript 6, and Pyright, then run:

```sh
SIMPLE_LSP_REAL_LSP=1 go test ./internal/tools \
  -run '^TestRealReactFastAPIMonorepoExample$' -count=1 -v
```

The same test runs in GitHub Actions.

## Not proven yet

The next evidence layer is agent-level A/B testing. We still need controlled tasks comparing Claude Code and Codex with and without `simple-lsp-mcp`, measuring at least:

- task success / correctness,
- agent-visible tool calls,
- source bytes returned to the model,
- elapsed time, and
- whether the agent touched the correct files and symbols.

Until those experiments exist, the project should claim **semantic capability and bounded context**, not a percentage improvement in agent accuracy or speed.
