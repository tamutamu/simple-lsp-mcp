# 08 - Deliver symbol search, document symbols, and get_symbol

Status: ready-for-agent
Blocked by: 04, 06, 07

## Outcome

The first end-to-end symbol-first workflow operates through MCP, lazy LSP, the
registry, normalization, and source extraction.

## Work

- Implement shared input validation, routing, sync, capability, timeout, and
  error-mapping middleware for tools.
- Implement `search_symbols`, including language-specific operation, parallel
  all-profile operation, post-result kind filtering, partial warnings, and
  limits.
- Implement `get_document_symbols` with hierarchy preservation and IDs on every
  node.
- Implement `get_symbol` from registry range only; do not call hover.
- Default `include_source=true`; cap source at 64 KiB and expose truncation.
- Register handlers into the MCP server shell.

## Acceptance

- One real or fake LSP process starts only on first relevant call.
- Multi-profile search preserves successful results when one executable is
  missing and records the warning.
- Unsupported symbol capabilities return `METHOD_NOT_SUPPORTED`.
- `search_symbols -> get_symbol` succeeds with Japanese/emoji source and safe
  one-based ranges.

## Verification

`go test -race ./internal/tools ./internal/mcpserver`

