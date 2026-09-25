# React + FastAPI monorepo proof

This directory is a reproducible proof fixture for `simple-lsp-mcp`. It is intentionally small enough to inspect, but it exercises the parts that matter in a real mixed-language monorepo.

## What this proves

One `simple-lsp-mcp` process opens the repository root, then routes each file to the LSP configured for its project:

- `apps/web/**` -> TypeScript Language Server (`.ts` and `.tsx`)
- `apps/api/**` -> Pyright (`.py`)

The same MCP tools work across both projects. A caller does not need a TypeScript-specific MCP API and a separate Python-specific MCP API. `path` is enough for language inference in target-based tools.

The configuration also demonstrates that LSPs are not hard-coded presets. Each project can pass its own:

- `args`
- `env`
- `settings` via `workspace/didChangeConfiguration`
- `initialization_options` via LSP `initialize`

See [`.simple-lsp.yaml`](.simple-lsp.yaml).

## Reproduce it

Install `typescript@6`, `typescript-language-server`, and `pyright`, then from the repository root run:

```sh
simple-lsp-mcp doctor --workspace examples/react-fastapi-monorepo \
  --probe apps/web/src/components/UserList.tsx

SIMPLE_LSP_REAL_LSP=1 go test ./internal/tools \
  -run '^TestRealReactFastAPIMonorepoExample$' -count=1 -v
```

The integration test uses the actual TypeScript Language Server and Pyright. It verifies that the same engine can:

1. resolve `loadUsers` from TSX without an explicit `language`,
2. resolve `list_users` from Python without an explicit `language`,
3. enumerate workspace symbols from both languages in one paginated tool,
4. preserve project-specific `env`, `settings`, and `initialization_options`, and
5. follow semantic calls across files when the language server reports call hierarchy data.

## Agent prompts

After connecting this directory as the MCP workspace, these are useful manual checks:

```text
Use simple-lsp only. Show me the implementation context for loadUsers.
```

```text
Use simple-lsp only. Find list_users and tell me which function provides its data.
```

```text
Use simple-lsp only. Enumerate the workspace symbols and show examples from both frontend and backend.
```

The point is not that LSP can parse TypeScript and Python. The point is that one small MCP surface gives an agent consistent, bounded semantic navigation across both projects while keeping each language server independently configurable.
