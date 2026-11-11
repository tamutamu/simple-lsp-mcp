#!/usr/bin/env python3
"""Fail-closed audit of Codex app-server A/B trial logs.

Do not count a passing source-tree grader as an accepted agent run unless the
agent turn itself completed successfully. Preserve aborted/quota-limited arms.
"""
from __future__ import annotations

import argparse
from collections import Counter, defaultdict
import json
from pathlib import Path


def useful_completed_mcp_call(item: dict) -> bool:
    """Reject transport successes that contain no usable semantic result."""
    if item.get("status") != "completed":
        return False
    result = item.get("result")
    if not isinstance(result, dict) or result.get("isError"):
        return False
    content = result.get("content")
    if not isinstance(content, list):
        return False
    for part in content:
        if not isinstance(part, dict):
            continue
        payload = part.get("text")
        if not isinstance(payload, str) or not payload.strip():
            continue
        if payload.strip() in ("{}", "[]", "null", ""):  # no usable evidence
            continue
        try:
            data = json.loads(payload)
        except json.JSONDecodeError:
            return True  # LSP Markdown/source is directly usable
        if isinstance(data, dict):
            if data.get("error") or data.get("isError"):
                continue
            meta=data.get("meta")
            # A syntactically successful call is not useful evidence when
            # the LSP explicitly reports that it has not checked the scope.
            if isinstance(meta, dict) and meta.get("complete") is False:
                continue
            if isinstance(meta, dict) and meta.get("truncated") is True:
                continue
            if set(data) == {"meta"}:
                continue
        if data in (None, {}, []):
            continue
        return True
    return False


def audit_trial(base: Path, trial: dict) -> dict:
    task, repetition, arm = trial["id"], trial["rep"], trial["arm"]
    paths = list(base.glob(f"{task}-*-rep{repetition}/{arm}-trace.jsonl"))
    if len(paths) != 1:
        return {**trial, "valid": False, "reason": f"expected one trace, found {len(paths)}", "actual_mcp_calls": 0}
    events = []
    try:
        for line in paths[0].read_text(encoding="utf-8").splitlines():
            events.append(json.loads(line)["event"])
    except (ValueError, KeyError) as exc:
        return {**trial, "valid": False, "reason": f"invalid trace: {exc}", "actual_mcp_calls": 0}
    turns = [(e.get("params") or {}).get("turn") or {} for e in events if e.get("method") == "turn/completed"]
    statuses = [t.get("status") for t in turns]
    calls = [((e.get("params") or {}).get("item") or {}) for e in events
             if e.get("method") == "item/completed"
             and ((e.get("params") or {}).get("item") or {}).get("type") == "mcpToolCall"]
    completed_calls = sum(useful_completed_mcp_call(i) for i in calls)
    rate_limited = any((e.get("params") or {}).get("error", {}).get("codexErrorInfo") == "usageLimitExceeded"
                       for e in events if e.get("method") == "error")
    valid = statuses == ["completed"] and not rate_limited
    reason = "accepted" if valid else "quota_exceeded" if rate_limited else f"turn_status={statuses}"
    count_match = trial.get("mcp_calls") == len(calls)
    valid = valid and count_match
    if not count_match:
        reason = "mcp_call_count_mismatch"
    return {**trial, "valid": valid, "reason": reason,
            "actual_mcp_calls": len(calls), "successful_mcp_calls": completed_calls,
            "turn_status": statuses, "trace": str(paths[0].relative_to(base))}


def audit(base: Path) -> dict:
    summary = json.loads((base / "summary.json").read_text(encoding="utf-8"))
    out = [audit_trial(base, row) for row in summary]
    counts = Counter(x["reason"] for x in out if not x["valid"])
    grouped = defaultdict(dict)
    for row in out:
        key = (row["id"], row["rep"])
        if row["arm"] in grouped[key]:
            raise ValueError(f"duplicate trial: {key}, {row['arm']}")
        grouped[key][row["arm"]] = row
    paired = []
    for (task, rep), arms in sorted(grouped.items()):
        if not {"baseline", "mcp"} <= arms.keys():
            continue
        left, right = arms["baseline"], arms["mcp"]
        if left["valid"] and right["valid"] and left["passed"] and right["passed"]:
            paired.append({"id": task, "rep": rep, "baseline_seconds": left["seconds"],
                           "mcp_seconds": right["seconds"], "mcp_calls": right["actual_mcp_calls"],
                           "successful_mcp_calls": right["successful_mcp_calls"]})
    return {"trials": len(out), "valid_turns": sum(x["valid"] for x in out),
            "verified_agent_and_grader_pass": sum(x["valid"] and x["passed"] for x in out),
            "invalid_reasons": dict(counts), "complete_passing_pairs": len(paired),
            "pairs_with_actual_mcp_use": sum(x["successful_mcp_calls"] > 0 for x in paired),
            "pairs": paired, "details": out}


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("trial_directory", type=Path)
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    report = audit(args.trial_directory)
    content = json.dumps(report, indent=2, ensure_ascii=False) + "\n"
    if args.output:
        args.output.write_text(content, encoding="utf-8")
    else:
        print(content)


if __name__ == "__main__":
    main()
