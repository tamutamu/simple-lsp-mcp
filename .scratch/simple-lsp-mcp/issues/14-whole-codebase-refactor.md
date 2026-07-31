# 14 - Refactor and format the complete codebase

Status: ready-for-agent

## Problem Statement

The Go implementation has grown through vertical slices. Repeated routing,
JSON-RPC, session-lifecycle, and MCP-schema mechanics make the modules harder
to navigate than necessary.

## Solution

Refactor every Go package for clearer module responsibilities and consistent
formatting while preserving the existing MCP contract and runtime behavior.

## Commits

1. Establish the current E2E behavior as the only verification gate requested
   by the user.
2. Consolidate language/profile selection and tool input routing without
   changing tool names, arguments, results, or errors.
3. Extract repeated MCP schema and handler mechanics into local helpers.
4. Separate LSP transport framing and session startup/shutdown mechanics into
   focused private helpers.
5. Normalize document, workspace, symbol, protocol, and configuration helpers
   for consistent names, import grouping, and layout.
6. Format all Go sources, rebuild the executable, and rerun the E2E suite.

## Decision Document

- The current `language`-required MCP contract is the behavior-preserving
  baseline for this refactor.
- No public MCP tool name, argument meaning, response shape, error code, or
  LSP request semantics may change.
- Refactoring is limited to Go sources and the built local executable.

## Testing Decisions

- Run only `scripts/test-codex-exec.sh`, as requested by the user.
- Do not add characterization or unit tests as part of this refactor.

## Out of Scope

- New features, language profiles, tool capabilities, configuration changes,
  and public-contract changes.
