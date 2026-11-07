#!/usr/bin/env python3
"""Audit Codex JSONL usage; never infer token cost from text lengths."""
import argparse
import json
from collections import Counter
from pathlib import Path

FIELDS = ("input_tokens", "cached_input_tokens", "output_tokens", "reasoning_output_tokens")

def audit(path):
    turns, items, errors = [], {}, []
    for number, line in enumerate(Path(path).read_text(encoding="utf-8").splitlines(), 1):
        if not line.strip():
            continue
        try:
            event = json.loads(line)
            if not isinstance(event, dict):
                raise ValueError("expected JSON object")
        except (ValueError, TypeError) as exc:
            errors.append({"line": number, "error": str(exc)})
            continue
        kind = event.get("type")
        if kind == "turn.completed":
            usage = event.get("usage") or {}
            if not isinstance(usage, dict):
                usage = {}
            values = {k: usage.get(k) if type(usage.get(k)) is int and usage[k] >= 0
                      else None for k in FIELDS}
            total, cached = values["input_tokens"], values["cached_input_tokens"]
            values["uncached_input_tokens"] = (
                total - cached if total is not None and cached is not None
                and cached <= total else None
            )
            turns.append(values)
        if kind in ("item.started", "item.updated", "item.completed", "item.failed"):
            item = event.get("item") or {}
            if not isinstance(item, dict):
                continue
            identity = item.get("id")
            # A single tool call may emit multiple lifecycle events.
            if identity is None:
                continue
            record = items.setdefault(str(identity), {"type": None, "server": None, "events": []})
            record["type"] = item.get("type") or record["type"]
            record["server"] = item.get("server") or record["server"]
            record["events"].append(kind)

    totals = {}
    for field in (*FIELDS, "uncached_input_tokens"):
        data = [turn[field] for turn in turns]
        totals[field] = sum(data) if data and all(v is not None for v in data) else None
    counts = Counter()
    for record in items.values():
        if record["type"] in ("mcp_tool_call", "command_execution"):
            counts[record["type"]] += 1
            if record["type"] == "mcp_tool_call" and record["server"] == "simple-lsp":
                counts["simple_lsp_attempts"] += 1
    return {
        "path": str(path), "turns_with_usage": len(turns),
        "usage_per_turn": turns, "usage_total": totals,
        "tool_attempts": dict(counts), "parse_errors": errors,
        "caveats": [
            "Token totals are Codex-reported; cached tokens are included in input.",
            "Missing usage is unknown (null), not zero.",
            "Call latency needs timestamps; counts do not establish MCP success.",
            "Payload bytes cannot be converted into billed or context tokens.",
        ],
    }

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("traces", nargs="+", type=Path)
    args = parser.parse_args()
    print(json.dumps([audit(path) for path in args.traces], ensure_ascii=False, indent=2))
