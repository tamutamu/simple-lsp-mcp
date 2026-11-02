"""Regression checks for Claude A/B evidence extraction."""
import unittest
from claude_trace import token_usage, trace_tool_counts


class TraceTest(unittest.TestCase):
    def test_model_usage_includes_cache_in_total_input(self):
        result = {"modelUsage": {
            "claude-a": {"inputTokens": 20, "cacheReadInputTokens": 100,
                         "cacheCreationInputTokens": 30, "outputTokens": 5},
            "claude-b": {"inputTokens": 10, "cacheReadInputTokens": 40,
                         "cacheCreationInputTokens": 0, "outputTokens": 8}}}
        self.assertEqual(token_usage(result), {"input_tokens": 200,
            "uncached_input_tokens": 30, "cached_tokens": 170, "output_tokens": 13})

    def test_usage_fallback_and_missing_fields_fail_closed(self):
        result = {"usage": {"input_tokens": 7, "cache_read_input_tokens": 3,
                            "cache_creation_input_tokens": 2, "output_tokens": 4}}
        self.assertEqual(token_usage(result)["input_tokens"], 12)
        self.assertIsNone(token_usage({"usage": {"input_tokens": 12}}))
        self.assertIsNone(token_usage({"modelUsage": {"claude": {"inputTokens": 12}}}))

    def test_only_matching_non_error_mcp_results_count(self):
        def assistant(ident, name):
            return {"type": "assistant", "message": {"content": [
                {"type": "tool_use", "id": ident, "name": name}]}}
        def response(ident, content, **flags):
            return {"type": "user", "message": {"content": [
                {"type": "tool_result", "tool_use_id": ident,
                 "content": content, **flags}]}}
        events = [
            assistant("1", "mcp__simple-lsp__inspect_symbol"),
            response("1", [{"type": "text", "text": '{"source":"def f(): pass"}'}]),
            assistant("2", "mcp__simple-lsp__find_references"),
            response("2", [{"type": "text", "text": '{"error":{"code":"INCOMPLETE_SEARCH"}}'}]),
            assistant("2b", "mcp__simple-lsp__inspect_symbol"),
            response("2b", [{"type": "text", "text": '{"error":{}}'}]),
            assistant("3", "mcp__simple-lsp__inspect_symbol"),
            response("3", [{"type": "text", "text": "Error: LSP failed"}]),
            assistant("4", "mcp__simple-lsp__inspect_symbol"),
            response("4", [{"type": "text", "text": "ok"}], is_error=True),
            assistant("5", "mcp__simple-lsp__inspect_symbol"),
            assistant("6", "Bash"),
            response("6", [{"type": "text", "text": "ok"}]),
        ]
        self.assertEqual(trace_tool_counts(events), {"tool_calls": 7, "mcp_calls": 6,
            "valid_mcp_calls": 1, "command_calls": 1, "native_lsp_calls": 0})

    def test_empty_or_unmatched_results_are_not_success(self):
        events = [
            {"type": "assistant", "message": {"content": [
                {"type": "tool_use", "id": "x", "name": "mcp__simple-lsp__inspect_symbol"}]}},
            {"type": "user", "message": {"content": [
                {"type": "tool_result", "tool_use_id": "other", "content": "ok"}]}},
        ]
        self.assertEqual(trace_tool_counts(events)["valid_mcp_calls"], 0)


if __name__ == "__main__":
    unittest.main()
