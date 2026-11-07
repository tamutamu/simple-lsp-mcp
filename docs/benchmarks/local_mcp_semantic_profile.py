#!/usr/bin/env python3
"""Measure real MCP semantic lookup and a literal rg lookup on one workspace.

The rg query is deliberately literal and NOT a semantic-equivalent oracle.
Reports latency, response size and semantic LSP locations independently.
"""
import argparse
import json
import os
import select
import statistics
import subprocess
import time
from pathlib import Path

class MCPClient:
    def __init__(self, binary, workspace):
        self.i = 0
        self.proc = subprocess.Popen(
            [str(binary), "--workspace", str(workspace)],
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            env=os.environ.copy())
        self.request("initialize", {
            "protocolVersion": "2025-06-18", "capabilities": {},
            "clientInfo": {"name": "semantic-mcp-profiler", "version": "1"}
        }, timeout=90)
        self.proc.stdin.write(b'{"jsonrpc":"2.0","method":"notifications/initialized"}\n')
        self.proc.stdin.flush()

    def request(self, method, params, timeout=120):
        self.i += 1
        payload = json.dumps({"jsonrpc": "2.0", "id": self.i, "method": method, "params": params})
        start = time.perf_counter()
        self.proc.stdin.write((payload + "\n").encode())
        self.proc.stdin.flush()
        while time.perf_counter() - start < timeout:
            if not select.select([self.proc.stdout], [], [], 1.0)[0]:
                continue
            line = self.proc.stdout.readline()
            if not line:
                raise RuntimeError("MCP server exited without a response")
            result = json.loads(line)
            if result.get("id") != self.i:
                continue
            duration_ms = (time.perf_counter() - start) * 1000
            if result.get("error"):
                raise RuntimeError(str(result["error"]))
            value = result.get("result", {})
            if value.get("isError") or value.get("is_error"):
                raise RuntimeError(str(value.get("content"))[:800])
            return value, duration_ms
        raise TimeoutError(f"{method} exceeded {timeout}s")

    def tool(self, name, params):
        result, duration = self.request("tools/call", {"name": name, "arguments": params})
        semantic = result.get("structuredContent") or result.get("structured_content") or {}
        return semantic, duration, len(json.dumps(result, ensure_ascii=False).encode())

    def close(self):
        self.proc.terminate()
        try:
            self.proc.wait(timeout=3)
        except subprocess.TimeoutExpired:
            self.proc.kill()


def main():
    a = argparse.ArgumentParser(description=__doc__)
    a.add_argument("--binary", type=Path, required=True)
    a.add_argument("--workspace", type=Path, required=True)
    a.add_argument("--path", required=True)
    a.add_argument("--symbol", required=True)
    a.add_argument("--repeats", type=int, default=6)
    args = a.parse_args()
    if args.repeats < 2:
        a.error("--repeats must be >=2")
    ws = args.workspace.resolve()
    client = MCPClient(args.binary.resolve(), ws)
    try:
        rows = []
        for tool, params in [
            ("get_document_symbols", {"path": args.path}),
            ("inspect_symbol", {"path": args.path, "symbol_path": args.symbol, "view": "source"}),
            ("find_references", {"path": args.path, "symbol_path": args.symbol,
                                 "include_declaration": True, "relation_view": "locations"}),
        ]:
            for i in range(args.repeats):
                response, elapsed, size = client.tool(tool, params)
                rows.append(dict(tool=tool, repeat=i + 1, ms=round(elapsed, 3),
                                 bytes=size, locations=len(response.get("locations", [])),
                                 meta=response.get("meta")))
            current = [x for x in rows if x["tool"] == tool]
            print(json.dumps({
                "tool": tool, "cold_ms": current[0]["ms"],
                "warm_median_ms": round(statistics.median(x["ms"] for x in current[1:]), 3),
                "response_bytes": current[-1]["bytes"],
                "locations": current[-1]["locations"],
                "meta": current[-1]["meta"],
            }, ensure_ascii=False), flush=True)
        rg = []
        for _ in range(args.repeats):
            t = time.perf_counter()
            run = subprocess.run(["rg", "-n", "-F", args.symbol, "-g", "*.py", "-g", "*.ts",
                                  "-g", "*.tsx", "."], cwd=ws, capture_output=True, text=True)
            rg.append(dict(ms=round((time.perf_counter() - t) * 1000, 3),
                           matches=len(run.stdout.splitlines()), bytes=len(run.stdout.encode()),
                           exit_code=run.returncode))
        print(json.dumps({"literal_rg": {
            "median_ms": round(statistics.median(x["ms"] for x in rg), 3),
            "matches": rg[0]["matches"], "response_bytes": rg[0]["bytes"],
            "note": "literal matches, not semantic references"
        }}), flush=True)
    finally:
        client.close()


if __name__ == "__main__":
    main()
