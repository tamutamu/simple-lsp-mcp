#!/usr/bin/env python3
"""Opt-in paired task evaluation. Runs external agent commands only with --execute.

Usage:
  python3 scripts/evaluate-agent-tasks.py --manifest testdata/evaluation/tasks.json \
    --baseline '["agent", "--prompt", "{prompt}"]' \
    --mcp '["agent", "--mcp", "{server}", "--prompt", "{prompt}"]' \
    --server /path/to/simple-lsp-mcp --execute

Arguments are JSON argv arrays: never passed through a shell. No network/API
calls are made by this harness itself, but the supplied agent commands may.
"""

import argparse
import json
import random
import shutil
import subprocess
import sys
import tempfile
import time
from pathlib import Path


def argv_argument(value):
    argv = json.loads(value)
    if not isinstance(argv, list) or not argv or not all(isinstance(x, str) and x for x in argv):
        raise ValueError("agent command must be a non-empty JSON array of non-empty strings")
    return argv


def read_tasks(manifest):
    data = json.loads(manifest.read_text(encoding="utf-8"))
    tasks = data.get("tasks")
    if not isinstance(tasks, list) or not tasks:
        raise ValueError("manifest must contain a non-empty tasks array")
    ids = set()
    for task in tasks:
        if not isinstance(task, dict) or not all(task.get(key) for key in ("id", "workspace", "prompt")):
            raise ValueError("each task needs id, workspace and prompt")
        if task["id"] in ids:
            raise ValueError("duplicate task id: " + str(task["id"]))
        ids.add(task["id"])
        task["check"] = task.get("check", [])
        if not isinstance(task["check"], list) or not task["check"] or not all(
            isinstance(value, str) and value for value in task["check"]
        ):
            raise ValueError("task check must be a non-empty argv list")
        src = (manifest.parent / task["workspace"]).resolve()
        if not src.is_dir():
            raise ValueError("task workspace not found: " + str(src))
        task["source"] = src
        overlays = task.get("oracle_files")
        if not isinstance(overlays, list) or not overlays:
            raise ValueError("each task requires oracle_files so the agent cannot simply modify visible tests")
        validated = []
        for item in overlays:
            if not isinstance(item, dict) or not isinstance(item.get("source"), str) or not isinstance(item.get("dest"), str):
                raise ValueError("oracle_files entries require source and dest strings")
            oracle = (manifest.parent / item["source"]).resolve()
            destination = Path(item["dest"])
            if not oracle.is_file() or destination.is_absolute() or ".." in destination.parts or destination == Path("."):
                raise ValueError("invalid oracle file mapping: " + repr(item))
            if (src / destination).exists():
                raise ValueError("oracle would overwrite a visible task file: " + str(destination))
            validated.append((oracle, destination))
        task["overlays"] = validated
    return tasks


def expand(argv, workspace, prompt, server):
    return [arg.replace("{workspace}", str(workspace)).replace("{prompt}", prompt).replace("{server}", server)
            for arg in argv]


def command(argv, cwd, timeout, log_path=None):
    start = time.monotonic()
    # Logs stay in temporary directories by default. Never echo agent output or
    # source code to stdout; the public summary only contains status metadata.
    if log_path is None:
        raise ValueError("a per-trial log path is required")
    with log_path.open("wb") as log:
        try:
            result = subprocess.run(argv, cwd=cwd, stdin=subprocess.DEVNULL, stdout=log, stderr=subprocess.STDOUT,
                                    timeout=timeout, check=False)
            return result.returncode, round(time.monotonic() - start, 3), False
        except subprocess.TimeoutExpired:
            return None, round(time.monotonic() - start, 3), True
        except OSError:
            return None, round(time.monotonic() - start, 3), False


def apply_oracle(task, workspace):
    for source, relative_path in task["overlays"]:
        destination = workspace / relative_path
        # Reject symlinked parents/files: an untrusted agent must not redirect
        # the hidden oracle write outside the disposable evaluation workspace.
        current = workspace
        for part in relative_path.parts:
            current = current / part
            if current.is_symlink():
                raise RuntimeError("oracle destination is a symlink: " + str(relative_path))
        if destination.exists():
            raise RuntimeError("agent-created file conflicts with hidden oracle: " + str(relative_path))
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, destination)


