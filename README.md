# simple-lsp-mcp

A read-only, symbol-first MCP bridge that lets AI agents use local Language Server Protocol (LSP) servers safely.

Instead of text search, it asks LSP servers for symbols, definitions, references, call relationships, type hierarchies, and diagnostics. It does not edit files, execute shell commands on behalf of tools, perform text search, or build a persistent source index.

## Supported languages

| MCP `language` | LSP configuration profile | Typical extensions |
| --- | --- | --- |
| `python` | `python` | `.py` |
| `typescript`, `typescriptreact` | `typescript-javascript` | `.ts`, `.tsx` |
| `javascript`, `javascriptreact` | `typescript-javascript` | `.js`, `.jsx` |
| `go` | `go` | `.go` |
| `html` | `html` | `.html` |
| `css` | `css` | `.css` |

The `language` argument and configuration profile are different. For example, a TypeScript tool call uses `language: "typescript"`, while `LSP_MCP_SERVERS` configures the `typescript-javascript` profile.

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

To avoid global installation, configure a launcher such as `npx` as shown for HTML and CSS below. `command` and `args` are passed directly to the process launcher, not through a shell: `~`, environment-variable expansion, pipes, and shell argument splitting are unavailable.

## Installation

To install the published module:

```sh
go install github.com/tamutamu/simple-lsp-mcp/cmd/simple-lsp-mcp@latest
```

To build from a checkout of this repository:

```sh
go build -o simple-lsp-mcp ./cmd/simple-lsp-mcp
```

Place the resulting executable on the MCP client's `PATH`, or specify its absolute path as the configured `command`.

## Quick start

This example configures a project that uses Go and TypeScript. Replace `/absolute/path/to/project` with the absolute path of the workspace to inspect.

```json
{
  "go": {"command": "gopls", "args": []},
  "typescript-javascript": {
    "command": "typescript-language-server",
    "args": ["--stdio"]
  }
}
```

Pass this JSON as `LSP_MCP_SERVERS` and supply the workspace to the server:

```sh
LSP_MCP_SERVERS='{"go":{"command":"gopls","args":[]},"typescript-javascript":{"command":"typescript-language-server","args":["--stdio"]}}' \
  simple-lsp-mcp --workspace /absolute/path/to/project
```

`--workspace` is required. MCP communicates over standard input and output, so normally an MCP client starts the server rather than an interactive shell.

### All-profile configuration example

```json
{
  "python": {"command": "pyright-langserver", "args": ["--stdio"]},
  "typescript-javascript": {
    "command": "typescript-language-server",
    "args": ["--stdio"]
  },
  "go": {"command": "gopls", "args": []},
  "html": {
    "command": "npx",
    "args": ["--yes", "--package=vscode-langservers-extracted", "vscode-html-language-server", "--stdio"]
  },
  "css": {
    "command": "npx",
    "args": ["--yes", "--package=vscode-langservers-extracted", "vscode-css-language-server", "--stdio"]
  }
}
```

You may remove profiles you do not use. Calling an unconfigured language returns `UNSUPPORTED_LANGUAGE`; a configured server executable that cannot be found returns `LANGUAGE_SERVER_NOT_FOUND`.

## Codex configuration

Add the following to `~/.codex/config.toml`. `command` may be a name on `PATH` or an absolute path to the built executable.

```toml
[mcp_servers.simple-lsp]
command = "simple-lsp-mcp"
args = ["--workspace", "/absolute/path/to/project"]
enabled_tools = [
  "search_symbols", "list_workspace_symbols", "get_document_symbols", "get_symbol",
  "get_definition", "find_references", "find_implementations", "get_type_definition",
  "get_declaration", "get_incoming_calls", "get_outgoing_calls", "get_supertypes",
  "get_subtypes", "get_diagnostics"
]
default_tools_approval_mode = "approve"
startup_timeout_sec = 20
tool_timeout_sec = 45
enabled = true

[mcp_servers.simple-lsp.env]
LSP_MCP_SERVERS = """
{"go":{"command":"gopls","args":[]},"typescript-javascript":{"command":"typescript-language-server","args":["--stdio"]}}
"""
```

In Codex, begin code exploration with `search_symbols` or `get_document_symbols`. For example, to list functions in `src/greeting.ts`, call `get_document_symbols` with `path: "src/greeting.ts"` and `language: "typescript"`.

