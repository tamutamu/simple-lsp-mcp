# simple-lsp-mcp

[![CI](https://github.com/tamutamu/simple-lsp-mcp/actions/workflows/ci.yml/badge.svg)](https://github.com/tamutamu/simple-lsp-mcp/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/tamutamu/simple-lsp-mcp.svg)](https://pkg.go.dev/github.com/tamutamu/simple-lsp-mcp)
[![Latest release](https://img.shields.io/github/v/release/tamutamu/simple-lsp-mcp)](https://github.com/tamutamu/simple-lsp-mcp/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**A semantic development layer for coding agents, powered by the language servers you already trust.**

`simple-lsp-mcp` gives Claude Code, Codex, and other MCP clients one consistent API to **understand code and make LSP-computed changes** across languages. It keeps the language server's semantic precision, but packages common coding-agent tasks into fewer, bounded tool calls.

```text
understand  → get_semantic_slice / get_symbol_context
assess      → impact_analysis
verify      → verify_change
rename      → rename_symbol
format      → format_document
quick-fix   → get_code_actions → apply_code_action
```

The high-level tools come first because they answer complete agent tasks. The precise LSP primitives — definitions, references, implementations, callers/callees, supertypes/subtypes, declarations, and type definitions — **remain available** when a caller needs exactly one relation. Nothing useful is hidden behind a separate mode.

### Why this exists

Coding agents can reason well once they have the right semantic context. The expensive part is often finding that context and changing code without guessing from text. Plain grep produces name collisions and string matches; reading whole files burns context; raw LSP calls can require several model-visible round trips.

`simple-lsp-mcp` turns those primitives into agent-oriented operations:

- `find_symbol` — resolve a human-readable symbol without first knowing its file.
- `get_symbol_outline` — inspect structure without returning source text.
- `get_symbol_context` — source plus selected callers, callees, references, and implementations in one call.
- `get_semantic_slice` — gather a bounded semantic source neighborhood for understanding or changing a symbol.
- `impact_analysis` — estimate refactor blast radius.
- `verify_change` — Semantic Change Guard: compare the current Git diff with LSP impact, related tests, and current diagnostics before an agent declares the task complete.
- `rename_symbol` — preview/apply a cross-file rename calculated by the language server.
- `format_document` — preview/apply document or range formatting.
- `get_code_actions` / `apply_code_action` — inspect and apply WorkspaceEdit-backed LSP fixes.

```text
❌ grep → read → grep → infer → edit matching text → hope
✅ semantic context → impact → LSP-computed edit → verify_change → finish
```

### Design boundaries

- **Reads stay read-only; writes are explicit.** `rename_symbol` and `format_document` preview by default. `apply_code_action` applies only the selected WorkspaceEdit-backed action.
- **No hidden index or embeddings.** Results come live from configured local language servers.
- **Bounded project warm-up where needed.** TypeScript/Pyright project-wide relations open only candidate source files containing the target spelling, under hard file/byte/match limits; semantic answers still come from the LSP.
- **No external indexing service.** Workspace code stays local to the MCP/LSP processes.
- **Bounded context.** High-level responses have result/source/byte budgets and fail closed when a bounded search cannot finish.
- **Cross-language API.** The same semantic operations work across supported LSP profiles; language is inferred for most target-based tools.
- **Low-level primitives remain public.** High-level tools guide the common path without removing precise LSP access.
- **Lazy startup.** A language server starts only when a request needs it.

See [SECURITY.md](SECURITY.md) for the write/confinement model and [docs/evidence.md](docs/evidence.md) for what is proven by real-language-server tests.

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

The complete tool surface is intentionally retained. Tools are exposed in this order: **high-level semantic workflows**, **discovery/inspection**, **precise LSP relationship primitives**, then **setup**. Agents should normally start with the high-level group; direct primitives remain useful for exact queries.

Every tool returns structured data. Symbol, position, and range lines and columns are **one-based**; paths are relative to the workspace.

A **target** identifies one symbol or position, in exactly one of three forms: a `symbol_id` returned by an earlier call, a human-readable `symbol_path` such as `"UserService/createUser"` (optionally scoped to one file with `path`; see [Finding a symbol by name](#finding-a-symbol-by-name) below), or `path` together with `line` and `column`.

### High-level semantic workflows

| Tool | Purpose | Required input |
| --- | --- | --- |
| `find_symbol` | Resolve one symbol by `symbol_path` without knowing its file | `symbol_path` |
| `get_symbol_outline` | Structure without source text | target or `path` |
| `get_symbol_context` | Source plus selected callers/callees/references/implementations | target |
| `get_semantic_slice` | Byte-bounded semantic source neighborhood | target |
| `impact_analysis` | Refactor blast radius and affected files | target |
| `verify_change` | Semantic Change Guard for the current Git diff | None (`base` defaults to `HEAD`) |
| `rename_symbol` | Preview/apply an LSP semantic rename | target, `new_name` |
| `format_document` | Preview/apply document or range formatting | `path` |
| `get_code_actions` | List LSP code actions without writing | `path`, `line`, `column` |
| `apply_code_action` | Apply one WorkspaceEdit-backed action | `path`, `line`, `column`, `action_id` |
| `get_diagnostics` | LSP diagnostics for a file/cached workspace state | `path` when file-specific |

### Discovery and direct inspection

| Tool | Purpose | Required input |
| --- | --- | --- |
| `search_symbols` | Search workspace symbols by name | `query`, `language` |
| `list_workspace_symbols` | Enumerate all configured source symbols page by page | None |
| `get_document_symbols` | Hierarchical symbols for one file | `path` |
| `get_symbol` | Reuse an acquired `symbol_id` and optional source | `symbol_id` |
| `get_hover` | LSP hover/type information for an exact target | target |

### Precise LSP relationship primitives

These stay public intentionally. Use them when one exact LSP relation is preferable to an aggregate high-level result.

| Tool | Purpose | Required input |
| --- | --- | --- |
| `get_definition` | Definition locations | target |
| `find_references` | Reference locations | target |
| `find_implementations` | Implementation locations | target |
| `get_type_definition` | Type-definition locations | target |
| `get_declaration` | Declaration locations | target |
| `get_incoming_calls` | Direct callers | target |
| `get_outgoing_calls` | Direct callees | target |
| `get_supertypes` | Direct supertypes | target |
| `get_subtypes` | Direct subtypes | target |

### Setup

| Tool | Purpose | Required input |
| --- | --- | --- |
| `onboard` | Detect projects and generate `.simple-lsp.yaml` | None |

`search_symbols` requires a non-empty name query. **`list_workspace_symbols` really lists the workspace**, without a query, by fetching `textDocument/documentSymbol` for each source file. Use `get_document_symbols` for just one file.

To enumerate, call `list_workspace_symbols(limit=100)` and repeat with `cursor=<next_cursor>` until `next_cursor` is absent. Optional `language` and `kinds` filter the results; without `language`, all configured language profiles are included. Each call visits at most 32 files and returns at most 100 symbols, including nested symbols, with paths, ranges, and reusable `symbol_id`/`symbol_path` values. `meta.complete=false` and `meta.truncated=true` mean more pages remain, not a failed search. A page may contain no symbols when files have none; continue while `next_cursor` exists. `.git`, dependency, cache, and generated-output directories (for example `node_modules`, `dist`, `build`) and symlinks are excluded. This is a live traversal, not a snapshot: restart without a cursor if the workspace changes during pagination. Errors are returned explicitly rather than silently skipping failed LSP requests.

### Finding a symbol by name

`find_symbol`, `get_symbol_outline`, `get_symbol_context`, and every target-based tool above accept `symbol_path`: the names of a symbol and its enclosing symbols, joined by `/`, such as `UserService/createUser`. A trailing portion alone (`createUser`) is accepted when it is unambiguous anywhere in the searched file or workspace; prefix the path with `/` to require an exact match instead. Escape a literal `/` inside a name as `%2F`.

A `symbol_path` reflects whatever shape the language server's own `documentSymbol` response has — it is never inferred or restructured. Methods commonly nest under their type (`UserService/createUser`), but some language servers report them as one flat, already-qualified name instead (for example Go's `gopls`, which reports `(*UserService).CreateUser` as a single segment). Call `get_symbol_outline` or `get_document_symbols` first if you are unsure which form a given server uses.

When no file is specified, the server searches every configured LSP instance for the selected language and inspects every server-returned candidate up to a 128-file safety limit. If the lookup cannot finish, it raises `INCOMPLETE_SEARCH` rather than reporting a false unique result or false not-found. Supply `path` to narrow an incomplete search. Completeness refers to the LSP-returned candidate set: language-server indexes may themselves omit unindexed files.

When a `symbol_path` matches more than one symbol, `find_symbol`, `get_symbol_outline`, and `get_symbol_context` return `{"ambiguous": true, "candidates": [...]}` instead of failing — each candidate carries its own `symbol_id`, so the next call can target it directly. The same ambiguity on any other tool is an `AMBIGUOUS_SYMBOL` error listing candidates in its message, since those tools' output shape has nowhere else to put them.

### Semantic Change Guard

`verify_change` is the post-edit guard for coding agents. With no arguments it compares the current Git working tree to `HEAD`; `base` can select another commit. It is read-only.

The guard:

1. reads staged, unstaged, and untracked working-tree changes,
2. maps changed lines to the innermost current LSP symbols,
3. reuses semantic impact/context to find callers, references, implementations, and affected files,
4. lists related test files,
5. checks LSP error diagnostics on changed files and declaration-affected files,
6. when current errors exist, materializes the committed `base` into a bounded temporary snapshot and asks an isolated LSP engine whether each error already existed,
7. for newly introduced diagnostics, asks the current language server for applicable WorkspaceEdit-backed code actions and returns reusable `apply_code_action` inputs, and
8. returns `PASS`, `WARNING`, or `FAIL` with structured findings.

A declaration change that reaches an untouched non-test file is a warning by itself. An LSP error becomes `FAIL` only when it is **new relative to the committed base**. Pre-existing errors remain visible in `diagnostics` and `diagnostic_diff.unchanged_count`, but do not make an unrelated change fail. If the base snapshot cannot be verified safely, current errors are downgraded to warnings instead of being presented as proven regressions. `repair_candidates` are suggestions only: `verify_change` remains read-only, and the agent must explicitly call `apply_code_action` to write a selected LSP fix.

The base snapshot is created outside the worktree with `git archive`; it never changes Git metadata or source files. Extraction is bounded to 20,000 files / 512 MiB, rejects links and path escapes, and uses the same generated/cache-directory exclusions as workspace traversal. A clean current diagnostic set skips the baseline LSP entirely, so the common successful path does not pay for a second language-server startup.

This still does **not** prove business correctness and does not yet build a complete before/after semantic graph for the base commit. The current semantic impact graph comes from the working tree; the base snapshot is used for diagnostic regression comparison. Future relation-graph comparison can strengthen change classification without changing the public `verify_change` API.

### LSP-backed editing

`simple-lsp-mcp` never guesses text replacements for semantic edits. It asks the configured language server to calculate them.

- `rename_symbol` calls `textDocument/prepareRename` when advertised, then `textDocument/rename`. It previews the complete cross-file `WorkspaceEdit` by default; `apply=true` writes it.
- `format_document` calls `textDocument/formatting`, or `textDocument/rangeFormatting` when a full start/end range is supplied. It also previews by default.
- `get_code_actions` calls `textDocument/codeAction` without writing. `apply_code_action` may use `codeAction/resolve`, then applies only an action backed by a `WorkspaceEdit`. Command-only actions, or actions that also require `workspace/executeCommand`, are refused rather than partially executed.

Before any write, every edit is validated: paths must stay inside the running workspace, targets must be regular non-symlink files, ranges must be valid and non-overlapping, and LSP resource operations such as create/delete/rename-file are rejected. All target files are rechecked for staleness before the first write.

## Verification

Run `go test ./...` and `go test -race ./...` before contributing. CI also runs integration tests against actual Go, TypeScript, and Python language servers.

For a concrete mixed-language proof, see [`examples/react-fastapi-monorepo`](examples/react-fastapi-monorepo/). One `simple-lsp-mcp` engine opens a React/TypeScript + FastAPI/Python monorepo, routes files to separate real LSP servers, lists symbols from both languages in one workspace traversal, and follows cross-file calls with `get_semantic_slice` on both sides. The fixture also exercises per-LSP `env`, `settings`, and `initialization_options`. CI runs this example so the claim cannot silently drift away from the implementation.

See [`docs/evidence.md`](docs/evidence.md) for exactly what is proven today and what still needs agent-level benchmarking. These tests verify semantic navigation and LSP-computed edits; they do **not** yet prove that an AI agent completes tasks faster or more accurately. No such claim is made without real agent-task results.

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
