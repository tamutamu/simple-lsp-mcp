# Simple LSP MCP

Status: ready-for-agent

## Product contract

The authoritative product specification is the implementation design supplied on
2026-07-31, version 0.1. It defines a read-only, symbol-first LSP-to-MCP bridge
implemented in Go.

This tracker decomposes that specification without changing these invariants:

- Exactly 14 stable MCP tools are always registered.
- Language servers are the only semantic index; no grep, AST index, embedding,
  or fallback emulation is exposed.
- MCP and LSP both use stdio; stdout is reserved for MCP protocol traffic.
- All public paths are workspace-relative and confined to the workspace real
  path.
- Language servers start lazily. TypeScript and JavaScript share one session.
- Runtime `ServerCapabilities` decide whether an operation is supported.
- Public positions are one-based Unicode code-point positions; LSP positions use
  the negotiated encoding.
- The bridge never applies edits and rejects `workspace/applyEdit`.
- A crashed language server is restarted and the tool operation retried at most
  once.

## Required tools

`search_symbols`, `list_workspace_symbols`, `get_document_symbols`, `get_symbol`, `get_definition`,
`find_references`, `find_implementations`, `get_type_definition`,
`get_declaration`, `get_incoming_calls`, `get_outgoing_calls`,
`get_supertypes`, `get_subtypes`, and `get_diagnostics`.

## Supported profiles

Python (Pyright), TypeScript/JavaScript (typescript-language-server), Go
(gopls), HTML, and CSS (vscode-langservers-extracted).

## Definition of done

All completion conditions in design version 0.1 are covered by the acceptance
criteria in `plan.md` and issues `01` through `13`.
