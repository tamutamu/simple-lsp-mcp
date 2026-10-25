#!/usr/bin/env python3
"""Run three-repeat Claude Code A/B trials against a pinned FastAPI template.

Only the optional simple-lsp MCP registration differs. The grader and task
prompts are held out from the agent worktrees. Never publish raw agent traces.
"""
from __future__ import annotations
import argparse
import csv
import json
import os
import select
import shutil
import socket
import statistics
import subprocess
import sys
import time
from concurrent.futures import ThreadPoolExecutor, as_completed
from pathlib import Path

REPO = Path(__file__).resolve().parents[2]
TASKS = json.loads((REPO / "docs/benchmarks/fastapi-template-tasks.json").read_text())
GRADER = REPO / "docs/benchmarks/fastapi-template-grader.py"
PINNED = "b1125d8839482cb7bbae603ddc6d0081bf87532e"
POLICY = (
    "# Identical agent guidance in both conditions\n"
    "Select source-reading, text search, editing, and semantic tools only as "
    "needed. No tool is mandatory. Use tests to establish correctness. "
    "Do not modify project instructions or install new dependencies.\n"
)
SUFFIX = (
    "Work only on this pinned official FastAPI template checkout. "
    "No PostgreSQL is available to the agent. Do not edit generated clients. "
    "Run focused tests or syntax checks where practical."
)

def call(argv, cwd=None, **kwargs):
    return subprocess.run(argv, cwd=cwd, capture_output=True, text=True, **kwargs)

def validate_lean_catalog(executable, source):
    """Reject the old 26-tool MCP: treatment must expose exactly 18 tools."""
    proc = subprocess.Popen([str(executable), "--workspace", str(source)],
                            stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                            stderr=subprocess.DEVNULL)
    try:
        for ident, method, params in (
            (1, "initialize", {"protocolVersion": "2025-06-18", "capabilities": {},
                               "clientInfo": {"name": "fastapi-ab-harness", "version": "1"}}),
            (2, "tools/list", {}),
        ):
            proc.stdin.write((json.dumps({"jsonrpc": "2.0", "id": ident,
                                          "method": method, "params": params}) + "\n").encode())
            proc.stdin.flush()
            if ident == 2 and not select.select([proc.stdout], [], [], 10)[0]:
                raise RuntimeError("MCP tools/list timed out")
            response = proc.stdout.readline()
            if not response:
                raise RuntimeError("MCP server exited during protocol handshake")
            obj = json.loads(response)
            if "error" in obj:
                raise RuntimeError("MCP handshake error: " + str(obj["error"]))
            if ident == 1:
                proc.stdin.write(b'{"jsonrpc":"2.0","method":"notifications/initialized"}\n')
                proc.stdin.flush()
            else:
                names = {tool["name"] for tool in obj["result"]["tools"]}
                if len(names) != 18 or "list_workspace_symbols" not in names or "rename_symbol" not in names:
                    raise RuntimeError("Expected the lean 18-tool MCP binary, got: " + ", ".join(sorted(names)))
        print("Lean MCP verified: 18 tools", flush=True)
    finally:
        proc.terminate()
        try: proc.wait(timeout=3)
        except subprocess.TimeoutExpired: proc.kill()


def prepare(source, work):
    if work.exists():
        raise RuntimeError("Refusing to reuse a checkout: " + str(work))
    work.mkdir(parents=True)
    proc = subprocess.Popen(["git", "-C", str(source), "archive", "HEAD"], stdout=subprocess.PIPE)
    subprocess.run(["tar", "-x", "-C", str(work)], stdin=proc.stdout, check=True)
    proc.stdout.close()
    if proc.wait():
        raise RuntimeError("git archive failed")
    for name in (".claude", ".agents", "CLAUDE.md", "AGENTS.md", ".mcp.json"):
        p = work / name
        if p.is_dir():
            shutil.rmtree(p)
        elif p.exists():
            p.unlink()
    (work / "CLAUDE.md").write_text(POLICY)
    for argv in (
        ["git", "init", "-q"], ["git", "add", "-A"],
        ["git", "-c", "user.name=Benchmark", "-c", "user.email=benchmark@example.invalid",
         "commit", "-qm", "pinned baseline"],
    ):
        r = call(argv, cwd=work)
        if r.returncode:
            raise RuntimeError(r.stderr)

