# simple-lsp-mcp

[![CI](https://github.com/tamutamu/simple-lsp-mcp/actions/workflows/ci.yml/badge.svg)](https://github.com/tamutamu/simple-lsp-mcp/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/tamutamu/simple-lsp-mcp.svg)](https://pkg.go.dev/github.com/tamutamu/simple-lsp-mcp)
[![Latest release](https://img.shields.io/github/v/release/tamutamu/simple-lsp-mcp)](https://github.com/tamutamu/simple-lsp-mcp/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**Stop making coding agents grep your whole repository.**

`simple-lsp-mcp` turns the LSP servers you already use into **precise, bounded semantic context** for Claude Code, Codex, and other MCP clients.

Instead of making an agent search strings, read whole files, and stitch relationships together itself, it can ask one semantic question and get a bounded answer from the language server:

```text
❌ grep → read → grep → read → infer relationships
✅ get_semantic_slice("UserService/createUser", max_bytes=24576)
```

`get_semantic_slice` returns root and callee source, type-definition locations, compact callers and implementations, and test-file candidates backed by LSP references under a depth/node/serialized-JSON-byte budget. For a shallower view, `get_symbol_context` returns source + callers + callees + references + implementations in one call.

## ⭐ Flagship: `get_semantic_slice`

`get_semantic_slice` is the main reason to use `simple-lsp-mcp`. Give it one symbol and it builds the smallest useful semantic code neighborhood an agent needs to understand or change that symbol.

```text
get_semantic_slice(
  symbol_path="UserService/createUser",
  max_bytes=24576,
  depth=2
)

→ root source
→ direct + transitive callee source
→ compact callers
→ type definitions + implementations
→ test-file candidates supported by reference locations
→ bounded by JSON bytes, depth, and node counts
```

Instead of making the agent perform several navigation calls and assemble the relationships itself, `get_semantic_slice` performs that semantic traversal inside the MCP server and returns one bounded result. The goal is simple: **fewer tool calls, less irrelevant code, more room for reasoning.**

The project stays deliberately narrow:

- **Semantic writes are explicit.** Navigation remains read-only. `rename_symbol` delegates rename calculation to the language server, previews by default, and writes only with `apply=true`; workspace escapes and unsafe edit shapes are rejected. The `onboard` tool writes `.simple-lsp.yaml`; `setup --apply` changes the chosen MCP client configuration.
- **No hidden index or embeddings.** Answers come live from your local LSP server.
- **No external indexing service.** The server reads your workspace locally and only returns requested results to your configured MCP client.
- **Byte-budgeted context.** High-level tools bound how much code is returned.
- **Language inference.** For most target-based tools, `language` can be omitted; it is inferred from `symbol_id`, file extension, or configured LSP profiles.
- **Lazy.** A language server starts only when a request needs it.

## Why

Coding agents are very good at reasoning about code once they have the right context. The expensive part is often *finding* that context. Text search produces name collisions and string matches; reading entire files burns context; raw LSP primitives are precise but can require several agent-visible round trips.

`simple-lsp-mcp` treats LSP as a semantic context backend. Low-level navigation tools remain available, but the main goal is to answer a whole code-understanding question in as few tool calls as possible:

- `find_symbol` — resolve a human-readable symbol without first knowing its file or position.
- `get_symbol_outline` — inspect structure without returning source text.
- `get_symbol_context` — source, callers, callees, references, and implementations in one call.
- `get_semantic_slice` — gather the minimum useful source neighborhood under a byte budget.
- `impact_analysis` — estimate refactor blast radius through transitive callers and affected files.
- `rename_symbol` — let the language server compute a cross-file semantic rename, preview it, then explicitly apply the validated WorkspaceEdit.
- `format_document` — preview/apply the language server's formatter without giving the MCP arbitrary text-edit authority.

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

The MCP language name and the LSP configuration profile are different. For example, TypeScript maps to the shared `typescript-javascript` profile. Most target-based tools now infer the language automatically from `symbol_id`, a file extension, or configured profiles; `search_symbols` requires a non-empty `query` and `language` to keep workspace-wide searches bounded.

## Onboarding tool & Configuration (`.simple-lsp.yaml`)

Language server configurations are read from `.simple-lsp.yaml` (or `.simple-lsp.yml`) in the root of the workspace.

### Quick Setup (Onboarding Tool)

You can automatically detect project structures (including monorepos) and generate `.simple-lsp.yaml` by executing the `onboard` MCP tool from your AI chat session.

Options:
- `overwrite` (boolean): Overwrite an existing configuration when explicitly requested.
- `workspace` (string): Optional directory identifying the **running server root** (defaults to that root). Other roots and subdirectories are rejected because a restarted server only loads configuration from its own root; use `--workspace` when launching the server to configure a different project.

If no configuration exists, the MCP server starts with no language servers configured; it does **not** write a file or automatically execute any language servers. Call `onboard` to generate the configuration, then **restart/reconnect the MCP server** to load the new profiles. The tool returns `restart_required: true` to make this explicit.


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
| TypeScript / JavaScript | `typescript-language-server` | `npm install -g typescript@6 typescript-language-server` |
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

**Verify the binary your client will launch:** run `command -v simple-lsp-mcp` (Windows: `where.exe simple-lsp-mcp`), then `simple-lsp-mcp --version`. If a version is older than the release you installed, update `PATH` or register the new binary by absolute path with `simple-lsp-mcp setup claude|codex --apply` (remove any existing registration first; setup refuses to overwrite it). Restart the client after replacing a running binary. A fresh Git checkout does not upgrade a previously installed executable automatically.

## One-command setup and diagnostics

Install the binary and use the clients' own MCP registration commands without manually editing JSON or TOML:

```sh
simple-lsp-mcp doctor --workspace .
simple-lsp-mcp doctor --workspace . --probe internal/tools/semantic.go
simple-lsp-mcp setup claude           # preview only
simple-lsp-mcp setup claude --apply   # explicitly register
simple-lsp-mcp setup codex --apply
```

`doctor` reads the existing `.simple-lsp.yaml`, reports the exact running binary/version and missing executables, and warns when `simple-lsp-mcp` on `PATH` resolves to a different binary. It never creates the configuration; use the `onboard` MCP tool when one is needed. `--probe` performs an actual LSP document-symbol request. `setup` defaults to a preview, does not install software, and refuses to silently overwrite an existing registration. `--apply` modifies only the selected MCP client's configuration through that client's CLI.

## Codex configuration

### Codex plugin package

The repository is directly installable as a Codex local plugin marketplace. Install the `simple-lsp-mcp` binary on `PATH`, then:

```sh
codex plugin marketplace add tamutamu/simple-lsp-mcp
codex plugin add simple-lsp-mcp@simple-lsp-mcp
```

The package uses `.codex-plugin/plugin.json` plus the root `.mcp.json`; the existing `.claude-plugin/marketplace.json` is also recognized by current Codex plugin tooling as a compatible repository marketplace. Start a new Codex session after installing or updating the plugin so the MCP tool list and server instructions are reloaded.

For direct MCP registration instead of the plugin package, add the following to `~/.codex/config.toml`. `command` may be a name on `PATH` or an absolute path to the built executable. `--workspace` is optional and defaults to the process current working directory.

```toml
[mcp_servers.simple-lsp]
command = "simple-lsp-mcp"
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
| `list_workspace_symbols` | Enumerate all configured source symbols, page by page (no name required) | None |
| `get_document_symbols` | Get hierarchical symbols for one file | `path` |
| `get_symbol` | Get an acquired `symbol_id` and its source | `symbol_id` |
| `find_symbol` | Get one symbol by `symbol_path`, without a prior search | `symbol_path` |
| `get_symbol_outline` | List a symbol's children or a file's top-level symbols without source | target or `path` |
| `get_symbol_context` | Source, callers, callees, references, and implementations in one call | target |
| `get_semantic_slice` | Byte-bounded source, types, references-backed test candidates, callers and implementations | target |
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
| `rename_symbol` | Preview/apply an LSP semantic rename across files | target, `new_name` |
| `format_document` | Preview/apply LSP document formatting | `path` |
| `onboard` | Scan workspace and generate configuration | None |

`search_symbols` requires a non-empty name query. **`list_workspace_symbols` really lists the workspace**, without a query, by fetching `textDocument/documentSymbol` for each source file. Use `get_document_symbols` for just one file.

To enumerate, call `list_workspace_symbols(limit=100)` and repeat with `cursor=<next_cursor>` until `next_cursor` is absent. Optional `language` and `kinds` filter the results; without `language`, all configured language profiles are included. Each call visits at most 32 files and returns at most 100 symbols, including nested symbols, with paths, ranges, and reusable `symbol_id`/`symbol_path` values. `meta.complete=false` and `meta.truncated=true` mean more pages remain, not a failed search. A page may contain no symbols when files have none; continue while `next_cursor` exists. `.git`, dependency, cache, and generated-output directories (for example `node_modules`, `dist`, `build`) and symlinks are excluded. This is a live traversal, not a snapshot: restart without a cursor if the workspace changes during pagination. Errors are returned explicitly rather than silently skipping failed LSP requests.

### Finding a symbol by name

`find_symbol`, `get_symbol_outline`, `get_symbol_context`, and every target-based tool above accept `symbol_path`: the names of a symbol and its enclosing symbols, joined by `/`, such as `UserService/createUser`. A trailing portion alone (`createUser`) is accepted when it is unambiguous anywhere in the searched file or workspace; prefix the path with `/` to require an exact match instead. Escape a literal `/` inside a name as `%2F`.

A `symbol_path` reflects whatever shape the language server's own `documentSymbol` response has — it is never inferred or restructured. Methods commonly nest under their type (`UserService/createUser`), but some language servers report them as one flat, already-qualified name instead (for example Go's `gopls`, which reports `(*UserService).CreateUser` as a single segment). Call `get_symbol_outline` or `get_document_symbols` first if you are unsure which form a given server uses.

When no file is specified, the server searches every configured LSP instance for the selected language and inspects every server-returned candidate up to a 128-file safety limit. If the lookup cannot finish, it raises `INCOMPLETE_SEARCH` rather than reporting a false unique result or false not-found. Supply `path` to narrow an incomplete search. Completeness refers to the LSP-returned candidate set: language-server indexes may themselves omit unindexed files.

When a `symbol_path` matches more than one symbol, `find_symbol`, `get_symbol_outline`, and `get_symbol_context` return `{"ambiguous": true, "candidates": [...]}` instead of failing — each candidate carries its own `symbol_id`, so the next call can target it directly. The same ambiguity on any other tool is an `AMBIGUOUS_SYMBOL` error listing candidates in its message, since those tools' output shape has nowhere else to put them.


### Semantic rename

`rename_symbol` uses the language server's `textDocument/prepareRename` and `textDocument/rename` support. It does **not** grep and replace text.

Preview first:

```text
rename_symbol(
  symbol_path="UserService/createUser",
  new_name="registerUser"
)

→ applied: false
→ file_count: 3
→ edit_count: 7
→ files: [...]
```

Apply the same semantic operation explicitly:

```text
rename_symbol(
  symbol_path="UserService/createUser",
  new_name="registerUser",
  apply=true
)
```

The returned `WorkspaceEdit` is validated before any write. Only regular files inside the running workspace are eligible; resource operations such as file create/delete/move and overlapping edits are rejected. All edits are validated before the first file is written, and write failures trigger best-effort rollback.


### Document formatting

`format_document(path="src/app.go")` asks the language server for `textDocument/formatting`, validates every returned edit, and previews the change. Add `apply=true` to write it. Formatting uses the same workspace confinement, overlap checks, preview-first behavior, and rollback path as semantic rename.

## Verification

Run `go test ./...` and `go test -race ./...` before contributing. CI also runs integration tests against actual Go, TypeScript, and Python language servers.

For a concrete mixed-language proof, see [`examples/react-fastapi-monorepo`](examples/react-fastapi-monorepo/). One `simple-lsp-mcp` engine opens a React/TypeScript + FastAPI/Python monorepo, routes files to separate real LSP servers, lists symbols from both languages in one workspace traversal, and follows cross-file calls with `get_semantic_slice` on both sides. The fixture also exercises per-LSP `env`, `settings`, and `initialization_options`. CI runs this example so the claim cannot silently drift away from the implementation.

See [`docs/evidence.md`](docs/evidence.md) for exactly what is proven today and what still needs agent-level benchmarking. These tests verify code navigation and semantic results; they do **not** yet prove that an AI agent completes tasks faster or more accurately. No such claim is made without real agent-task results.

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
