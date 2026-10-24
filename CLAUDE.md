# Claude Code project guidance

For cross-file changes, debugging, or substantial refactoring, **actually call** the connected `simple-lsp` MCP's `get_symbol_context` / `get_semantic_slice` and `impact_analysis` for the relevant symbol before editing. Tool discovery alone is not enough. Prefer semantic `rename_symbol` over text substitutions; after changing code use `verify_change` and real tests. If MCP is unavailable, use normal tools and do not block.

Follow the remaining project conventions in `AGENTS.md`. Do not force MCP use for documentation, literal text search, or trivial edits.
