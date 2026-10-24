# Claude Code project guidance

The connected `simple-lsp` MCP is a set of precise **optional LSP primitives**, not a mandatory workflow. For ambiguous symbols, cross-file renames, inheritance, or call dependencies, select exactly the semantic query needed (`get_definition`, `find_references`, `get_incoming_calls`, `find_implementations`, etc.). Prefer semantic `rename_symbol` when appropriate. Do not call MCP tools for routine edits or for the sake of tool usage; use normal file tools and tests as needed. The agent decides how to combine results.

See `AGENTS.md` for the project's remaining conventions.