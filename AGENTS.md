## Agent skills

### Issue tracker

Issues and specs are tracked as local Markdown under `.scratch/`. See `docs/agents/issue-tracker.md`.

### Triage labels

Uses the default five canonical triage labels. See `docs/agents/triage-labels.md`.

### Domain docs

Uses a single-context layout. See `docs/agents/domain.md`.

### Semantic coding with simple-lsp MCP

When the `simple-lsp` MCP is connected, use it for **semantic** work, not routine text search:

- **Before a cross-file feature or refactor**, call `get_symbol_context` or `get_semantic_slice` on the primary symbol and `impact_analysis` for likely affected callers, implementations, and tests. Discover the tool if necessary; do not stop after merely discovering it.
- Use `find_symbol` to disambiguate names. Prefer `rename_symbol` (preview first) for symbol renames instead of identifier text replacement.
- After edits, use `verify_change` as an additional semantic check and run relevant real tests. An incomplete/truncated impact result is **not** proof that nothing else is affected.
- Keep grep/read for literal text, configuration, and documentation. If the MCP is unavailable or fails, use standard code tools and continue; do not install it or block the task.
- Avoid repeated calls that do not answer a concrete coding question. The goal is accurate, minimal changes, not maximizing MCP usage.
