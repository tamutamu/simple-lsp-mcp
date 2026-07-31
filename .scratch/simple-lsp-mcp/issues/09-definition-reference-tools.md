# 09 - Implement definition and reference relationship tools

Status: ready-for-agent
Blocked by: 08

## Outcome

Definition, reference, implementation, type-definition, and declaration
relationships share one correct target-resolution and normalization path.

## Work

- Implement `get_definition`, `find_references`, `find_implementations`,
  `get_type_definition`, and `get_declaration`.
- Support both symbol ID and source-position targets.
- Check the exact provider capability before each request.
- Normalize null, single, and array Location/LocationLink results.
- Resolve a containing DocumentSymbol when available and attach symbol metadata
  without failing the base location when enrichment is unavailable.
- Honor `include_declaration` and result limits.

## Acceptance

- All five tools cover symbol and positional targets.
- Each unsupported provider returns the specified method and language in
  `METHOD_NOT_SUPPORTED`.
- Location enrichment never invents hierarchy and never escapes the workspace.
- `search_symbols -> get_symbol -> find_references` passes end to end without
  text search.

## Verification

`go test ./internal/tools -run 'Definition|References|Implementation|TypeDefinition|Declaration'`

