# 07 - Implement the MCP server shell and 13 schemas

Status: ready-for-agent
Blocked by: 01

## Outcome

The stdio MCP server initializes and always lists exactly 13 tools with stable
input/output schemas and consistent error/result encoding.

## Work

- Construct the server with the official Go SDK and register all tool metadata.
- Express the mutually exclusive Target forms in JSON Schema.
- Define schemas for filters, limits, metadata, normalized symbols, locations,
  hierarchies, diagnostics, and structured errors.
- Convert domain results to `structuredContent` and semantically identical JSON
  `TextContent`.
- Reserve stdout for SDK protocol output and route slog to stderr.
- Add a handler registry so later tickets attach implementations without
  redefining schemas.

## Acceptance

- `tools/list` contains exactly the specified names in a stable contract.
- Every input and output schema validates against representative success and
  error fixtures.
- Structured content and parsed text content are deeply equal.
- A subprocess contract test proves stderr logs never contaminate stdout.

## Verification

`go test ./internal/mcpserver`

