#!/usr/bin/env python3
"""Fail-fast authenticated Codex model transport check for agent A/B runners.

Run this once on the *host* before spending time preparing paired trials.
The isolated coding trials still use their original network-disabled sandbox.
A completed model reply with token usage is required: app-server init alone is
not sufficient to detect a sandbox-level outbound network denial.
"""
from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import shutil
import sys

from run_codex_mcp_discovery_smoke import AppServer


def preflight(codex: str, codex_home: Path, workspace: Path, timeout: int) -> dict:
    env = os.environ.copy()
    env["CODEX_HOME"] = str(codex_home.resolve())
    command = [codex, "app-server", "--stdio"]
    app = AppServer(command, workspace, timeout, env)
    try:
        app.request("initialize", {"clientInfo": {
            "name": "simple-lsp-agent-ab-preflight", "version": "1",
        }})
        app.notify("initialized")
        thread = app.request("thread/start", {
            "cwd": str(workspace.resolve()), "ephemeral": True,
            "model": "gpt-5.6-sol", "approvalPolicy": "never",
            "sandbox": "read-only",
        })["thread"]["id"]
        turn = app.request("turn/start", {
            "threadId": thread,
            "input": [{"type": "text", "text": "Reply with only OK."}],
            "effort": "low",
        })["turn"]["id"]
        usage = None
        final_status = None
        failure = None
        for _ in range(2000):
            event = app._read()
            method = event.get("method")
            params = event.get("params") or {}
            if method == "thread/tokenUsage/updated":
                usage = (params.get("tokenUsage") or {}).get("total")
            if method == "turn/completed" and (params.get("turn") or {}).get("id") == turn:
                result = params["turn"]
                final_status = result.get("status")
                failure = result.get("error")
                break
        if final_status != "completed":
            return {"connected": False, "reason": "model turn failed",
                    "error": str(failure)[:500]}
        if not usage or int(usage.get("inputTokens") or 0) <= 0:
            return {"connected": False, "reason": "model reply has no billed input token evidence"}
        return {"connected": True, "model": "gpt-5.6-sol", "has_input_token_evidence": True}
    finally:
        app.close()


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--codex", default=shutil.which("codex") or "")
    parser.add_argument("--codex-home", type=Path, required=True)
    parser.add_argument("--workspace", type=Path, default=Path.cwd())
    parser.add_argument("--timeout", type=int, default=80)
    args = parser.parse_args()
    if not args.codex or not args.codex_home.is_dir():
        print(json.dumps({"connected": False, "reason": "Codex executable or authenticated CODEX_HOME missing"}))
        return 2
    try:
        result = preflight(args.codex, args.codex_home, args.workspace, args.timeout)
    except (TimeoutError, OSError, RuntimeError, KeyError, ValueError) as exc:
        result = {"connected": False, "reason": type(exc).__name__,
                  "error": str(exc)[:500]}
    print(json.dumps(result,ensure_ascii=False))
    return 0 if result["connected"] else 2


if __name__=="__main__":
    sys.exit(main())