## Claude Code configuration

Add this MCP configuration to Claude Code:

```json
{
  "mcpServers": {
    "simple-lsp": {
      "command": "simple-lsp-mcp",
      "args": ["--workspace", "/absolute/path/to/project"],
      "env": {
        "LSP_MCP_SERVERS": "{\"python\":{\"command\":\"pyright-langserver\",\"args\":[\"--stdio\"]},\"typescript-javascript\":{\"command\":\"typescript-language-server\",\"args\":[\"--stdio\"]},\"go\":{\"command\":\"gopls\",\"args\":[]},\"html\":{\"command\":\"npx\",\"args\":[\"--yes\",\"--package=vscode-langservers-extracted\",\"vscode-html-language-server\",\"--stdio\"]},\"css\":{\"command\":\"npx\",\"args\":[\"--yes\",\"--package=vscode-langservers-extracted\",\"vscode-css-language-server\",\"--stdio\"]}}"
      }
    }
  }
}
```

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

### Common inputs

- `language`: An MCP language name from the table above. Required for symbol and file operations.
- `path`: A workspace-relative file path. Absolute paths, `..`, and symbolic links that escape the workspace are rejected.
- `line`, `column`: One-based positions.
- `limit`: The maximum result count. It defaults to `--max-results` (500 by default). Values above that maximum are clamped.
- `kinds`: A kind filter for `search_symbols` and `list_workspace_symbols`, for example `["function", "class"]`.

Tools that accept a target require exactly one of these forms:

```json
{"symbol_id": "sym_..."}
```

```json
{"path": "src/main.go", "line": 12, "column": 8}
```

Get `symbol_id` values from `search_symbols`, `list_workspace_symbols`, `get_document_symbols`, or hierarchy-tool results. IDs are valid only in the same MCP server process; if their file changes, calls return `STALE_SYMBOL`. `get_symbol` includes source by default; set `include_source: false` to omit it. Set `include_declaration: true` on `find_references` to include the declaration.

If an LSP server does not advertise a requested capability, the relevant tool returns `METHOD_NOT_SUPPORTED`. Capability support depends on the language and LSP server implementation.

## Server options

| Option | Default | Description |
| --- | --- | --- |
| `--workspace` | none | Required workspace root to inspect |
| `--log-level` | `info` | `debug`, `info`, `warn`, or `error` |
| `--request-timeout` | `15s` | Timeout for each LSP request |
| `--diagnostics-wait` | `2s` | Time to wait for push diagnostics |
| `--max-results` | `500` | Maximum result count per tool |

## Troubleshooting

| Response code | Cause and resolution |
| --- | --- |
| `INVALID_PATH` | Ensure `path` is workspace-relative and names an existing file. |
| `UNSUPPORTED_LANGUAGE` | Check that `language` appears in the supported-language table and its profile is configured in `LSP_MCP_SERVERS`. |
| `LANGUAGE_SERVER_NOT_FOUND` | Confirm the configured `command` is executable from the MCP process `PATH`. |
| `LANGUAGE_SERVER_START_FAILED` | Check LSP launch arguments, project configuration, and stdio mode. |
| `METHOD_NOT_SUPPORTED` | The selected LSP server does not implement that LSP capability. Use a supporting server or an available tool. |
| `REQUEST_TIMEOUT` | Increase `--request-timeout`, or inspect LSP startup and project-analysis problems. |
| `STALE_SYMBOL` | The source file changed after the symbol was acquired. Search again and use the new `symbol_id`. |

## Development and verification

Run unit tests and build the command:

```sh
go test ./...
go build ./cmd/simple-lsp-mcp
```

### Codex end-to-end verification

`scripts/test-codex-exec.sh` exercises the complete path through the Codex CLI, this MCP server, and real LSP servers. It covers Python, TypeScript, Go, HTML, and CSS fixtures, and verifies that Codex calls the `simple_lsp` tool and receives the expected symbol.

```sh
bash scripts/test-codex-exec.sh
```

The check requires a logged-in Codex CLI and can consume API quota. It also requires `codex`, `go`, `pyright-langserver`, `typescript-language-server`, `gopls`, `npx`, and `jq`. `npx` downloads the HTML and CSS LSP package if it is not cached.

To run one case only:

```sh
CODEX_E2E_CASE=typescript bash scripts/test-codex-exec.sh
```
