# simple-lsp-mcp

[![CI](https://github.com/tamutamu/simple-lsp-mcp/actions/workflows/ci.yml/badge.svg)](https://github.com/tamutamu/simple-lsp-mcp/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/tamutamu/simple-lsp-mcp.svg)](https://pkg.go.dev/github.com/tamutamu/simple-lsp-mcp)
[![Latest release](https://img.shields.io/github/v/release/tamutamu/simple-lsp-mcp)](https://github.com/tamutamu/simple-lsp-mcp/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

A read-only, symbol-first MCP bridge that lets AI agents use local Language Server Protocol (LSP) servers safely.

Instead of text search, it asks LSP servers for symbols, definitions, references, call relationships, type hierarchies, and diagnostics. It does not edit files, execute shell commands on behalf of tools, perform text search, or build a persistent source index.

## Why

Coding agents commonly explore a codebase with `grep`/`ripgrep` and read whole
files into context to figure out what a symbol means. That works, but it burns
tokens, gets confused by name collisions and string matches, and has no notion
of "this identifier is actually a call to that function three files away."

An LSP server already answers those questions precisely, because that's its
job in every editor. `simple-lsp-mcp` exposes that existing, precise knowledge
to an agent as small, structured MCP tool calls instead of large blobs of
grep output — one `get_definition` call instead of a multi-file text search,
one `find_references` call instead of guessing from string matches.

It stays deliberately narrow:

- **Read-only.** No tool ever writes, edits, or deletes a file.
- **No shell.** `command` and `args` are passed straight to the process
  launcher; there is no shell to inject into.
- **No hidden index.** Answers come live from the LSP server backing your own
  editor setup — nothing is scraped, cached long-term, or sent anywhere.
- **Lazy.** A language server starts only on the first request that needs it.

See [SECURITY.md](SECURITY.md) for the full security model.

## Supported languages

| MCP `language` | LSP configuration profile | Typical extensions |
| --- | --- | --- |
| `python` | `python` | `.py` |
| `typescript`, `typescriptreact` | `typescript-javascript` | `.ts`, `.tsx` |
| `javascript`, `javascriptreact` | `typescript-javascript` | `.js`, `.jsx` |
| `go` | `go` | `.go` |
| `html` | `html` | `.html` |
| `css` | `css` | `.css` |

The `language` argument and configuration profile are different. For example, a TypeScript tool call uses `language: "typescript"`, which maps to the `typescript-javascript` profile in `.simple-lsp.json`.

## Onboarding tool & Configuration (`.simple-lsp.yaml`)

Language server configurations are read from `.simple-lsp.yaml` (or `.simple-lsp.yml`) in the root of the workspace.

### Quick Setup (Onboarding Tool)

You can automatically detect project structures (including monorepos) and generate `.simple-lsp.yaml` by executing the `onboard` MCP tool from your AI chat session.

Options:
- `overwrite` (boolean): Overwrite existing configuration if present.
- `workspace` (string): Target workspace directory (defaults to process working directory).


### Configuration Format

`.simple-lsp.yaml` maps workspace relative paths (e.g. `.` for root, `apps/frontend`, `apps/backend`) to language server profiles and custom options:

```yaml
.:
  python:
    command: pyright-langserver
    args: ["--stdio"]
  go:
    command: gopls
    args: []

apps/frontend:
  typescript-javascript:
    command: typescript-language-server
    args: ["--stdio"]
    settings:
      tsserver:
        maxTsServerMemory: 4096

apps/backend:
  python:
    command: pyright-langserver
    args: ["--stdio"]
    env:
      PYTHONPATH: "apps/backend"
```

Each server entry supports:
- `command`: Command executable name or path
- `args`: Command-line arguments
- `pattern`: Optional file match pattern to route requests
- `env`: Map of custom environment variables
- `settings`: Custom settings map passed via `workspace/didChangeConfiguration`
- `initialization_options`: Custom options passed during LSP initialization

## Requirements

- Go 1.26 or later when building from this repository
- An LSP server for every language you intend to use
- An MCP client such as Codex or Claude Code

Install and configure only the LSP servers you need. Each server starts lazily, on the first request for its language.

| Language | LSP server | Example installation |
| --- | --- | --- |
| Python | `pyright-langserver` | `npm install -g pyright` |
| TypeScript / JavaScript | `typescript-language-server` | `npm install -g typescript typescript-language-server` |
| Go | `gopls` | `go install golang.org/x/tools/gopls@latest` |
| HTML / CSS | `vscode-html-language-server`, `vscode-css-language-server` | `npm install -g vscode-langservers-extracted` |

To avoid global installation, configure a launcher such as `npx` as shown for HTML and CSS above. `command` and `args` are passed directly to the process launcher, not through a shell: `~`, environment-variable expansion, pipes, and shell argument splitting are unavailable.

## Installation

Prebuilt binaries for Linux, macOS, and Windows (amd64 and arm64) are attached
to every [release](https://github.com/tamutamu/simple-lsp-mcp/releases).
Download the archive for your platform, extract it, and place `simple-lsp-mcp`
on your `PATH`.

With Go 1.26 or later installed:

```sh
go install github.com/tamutamu/simple-lsp-mcp/cmd/simple-lsp-mcp@latest
```

To build from a checkout of this repository:

```sh
go build -o simple-lsp-mcp ./cmd/simple-lsp-mcp
```

Place the resulting executable on the MCP client's `PATH`, or specify its absolute path as the configured `command`.

## Codex configuration

Add the following to `~/.codex/config.toml`. `command` may be a name on `PATH` or an absolute path to the built executable. `--workspace` is optional and defaults to the process current working directory.

```toml
[mcp_servers.simple-lsp]
command = "simple-lsp-mcp"
enabled_tools = [
  "search_symbols", "list_workspace_symbols", "get_document_symbols", "get_symbol",
  "get_definition", "find_references", "find_implementations", "get_type_definition",
  "get_declaration", "get_incoming_calls", "get_outgoing_calls", "get_supertypes",
  "get_subtypes", "get_diagnostics", "onboard"
]
default_tools_approval_mode = "approve"
startup_timeout_sec = 20
tool_timeout_sec = 45
enabled = true
```

In Codex, begin code exploration with `search_symbols` or `get_document_symbols`. For example, to list functions in `src/greeting.ts`, call `get_document_symbols` with `path: "src/greeting.ts"` and `language: "typescript"`.

## Claude Code configuration

With the binary on your `PATH`, add it as a project or user MCP server:

```sh
claude mcp add simple-lsp -- simple-lsp-mcp
```

Or add this configuration directly:

```json
{
  "mcpServers": {
    "simple-lsp": {
      "command": "simple-lsp-mcp"
    }
  }
}
```

This repository is also a Claude Code plugin (`.claude-plugin/plugin.json`), so
it can be installed from a marketplace pointing at this repository instead of
hand-editing MCP config:

```sh
claude plugin marketplace add tamutamu/simple-lsp-mcp
claude plugin install simple-lsp-mcp@simple-lsp-mcp
```

The plugin still expects `simple-lsp-mcp` to be installed and on your `PATH` —
see [Installation](#installation) above.

## MCP tools

Every tool returns structured data. Symbol, position, and range lines and columns are **one-based**; paths are relative to the workspace.

| Tool | Purpose | Required input |
| --- | --- | --- |
| `search_symbols` | Search workspace symbols by name | `query`, `language` |
| `list_workspace_symbols` | List workspace symbols for a language | `language` |
| `get_document_symbols` | Get hierarchical symbols for one file | `path`, `language` |
| `get_symbol` | Get an acquired `symbol_id` and its source | `symbol_id` |
| `get_definition` | Go to a definition | `language` and target |
| `find_references` | Get reference locations | `language` and target |
| `find_implementations` | Get implementation locations | `language` and target |
| `get_type_definition` | Go to a type definition | `language` and target |
| `get_declaration` | Go to a declaration | `language` and target |
| `get_incoming_calls` | Get direct callers | `language` and target |
| `get_outgoing_calls` | Get direct callees | `language` and target |
| `get_supertypes` | Get direct supertypes | `language` and target |
| `get_subtypes` | Get direct subtypes | `language` and target |
| `get_diagnostics` | Get diagnostics for a file | `language` when `path` is supplied |
| `onboard` | Scan workspace and generate configuration | None |

## Server options

| Option | Default | Description |
| --- | --- | --- |
| `--workspace` | Current working directory | Workspace root to inspect (defaults to process current working directory) |
| `--log-level` | `info` | `debug`, `info`, `warn`, or `error` |
| `--request-timeout` | `15s` | Timeout for each LSP request |
| `--diagnostics-wait` | `2s` | Time to wait for push diagnostics |
| `--max-results` | `500` | Maximum result count per tool |
| `--version` | `false` | Print version information and exit |

## Contributing

Bug reports, new language profiles, and documentation fixes are all welcome.
See [CONTRIBUTING.md](CONTRIBUTING.md) for the development setup and the
design constraints a change needs to respect. Please also read the
[Code of Conduct](CODE_OF_CONDUCT.md).

## Security

See [SECURITY.md](SECURITY.md) to report a vulnerability, and for the security
model this server is designed around.

## Changelog

See [CHANGELOG.md](CHANGELOG.md).

## License

[MIT](LICENSE)
