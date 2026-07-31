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

## Codex configuration

Add the following to `~/.codex/config.toml`. Replace `/absolute/path/to/project` with the workspace to inspect. `command` may be a name on `PATH` or an absolute path to the built executable.

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
{"python":{"command":"pyright-langserver","args":["--stdio"]},"typescript-javascript":{"command":"typescript-language-server","args":["--stdio"]},"go":{"command":"gopls","args":[]},"html":{"command":"npx","args":["--yes","--package=vscode-langservers-extracted","vscode-html-language-server","--stdio"]},"css":{"command":"npx","args":["--yes","--package=vscode-langservers-extracted","vscode-css-language-server","--stdio"]}}
"""
```

This config enables Python, TypeScript/JavaScript, Go, HTML, and CSS. Remove profiles you do not use. Calling an unconfigured language returns `UNSUPPORTED_LANGUAGE`; a configured server executable that cannot be found returns `LANGUAGE_SERVER_NOT_FOUND`.

In Codex, begin code exploration with `search_symbols` or `get_document_symbols`. For example, to list functions in `src/greeting.ts`, call `get_document_symbols` with `path: "src/greeting.ts"` and `language: "typescript"`.

## Claude Code configuration

Add this configuration to Claude Code. It enables Python, TypeScript/JavaScript, Go, HTML, and CSS:

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

## Server options

| Option | Default | Description |
| --- | --- | --- |
| `--workspace` | none | Required workspace root to inspect |
| `--log-level` | `info` | `debug`, `info`, `warn`, or `error` |
| `--request-timeout` | `15s` | Timeout for each LSP request |
| `--diagnostics-wait` | `2s` | Time to wait for push diagnostics |
| `--max-results` | `500` | Maximum result count per tool |