def execute_trial(args, case, mode, rep):
    directory = args.output / "runs" / (case + "-" + mode + "-" + str(rep))
    result_file = directory / "result.json"
    if args.resume and result_file.exists():
        return json.loads(result_file.read_text())
    if directory.exists():
        raise RuntimeError("Existing incomplete trial: " + str(directory))
    directory.mkdir(parents=True)
    work = directory / "checkout"
    prepare(args.source, work)
    env = os.environ.copy()
    env["PATH"] = os.pathsep.join([str(args.python.parent), str(args.node_bin),
                                   str(args.lsp_bin), env.get("PATH", "")])
    env["PYTHONPATH"] = str(work / "backend")
    mcp_cfg = directory / "mcp.json"
    if mode == "mcp":
        mcp_cfg.write_text(json.dumps({"mcpServers": {"simple-lsp": {
            "command": str(args.mcp),
            "args": ["--workspace", str(work)],
            "env": {"PATH": os.pathsep.join([str(args.node_bin), str(args.lsp_bin),
                                             "/usr/local/bin:/usr/bin:/bin"]),
                    "SIMPLE_LSP_TSSERVER_PATH": args.tsserver},
        }}}))
    argv = [args.claude, "-p", TASKS[case] + " " + SUFFIX,
            "--model", args.model, "--effort", "medium",
            "--max-turns", "24", "--max-budget-usd", str(args.budget),
            "--no-session-persistence", "--strict-mcp-config",
            "--disable-slash-commands", "--output-format", "stream-json",
            "--verbose", "--permission-mode", "dontAsk",
            "--allowedTools", "Read", "Edit", "Write", "Glob", "Grep",
            "Bash", "ToolSearch", "mcp__simple-lsp__*",
            "--disallowedTools", "LSP"]
    if mode == "mcp":
        argv += ["--mcp-config", str(mcp_cfg)]
    start = time.monotonic()
    try:
        process = call(argv, cwd=work, env=env, timeout=args.timeout)
        stdout, stderr, exitcode = process.stdout, process.stderr, process.returncode
    except subprocess.TimeoutExpired as ex:
        stdout = ex.stdout or b""
        stderr = ex.stderr or b""
        if isinstance(stdout, bytes): stdout = stdout.decode(errors="replace")
        if isinstance(stderr, bytes): stderr = stderr.decode(errors="replace")
        exitcode = 124
    elapsed = round(time.monotonic() - start, 2)
    (directory / "trace.jsonl").write_text(stdout)
    (directory / "error.log").write_text(stderr)
    events = []
    for line in stdout.splitlines():
        try: events.append(json.loads(line))
        except (ValueError, TypeError): pass
    final = next((e for e in reversed(events) if e.get("type") == "result"), {})
    tools = [part.get("name") for e in events if e.get("type") == "assistant"
             for part in e.get("message", {}).get("content", [])
             if isinstance(part, dict) and part.get("type") == "tool_use"]
    row = {
        "case": case, "mode": mode, "repeat": rep, "seconds": elapsed,
        "exit_code": exitcode, "completed": bool(final) and exitcode == 0
                     and not final.get("is_error"),
        "cost_usd": final.get("total_cost_usd"), "turns": final.get("num_turns"),
        "tool_calls": len(tools),
        "mcp_calls": sum(str(tool).startswith("mcp__simple-lsp__") for tool in tools),
        "native_lsp_calls": tools.count("LSP"),
        "graded": False, "accepted": False, "grader": None,
        "changed_files": call(["git", "diff", "--name-only"], cwd=work).stdout.splitlines(),
        "worktree": str(work),
    }
    result_file.write_text(json.dumps(row, indent=2, ensure_ascii=False) + "\n")
    print("AGENT", case, mode, rep, "completed", row["completed"],
          "seconds", elapsed, "mcp_calls", row["mcp_calls"], flush=True)
    return row

def score(args, rows):
    for row in rows:
        if row["graded"]:
            continue
        if row["case"] == "summary" and not args.postgres_url:
            row["grader"] = {"error": "PostgreSQL required; not graded"}
            continue
        work = Path(row["worktree"])
        env = os.environ.copy()
        if row["case"] == "summary":
            env["BENCH_POSTGRES_URL"] = args.postgres_url
            env["BENCH_ALLOW_DISPOSABLE_RESET"] = "YES"
        proc = call([str(args.python), str(GRADER), str(work), row["case"]],
                    cwd=work, env=env, timeout=60)
        try: result = json.loads(proc.stdout.strip().splitlines()[-1])
        except (ValueError, IndexError):
            result = {"success": False, "error": (proc.stdout + proc.stderr)[-600:]}
        syntax = call([str(args.python), "-I", "-m", "compileall", "-q",
                       str(work / "backend/app")], cwd=work, timeout=45).returncode == 0
        clean = call(["git", "diff", "--check"], cwd=work).returncode == 0
        row["graded"] = True
        row["grader"] = result
        row["accepted"] = bool(row["completed"] and result.get("success") and syntax and clean)
        (work.parent / "result.json").write_text(json.dumps(row, indent=2) + "\n")
        print("GRADE", row["case"], row["mode"], row["repeat"], row["accepted"], flush=True)

