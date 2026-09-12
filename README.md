# simple-lsp-mcp

[![CI](https://github.com/tamutamu/simple-lsp-mcp/actions/workflows/ci.yml/badge.svg)](https://github.com/tamutamu/simple-lsp-mcp/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/tamutamu/simple-lsp-mcp.svg)](https://pkg.go.dev/github.com/tamutamu/simple-lsp-mcp)
[![Latest release](https://img.shields.io/github/v/release/tamutamu/simple-lsp-mcp)](https://github.com/tamutamu/simple-lsp-mcp/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**Stop making coding agents grep your whole repository.**

`simple-lsp-mcp` turns the LSP servers you already use into **precise, token-efficient semantic context** for Claude Code, Codex, and other MCP clients.

Instead of making an agent search strings, read whole files, and stitch relationships together itself, it can ask one semantic question and get a bounded answer from the language server:

```text
❌ grep → read → grep → read → infer relationships
✅ get_semantic_slice("UserService/createUser", max_tokens=6000)
```

`get_semantic_slice` returns the root source, the source of code it calls, compact callers, and implementations under a strict depth/node/token budget. For a shallower view, `get_symbol_context` returns source + callers + callees + references + implementations in one call.

## ⭐ Flagship: `get_semantic_slice`

`get_semantic_slice` is the main reason to use `simple-lsp-mcp`. Give it one symbol and it builds the smallest useful semantic code neighborhood an agent needs to understand or change that symbol.

```text
get_semantic_slice(
  symbol_path="UserService/createUser",
  max_tokens=6000,
  depth=2
)

→ root source
→ direct + transitive callee source
→ compact callers
→ implementations
→ bounded by token, depth, and node budgets
```

Instead of making the agent perform several navigation calls and assemble the relationships itself, `get_semantic_slice` performs that semantic traversal inside the MCP server and returns one bounded result. The goal is simple: **fewer tool calls, less irrelevant code, more room for reasoning.**

The project stays deliberately narrow:

- **Read-only.** It understands code; it never edits it.
- **No hidden index or embeddings.** Answers come live from your local LSP server.
- **No external indexing service.** The server reads your workspace locally and only returns requested results to your configured MCP client.
- **Token-budgeted context.** High-level tools bound how much code is returned.
- **Language inference.** For most target-based tools, `language` can be omitted; it is inferred from `symbol_id`, file extension, or configured LSP profiles.
- **Lazy.** A language server starts only when a request needs it.

## Why

Coding agents are very good at reasoning about code once they have the right context. The expensive part is often *finding* that context. Text search produces name collisions and string matches; reading entire files burns context; raw LSP primitives are precise but can require several agent-visible round trips.

`simple-lsp-mcp` treats LSP as a semantic context backend. Low-level navigation tools remain available, but the main goal is to answer a whole code-understanding question in as few tool calls as possible:

- `find_symbol` — resolve a human-readable symbol without first knowing its file or position.
- `get_symbol_outline` — inspect structure without returning source text.
- `get_symbol_context` — source, callers, callees, references, and implementations in one call.
- `get_semantic_slice` — gather the minimum useful source neighborhood under a token budget.
- `impact_analysis` — estimate refactor blast radius through transitive callers and affected files.

That distinction matters: the project is not trying to expose every LSP method. It is trying to reduce **agent tool calls and context consumed per code-understanding task**.

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

The MCP language name and the LSP configuration profile are different. For example, TypeScript maps to the shared `typescript-javascript` profile. Most target-based tools now infer the language automatically from `symbol_id`, a file extension, or configured profiles; `search_symbols` and `list_workspace_symbols` still require `language` to keep workspace-wide searches bounded.

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
  "find_symbol", "get_symbol_outline", "get_symbol_context", "get_semantic_slice",
  "get_definition", "find_references", "find_implementations", "get_type_definition",
  "get_declaration", "get_hover", "get_incoming_calls", "get_outgoing_calls", "get_supertypes",
  "get_subtypes", "get_diagnostics", "impact_analysis", "onboard"
]
default_tools_approval_mode = "approve"
startup_timeout_sec = 20
tool_timeout_sec = 45
enabled = true
```

In Codex, begin with `get_semantic_slice` when you need enough code to understand or change one symbol, `get_symbol_context` when you need its immediate neighborhood, or `get_symbol_outline` when you only need structure. `language` is usually unnecessary: `get_document_symbols(path="src/greeting.ts")` infers TypeScript from the path, while `find_symbol(symbol_path="formatGreeting")` can search configured profiles when no path is known.

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

A **target** identifies one symbol or position, in exactly one of three forms: a `symbol_id` returned by an earlier call, a human-readable `symbol_path` such as `"UserService/createUser"` (optionally scoped to one file with `path`; see [Finding a symbol by name](#finding-a-symbol-by-name) below), or `path` together with `line` and `column`.

| Tool | Purpose | Required input |
| --- | --- | --- |
| `search_symbols` | Search workspace symbols by name | `query`, `language` |
| `list_workspace_symbols` | List workspace symbols for a language | `language` |
| `get_document_symbols` | Get hierarchical symbols for one file | `path` |
| `get_symbol` | Get an acquired `symbol_id` and its source | `symbol_id` |
| `find_symbol` | Get one symbol by `symbol_path`, without a prior search | `symbol_path` |
| `get_symbol_outline` | List a symbol's children or a file's top-level symbols without source | target or `path` |
| `get_symbol_context` | Source, callers, callees, references, and implementations in one call | target |
| `get_semantic_slice` | Token-budgeted root + dependency source + compact dependents | target |
| `get_definition` | Go to a definition | target |
| `find_references` | Get reference locations | target |
| `find_implementations` | Get implementation locations | target |
| `get_type_definition` | Go to a type definition | target |
| `get_declaration` | Go to a declaration | target |
| `get_hover` | Get the type inferred for an expression | target |
| `get_incoming_calls` | Get direct callers | target |
| `get_outgoing_calls` | Get direct callees | target |
| `get_supertypes` | Get direct supertypes | target |
| `get_subtypes` | Get direct subtypes | target |
| `get_diagnostics` | Get diagnostics for a file | `path` when file-specific |
| `impact_analysis` | Estimate blast radius: callers, references, implementations, affected files | target |
| `onboard` | Scan workspace and generate configuration | None |

### Finding a symbol by name

`find_symbol`, `get_symbol_outline`, `get_symbol_context`, and every target-based tool above accept `symbol_path`: the names of a symbol and its enclosing symbols, joined by `/`, such as `UserService/createUser`. A trailing portion alone (`createUser`) is accepted when it is unambiguous anywhere in the searched file or workspace; prefix the path with `/` to require an exact match instead. Escape a literal `/` inside a name as `%2F`.

A `symbol_path` reflects whatever shape the language server's own `documentSymbol` response has — it is never inferred or restructured. Methods commonly nest under their type (`UserService/createUser`), but some language servers report them as one flat, already-qualified name instead (for example Go's `gopls`, which reports `(*UserService).CreateUser` as a single segment). Call `get_symbol_outline` or `get_document_symbols` first if you are unsure which form a given server uses.

When a `symbol_path` matches more than one symbol, `find_symbol`, `get_symbol_outline`, and `get_symbol_context` return `{"ambiguous": true, "candidates": [...]}` instead of failing — each candidate carries its own `symbol_id`, so the next call can target it directly. The same ambiguity on any other tool is an `AMBIGUOUS_SYMBOL` error listing candidates in its message, since those tools' output shape has nowhere else to put them.

## Context-efficiency benchmark

A reproducible benchmark runner is included so performance claims can be measured against a real repository instead of guessed:

```sh
go run ./cmd/simple-lsp-bench \
  --workspace . \
  --symbol 'UserService/createUser' \
  --depth 2 \
  --max-tokens 6000
```

It compares five separate navigation calls with `get_symbol_context` and `get_semantic_slice`, reporting agent-visible tool calls, elapsed time, serialized bytes, and an approximate response-token count. See [docs/benchmark.md](docs/benchmark.md) for methodology and publishing guidance.

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
