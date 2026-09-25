# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Add an executable React/TypeScript + FastAPI/Python monorepo proof under `examples/react-fastapi-monorepo`, backed by real TypeScript Language Server and Pyright integration tests.
- Add evidence documentation that separates verified semantic capabilities from the still-unproven agent-level speed/accuracy hypothesis.
- Verify that per-LSP `env`, `settings`, and `initialization_options` are actually forwarded to the child language-server process and protocol messages.
- Add `rename_symbol`: preview by default, optionally apply the language server's semantic `WorkspaceEdit`, with workspace confinement, regular-file checks, overlap validation, and best-effort rollback.
- Advertise LSP rename/workspace-edit client capabilities and exercise cross-file semantic rename against real `gopls` in CI.

## [0.9.0] - 2026-09-25

### Added

- Add Codex plugin packaging metadata and MCP server-level usage instructions so compatible agents receive concise guidance toward `get_semantic_slice`, outlines, impact analysis, pagination, and incomplete-search handling.
- Advertise MCP tool safety annotations: navigation/analysis tools are read-only and closed-world, while `onboard` is explicitly marked as a configuration write.
- `doctor` now reports the exact running binary/version and warns when the `simple-lsp-mcp` found on `PATH` is a different executable, making stale installations visible.

### Fixed

- Loading a workspace without configuration no longer creates `.simple-lsp.yaml` implicitly or enables language servers; the `onboard` tool is the explicit way to configure them.
- Restrict onboarding to the running workspace root, reject escaping paths and symlinks, and protect configuration writes against accidental or symlink-target overwrites.
- Report `restart_required: true` after onboarding because a running server does not hot-reload its configuration.
- Document how to detect stale binaries on the MCP client's `PATH`.

## [0.8.0] - 2026-09-23

### Breaking changes

- Replace `get_semantic_slice.max_tokens` with `max_bytes`; clients must update arguments to use a serialized-JSON byte limit.
- `list_workspace_symbols` no longer accepts a name query: it now enumerates configured source symbols page by page. Use `search_symbols` for name-based searches, and follow `next_cursor` to continue listing.

### Added

- Restore `list_workspace_symbols` as a real, paginated, query-free enumeration of configured source files via per-document LSP symbols (including nested symbols).
- `simple-lsp-mcp doctor` for configuration/executable checks and opt-in real-LSP probing; `setup claude|codex` for preview-first MCP registration.
- Real `gopls`, TypeScript Language Server, and Pyright integration tests in CI.
- Type-definition locations and reference-backed test-file candidates in `get_semantic_slice`.
- A real-`gopls` semantic-content integration test.

### Changed

- Remove the misleading `estimated_tokens` response field and `max_tokens` input; use an explicitly serialized-JSON `max_bytes` limit instead.
- Search all configured LSP instances in monorepos, exhaust up to 128 candidate files, and fail with `INCOMPLETE_SEARCH` rather than presenting partial searches as complete.
- Clarify source-code read-only behavior and the explicitly opt-in configuration writes from onboarding/setup.
- Pin TypeScript 6 for the TypeScript Language Server in CI and installation docs; the newer TypeScript 7 package does not provide its expected `tsserver.js`.

### Removed

- Remove `simple-lsp-bench` and the unvalidated agent-evaluation harness; neither established real coding-task benefits. Keep focused real-LSP integration tests instead.
- Remove the obsolete Codex smoke-test script and its unused fixtures.


## [0.7.1] - 2026-09-12

### Fixed

- Require a non-empty query for `search_symbols` and `list_workspace_symbols` instead of sending an empty workspace-symbol query to the language server.

## [0.7.0] - 2026-09-12

### Added

- `get_semantic_slice`: a token-budgeted semantic context tool that returns root source, bounded callee source, compact callers, and implementations in one call.
- `cmd/simple-lsp-bench`: a reproducible context-efficiency benchmark comparing separate navigation calls with the aggregate context tools.
- Automatic language inference for target-based tools from `symbol_id`, source-file extension, or configured LSP profiles for bare `symbol_path` lookups.

### Changed

