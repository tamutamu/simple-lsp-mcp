# 01 - Bootstrap the Go module and freeze domain contracts

Status: ready-for-agent
Blocked by:

## Outcome

A compilable Go module with shared protocol-neutral types, application errors,
limits, and test conventions that later packages can depend on without importing
the MCP SDK.

## Work

- Create `go.mod`, the command package, and the planned `internal/` package
  skeleton only where code is immediately needed.
- Select and pin the official MCP Go SDK version.
- Define public normalized types: positions, ranges, symbols, locations,
  diagnostics, response metadata, targets, and the 13 input/output contracts.
- Define all specified error codes and a typed error carrying safe structured
  details.
- Define limit defaults: 15s request timeout, 2s diagnostics wait, 500 maximum
  results, and 64 KiB symbol source.
- Add compile-time JSON contract tests for required/optional fields and
  one-based position validation.

## Acceptance

- `go test ./...` passes from a clean checkout.
- Domain packages do not import MCP SDK packages.
- All 13 tool names and error codes have one canonical definition.
- Invalid target combinations and invalid limits are representable as
  `INVALID_ARGUMENT`.

## Verification

`go test ./...`

