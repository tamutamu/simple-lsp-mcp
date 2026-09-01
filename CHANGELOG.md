# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/tamutamu/simple-lsp-mcp/compare/v0.6.0...HEAD
[0.6.0]: https://github.com/tamutamu/simple-lsp-mcp/compare/v0.5.3...v0.6.0
[0.5.3]: https://github.com/tamutamu/simple-lsp-mcp/compare/v0.5.2...v0.5.3
[0.5.2]: https://github.com/tamutamu/simple-lsp-mcp/compare/v0.5.1...v0.5.2
[0.5.1]: https://github.com/tamutamu/simple-lsp-mcp/compare/v0.2.0...v0.5.1
[0.2.0]: https://github.com/tamutamu/simple-lsp-mcp/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/tamutamu/simple-lsp-mcp/compare/v0.0.3...v0.1.0
[0.0.1]: https://github.com/tamutamu/simple-lsp-mcp/releases/tag/v0.0.1
