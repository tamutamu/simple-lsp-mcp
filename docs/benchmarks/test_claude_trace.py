"""Regression checks for Claude A/B evidence extraction."""
import json
import subprocess
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch
import run_fastapi_agent_ab as claude_runner
from claude_trace import token_usage, trace_tool_counts
from run_fastapi_agent_ab import STUDY, report
from analyze_codex_ab import read_paired


class TraceTest(unittest.TestCase):
    def test_claude_csv_preserves_study_for_shared_qualification_analyzer(self):
        # Previously report() omitted 'study', so --study claude-required
        # discarded every trial as unrelated and could never evaluate A/B.
        with tempfile.TemporaryDirectory() as td:
            rows = [
                {"study": STUDY, "case": "search", "mode": mode, "repeat": 1,
                 "seconds": 10.0, "input_tokens": 1000, "cost_usd": 0.1,
                 "completed": True, "graded": True, "accepted": True,
                 "mcp_calls": int(mode == "mcp"),
                 "valid_mcp_calls": int(mode == "mcp")}
                for mode in ("baseline", "mcp")
            ]
            report(SimpleNamespace(output=Path(td), cases=["search"]), rows)
            paired = read_paired(Path(td) / "results.csv", STUDY)
            self.assertEqual(len(paired), 1)
            self.assertEqual(set(paired[("search", "1")]), {"baseline", "mcp"})

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
            "valid_mcp_calls": 1, "command_calls": 1, "native_lsp_calls": 0,
            "repeated_tool_calls": 0})

    def test_nested_json_mcp_errors_do_not_count_as_successful_semantics(self):
        def assistant(ident):
            return {"type": "assistant", "message": {"content": [
                {"type": "tool_use", "id": ident,
                 "name": "mcp__simple-lsp__inspect_symbol"}]}}
        def response(ident, payload):
            return {"type": "user", "message": {"content": [
                {"type": "tool_result", "tool_use_id": ident,
                 "content": [{"type": "text", "text": payload}]}]}}
        events = [
            assistant("array"),
            response("array", '[{"result":{"error":{"code":"INCOMPLETE_SEARCH"}}}]'),
            assistant("nested"),
            response("nested", '{"result":{"data":{"error":{"code":"LSP_FAILED"}}}}'),
            assistant("valid"),
            response("valid", '{"result":{"source":"def f(): pass","references":[]}}'),
        ]
        counts = trace_tool_counts(events)
        self.assertEqual(counts["mcp_calls"], 3)
        self.assertEqual(counts["valid_mcp_calls"], 1)

    def test_identical_tool_inputs_counted_without_confusing_distinct_reads(self):
        events = [{"type": "assistant", "message": {"content": [
            {"type": "tool_use", "id": "a", "name": "Read", "input": {"file_path": "x.py"}},
            {"type": "tool_use", "id": "b", "name": "Read", "input": {"file_path": "x.py"}},
            {"type": "tool_use", "id": "c", "name": "Read", "input": {"file_path": "y.py"}}]}}]
        counts = trace_tool_counts(events)
        self.assertEqual(counts["tool_calls"], 3)
        self.assertEqual(counts["repeated_tool_calls"], 1)

    def test_empty_or_unmatched_results_are_not_success(self):
        events = [
            {"type": "assistant", "message": {"content": [
                {"type": "tool_use", "id": "x", "name": "mcp__simple-lsp__inspect_symbol"}]}},
            {"type": "user", "message": {"content": [
                {"type": "tool_result", "tool_use_id": "other", "content": "ok"}]}},
        ]
        self.assertEqual(trace_tool_counts(events)["valid_mcp_calls"], 0)


    def test_claude_independent_grader_failures_are_recorded_as_rejections(self):
        scenarios = ("truthy_false", "nonzero_exit", "timeout", "invalid_json",
                     "syntax_timeout", "diff_timeout")
        for scenario in scenarios:
            with self.subTest(scenario=scenario), tempfile.TemporaryDirectory() as td:
                work = Path(td) / "checkout"
                work.mkdir()
                row = {"case": "search", "mode": "baseline", "repeat": 1,
                       "worktree": str(work), "completed": True,
                       "graded": False, "accepted": False}
                args = SimpleNamespace(python=Path("/usr/bin/python3"), postgres_url="")

                def fake_call(argv, **kwargs):
                    command = " ".join(map(str, argv))
                    if "fastapi-template-grader.py" in command:
                        if scenario == "timeout":
                            raise subprocess.TimeoutExpired(argv, 60)
                        if scenario == "truthy_false":
                            return SimpleNamespace(returncode=0,
                                                   stdout='{"success": "false"}', stderr="")
                        if scenario == "nonzero_exit":
                            return SimpleNamespace(returncode=2,
                                                   stdout='{"success": true}', stderr="failed")
                        if scenario == "invalid_json":
                            return SimpleNamespace(returncode=0,
                                                   stdout='[{"success": true}]', stderr="")
                        return SimpleNamespace(returncode=0,
                                               stdout='{"success": true}', stderr="")
                    if scenario == "syntax_timeout" and "compileall" in command:
                        raise subprocess.TimeoutExpired(argv, 45)
                    if scenario == "diff_timeout" and "diff --check" in command:
                        raise subprocess.TimeoutExpired(argv, 30)
                    return SimpleNamespace(returncode=0, stdout="", stderr="")

                with patch.object(claude_runner, "call", side_effect=fake_call):
                    claude_runner.score(args, [row])
                self.assertTrue(row["graded"])
                self.assertFalse(row["accepted"])
                self.assertEqual(row["grader_exitcode"], 124 if scenario == "timeout"
                                 else 2 if scenario == "nonzero_exit" else 0)
                self.assertFalse(json.loads((work.parent / "result.json").read_text())["accepted"])

    def test_claude_independent_grader_accepts_only_real_success(self):
        with tempfile.TemporaryDirectory() as td:
            work = Path(td) / "checkout"
            work.mkdir()
            row = {"case": "search", "mode": "mcp", "repeat": 1,
                   "worktree": str(work), "completed": True,
                   "graded": False, "accepted": False}
            args = SimpleNamespace(python=Path("/usr/bin/python3"), postgres_url="")
            with patch.object(claude_runner, "call", return_value=SimpleNamespace(
                    returncode=0, stdout='{"success": true}', stderr="")):
                claude_runner.score(args, [row])
            self.assertTrue(row["graded"])
            self.assertTrue(row["accepted"])
            self.assertEqual(row["grader_exitcode"], 0)



if __name__ == "__main__":
    unittest.main()
