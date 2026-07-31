# 05 - Implement document synchronization and position conversion

Status: ready-for-agent
Blocked by: 02, 04

## Outcome

Known documents are safely loaded and synchronized, and public Unicode
code-point positions convert exactly to negotiated LSP encodings.

## Work

- Store path, URI, language ID, version, hash, bytes, and open state.
- On first use send full-text `didOpen`; before later operations hash the file
  and send full-text `didChange` when changed.
- Serialize synchronization per document and keep versions monotonic.
- Convert both directions between one-based public positions and zero-based
  UTF-8/UTF-16/UTF-32 LSP positions.
- Reject invalid lines, columns, mid-code-unit positions, invalid UTF-8, and
  changed snapshots during conversion.
- Produce safe file URIs on Unix and Windows.

## Acceptance

- Unchanged files do not emit duplicate changes.
- Concurrent sync emits one open and ordered versions.
- Tests cover ASCII, Japanese, emoji, combining marks, CRLF, empty/final lines,
  and all three encodings.
- No document read bypasses workspace resolution.

## Verification

`go test -race ./internal/document`