def report(args, rows):
    path = args.output / "results.csv"
    keys = ("case", "mode", "repeat", "seconds", "exit_code", "completed",
            "cost_usd", "turns", "tool_calls", "mcp_calls", "native_lsp_calls",
            "graded", "accepted")
    with path.open("w", newline="") as f:
        writer = csv.DictWriter(f, keys)
        writer.writeheader()
        for row in sorted(rows, key=lambda r: (r["case"], r["repeat"], r["mode"])):
            writer.writerow({k: row.get(k) for k in keys})
    print("CSV:", path, flush=True)
    print("All graded:", all(row["graded"] for row in rows), flush=True)
    for case in args.cases:
        groups = {mode: [r for r in rows if r["case"] == case and r["mode"] == mode]
                  for mode in ("baseline", "mcp")}
        for mode, group in groups.items():
            t = statistics.mean(r["seconds"] for r in group) if group else 0
            cost = [r["cost_usd"] for r in group if r["cost_usd"] is not None]
            print("SUMMARY", case, mode, "pass", sum(r["accepted"] for r in group),
                  "/", len(group), "mean_seconds", round(t, 2),
                  "mean_cost", round(statistics.mean(cost), 4) if len(cost) == len(group) else None)

def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--source", required=True, type=Path)
    ap.add_argument("--mcp", required=True, type=Path)
    ap.add_argument("--python", required=True, type=Path)
    ap.add_argument("--output", required=True, type=Path)
    ap.add_argument("--claude", default="claude")
    ap.add_argument("--model", default="sonnet")
    ap.add_argument("--node-bin", type=Path, default=Path("/usr/bin"))
    ap.add_argument("--lsp-bin", type=Path, default=Path("/usr/local/bin"))
    ap.add_argument("--tsserver", default="")
    ap.add_argument("--cases", nargs="+", default=list(TASKS), choices=list(TASKS))
    ap.add_argument("--repeats", type=int, default=3)
    ap.add_argument("--parallel", type=int, default=2)
    ap.add_argument("--timeout", type=int, default=360)
    ap.add_argument("--budget", type=float, default=0.85)
    ap.add_argument("--postgres-url", default="")
    ap.add_argument("--plan", action="store_true")
    ap.add_argument("--resume", action="store_true")
    ap.add_argument("--skip-network-check", action="store_true")
    args = ap.parse_args()
    for k in ("source", "mcp", "python", "output"):
        setattr(args, k, getattr(args, k).resolve())
    sha = call(["git", "rev-parse", "HEAD"], cwd=args.source, check=True).stdout.strip()
    if sha != PINNED:
        ap.error("Template must be pinned to " + PINNED + " (found " + sha + ")")
    if not args.mcp.is_file() or not args.python.is_file():
        ap.error("MCP executable or Python environment not found")
    if args.repeats < 1 or args.parallel < 1:
        ap.error("Invalid repeats/parallel")
    if args.postgres_url and not ("@localhost:" in args.postgres_url or "@127.0.0.1:" in args.postgres_url):
        ap.error("PostgreSQL must be disposable localhost instance; grader resets the bench schema")
    names = [(case, mode, rep) for case in args.cases
             for rep in range(1, args.repeats + 1)
             for mode in (("baseline", "mcp") if rep % 2 else ("mcp", "baseline"))]
    print("Pinned", sha, "tasks", len(args.cases), "repeats", args.repeats,
          "trials", len(names), "output", args.output, flush=True)
    if args.plan: return
    if "summary" in args.cases and not args.postgres_url:
        ap.error("Summary requires --postgres-url for a disposable local PostgreSQL bench database")
    if not shutil.which(args.claude):
        ap.error("Claude CLI not installed")
    validate_lean_catalog(args.mcp, args.source)
    if not args.skip_network_check:
        try: socket.getaddrinfo("api.anthropic.com", 443)
        except OSError as ex: ap.error("Claude API endpoint unreachable (DNS): " + str(ex))
    args.output.mkdir(parents=True, exist_ok=True)
    with ThreadPoolExecutor(max_workers=args.parallel) as pool:
        pending = [pool.submit(execute_trial, args, *name) for name in names]
        rows = [future.result() for future in as_completed(pending)]
    score(args, rows)
    report(args, rows)

if __name__ == "__main__":
    main()