def run_trial(task, variant, agent_argv, server, work_root, agent_timeout, check_timeout):
    workspace = work_root / "workspace"
    shutil.copytree(task["source"], workspace, ignore=shutil.ignore_patterns(".git", "node_modules", ".venv", "dist"))
    # The agent never sees the hidden acceptance tests. Validate the initial
    # fixture in an independent copy before starting any agent process.
    preflight = work_root / "preflight"
    shutil.copytree(task["source"], preflight, ignore=shutil.ignore_patterns(".git", "node_modules", ".venv", "dist"))
    apply_oracle(task, preflight)
    baseline_exit, _, baseline_timeout = command(task["check"], preflight, check_timeout, work_root / "before.log")
    shutil.rmtree(preflight)
    if baseline_timeout or baseline_exit is None:
        raise RuntimeError("acceptance test could not run before agent: " + task["id"])
    if baseline_exit == 0:
        raise RuntimeError("fixture already passes acceptance test: " + task["id"])
    agent_exit, elapsed, timed_out = command(expand(agent_argv, workspace, task["prompt"], server), workspace,
                                             agent_timeout, work_root / "agent.log")
    apply_oracle(task, workspace)
    check_exit, check_elapsed, check_timeout_hit = command(task["check"], workspace, check_timeout,
                                                           work_root / "after.log")
    return {"task": task["id"], "variant": variant, "agent_exit": agent_exit, "agent_timeout": timed_out,
            "agent_seconds": elapsed, "acceptance_exit": check_exit, "acceptance_timeout": check_timeout_hit,
            "acceptance_seconds": check_elapsed,
            "passed": agent_exit == 0 and not timed_out and check_exit == 0 and not check_timeout_hit}


def evaluate(tasks, baseline, mcp, server, repeats, seed, agent_timeout, check_timeout, keep_logs):
    rng = random.Random(seed)
    results = []
    with tempfile.TemporaryDirectory(prefix="simple-lsp-eval-") as temp:
        root = Path(temp)
        for task in tasks:
            for repeat in range(repeats):
                order = [("baseline", baseline), ("mcp", mcp)]
                rng.shuffle(order)
                for variant, argv in order:
                    trial_root = root / (task["id"] + "-" + str(repeat + 1) + "-" + variant)
                    trial_root.mkdir()
                    result = run_trial(task, variant, argv, server, trial_root, agent_timeout, check_timeout)
                    result["repeat"] = repeat + 1
                    results.append(result)
                    if keep_logs:
                        dest = Path(keep_logs) / task["id"] / (str(repeat + 1) + "-" + variant)
                        if dest.exists():
                            raise ValueError("log output already exists: " + str(dest))
                        dest.parent.mkdir(parents=True, exist_ok=True)
                        shutil.copytree(trial_root, dest)
    totals = {variant: {"attempts": 0, "passed": 0} for variant in ("baseline", "mcp")}
    for result in results:
        totals[result["variant"]]["attempts"] += 1
        totals[result["variant"]]["passed"] += int(result["passed"])
    return {"seed": seed, "repeats": repeats, "results": results, "summary": totals,
            "note": "Only acceptance-test outcomes; model usage and causal benefit are not inferred. No agent calls unless --execute."}


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--manifest", required=True, type=Path)
    parser.add_argument("--baseline", required=True, type=argv_argument, help="baseline agent command as JSON argv")
    parser.add_argument("--mcp", required=True, type=argv_argument, help="MCP-enabled agent command as JSON argv")
    parser.add_argument("--server", default="", help="absolute path to MCP binary for {server} placeholder")
    parser.add_argument("--repeats", type=int, default=3)
    parser.add_argument("--seed", type=int, default=42)
    parser.add_argument("--agent-timeout", type=int, default=600)
    parser.add_argument("--check-timeout", type=int, default=120)
    parser.add_argument("--execute", action="store_true", help="opt in to agent/API calls and acceptance command execution")
    parser.add_argument("--keep-logs", type=Path, help="sensitive: retain agent logs and modified source in this directory")
    parser.add_argument("--output", type=Path, help="save non-source summary JSON")
    args = parser.parse_args(argv)
    try:
        if args.repeats < 1 or args.repeats > 100 or args.agent_timeout < 1 or args.check_timeout < 1:
            parser.error("repeats must be 1-100; timeouts must be positive")
        tasks = read_tasks(args.manifest.resolve())
        if args.execute and args.baseline == args.mcp:
            parser.error("baseline and MCP commands are identical; configure a separate MCP-enabled agent command")
        if not args.execute:
            print(json.dumps({"status": "dry_run", "tasks": [x["id"] for x in tasks], "repeats": args.repeats,
                              "baseline_executable": args.baseline[0], "mcp_executable": args.mcp[0],
                              "note": "No agents or acceptance checks were executed; rerun with --execute to opt in."}, indent=2))
            return 0
        report = evaluate(tasks, args.baseline, args.mcp, args.server, args.repeats, args.seed,
                          args.agent_timeout, args.check_timeout, args.keep_logs)
        text = json.dumps(report, indent=2) + "\n"
        if args.output:
            if args.output.exists():
                parser.error("refusing to overwrite existing output file")
            args.output.write_text(text, encoding="utf-8")
        print(text, end="")
        return 0
    except (ValueError, OSError, RuntimeError) as error:
        print("evaluation failed: " + str(error), file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
