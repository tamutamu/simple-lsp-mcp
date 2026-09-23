# Contributing to simple-lsp-mcp

Thanks for taking the time to contribute. This project is small and the bar for
a first contribution is intentionally low: a typo fix, a clearer tool
description, or a new language profile are all genuinely useful.

## Ways to help

- **Report a bug.** Open an issue with the language, the LSP server and version,
  the tool you called, and the arguments you passed.
- **Add a language profile.** Anything with a stdio LSP server can work. See
  [Adding a language](#adding-a-language) below.
- **Improve tool descriptions.** These are read by the model, not by humans, so
  a sharper sentence measurably improves agent behaviour.
- **Improve docs.** If you got stuck, the docs were unclear. Say where.

## Development setup

Requirements: Go 1.26 or later.

```sh
git clone https://github.com/tamutamu/simple-lsp-mcp.git
cd simple-lsp-mcp
go build ./...
go test ./...
```

Before opening a pull request, run what CI runs:

```sh
go test ./...
go test -race ./...
go vet ./...
gofmt -l .          # must print nothing
```

### Testing with actual language servers

The opt-in integration tests use real LSP subprocesses instead of mocks.
Install `gopls` to run the Go tests locally:

```sh
SIMPLE_LSP_REAL_LSP=go go test ./internal/tools -run '^TestReal' -count=1 -v
```

CI installs `gopls`, TypeScript Language Server, and Pyright and runs the full
integration suite. For manual MCP client testing, install the binary and follow
the [client setup instructions](README.md#one-command-setup-and-diagnostics).

## Adding a language

Two places need to know about a new language:

1. `internal/language/profile.go` — map the MCP `language` argument and the file
   extensions to an LSP configuration profile.
2. `internal/onboard/onboard.go` — teach the `onboard` tool how to detect the
   project (marker files such as `go.mod`, `pyproject.toml`, `package.json`) and
   what default `command` and `args` to emit.

Add a table-driven test in the matching `_test.go`, add a fixture under
`testdata/` if the detection logic is non-trivial, and update the supported
languages table in `README.md`.

## Design constraints

These are deliberate. A pull request that breaks one of them will be asked to
change, so it is worth knowing them up front:

- **Read-only source navigation.** Navigation never edits source or executes shell commands on a tool's behalf. The explicit `onboard` tool is allowed to write `.simple-lsp.yaml`. `command` and `args` are passed directly to the process
  launcher, never through a shell.
- **Symbol-first.** No text search, no grep fallback, no persistent source
  index. If the LSP server cannot answer it, the tool returns an error rather
  than guessing.
- **Lazy.** A language server starts on the first request for its language and
  not before.
- **Stable tool surface.** Tool names and their output shapes are a public API.
  Renaming or removing one is a breaking change.

## Commit messages

Conventional Commits (`feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`).
The release notes are generated from these, so the subject line is what users
will read.

## Cutting a release (maintainers)

1. Move `## [Unreleased]` in `CHANGELOG.md` to a new `## [x.y.z] - YYYY-MM-DD`
   heading and add the compare link at the bottom.
2. Bump the `version` field in `server.json`, `.claude-plugin/plugin.json`, and
   `.claude-plugin/marketplace.json` to match.
3. Commit, then `git tag vx.y.z && git push origin vx.y.z`.
4. The [release workflow](.github/workflows/release.yml) builds and publishes
   binaries via GoReleaser once the tag is pushed.

## Pull requests

- One logical change per pull request.
- Include a test for behaviour changes.
- Update `README.md` and `CHANGELOG.md` when user-visible behaviour changes.
- CI must be green.

By contributing, you agree that your contributions are licensed under the
[MIT License](LICENSE).
