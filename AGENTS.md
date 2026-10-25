## Agent skills

### Issue tracker

Issues and specs are tracked as local Markdown under `.scratch/`. See `docs/agents/issue-tracker.md`.

### Triage labels

Uses the default five canonical triage labels. See `docs/agents/triage-labels.md`.

### Domain docs

Uses a single-context layout. See `docs/agents/domain.md`.

### Focused LSP bridge

When `simple-lsp` is connected, invoke at least one relevant LSP tool before editing source, as required by `CLAUDE.md`. If it is not connected, continue with normal tools. For example, use `search_symbols` or `get_document_symbols` to locate a declaration, `inspect_symbol(view="source")` for a targeted implementation, `find_references(relation_view="lines")` for exact uses and source lines, `get_incoming_calls` / `get_outgoing_calls` for direct call edges, and `find_implementations` or type-hierarchy tools for polymorphism. Follow up with **only** the specific relation you need. The MCP does not decide impact or implementation strategy: the coding agent does.

Prefer `rename_symbol` for a semantic rename where name collisions are possible; preview writes by default. For routine edits, choose the needed information: a compact file outline, a targeted function body via `inspect_symbol`, or only its location or signature. Do not reread entire files when the targeted source snippet suffices; use normal editing and tests afterward. After code changes, run actual tests; call `get_diagnostics` only if type/diagnostic feedback would help. Never treat diagnostics as a substitute for tests. If the MCP is not connected, continue with ordinary tools rather than installing anything.
