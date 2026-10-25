# Claude Code project guidance

When the `simple-lsp` MCP server is connected, **you MUST use at least one of its actual LSP tools before editing source code**. First identify the file or symbol with regular file search if necessary, then call a focused LSP tool that contributes to the task:

- For modifying an existing module: `get_document_symbols` on the relevant source file.
- For changing a declaration or a public interface: `find_references` (and `find_implementations` if relevant).
- For renaming a function, method, or type across files: prefer `rename_symbol` to generate a semantic preview and apply the LSP edit when valid.
- For uncertainty about a call or type: use the particular definition, call-hierarchy, hover, or type-hierarchy primitive needed.

Use the MCP results to guide the edit; **do not call unrelated tools just to increase tool counts**. Simple LSP exposes individual LSP primitives, not aggregate impact analysis or verification. After editing, run appropriate application tests with normal tools; LSP diagnostics do not replace tests.

When `simple-lsp` is NOT connected, continue entirely with ordinary file/search/edit/test tools. Do not install MCP or stop work. This condition keeps the same `CLAUDE.md` usable in both baseline and MCP-enabled evaluations.
