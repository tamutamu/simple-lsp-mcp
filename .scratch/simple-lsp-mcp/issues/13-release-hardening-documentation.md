# 13 - Harden CLI, CI, releases, observability, and user documentation

Status: ready-for-agent
Blocked by: 12

## Outcome

The server is reproducibly buildable and operable as a single cross-platform
binary with documented installation, configuration, security, and support
boundaries.

## Work

- Finish CLI validation, signals, bounded shutdown, version output, and exit
  codes.
- Add structured stderr logging at specified levels with redaction tests.
- Add CI for format, vet, tests, race tests where supported, and multi-OS builds.
- Add GoReleaser configuration for Linux/macOS/Windows archives and checksums.
- Write README installation, language-server prerequisites, MCP host examples,
  tool reference, config reference, troubleshooting, and limitations.
- Add MCP Inspector smoke instructions and a scripted fake-LSP smoke path.
- Audit dependencies, licenses, generated artifacts, and release contents.

## Acceptance

- `go test ./...`, `go test -race ./...`, and `go vet ./...` pass.
- GoReleaser snapshot produces the expected OS/architecture binaries.
- CI proves stdout protocol cleanliness and all 13 tool contracts.
- README states that language servers are not auto-installed and that
  capabilities vary at runtime.
- A final checklist maps every design completion condition to an automated test
  or documented manual verification.

## Verification

`go test ./... && go test -race ./... && go vet ./...`

`goreleaser release --snapshot --clean`

