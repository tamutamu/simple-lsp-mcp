# simple-lsp-mcp

[![CI](https://github.com/tamutamu/simple-lsp-mcp/actions/workflows/ci.yml/badge.svg)](https://github.com/tamutamu/simple-lsp-mcp/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/tamutamu/simple-lsp-mcp.svg)](https://pkg.go.dev/github.com/tamutamu/simple-lsp-mcp)
[![Latest release](https://img.shields.io/github/v/release/tamutamu/simple-lsp-mcp)](https://github.com/tamutamu/simple-lsp-mcp/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**A lightweight bridge between coding agents and real language servers.**

`simple-lsp-mcp` exposes **18 focused LSP tools** to Claude Code, Codex, and other MCP clients. It supplies authoritative symbol definitions, references, implementations, call/type relationships, diagnostics, and LSP-computed edits. **The agent** decides what to inspect and whether a change is safe; the MCP does not synthesize an impact graph, rerun its own Git analysis, or dictate a development workflow.

```text
find a symbol   → search_symbols / get_document_symbols / list_workspace_symbols
follow a link   → get_definition / find_references / find_implementations
inspect calls   → get_incoming_calls / get_outgoing_calls
inspect types   → get_type_definition / get_supertypes / get_subtypes
make a change   → rename_symbol / format_document / code actions
check a file    → get_diagnostics
```

### Why this exists

Text search finds spelling matches; LSP resolves **which declaration a use refers to**, including languages with overloaded names, interface implementations, and large dependency graphs. Instead of reading unrelated files, an agent can ask precisely for one semantic relationship and choose its next step based on the result. With the optional `CLAUDE.md` integration policy enabled, invoke **one useful LSP operation before editing source** when connected; use normal text search as well if that is the quickest way to locate the file. No fixed multi-step analysis or verification sequence is imposed.

### When coding agents should use it

Connecting MCP is optional, but when connected under this repository's `CLAUDE.md`, use at least one relevant LSP operation before modifying source. For a name collision, cross-file rename, interface implementation, or caller investigation, prefer the relevant single LSP primitive. For routine edits, `get_document_symbols` supplies a compact file outline, and `inspect_symbol(view="source")` reads the actual relevant implementation without dumping unrelated functions. Use normal editor, grep and tests afterward. Never chain unrelated tools merely to increase tool counts. Reuse `symbol_id` returned by searches or `symbol_path` to target subsequent calls; explicit `path`, `line`, `column` are also supported.

The `/simple-lsp-mcp-setup` skill can append a short **conditional** policy to your project's `CLAUDE.md` or `AGENTS.md` when explicitly requested. The checked-in `CLAUDE.md` for this repository now **requires at least one relevant LSP tool call before source edits when MCP is connected**. For the FastAPI A/B study, that exact conditional policy is shared by both groups; disconnected runs use ordinary tools.

### Evidence: measured on real coding tasks

We compared **Claude Code with and without the previous 26-tool simple-lsp-mcp v0.10.2** on the official [FastAPI full-stack template](https://github.com/fastapi/full-stack-fastapi-template) (pinned commit `b1125d8`): **six tasks × three runs × two conditions = 36 independent coding sessions**. Both conditions used the same model, prompts, project instructions and file tools. **Claude's own LSP was disabled**; the only intended difference was whether simple-lsp-mcp was connected.

| Task | Verified solutions without / with MCP | Average time without / with MCP | Average estimated API cost without / with MCP |
| --- | --- | ---: | ---: |
| Item search feature | 3/3 / 3/3 | 23.37s / 23.03s | $0.0802 / $0.0992 |
| Pagination bug fix | 3/3 / 3/3 | 14.23s / 22.23s | $0.0495 / $0.0921 |
| Item authorization refactor | 3/3 / 3/3 | 15.93s / 19.57s | $0.0559 / $0.0790 |
| Owner-scoped item summary feature | 3/3 / 3/3 | 25.10s / 34.10s | $0.0755 / $0.1252 |
| Cross-file user lookup rename | 3/3 / 3/3 | 17.03s / 19.53s | $0.0396 / $0.0919 |
| Cross-file password helper rename | 3/3 / 3/3 | 11.73s / 17.90s | $0.0351 / $0.0793 |
| **All tasks, equal task weight** | **18/18 / 18/18** | **17.90s / 22.73s** | **$0.0560 / $0.0945** |

**What the evidence says:** in this compact real project, MCP made **no measurable difference in acceptance-test success** and increased average time (27%) and reported cost (69%). All 18 MCP-enabled sessions actually used the tools (47 MCP calls total); neither condition used Claude's LSP. This is **not** evidence of a universal coding productivity boost, and we won't advertise one. The exact task prompts, independent HTTP/AST graders, PostgreSQL cross-check, methodology and **all 36 raw outcome rows** are published in the [FastAPI A/B report](docs/benchmarks/fastapi-template-2026-10-24.md).

**What we changed afterward:** the benchmark tested the old composite-tool architecture, **not** this 18-tool version. We removed the composite analysis/guard tools and eliminated the mandatory tool workflow. The previous [three-pair Go rename pilot](docs/evidence.md#coding-agent-outcome-ab-pilot-2026-10-24) did show savings on that one task; the FastAPI results show why one workload cannot justify blanket productivity claims. The lean bridge still requires its own A/B study. A [36-run reproducible evaluation harness](docs/benchmarks/run_fastapi_agent_ab.py) is available. The first 2026-10-25 authenticated run revealed that optional MCP was never invoked; the controlled rerun explicitly mandates relevant LSP calls whenever MCP is connected. Interpret the two configurations separately.

**2026-10-25: real Codex A/B on the lean 18-tool build.** Six pinned FastAPI tasks × three runs × two conditions (**36 accepted solutions / 36 attempts**) with Codex CLI 0.147.0, gpt-5.6-sol, and a shared conditional mandatory-MCP `AGENTS.md`: adding Simple LSP did **not** improve focused grader acceptance (**18/18 in both arms**). Mean duration was **66.08 s without MCP vs 70.01 s with MCP (+5.95%)**; average reported input tokens were **307,843 vs 477,106 (+55.0%)**. The agent made 48 MCP calls, of which **11 `rename_symbol` calls were cancelled by Codex** (including read-only previews), and one run did not connect to MCP. This is not evidence of a general benefit or a significant slowdown beyond these tasks. The [complete measured report](docs/benchmarks/fastapi-codex-lean-2026-10-25.md), [36-row data](docs/benchmarks/fastapi-codex-lean-2026-10-25.csv) and [reproducible runner](docs/benchmarks/run_fastapi_codex_ab.py) are published; the rename approval issue is the first remediation priority.

**MCP payload audit:** In the real Codex traces, `get_document_symbols` accounted for **87% of successful MCP response bytes**. The default response now returns a compact outline and preserves `detail="full"` for the complete tree. On representative FastAPI files, direct Pyright/TypeScript payloads decreased by **84–94%** with semantic symbol handles still usable. This is **not** an agent-task speedup claim; details and limits are in the [payload audit](docs/benchmarks/fastapi-codex-mcp-payload-audit-2026-10-25.md).

**Lean bridge follow-up (direct LSP only):** A [three-pair test on the same FastAPI checkout](docs/benchmarks/fastapi-template-lean-direct-2026-10-24.md) confirmed that the 18-tool catalog is 29.6% smaller and that position-based Python reference searches now return **13** actual references at first request rather than **1** (a previously hidden incomplete result). This improvement needs about 163 ms for that initial reference request versus about 5 ms before the fix; the entire direct-tool sequence was about 6.6% slower. **It is a direct-tool test, not an agent A/B result.** The independent Codex coding-agent A/B result is reported immediately above.

### Design boundaries

- **Reads stay read-only; writes are explicit.** `rename_symbol` and `format_document` preview by default. `apply_code_action` applies only the selected WorkspaceEdit-backed action.
- **No hidden index or embeddings.** Results come live from configured local language servers.
- **Bounded project warm-up where needed.** TypeScript/Pyright project-wide relations open only candidate source files containing the target spelling, under hard file/byte/match limits; semantic answers still come from the LSP.
- **No external indexing service.** Workspace code stays local to the MCP/LSP processes.
- **Bounded results.** Searches and symbol listings enforce result/pagination budgets and surface incomplete operations.
- **Cross-language API.** The same semantic operations work across supported LSP profiles; language is inferred for most target-based tools.
- **Direct LSP operations.** The agent—not the MCP—decides whether and how to combine the results.
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

## Setup from inside the coding agent

Once the **simple-lsp-mcp plugin** is installed, run the bundled setup skill directly in the agent. It checks the host, installs an official checksummed binary if needed, inspects existing MCP registrations before changing anything, auto-detects the workspace, and probes real language servers. No manual setup commands or per-language Claude Code plugins are required.

- **Claude Code:** `/simple-lsp-mcp-setup` (full name `/simple-lsp-mcp:simple-lsp-mcp-setup`).
- **Codex:** `$simple-lsp-mcp-setup` (Codex's skill invocation syntax, not a custom `/` command).

**The skill must be installed before it can be invoked.** The plugin includes it via `skills/simple-lsp-mcp-setup/SKILL.md`; installing only the standalone MCP binary does not add an agent slash command. The skill's release installer uses official SHA-256-verified GitHub assets and never needs administrator rights. If you are developing from an unpublished local checkout, the skill can build the local binary with Go instead. Client registration or plugin startup may require restarting the agent session; the skill will report that status rather than falsely claiming connection.

The packaged `.mcp.json` already enables managed LSP provisioning for plugin users. Every detected language server is installed under the private versioned cache and launched by an absolute path, without requiring an LSP executable on PATH. Third-party packages are fetched only when that language is first used. See [Security](SECURITY.md) for this behavior. CLI-only users can still choose the explicit `--auto-install-lsp` flag shown below.

## Automatic LSP setup (no per-language plugins)

**Register `simple-lsp-mcp` once with Claude Code or Codex.** You do not need
separate LSP plugins for each language. Without any `.simple-lsp.yaml`, the MCP
automatically discovers Go, Python, TypeScript/JavaScript, HTML and CSS projects
(including monorepos) and starts language servers only when an MCP tool needs
them. In the default no-download mode, the MCP uses LSPs installed on PATH;
managed mode instead uses its **own isolated versioned cache**. Detection does not modify workspace files or
install any packages.

For a **single opt-in that also downloads missing language servers**, run:

```sh
simple-lsp-mcp setup claude --apply --auto-install-lsp
# or
simple-lsp-mcp setup codex --apply --auto-install-lsp
```

Managed mode uses **pinned packages** and installs them exactly once into the
user cache (`~/.cache/simple-lsp-mcp/servers/` on Linux, or the platform's user
cache directory). The package installer uses `npm install` (Pyright,
TypeScript Language Server, HTML/CSS) or `go install` (gopls), but the actual
language server is subsequently started **directly by absolute path**, never
by searching PATH or invoking `npx` / `go run` again. No LSP global install and
no PATH modifications are necessary. Versions are isolated and shared across
workspaces. Override the cache location with the absolute-path environment
variable `SIMPLE_LSP_CACHE_DIR`.

The host needs Node.js and npm to install Node-based servers (and Node.js to
run them), or Go for the first gopls installation. The MCP does not install
those runtimes. Go must remain available for Go workspace analysis. First use may access the network; subsequent launches use the
managed cache, even when offline. No package installation occurs on startup or
when running `doctor` without `--probe`. Without `--auto-install-lsp`, missing
LSP executables are reported rather than downloaded.

An existing `.simple-lsp.yaml` (or `.simple-lsp.yml`) always wins over discovery
and the managed defaults. For custom language servers, project-specific settings
or unusual layouts, create `.simple-lsp.yaml` yourself; CLI `setup` and `doctor` handle client registration and diagnostics. **Ordinary projects need
no YAML and no restart after detection.**

### Inspect without installing

```sh
simple-lsp-mcp doctor --workspace .
simple-lsp-mcp doctor --workspace . --auto-install-lsp
# --probe is optional: it really starts the LSP (and may download in managed mode).
simple-lsp-mcp doctor --workspace . --auto-install-lsp --probe frontend/src/main.ts
```

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
- An installed LSP server for each language, or Node.js/npm and/or Go with `--auto-install-lsp`
- An MCP client such as Codex or Claude Code

No separate Claude Code LSP plugin is needed. The plugin-managed LSP mode installs and launches pinned language servers entirely from a private cache; individual LSP executables need not be on PATH. Each server starts lazily on the first request for its language.

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
simple-lsp-mcp setup claude --apply    # explicitly register; no downloads
simple-lsp-mcp setup codex --apply
simple-lsp-mcp setup claude --apply --auto-install-lsp  # permit first-use downloads
simple-lsp-mcp setup codex --apply --auto-install-lsp
```

`doctor` reports auto-discovered or explicitly configured LSPs, the running binary/version, and missing executables. It never writes configuration and only attempts a first-use package download if **both** `--auto-install-lsp` and `--probe` are supplied. `setup` defaults to preview, does not download software, and refuses to overwrite existing registrations. `--apply` registers the MCP using the selected client's CLI. To change an existing MCP registration to managed mode, remove it using that client's CLI before registering again.

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

In Codex, call the particular LSP primitive that answers your question: `search_symbols` to find a name, `get_document_symbols(path="src/greeting.ts")` to inspect one file, `find_references` for exact usages, and `rename_symbol` for an LSP-calculated refactor. The MCP infers the language from file paths or symbol handles when possible.

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

The plugin includes the `/simple-lsp-mcp-setup` skill. Run it from Claude Code to install the binary and verify LSPs from inside the agent. Codex includes the same skill as `$simple-lsp-mcp-setup`. If you install the binary separately, it still needs to be resolvable by the MCP client or registered by absolute path; a session restart may be necessary.

**Design reference:** [39 coding-agent actions mapped to required information and appropriate providers](docs/agent-coding-information-needs.md). The [action-first MCP tool and small Codex A/B report](docs/benchmarks/fastapi-codex-action-first-2026-10-25.md) includes direct wire-size checks and an independently graded two-task sample (4/4 passes); this preliminary sample is **not** evidence of universal productivity improvement. This is the source of truth for selecting LSP-derived information; file reading, Git, tests and judgment remain outside the MCP.

## MCP tools (19 goal-oriented operations)

Every tool returns structured data. Positions are **one-based**, paths workspace-relative. A target may be a `symbol_id` from another tool, a `symbol_path` such as `UserService/createUser` (optionally scoped with `path`), or a file `path` plus `line` and `column`. Use `search_symbols` / `get_document_symbols` to discover identifiers, then request **only the specific relation needed**. Tool calls are not mandatory.

| Tool | LSP operation / purpose |
| --- | --- |
| `search_symbols` | `workspace/symbol` name query (requires `query`, `language`) |
| `list_workspace_symbols` | Paginated complete workspace traversal using document symbols (no query required) |
| `get_document_symbols` | Concise file outline by default (top-level symbols, nested declarations and class members); `detail=full` returns the complete LSP tree |
| `get_hover` | `textDocument/hover` for type and documentation |
| `inspect_symbol` | Read one LSP-identified declaration as **real source code**, a bounded signature/header preview, or precise location (`view`) |
| `get_definition` | `textDocument/definition` with optional `relation_view="lines"` for actual source-line evidence |
| `get_declaration` | `textDocument/declaration` |
| `get_type_definition` | `textDocument/typeDefinition` |
| `find_references` | `textDocument/references`; `relation_view="lines"` also returns the matching source line |
| `find_implementations` | `textDocument/implementation` |
| `get_incoming_calls` | Prepare call hierarchy, then direct incoming calls |
| `get_outgoing_calls` | Prepare call hierarchy, then direct outgoing calls |
| `get_supertypes` | Prepare type hierarchy, then direct supertypes |
| `get_subtypes` | Prepare type hierarchy, then direct subtypes |
| `get_diagnostics` | Pull/push LSP diagnostics |
| `rename_symbol` | Preview / apply LSP `textDocument/rename` |
| `format_document` | Preview / apply document or range formatting |
| `get_code_actions` | List LSP code actions |
| `apply_code_action` | Apply one selected, validated WorkspaceEdit-backed LSP action |

By default, `get_document_symbols(path="src/example.ts")` returns one file path and compact `name`, `kind`, one-based `line`, and `symbol_id` fields, with nested declarations and relevant class members. Function-local variables and duplicate ranges/path fields are omitted. Use `detail="full"` for the legacy complete symbol tree with exact ranges and all locals. Handles returned by the outline remain usable for LSP navigation. This is a tool-payload optimization, **not** evidence of faster coding agents.

**Choosing the right return shape:** Use `get_document_symbols(path="src/app.ts")` for a compact file outline. When a symbol name and file are already known, call `inspect_symbol(path="src/app.ts", symbol_path="MyFunction", view="source")` directly rather than requiring a separate outline lookup. Use `inspect_symbol(symbol_id="...", view="source")` for a specific function's real code body, `view="signature"` for a bounded declaration header, or `view="location"` when only its exact range matters. The same tool also accepts a `symbol_path` or file `path` with one-based `line`/`column`. `find_references(symbol_id="...", relation_view="lines")` returns semantic references with bounded real source lines; omitting the view returns positions only. Source responses are capped at 120 lines/16 KiB by default (maximum `max_lines=500`) and explicitly report when incomplete. Do not reread entire files if the source snippet already answers the coding question. `rg` remains the right tool for literal text search.

`list_workspace_symbols(limit=100)` yields `next_cursor` when another page exists. Continue until absent; it does **not** require a query and remains available for full workspace enumeration. Workspace scans omit ignored dependency and generated directories and surface incomplete searches rather than silently returning false negatives.

All position-target tools also accept a `symbol_path`; if you do not know its exact shape, inspect `get_document_symbols` first. Paths are based on the language server's own document symbol tree. Ambiguous names return `AMBIGUOUS_SYMBOL` with candidates instead of guessing. When a pathless search cannot finish inside the bounded workspace traversal, it reports `INCOMPLETE_SEARCH`; supply `path` to narrow the request.

**Removed in the lean interface:** `find_symbol`, `get_symbol_outline`, `get_symbol_context`, `get_semantic_slice`, `impact_analysis`, `verify_change`, `get_symbol`, and the in-band `onboard` configurator. Ordinary file tools / Git and the agent compose any needed workflow. Client installation still uses `simple-lsp-mcp setup` / `doctor`; no MCP-only setup tool is required.

### LSP-backed editing

`simple-lsp-mcp` never guesses text replacements for semantic edits. It asks the configured language server to calculate them.

- `rename_symbol` calls `textDocument/prepareRename` when advertised, then `textDocument/rename`. It previews the complete cross-file `WorkspaceEdit` by default; `apply=true` writes it.
- `format_document` calls `textDocument/formatting`, or `textDocument/rangeFormatting` when a full start/end range is supplied. It also previews by default.
- `get_code_actions` calls `textDocument/codeAction` without writing. `apply_code_action` may use `codeAction/resolve`, then applies only an action backed by a `WorkspaceEdit`. Command-only actions, or actions that also require `workspace/executeCommand`, are refused rather than partially executed.

Before any write, every edit is validated: paths must stay inside the running workspace, targets must be regular non-symlink files, ranges must be valid and non-overlapping, and LSP resource operations such as create/delete/rename-file are rejected. All target files are rechecked for staleness before the first write.

## Verification

Run `go test ./...` and `go test -race ./...` before contributing. CI also runs integration tests against actual Go, TypeScript, and Python language servers.

For a concrete mixed-language proof, see [`examples/react-fastapi-monorepo`](examples/react-fastapi-monorepo/). One `simple-lsp-mcp` engine opens a React/TypeScript + FastAPI/Python monorepo, routes files to separate real LSP servers, lists symbols from both languages in one workspace traversal, and allows separate reference and call-hierarchy lookups. The fixture also exercises per-LSP `env`, `settings`, and `initialization_options`. CI runs this example so the claim cannot silently drift away from the implementation.

See [`docs/evidence.md`](docs/evidence.md) for exactly what is proven today and what still needs agent-level benchmarking. These tests verify semantic navigation and LSP-computed edits; they do **not** yet prove that an AI agent completes tasks faster or more accurately. No such claim is made without real agent-task results.

## Server options

| Option | Default | Description |
| --- | --- | --- |
| `--workspace` | Current working directory | Workspace root to inspect (defaults to process current working directory) |
| `--log-level` | `info` | `debug`, `info`, `warn`, or `error` |
| `--auto-install-lsp` | `false` | Install and use isolated pinned LSP packages on first use (requires Node.js/npm or Go; no LSP PATH) |
| `--request-timeout` | `15s` (`180s` with managed LSP) | Timeout for each LSP request; explicitly supplied value takes priority |
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
