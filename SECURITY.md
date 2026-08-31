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

- **No writes.** No tool modifies, creates, or deletes a file.
- **No shell.** `command` and `args` from `.simple-lsp.yaml` are passed directly
  to the process launcher. There is no shell interpretation, so `~`,
  environment-variable expansion, pipes, globbing, and shell argument splitting
  do not apply.
- **No arbitrary command execution from tool input.** Tool arguments cannot
  choose which process is launched. Only `.simple-lsp.yaml` can, and that file
  is under the user's control in the workspace.
- **Workspace confinement.** Paths in tool arguments are resolved relative to
  the workspace root. Escaping the workspace root is a bug.
- **No network access.** The server speaks stdio to the MCP client and stdio to
  local language servers. It makes no outbound network requests.

## What is trusted

`.simple-lsp.yaml` is trusted configuration. It names executables that will be
launched. Treat it the way you would treat a `Makefile` or a `.vscode/` folder:
review it before opening an untrusted repository with this server enabled.

Language servers themselves are third-party processes and are outside this
project's trust boundary. `simple-lsp-mcp` does not sandbox them.
