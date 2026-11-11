# Claude Code project guidance

When the `simple-lsp` MCP server is connected, use a focused LSP tool **when semantic identity, precise types, references or callers would materially improve the task**. For an unambiguous compiler error or a literal match, normal file search and tests may be sufficient; there is no required LSP-call quota. If semantic information is needed, identify the file or symbol with regular file search if necessary, then choose the smallest useful LSP operation:

- For a **known file and function**, use `inspect_symbol(path=..., symbol_path=..., view="edit")` **directly**. Do not first call `get_document_symbols` and do not reread that function with `sed` or `cat`. The edit view includes the existing code, decorators and selected direct imports.
- For multiple known functions, pass `targets` (up to 8) to one `inspect_symbol(view="edit")` call; avoid one MCP call per function.
- Use `get_document_symbols` only when you do not yet know the declaration name or need a structural overview. Choose `view="signature"` or `view="location"` when the body is unnecessary.
- For changing declarations or public interfaces, use `find_references(relation_view="lines")` to see semantically exact usages with their source lines (and `find_implementations` if relevant).
- For renaming across files, call **read-only** `preview_rename` to inspect the LSP's semantic WorkspaceEdit; invoke `rename_symbol(apply=true)` only if authorized. For formatting, call **read-only** `preview_format` first, then `format_document(apply=true)` only with write authorization. Never call the write-capable tools just to preview: clients may block them even for `apply=false`.
- For a **type mismatch in a known file**, `get_diagnostics(path=...)` can show the exact compiler error and location from a warm language server without a full build; use that specific result when it provides useful evidence, then still run tests. Do not use it if the compiler has already supplied the same actionable information.
- For uncertainty about a call or type: use the particular definition, call-hierarchy, hover, or type-hierarchy primitive needed.

Do not read an entire file again if `inspect_symbol` already returned all source needed for the edit. **Do not call unrelated tools just to increase tool counts**. Simple LSP exposes individual LSP primitives and an optional bounded impact graph, but does not replace tests or independent impact judgment. After editing, run appropriate application tests with normal tools; LSP diagnostics do not replace tests.

When `simple-lsp` is NOT connected, continue entirely with ordinary file/search/edit/test tools. Do not install MCP or stop work. This condition keeps the same `CLAUDE.md` usable in both baseline and MCP-enabled evaluations.
