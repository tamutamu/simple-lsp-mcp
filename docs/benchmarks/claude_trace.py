"""Extract auditable Claude Code stream-json A/B evidence.

Only a completed, non-error MCP tool result counts as actual usage. Claude's
modelUsage is preferred over final usage because it includes per-model cache
read/write tokens, which are part of total prompt input.
"""
from __future__ import annotations

import json


def _nonnegative(value):
    if isinstance(value, bool):
        return None
    # Token counts must be exact integers: int(1.9) silently becomes 1.
    if isinstance(value, int):
        return value if value >= 0 else None
    if isinstance(value, str) and value.isascii() and value.isdecimal():
        return int(value)
    return None


def token_usage(final):
    """Return total, noncached, cached input and output, or None if unverified."""
    models = final.get("modelUsage")
    if isinstance(models, dict) and models:
        totals = [0, 0, 0]
        for model in models.values():
            if not isinstance(model, dict):
                return None
            values = [_nonnegative(model.get(k)) for k in (
                "inputTokens", "cacheReadInputTokens", "cacheCreationInputTokens",
                "outputTokens")]
            if any(v is None for v in values):
                return None
            totals[0] += values[0]
            totals[1] += values[1] + values[2]
            totals[2] += values[3]
        return {"input_tokens": totals[0] + totals[1],
                "uncached_input_tokens": totals[0],
                "cached_tokens": totals[1], "output_tokens": totals[2]}
    usage = final.get("usage")
    if not isinstance(usage, dict):
        return None
    values = [_nonnegative(usage.get(k)) for k in (
        "input_tokens", "cache_read_input_tokens", "cache_creation_input_tokens",
        "output_tokens")]
    if any(v is None for v in values):
        return None
    return {"input_tokens": sum(values[:3]),
            "uncached_input_tokens": values[0],
            "cached_tokens": values[1] + values[2], "output_tokens": values[3]}


def _has_nested_error(value):
    """Detect errors hidden inside JSON arrays or nested structured MCP results."""
    if isinstance(value, dict):
        if "error" in value or value.get("isError") or value.get("is_error"):
            return True
        return any(_has_nested_error(part) for part in value.values())
    if isinstance(value, list):
        return any(_has_nested_error(part) for part in value)
    return False


def _has_successful_content(content):
    """Require actual semantic data, not merely a successful MCP envelope.

    Claude can emit a completed tool_result containing {"success": true},
    {"result": []}, or a bare "ok". Those acknowledge execution but do not
    show that the agent received source or other useful LSP information.
    """
    if isinstance(content, list):
        # A partially empty multi-block result cannot prove all requested
        # source snippets were delivered.
        return bool(content) and all(_has_successful_content(part) for part in content)
    if isinstance(content, dict):
        if _has_nested_error(content):
            return False
        if content.get("type") == "text":
            return _has_successful_content(content.get("text"))
        if "content" in content:
            return _has_successful_content(content["content"])
        # Status-only wrappers (including nested ones) are not semantic data.
        control = {"success", "ok", "status", "isError", "is_error",
                   "meta", "complete", "truncated"}
        return any(_has_successful_content(value) for key, value in content.items()
                   if key not in control)
    if not isinstance(content, str) or not content.strip():
        return False
    s = content.strip()
    if s.lower().startswith(("error:", "error ", "mcp error", "lsp error")):
        return False
    if s.lower() in {"ok", "success", "done", "true", "false", "null", "none"}:
        return False
    try:
        decoded = json.loads(s)
    except (TypeError, ValueError):
        # Ordinary source code / symbol names are valid non-JSON responses.
        return True
    # Recurse into JSON. bool/int/null, empty arrays, and empty objects are
    # not source data; a nonempty outer envelope alone is insufficient.
    return _has_successful_content(decoded)


def successful_tool_result(part):
    return (isinstance(part, dict) and part.get("type") == "tool_result"
            and not part.get("is_error") and not part.get("isError")
            and _has_successful_content(part.get("content")))


def trace_tool_counts(events):
    """Match each MCP invocation to its tool_result ID, not merely an attempt."""
    uses = {}
    results = {}
    for event in events:
        if event.get("type") not in ("assistant", "user"):
            continue
        message = event.get("message") or {}
        parts = message.get("content") or []
        if not isinstance(parts, list):
            continue
        for part in parts:
            if not isinstance(part, dict):
                continue
            if event["type"] == "assistant" and part.get("type") == "tool_use":
                if part.get("id"):
                    uses[part["id"]] = (part.get("name", ""), part.get("input"))
            elif event["type"] == "user" and part.get("type") == "tool_result":
                if part.get("tool_use_id"):
                    results[part["tool_use_id"]] = part
    mcp_ids = [ident for ident, (name, _) in uses.items()
               if isinstance(name, str) and name.startswith("mcp__simple-lsp__")]
    seen_inputs = set()
    repeats = 0
    for name, arguments in uses.values():
        if not isinstance(arguments, dict):
            continue
        signature = (name, json.dumps(arguments, sort_keys=True, ensure_ascii=False))
        if signature in seen_inputs:
            repeats += 1
        seen_inputs.add(signature)
    return {"tool_calls": len(uses), "mcp_calls": len(mcp_ids),
            "repeated_tool_calls": repeats,
            "valid_mcp_calls": sum(successful_tool_result(results.get(ident))
                                   for ident in mcp_ids),
            "command_calls": sum(name == "Bash" for name, _ in uses.values()),
            "native_lsp_calls": sum(name == "LSP" for name, _ in uses.values())}
