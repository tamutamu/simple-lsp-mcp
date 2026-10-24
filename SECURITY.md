# Security Policy

## Supported versions

Only the latest released version receives security fixes.

## Reporting a vulnerability

Please do **not** open a public issue for a security vulnerability.

Report it privately through
[GitHub Security Advisories](https://github.com/tamutamu/simple-lsp-mcp/security/advisories/new).

Please include the version, the platform, a description of the impact, and
steps to reproduce. You can expect an initial response within 7 days.

## Security model

`simple-lsp-mcp` is designed to be safe to expose to an autonomous agent. The
following properties are intentional and are treated as security boundaries —
a bug that breaks one of them is a vulnerability, not a feature request:

- **Reads stay read-only; writes are explicit LSP operations.** Navigation and diagnostic tools never modify source files. `rename_symbol` and `format_document` are preview-only unless `apply=true`; `apply_code_action` is an explicit write. All source writes come from a language-server `WorkspaceEdit` or formatting edit and are validated before application: workspace escapes, symlink targets, overlapping edits, resource operations, and stale target files are rejected. Missing configuration is never created on startup. The `setup` CLI can change Claude/Codex registration only with `--apply`.
- **No shell.** `command` and `args` from `.simple-lsp.yaml` are passed directly
  to the process launcher. There is no shell interpretation, so `~`,
  environment-variable expansion, pipes, globbing, and shell argument splitting
  do not apply.
- **No arbitrary command execution from tool input.** Tool arguments cannot
  choose which process is launched. Only `.simple-lsp.yaml` can choose language-server executables, and that file is under the user's control in the workspace.
- **Workspace confinement.** Paths in tool arguments are resolved relative to
  the workspace root. Escaping the workspace root is a bug.
- **Plugin-managed startup and agent setup:** The shipped plugin `.mcp.json` opts in to `--auto-install-lsp` so first use may download pinned third-party language-server packages. Installing the plugin is therefore consent to that behavior; users who prefer no downloads can register the standalone MCP without this flag. The `/simple-lsp-mcp-setup` skill can install an official release binary using HTTPS, and verifies the published SHA-256 checksums before installing to user-local storage. It does not modify PATH, shell profiles or source files, and asks permission before replacing an existing MCP registration. Because the published checksum and archive come from the same GitHub release, this detects corruption but does not independently establish publisher authenticity.
- **Automatic LSP discovery is read-only.** Without `.simple-lsp.yaml`, supported project markers are detected in memory; ignored directories and symlinks are not traversed. Without managed mode, installed language servers start lazily. In `--auto-install-lsp` mode, every detected language server runs from a versioned user-owned cache, regardless of system-wide LSP installs. On cache miss, pinned packages are installed by `npm install --ignore-scripts` or `go install`. No source-repository dependency files, global packages, PATH, or shell profiles are modified; installation can access the network, and the downloaded language server itself is third-party executable code. This requires explicit one-time opt-in at MCP registration; without the flag automatic discovery never introduces package downloads. An explicit `.simple-lsp.yaml` can still configure its own package-manager launchers, so review trusted configuration separately. Node.js/npm or Go must be installed separately. Cache writes occur only when a requested language needs provisioning; files are staged and renamed after successful installation, with a per-version lock to prevent concurrent installers.
- **No direct network requests by navigation tools.** The server speaks stdio to the MCP client and local language servers. Configured child processes (for example `npx`) may independently download software or access the network; review `.simple-lsp.yaml` first.

## Current edit limitations

LSP-computed edits are validated before they are written, but file writes are
**not transactional**. Source files are currently updated with `os.WriteFile`;
a crash or write error after truncation can leave the current file incomplete.
For multi-file edits the engine attempts to restore files already written if a
write returns an error, but that rollback is best-effort and cannot recover
from process termination. Validation and writes are also separated in time;
concurrent external file edits or symlink replacement can race the preflight
checks. Run write tools only on a trusted workspace and avoid concurrent
modification of their target files.

## What is trusted

`.simple-lsp.yaml` is trusted configuration. It names executables that will be
launched. Treat it the way you would treat a `Makefile` or a `.vscode/` folder:
review it before opening an untrusted repository with this server enabled.

Language servers themselves are third-party processes and are outside this
project's trust boundary. `simple-lsp-mcp` does not sandbox them.