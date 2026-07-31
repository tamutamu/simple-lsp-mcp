#!/usr/bin/env bash
# Runs opt-in end-to-end checks through the real Codex CLI, MCP server, and LSPs.
# It requires a logged-in Codex CLI and may consume API quota.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
work_dir=$(mktemp -d)
cleanup() {
  if [[ ${CODEX_E2E_KEEP_OUTPUT:-} == 1 ]]; then
    echo "kept Codex JSONL output in $work_dir"
  else
    rm -rf "$work_dir"
  fi
}
trap cleanup EXIT

require() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "required executable not found: $1" >&2
    exit 1
  fi
}

require codex
require go
require pyright-langserver
require typescript-language-server
require gopls
require npx
require jq

server_bin="$work_dir/simple-lsp-mcp"
go build -o "$server_bin" ./cmd/simple-lsp-mcp

run_case() {
  local name=$1
  local workspace=$2
  local profile=$3
  local command=$4
  local args=$5
  local expected=$6
  local tool=${7:-search_symbols}
  local document_path=${8:-}
  local output="$work_dir/$name.jsonl"
  if [[ -n ${CODEX_E2E_CASE:-} && $CODEX_E2E_CASE != "$name" ]]; then
    return
  fi
  local servers
  servers=$(printf '{"%s":{"command":"%s","args":%s}}' "$profile" "$command" "$args")

  local prompt="Use the simple_lsp $tool tool to find $expected. Do not use shell commands or read source files. Reply with exactly the symbol name returned by that tool."
  if [[ $tool == get_document_symbols ]]; then
    prompt="Use the simple_lsp get_document_symbols tool for $document_path and find $expected. Do not use shell commands or read source files. Reply with exactly the symbol name returned by that tool."
	elif [[ $tool == get_diagnostics ]]; then
		prompt="Use the simple_lsp get_diagnostics tool for $document_path. Do not use shell commands or read source files. Reply with the diagnostics returned by that tool."
  fi

  LSP_MCP_SERVERS="$servers" codex exec --ephemeral --skip-git-repo-check --dangerously-bypass-approvals-and-sandbox --json \
    -C "$workspace" \
    -c "mcp_servers.simple_lsp.command=\"$server_bin\"" \
    -c "mcp_servers.simple_lsp.args=[\"--workspace\", \"$workspace\"]" \
    -c "mcp_servers.simple_lsp.env={LSP_MCP_SERVERS='$servers'}" \
    "$prompt" \
    >"$output" </dev/null

  local assertion='
    .type == "item.completed"
    and .item.type == "mcp_tool_call"
    and .item.tool == $tool
    and .item.status == "completed"
    and any(.item.result.structured_content.symbols[]?; .name == $expected)
  '
  if [[ $expected == diagnostics-empty ]]; then
    assertion='
      .type == "item.completed"
      and .item.type == "mcp_tool_call"
      and .item.tool == $tool
      and .item.status == "completed"
      and .item.result.structured_content.meta.complete == true
      and (.item.result.structured_content.diagnostics | length) == 0
    '
  fi
  if ! jq -se --arg tool "$tool" --arg expected "$expected" "any(.[]; $assertion)" "$output" >/dev/null; then
    echo "$name: simple_lsp $tool did not complete with $expected" >&2
    cat "$output" >&2
    exit 1
  fi
  echo "$name: $tool returned $expected"
}

run_case python "$repo_root/testdata/codex-exec/python" python pyright-langserver '["--stdio"]' format_greeting
run_case typescript "$repo_root/testdata/codex-exec/typescript" typescript-javascript typescript-language-server '["--stdio"]' formatGreeting get_document_symbols src/greeting.ts
run_case go "$repo_root/testdata/codex-exec/go" go gopls '[]' FormatGreeting

run_case html "$repo_root/testdata/codex-exec/web" html npx '["--yes", "--package=vscode-langservers-extracted", "vscode-html-language-server", "--stdio"]' main.hero get_document_symbols index.html
run_case css "$repo_root/testdata/codex-exec/web" css npx '["--yes", "--package=vscode-langservers-extracted", "vscode-css-language-server", "--stdio"]' .hero get_document_symbols styles.css
