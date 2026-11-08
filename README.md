# simple-lsp-mcp

[![CI](https://github.com/tamutamu/simple-lsp-mcp/actions/workflows/ci.yml/badge.svg)](https://github.com/tamutamu/simple-lsp-mcp/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/tamutamu/simple-lsp-mcp.svg)](https://pkg.go.dev/github.com/tamutamu/simple-lsp-mcp)
[![Latest release](https://img.shields.io/github/v/release/tamutamu/simple-lsp-mcp)](https://github.com/tamutamu/simple-lsp-mcp/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**A lightweight bridge between coding agents and real language servers.**

`simple-lsp-mcp` exposes **23 MCP tools** in its full profile to Claude Code, Codex, and other MCP clients. It provides LSP symbol definitions, references, implementations, call/type relationships, diagnostics, computed edits and an optional transitive impact investigation. **The agent** decides what to inspect and whether a change is safe; a language server's results are limited by its configured workspace and must not be assumed complete across independent Go modules.

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

### Transitive impact before modifying a declaration

Use the read-only `analyze_impact` MCP tool for a function, variable or method declared on a known line. No character column or preflight symbol search is needed:

```json
{"path": "src/services/task.ts", "line": 12, "depth": 3}
```

`depth=1` returns directly dependent declarations; `depth=2` follows their references one additional hop; `depth=3` follows two additional hops. The default is 8 and the maximum is 16. `max_nodes` defaults to 80 and can be raised to 200. A declaration must start on the specified line; if several declarations start on that line, the tool rejects the ambiguous target rather than guessing.

The MCP response is a compact graph of `nodes` (source path, declaration line, name, kind and hop depth) and `edges` (dependent declarations, reference counts and example lines). It visits each declaration once, so mutually recursive functions and cycles are safe. When exploration stops at the requested depth or a resource limit, `meta.complete=false` and `meta.limit_reasons` explain why. Unmapped references are counted separately, also making the analysis incomplete. LSP references are static evidence: dynamic calls, runtime reflection, missing project indexes and behavior-dependent changes may not appear. These are **potential impacts**, not a guaranteed list of code that must be edited.

### One-call semantic edit context (opt-in)

Use \`inspect_symbol\` with \`related\` when a cross-file change needs more than source text.
The MCP resolves the exact declaration once, then obtains only the explicitly
requested facts from the language server:

\`\`\`json
{"path":"src/services/task.ts","symbol_path":"TaskService/list","view":"edit","related":["references","incoming_calls","implementations"],"relation_limit":8}
\`\`\`

Go receivers may be targeted using either the server-reported flat symbol
(e.g. \`(*Log).EmitAuditEvent\`) or \`symbol_path="Log/EmitAuditEvent"\`.
The latter only matches the named receiver and method; it never guesses
between multiple matching declarations. Anchored paths remain strict.

The response contains the original code plus compact, LSP-resolved references
(grouped by file), callers and implementations. It reports each unsupported or
failed relation in \`related_meta.failed\` and each bounded result in
\`related_meta.truncated\`; an empty successful result is distinct from an
unavailable relationship. No relations run unless \`related\` is supplied, and
the feature does not attempt to infer dynamic/runtime dependencies.
The option is for **one symbol**; use normal \`inspect_symbol(targets=[...])\`
for multiple source bodies without relation fan-out. This is a design change,
**not** a proven speedup: measure it against separate LSP calls and Codex without MCP.

### When coding agents should use it

Connecting MCP is optional, but when connected under this repository's `CLAUDE.md`, use at least one relevant LSP operation before modifying source. For a **known file/function**, call `inspect_symbol(path=..., symbol_path=..., view="edit")` directly, rather than fetching an outline first or rereading the same file. For cross-file renames, call the **read-only** `preview_rename` before any write-capable `rename_symbol(apply=true)` request; this separates safe inspection from tools requiring destructive-operation approval. Large renames (more than 12 edits) return file counts and bounded samples to avoid flooding agent context; use `preview_rename(preview_detail="full")` to review every exact edit when needed. The write-capable `rename_symbol` response remains unchanged. For a name collision, cross-file rename, interface implementation, or caller investigation, prefer the relevant single LSP primitive. For routine edits, `get_document_symbols` supplies a compact file outline, and `inspect_symbol(view="source")` reads the actual relevant implementation without dumping unrelated functions. Use normal editor, grep and tests afterward. Never chain unrelated tools merely to increase tool counts. Reuse `symbol_id` returned by searches or `symbol_path` to target subsequent calls; explicit `path`, `line`, `column` are also supported.

The `/simple-lsp-mcp-setup` skill can append a short **conditional** policy to your project's `CLAUDE.md` or `AGENTS.md` when explicitly requested. The checked-in `CLAUDE.md` for this repository now **requires at least one relevant LSP tool call before source edits when MCP is connected**. For the FastAPI A/B study, that exact conditional policy is shared by both groups; disconnected runs use ordinary tools.

### Scenario-first evaluation: where LSP helps, and where it does not

A [development-ticket-first evaluation matrix](docs/benchmarks/scenario-first-evaluation.md) now maps **16 real development situations** to candidate LSP capabilities, ordinary non-MCP workflows and independently checked outcomes. Its [initial three-scenario Codex pilot](docs/benchmarks/scenario-first-pilot-2026-11-08.md) deliberately **does not require Codex to call LSP**, and records unfavorable results.

The pilot uncovered a serious correctness gap: in a Go multi-module workspace, `textDocument/implementation` returned **2 locations with `complete=true`**, while a separate whole-tree Go AST scan found **24 explicit production method declarations** matching the interface; Codex without MCP found all 24. The original grader incorrectly considered the LSP result to be the whole truth. This is documented rather than hidden. The server now warns `requires_cross_module_review=true` and reports `complete=false` where multiple Go modules may cause incomplete implementation lookup. **The missing implementations are not yet automatically retrieved by the LSP tool.**

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

- **Reads stay read-only; writes are explicit.** `preview_rename` is strictly read-only; the backward-compatible `rename_symbol` previews by default but is marked write-capable because `apply=true` mutates files. `preview_format` is strictly read-only; `format_document` also previews by default but remains marked write-capable because `apply=true` mutates files. `apply_code_action` applies only the selected WorkspaceEdit-backed action.
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

Core performance and correctness fixes, including language-server restart resynchronization and removal of redundant warm-up file reads, are tracked in the [2026-11-01 implementation audit](docs/benchmarks/core-performance-audit-2026-11-01.md). The global source-file scan before some semantic reference operations remains a known bottleneck; this is not yet a product-level performance proof.

## MCP tool profiles

### Experiment: remove duplicated MCP result payload

By default each response includes the same JSON as both `content` text and `structuredContent`, for backward compatibility. This doubles the representation on the protocol wire. To test whether your **MCP client** benefits, use `--result-format text` (alongside `--tool-profile coding` if desired). This returns the same JSON in one text block, omits the duplicate `structuredContent`, and does not advertise a structured output schema. `--result-format both` is the unchanged default.

**This is an opt-in, client-compatibility experiment.** A smaller MCP response is not proof of fewer model input tokens; the client may expose only one representation to the model already. Validate your client's support before adopting it. The [predeclared significance gates](docs/benchmarks/significance-gates-2026-11-01.md) require repeated *graded agent tasks*, not just smaller protocol output.


Direct invocation defaults to `--tool-profile full` with **23** operations. `simple-lsp-mcp setup codex` registers the **15-tool `coding` profile** with `--result-format text`; the profile includes read-only source inspection, symbol navigation, read-only `preview_rename`, and the write-capable `rename_symbol(apply=true)` tool. A Codex agent running an explicit cross-file rename can now apply all semantic edits in a **single authorized MCP call**, instead of reading each source and patching manually. This was **not possible** in the former coding profile, which omitted rename entirely. `rename_symbol` remains marked destructive so clients can enforce write approval; `preview_rename` is separately read-only.

For multi-step Go refactoring, the opt-in `--tool-profile refactor` exposes only **four task-relevant tools**: `inspect_symbol`, `investigate_change`, `preview_rename`, and `rename_symbol`. To register it for new Codex sessions, use `simple-lsp-mcp setup codex --apply --tool-profile refactor` (the setup command does not replace existing registrations). In a direct MCP `tools/list` check, the 15-tool `coding` catalog serialized to **17,163 bytes**, versus about **6,900 bytes** for the 4-tool `refactor` catalog (about 60% smaller). The refactor profile also removes the large `preview_detail=full` and `investigate_change(detail=graph)` choices from its advertised schema, and returns a **minimal file/count/candidate preview**. In a direct real-gopls run, the full rename response was about 23.8KB; the planning preview was **4.4KB**, or **5.2KB with bounded Go method signatures** that help distinguish unrelated same-name RPC methods. Full edit-range previews remain available in coding/full profiles. These are catalog/payload bytes, **not model tokens**; see the [Codex response-shaping evaluation](docs/benchmarks/codex-refactor-response-shaping-2026-11-08.md) for agent-level observations.

For one-purpose refactoring workflows, the direct server option `--tool-profile rename` exposes just the two rename tools, without changing the default configuration. To expose all navigation, formatting and code-action tools to Codex use `simple-lsp-mcp setup codex --apply --tool-profile full`; Claude Code setup retains full by default. Existing registrations must be updated and restarted to obtain the new surface.

Earlier (2026-11-07) direct JSON-RPC `tools/list` measurements with text results: **14,222 bytes (coding: 14 tools)** versus **2,364 bytes (rename: 2 tools)**. These are schema bytes, **not** model tokens. In paired Codex rename tasks, the small profile reduced total input tokens in both exploratory trials, but did **not** reliably reduce fresh (non-cached) input. See [semantic rename A/B audit](docs/benchmarks/semantic-rename-codex-2026-11-07.md) for actual times, token accounting, identical Git diff hashes and limitations.

When several exact functions are known, call `inspect_symbol(view="edit", targets=[{"path":"backend/app/api/routes/items.py","symbol_path":"read_items"},{"path":"backend/app/api/routes/users.py","symbol_path":"read_users"}])` for their actual source contexts in one call. Supports 1–8 targets, rejects ambiguous/duplicate results and results above 32 KiB. Single-symbol requests are unchanged. In the direct warm-language-server test, three calls took **12.37 ms** vs **12.75 ms** batched and produced **4,117 B** vs **4,133 B** of data. Thus batching saves client calls, **not** backend latency.

[Measurements and limitations](docs/benchmarks/coding-profile-batch-2026-10-26.md).
### Atomic semantic rename across Go interfaces

A Go method can implement several **independent interfaces**. The batch operation requires all targets to share the same existing method name. Renaming just one declaration can leave other interface contracts or callers broken. Use `rename_symbol(new_name="RecordAuditEvent", targets=[{"path":"lib/events/api.go","symbol_path":"IAuditLog/EmitAuditEvent"},{"path":"api/types/events/api.go","symbol_path":"Emitter/EmitAuditEvent"}], apply=true)`. Each target is resolved by the LSP against unchanged source; the combined edits are checked for overlaps, unsafe paths and stale files **before any write**, with rollback on write failure. `preview_rename` supports the same `targets[]` without modifying files.

For Go, the server preloads semantic references before asking `gopls` for rename edits, and compares **normalized edits** to avoid rejecting identical plans with different edit orders. If the server alternates between compatible subsets and supersets, it selects an **actual validated LSP response** containing every observed edit, with `rename_plan_fluctuated=true`; incompatible plans still abort before writing. In a batch, `covered_target_indices` identifies method declarations already covered by an earlier interface edit, preventing unsafe redundant rename requests. A top-level `path` + `symbol_path` may also be combined with `targets[]`; the same identity is not counted twice, and an optional top-level `language` is inherited. If a Go implementation target appears before its interface and gopls specifically rejects it for violating assignability, the tool defers that target once, computes the other explicitly requested edits, then requires the implementation to be covered by an exact LSP edit or to rename successfully before writing. The result may include `coverage_complete=false` and `unverified_build_tag_files`: source excluded by build constraints or GOOS/GOARCH suffixes is reported for review, **never silently rewritten by string replacement**. Such a result must not be treated as proof of a complete project-wide refactor.

For Go rename previews, the optional `unrenamed_method_candidates` field lists other **syntax-confirmed interface and receiver methods** still bearing the old name. This helps agents discover independently declared interfaces **before** choosing `rename_symbol(targets=[...])`. Generated declarations have `generated: true` and are listed after ordinary code. The optional bounded `signature` field shows a Go-syntax hint (parameters and return types) to help distinguish the audit-event interface from an unrelated gRPC handler with the same method name. These names are candidates, not proven semantic dependencies; the tool never changes them without an explicit target.

In the [Teleport Codex refactor comparison](docs/benchmarks/codex-teleport-refactor-2026-11-08.md), both arms modified 49 files and passed scoped compile and diff checks. An earlier exploratory run reduced elapsed time (137.73 vs 212.28 s) but increased fresh input tokens 8.8%. After accepting natural batch arguments and deferring interface-dependent methods, another pair completed at **134.43 vs 144.92 s**, with **30.1% lower total input and 7.9% lower fresh input** on the LSP arm. In a subsequent [two-pair response-shaping evaluation](docs/benchmarks/codex-refactor-response-shaping-2026-11-08.md) with a minimal preview and Go method signatures, Codex completed the same 49-file change using **34.0% less time, 60.2% fewer total input tokens and 39.4% fewer fresh input tokens** on average; all four tasks passed the same scoped checks. The LSP arm used only 2–3 MCP operations per task without failed rename requests. These are exploratory observations, not general estimates; alternate Go build-tag compilation remains unverified.

### High-level change-impact investigation

`investigate_change` combines exact LSP declaration resolution, transitive references (depth configurable), and enclosing-declaration attribution into **one MCP call**. The default `detail=files` groups affected declarations by file, including counts and explicit completeness limits. For longer work, send 2–6 `targets` with `detail=union` to analyze several functions with one call and return a deduplicated file union. The read-only, minimal `--tool-profile investigate` exposes just this tool.

The [earlier Codex pilot](docs/benchmarks/composite-impact-evaluation-2026-11-07.md) exposed an unfair evaluation setup: Codex's sandbox prevented the ordinary `gopls` CLI from writing its Go build cache, causing incorrect/empty references. **Do not use the old pilot's relative speed, fresh-token or correctness figures as product evidence.** The tool reports truncation and unmapped references rather than claiming exhaustive impact. It now also reports `incomplete` as an explicit boolean for convenient agent output.

In the [corrected six-symbol, three-phase Codex evaluation](docs/benchmarks/codex-longrun-impact-2026-11-08.md) on 4,361 Go sources, both arms used identical writable per-checkout Go caches and Codex permissions. Direct references were independently cross-checked with `gopls references`. Across two trials, the one-session LSP arm completed **all three phases correctly in three MCP calls**, with **26.5% fewer fresh input tokens, 59.5% fewer total input tokens and 72.2% less elapsed time** than the ordinary Codex arm, which did **not** complete all three phases accurately. The groups have unequal correctness and only two exploratory trials, so these are not general estimates of same-quality task performance. In one additional unforced Codex trial with the normal 15-tool coding profile, the agent **selected `investigate_change` on its own**, without a tool-use requirement.

## MCP tools (23 goal-oriented operations)

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
| `rename_symbol` | LSP semantic rename, with optional 2–6 independent interface targets applied atomically |
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