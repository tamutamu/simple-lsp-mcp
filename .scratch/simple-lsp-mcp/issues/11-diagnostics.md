# 11 - Implement push and pull diagnostics

Status: ready-for-agent
Blocked by: 05, 08

## Outcome

Document and workspace diagnostics use pull when advertised and otherwise return
well-defined push-cache results with explicit completeness.

## Work

- Maintain a URI-keyed publishDiagnostics cache with document versions when
  supplied.
- For a path, sync first, prefer document pull diagnostics, otherwise wait up to
  the configured push deadline.
- Without a path, prefer workspace diagnostics; otherwise return current cache
  with `complete=false`.
- Support related information, severity filtering, limits, refresh requests,
  unchanged/full pull reports, and partial-result progress if required by the
  chosen protocol subset.
- Normalize severities and jail every related location.

## Acceptance

- Pull takes precedence when `diagnosticProvider` is advertised.
- Push timeout returns deterministic metadata, not a hung request.
- Workspace fallback clearly reports incomplete cached coverage.
- Stale-version notifications do not overwrite newer cached diagnostics.
- Severity and related-location fixtures normalize correctly.

## Verification

`go test -race ./internal/tools ./internal/lsp/session -run Diagnostics`

