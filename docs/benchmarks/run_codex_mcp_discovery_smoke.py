#!/usr/bin/env python3
"""Verify simple-lsp advertisement and invocation through Codex app-server.

This is deliberately not an agent evaluation: it checks the connector boundary
without asking a model to select a tool. The task is still realistic code
navigation: inspect a known Go file and return its NewWithOptions declaration.
"""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import select
import shutil
import subprocess
import sys
import tempfile
import time
from typing import Any


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--workspace", type=Path, default=Path.cwd())
    parser.add_argument("--codex", default=shutil.which("codex"))
    parser.add_argument("--mcp", default=shutil.which("simple-lsp-mcp"))
    parser.add_argument("--timeout", type=float, default=60.0)
    parser.add_argument("--result-format", choices=("both", "text", "readable"), default="readable")
    return parser.parse_args()


class AppServer:
    def __init__(
        self, command: list[str], cwd: Path, timeout: float, env: dict[str, str]
    ) -> None:
        self.deadline = time.monotonic() + timeout
        self.next_id = 0
        self.stderr = ""
        self.process = subprocess.Popen(
            command,
            cwd=cwd,
            env=env,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            bufsize=1,
        )

    def close(self) -> None:
        self.process.terminate()
        try:
            _, self.stderr = self.process.communicate(timeout=3)
        except subprocess.TimeoutExpired:
            self.process.kill()
            _, self.stderr = self.process.communicate()

    def notify(self, method: str, params: dict[str, Any] | None = None) -> None:
        packet: dict[str, Any] = {"jsonrpc": "2.0", "method": method}
        if params is not None:
            packet["params"] = params
        self._write(packet)

    def request(self, method: str, params: dict[str, Any] | None = None) -> Any:
        self.next_id += 1
        request_id = self.next_id
        packet: dict[str, Any] = {
            "jsonrpc": "2.0",
            "id": request_id,
            "method": method,
        }
        if params is not None:
            packet["params"] = params
        self._write(packet)
        while True:
            event = self._read()
            if event.get("id") != request_id:
                continue
            if "error" in event:
                raise RuntimeError(f"{method} failed: {event['error']}")
            return event.get("result")

    def _write(self, packet: dict[str, Any]) -> None:
        assert self.process.stdin is not None
        self.process.stdin.write(json.dumps(packet) + "\n")
        self.process.stdin.flush()

    def _read(self) -> dict[str, Any]:
        assert self.process.stdout is not None
        remaining = self.deadline - time.monotonic()
        if remaining <= 0:
            raise TimeoutError("Codex app-server smoke timed out")
        ready, _, _ = select.select([self.process.stdout], [], [], remaining)
        if not ready:
            raise TimeoutError("Codex app-server smoke timed out")
        line = self.process.stdout.readline()
        if not line:
            raise RuntimeError(
                f"Codex app-server exited with {self.process.poll()}"
            )
        return json.loads(line)


def main() -> int:
    args = parse_args()
    workspace = args.workspace.resolve()
    if not args.codex:
        raise SystemExit("codex executable not found; pass --codex")
    if not args.mcp:
        raise SystemExit("simple-lsp-mcp executable not found; pass --mcp")

    command = [
        args.codex,
        "app-server",
        "--stdio",
        "-c",
        f'mcp_servers.simple-lsp.command="{Path(args.mcp).resolve()}"',
        "-c",
        (
            "mcp_servers.simple-lsp.args="
            f'["--workspace","{workspace}","--tool-profile","full",'
            f'"--result-format","{args.result_format}"]'
        ),
    ]
    started = time.monotonic()
    with tempfile.TemporaryDirectory(prefix="simple-lsp-codex-home-") as codex_home:
        env = os.environ.copy()
        env["CODEX_HOME"] = codex_home
        app = AppServer(command, workspace, args.timeout, env)
        try:
            app.request(
                "initialize",
                {
                    "clientInfo": {
                        "name": "simple-lsp-discovery-smoke",
                        "title": "simple-lsp discovery smoke",
                        "version": "1.0",
                    }
                },
            )
            app.notify("initialized")
            thread = app.request(
                "thread/start",
                {
                    "cwd": str(workspace),
                    "ephemeral": True,
                    "approvalPolicy": "never",
                    "sandbox": "read-only",
                },
            )["thread"]["id"]

            inventory = app.request(
                "mcpServerStatus/list",
                {"threadId": thread, "detail": "full"},
            )
            server = next(
                (
                    item
                    for item in inventory["data"]
                    if item["name"] == "simple-lsp"
                ),
                None,
            )
            if server is None:
                raise AssertionError("simple-lsp missing from app-server inventory")
            server_info = server.get("serverInfo") or {}
            if not (server_info.get("title") or "").strip():
                raise AssertionError("simple-lsp server has no human-readable title")
            tools = server["tools"]
            if len(tools) < 20:
                raise AssertionError(f"public MCP tool catalog unexpectedly shrank: {len(tools)}")
            if "get_document_symbols" not in tools:
                raise AssertionError(
                    f"get_document_symbols not advertised; tools={sorted(tools)}"
                )
            description = tools["get_document_symbols"].get("description") or ""
            title = tools["get_document_symbols"].get("title") or ""
            if not title.strip():
                raise AssertionError("get_document_symbols has no human-readable title")
            if not description.strip():
                raise AssertionError("get_document_symbols has no discovery description")

            result = app.request(
                "mcpServer/tool/call",
                {
                    "threadId": thread,
                    "server": "simple-lsp",
                    "tool": "get_document_symbols",
                    "arguments": {
                        "path": "internal/mcpserver/server.go",
                        "include_source": True,
                    },
                },
            )
            rendered = json.dumps(result, ensure_ascii=False)
            if result.get("isError"):
                raise AssertionError(
                    f"get_document_symbols returned an MCP error: {rendered}"
                )
            if (
                "NewWithOptions" not in rendered
                or "internal/mcpserver/server.go" not in rendered
            ):
                raise AssertionError(
                    "get_document_symbols did not return NewWithOptions: "
                    + rendered[:1200]
                )

            if args.result_format == "readable":
                inspected = app.request(
                    "mcpServer/tool/call",
                    {
                        "threadId": thread,
                        "server": "simple-lsp",
                        "tool": "inspect_symbol",
                        "arguments": {
                            "path": "internal/mcpserver/server.go",
                            "symbol_path": "NewWithOptions",
                            "view": "source",
                        },
                    },
                )
                if inspected.get("isError") or inspected.get("structuredContent"):
                    raise AssertionError("readable inspect unexpectedly errored or duplicated structured content")
                content = inspected.get("content") or []
                readable = content[0].get("text", "") if content else ""
                if "func NewWithOptions(" not in readable or "```go" not in readable:
                    raise AssertionError(f"inspect_symbol did not return unescaped Go source: {readable[:800]}")

            print(
                json.dumps(
                    {
                        "ok": True,
                        "server": server_info,
                        "advertised_tool_count": len(tools),
                        "result_format": args.result_format,
                        "advertised_tool": "get_document_symbols",
                        "tool_title": title,
                        "description": description,
                        "task": "inspect server.go and return NewWithOptions",
                        "result_contains": [
                            "NewWithOptions",
                            "internal/mcpserver/server.go",
                        ],
                        "elapsed_seconds": round(time.monotonic() - started, 3),
                    },
                    indent=2,
                    ensure_ascii=False,
                )
            )
            return 0
        finally:
            app.close()
            if app.stderr:
                print(app.stderr, file=sys.stderr, end="")


if __name__ == "__main__":
    raise SystemExit(main())
