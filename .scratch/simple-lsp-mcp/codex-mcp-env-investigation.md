# Codex MCP environment investigation

Date: 2026-07-31

## Finding

The project configuration is active and takes precedence over the user
configuration. Its `LSP_MCP_SERVERS` value defines only the Go profile:

```toml
[mcp_servers.simple-lsp.env]
LSP_MCP_SERVERS = """{"go":{"command":"gopls","args":[]}}"""
```

`LSP_MCP_SERVERS` is one environment-variable string, so this project value
replaces the user-level value containing the Python profile; it does not merge
the JSON objects. Consequently `simple-lsp-mcp` receives no `python` profile
and correctly reports `UNSUPPORTED_LANGUAGE`.

This is not an unsupported Codex syntax. The documented key
`mcp_servers.<id>.env` is a `map<string,string>` whose entries are forwarded to
the MCP stdio server. The current configuration uses the documented nested-TOML
form.

## Evidence

- `.codex/config.toml` sets `LSP_MCP_SERVERS` to Go only, while
  `~/.codex/config.toml` sets the same variable to a JSON value containing
  Python, TypeScript/JavaScript, Go, HTML, and CSS.
- `codex mcp get simple-lsp` run from this repository resolves the configured
  `simple-lsp` stdio server and its `LSP_MCP_SERVERS` environment entry.
- Codex CLI version used for the check: `0.144.6`.

## Official documentation

- [Config basics: configuration layers and precedence](https://developers.openai.com/codex/config-basic)
  documents project `.codex/config.toml` overrides, their higher precedence
  over user config, and that project config can configure MCP servers.
- [Configuration reference: `mcp_servers.<id>.env`](https://developers.openai.com/codex/config-reference)
  defines the key as `map<string,string>` and states that it forwards
  environment variables to the MCP stdio server.
- [MCP: stdio server configuration](https://developers.openai.com/codex/mcp/)
  shows the same `[mcp_servers.<id>.env]` nested-table form and distinguishes
  literal `env` values from `env_vars`, which whitelists inherited variables.
- [Advanced configuration: one-off overrides](https://developers.openai.com/codex/config-advanced)
  documents `--config` / `-c` nested-key overrides.
- [Codex source: MCP connection manager](https://github.com/openai/codex/blob/main/codex-rs/codex-mcp/src/connection_manager.rs)
  starts enabled MCP servers asynchronously. Their process environment is set
  when they launch, so a configuration change requires a new MCP process.

## Non-mutating remedies

1. Remove the project-level `LSP_MCP_SERVERS` entry to inherit the user-level
   value.
2. Or set the project-level value to the complete JSON profile set, including
   `python`.
3. For a one-off run, use a `--config` override with the complete desired
   `mcp_servers.simple-lsp.env.LSP_MCP_SERVERS` value.

The root cause is resolved configuration precedence, not invalid syntax or a
reload limitation. Restarting alone preserves the same effective project value.
