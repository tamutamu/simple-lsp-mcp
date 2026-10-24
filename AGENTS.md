## Agent skills

### Issue tracker

Issues and specs are tracked as local Markdown under `.scratch/`. See `docs/agents/issue-tracker.md`.

### Triage labels

Uses the default five canonical triage labels. See `docs/agents/triage-labels.md`.

### Domain docs

Uses a single-context layout. See `docs/agents/domain.md`.

### Focused LSP bridge

Use `simple-lsp` only when the requested code task benefits from semantic identity rather than text search. For example, use `search_symbols` or `get_document_symbols` to locate a declaration, `find_references` for exact uses, `get_incoming_calls` / `get_outgoing_calls` for direct call edges, and `find_implementations` or type-hierarchy tools for polymorphism. Follow up with **only** the specific relation you need. The MCP does not decide impact or implementation strategy: the coding agent does.

Prefer `rename_symbol` for a semantic rename where name collisions are possible; preview writes by default. For routine edits use normal code-reading, editing and tests, without unnecessary MCP round trips. After code changes, run actual tests; call `get_diagnostics` only if type/diagnostic feedback would help. Never treat diagnostics as a substitute for tests. If the MCP is not connected, continue with ordinary tools rather than installing anything.