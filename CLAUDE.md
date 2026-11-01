# Claude Code project guidance

When the `simple-lsp` MCP server is connected, **you MUST use at least one of its actual LSP tools before editing source code**. First identify the file or symbol with regular file search if necessary, then call a focused LSP tool that contributes to the task:

- For a **known file and function**, use `inspect_symbol(path=..., symbol_path=..., view="edit")` **directly**. Do not first call `get_document_symbols` and do not reread that function with `sed` or `cat`. The edit view includes the existing code, decorators and selected direct imports.
- For multiple known functions, pass `targets` (up to 8) to one `inspect_symbol(view="edit")` call; avoid one MCP call per function.
- Use `get_document_symbols` only when you do not yet know the declaration name or need a structural overview. Choose `view="signature"` or `view="location"` when the body is unnecessary.
- For changing declarations or public interfaces, use `find_references(relation_view="lines")` to see semantically exact usages with their source lines (and `find_implementations` if relevant).
- For renaming a function, method, or type across files: prefer `rename_symbol` to generate a semantic preview and apply the LSP edit when valid.
- For uncertainty about a call or type: use the particular definition, call-hierarchy, hover, or type-hierarchy primitive needed.

Do not read an entire file again if `inspect_symbol` already returned all source needed for the edit. **Do not call unrelated tools just to increase tool counts**. Simple LSP exposes individual LSP primitives, not aggregate impact analysis or verification. After editing, run appropriate application tests with normal tools; LSP diagnostics do not replace tests.

When `simple-lsp` is NOT connected, continue entirely with ordinary file/search/edit/test tools. Do not install MCP or stop work. This condition keeps the same `CLAUDE.md` usable in both baseline and MCP-enabled evaluations.