- `get_symbol_context` gathers callers, callees, references, and implementations concurrently to reduce aggregate latency.
- Target-based MCP schemas no longer require `language` when it can be inferred safely.
- README positioning now focuses on token-efficient semantic context rather than being a generic LSP-to-MCP bridge.

## [0.6.0] - 2026-09-02

### Added

- MIT `LICENSE`.
- `CONTRIBUTING.md`, `SECURITY.md`, `CODE_OF_CONDUCT.md`, and this changelog.
- Issue and pull request templates.
- GoReleaser configuration and a release workflow that publishes prebuilt
  binaries for Linux, macOS, and Windows to GitHub Releases.
- `server.json` for the official Model Context Protocol registry.
- Claude Code plugin manifest under `.claude-plugin/`, so the server can be
  installed with `claude plugin install` instead of hand-editing MCP config.
- `get_hover`: the type a language server infers for an expression with no
  declaration of its own, such as a local variable assigned from a generic
  call — the one thing no other tool can answer.
- `symbol_path`, a human-readable identifier such as `UserService/createUser`
  built from a file's own document symbol tree. Every existing target-based
  tool (`get_definition`, `find_references`, `get_incoming_calls`, and so on)
  now accepts it alongside `symbol_id` and `path`+`line`+`column`.
- `find_symbol`: resolve a `symbol_path` directly, without a prior search call.
- `get_symbol_outline`: list a symbol's direct children, or a file's
  top-level symbols, without ever returning source text.
- `get_symbol_context`: a symbol's source, callers, callees, references, and
  implementations in a single call.
- `impact_analysis`: the blast radius of changing a symbol — direct and
  transitive callers, references, implementations, and the files they touch
  — under a fixed request/expansion/node/time budget so a highly-connected
  symbol degrades to a partial result instead of hanging.

### Changed

- Tool input schemas now advertise only the parameters each tool actually
  accepts, and every parameter carries a description.
- Positioning: from "a read-only, symbol-first MCP bridge for LSP" to
  competing on how few tool calls an agent needs to understand a codebase,
  rather than on how many LSP methods are exposed.

## [0.5.3] - 2026-08-16

### Fixed

- Resolve the correct session root directory without duplicate path joins.

## [0.5.2] - 2026-08-16

### Added

- Version is resolved dynamically from build info, so `go install`-ed binaries
  report a real version instead of `dev`.

## [0.5.1] - 2026-08-16

### Changed

- Simplified the `.simple-lsp.yaml` schema: mapping keys are now the base
  directory, removing a level of nesting.

### Added

- `--version` flag.

## [0.2.0] - 2026-08-12

### Added

- YAML-based onboarding and monorepo profiles.
- `onboard` MCP tool, replacing the previous `init` CLI subcommand.

## [0.1.0] - 2026-08-09

### Added

- Language server configuration loaded from a workspace configuration file,
  with auto-generated defaults.

## [0.0.1] - 2026-07-31

### Added

- Initial release: a read-only, symbol-first MCP bridge over local LSP servers,
  exposing symbol search, definitions, references, implementations, type
  definitions, declarations, call hierarchy, type hierarchy, and diagnostics.

[Unreleased]: https://github.com/tamutamu/simple-lsp-mcp/compare/v0.8.0...HEAD
[0.8.0]: https://github.com/tamutamu/simple-lsp-mcp/compare/v0.7.1...v0.8.0
[0.7.1]: https://github.com/tamutamu/simple-lsp-mcp/compare/v0.7.0...v0.7.1
[0.7.0]: https://github.com/tamutamu/simple-lsp-mcp/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/tamutamu/simple-lsp-mcp/compare/v0.5.3...v0.6.0
[0.5.3]: https://github.com/tamutamu/simple-lsp-mcp/compare/v0.5.2...v0.5.3
[0.5.2]: https://github.com/tamutamu/simple-lsp-mcp/compare/v0.5.1...v0.5.2
[0.5.1]: https://github.com/tamutamu/simple-lsp-mcp/compare/v0.2.0...v0.5.1
[0.2.0]: https://github.com/tamutamu/simple-lsp-mcp/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/tamutamu/simple-lsp-mcp/compare/v0.0.3...v0.1.0
[0.0.1]: https://github.com/tamutamu/simple-lsp-mcp/releases/tag/v0.0.1
