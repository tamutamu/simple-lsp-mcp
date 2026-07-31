# 06 - Implement normalization and ephemeral symbol registry

Status: ready-for-agent
Blocked by: 05

## Outcome

All relevant LSP result variants become stable relative-path output, and opaque
symbol handles survive unambiguous file edits without becoming an index.

## Work

- Normalize Location/LocationLink, DocumentSymbol/SymbolInformation, hierarchy
  items, and diagnostics.
- Preserve DocumentSymbol children; for SymbolInformation use declared
  `containerName` only and do not infer parents.
- Resolve URIs through the workspace jail before emitting paths.
- Generate process-local opaque IDs and store the specified SymbolRecord data.
- Validate file hash before use; on mismatch rerun document symbols and match by
  name, kind, container, and old-position proximity.
- Return ambiguous and stale errors without selecting a candidate.
- Add preview extraction and centralized result/source truncation helpers.

## Acceptance

- Null, empty, mixed Location/LocationLink, flat, and hierarchical fixtures
  normalize consistently.
- IDs are opaque, unique, nonpersistent, and never derived from absolute paths.
- Rebinding tests cover exact, moved, renamed, ambiguous, deleted, and unrelated
  same-name symbols.
- All emitted locations remain inside the workspace.

## Verification

`go test ./internal/normalize ./internal/symbol`

