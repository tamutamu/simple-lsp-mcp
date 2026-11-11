## Agent skills

### Issue tracker

Issues and specs are tracked as local Markdown under `.scratch/`. See `docs/agents/issue-tracker.md`.

### Triage labels

Uses the default five canonical triage labels. See `docs/agents/triage-labels.md`.

### Domain docs

Uses a single-context layout. See `docs/agents/domain.md`.

### Focused LSP bridge

When `simple-lsp` is connected, use its semantic tools where they provide information a text search cannot resolve reliably; do not call one solely to satisfy a quota. If it is not connected, continue with normal tools. For example, use `search_symbols` or `get_document_symbols` to locate a declaration, `inspect_symbol(view="source")` for a targeted implementation, `find_references(relation_view="lines")` for exact uses and source lines, `get_incoming_calls` / `get_outgoing_calls` for direct call edges, and `find_implementations` or type-hierarchy tools for polymorphism. For an **unambiguous literal selector** such as `tools.New(`, ordinary `rg` may answer more cheaply than requesting every LSP reference. Use LSP when aliases, polymorphism or semantic identity affect the answer. When only production callers matter, use `find_references(exclude_test_files=true,include_declaration=false,relation_view="lines")` to omit test-file hits **by request**; the tool reports the excluded count. Keep all references when tests or complete change impact matter. **Never use `inspect_symbol(related=["references"])` as an exhaustive reference listing:** its per-relation limit is at most 20, and it explicitly reports truncation. Do not search again for paths already supplied unless the result is incomplete. The MCP does not decide impact or implementation strategy: the coding agent does.

For semantic rename use **read-only** `preview_rename`; for formatting use **read-only** `preview_format` (full profile). Call write-capable `rename_symbol(apply=true)` or `format_document(apply=true)` only with authorization; destructive annotations can block previews in noninteractive clients. When the file and declaration are known, call `inspect_symbol(path=...,symbol_path=...,view="edit")` **directly without calling an outline first**. Batch multiple **verified** functions with `targets` (up to 8); guessing one target invalidates the entire batch. Reuse returned code while the file is unchanged; after an edit, reread only the changed lines or newly required context.
For a type error in a known file, a warm `get_diagnostics(path=...)` can reveal the exact mismatch quickly. Avoid duplicate diagnostic retrieval if the compiler already supplied the same facts.

For routine edits, choose the needed information: a compact file outline, a targeted function body via `inspect_symbol`, or only its location or signature. Do not reread entire files when the targeted source snippet suffices; use normal editing and tests afterward. After code changes, run actual tests; call `get_diagnostics` only if type/diagnostic feedback would help. Never treat diagnostics as a substitute for tests. If the MCP is not connected, continue with ordinary tools rather than installing anything.
