# 02 - Implement configuration, language routing, and workspace jail

Status: ready-for-agent
Blocked by: 01

## Outcome

Validated runtime configuration maps supported extensions and explicit language
names to five lazy session keys while every file access is confined to one
workspace.

## Work

- Implement CLI/config merge with array-form commands and no shell expansion.
- Register defaults for Python, TS/JS, Go, HTML, and CSS.
- Model language IDs separately from shared session keys; route TS and JS to one
  key.
- Resolve the workspace real path at startup.
- Accept public relative paths only; reject absolute, nonexistent, outside, and
  symlink-escaping paths.
- Handle platform-specific volume/UNC syntax in tests without weakening Unix
  checks.
- Check executables at session start, not process startup, to preserve laziness.

## Acceptance

- Routing covers every extension and language ID in the design.
- TS and JS resolve to the same session key but correct document language IDs.
- Jail tests cover traversal, nested symlinks, broken symlinks, Windows drives,
  UNC paths, and valid in-workspace symlinks.
- Unknown extensions/languages and missing executables map to their specified
  error codes.

## Verification

`go test ./internal/config ./internal/language ./internal/workspace`

