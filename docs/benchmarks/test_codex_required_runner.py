"""Regression tests for the Codex A/B runner's qualification provenance."""
import csv
import json
import subprocess
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch

import run_fastapi_codex_coding_required_ab as runner
from analyze_codex_ab import read_paired


class RequiredABProvenanceTest(unittest.TestCase):
    def trial(self, mode):
        return dict(
            study="coding-required", case="search", mode=mode, repeat=1,
            retry_count=0, seconds=10.0, exitcode=0, completed=True,
            graded=True, accepted=True, input_tokens=1000, cached_tokens=100,
            output_tokens=100, reasoning_tokens=10, mcp_calls=int(mode == "mcp"),
            valid_mcp_calls=int(mode == "mcp"), command_calls=2,
            model="fixed-model", effort="low", agent_version="codex 0.147.0",
            mcp_binary_sha="fixed-binary", source_commit=runner.SHA,
            policy_sha="fixed-policy", tasks_sha="fixed-tasks", grader_sha="fixed-grader",
        )

    def test_report_csv_is_readable_by_predeclared_qualification_gate(self):
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp)
            runner.report(SimpleNamespace(output=out, model="fixed-model",
                                          effort="low", cases=["search"]),
                          [self.trial("baseline"), self.trial("mcp")])
            with (out / "results.csv").open(newline="") as stream:
                rows = list(csv.DictReader(stream))
            self.assertEqual([r["study"] for r in rows],
                             ["coding-required", "coding-required"])
            self.assertEqual([r["retry_count"] for r in rows], ["0", "0"])
            pairs = read_paired(out / "results.csv", "coding-required")
            self.assertEqual(len(pairs), 1)
            self.assertTrue(all(arm["retry_verified"]
                                for arm in pairs[("search", "1")].values()))

    def test_retried_incomplete_trial_cannot_masquerade_as_first_attempt(self):
        with tempfile.TemporaryDirectory() as tmp:
            output = Path(tmp)
            directory = output / "runs" / "search-baseline-1"
            directory.mkdir(parents=True)
            (directory / "outcome.json").write_text(
                json.dumps({"completed": False, "retry_count": 0}))
            o = SimpleNamespace(
                output=output, resume=True, retry_incomplete=True,
                source=output, node=Path("/usr/bin/node"),
                codex=Path("/tmp/codex.js"), model="fixed-model", effort="low",
                mcp=Path("/tmp/simple-lsp-mcp"), lsp_bin=Path("/tmp"),
                tsserver="", timeout=10, agent_version="codex 0.147.0",
                mcp_binary_sha="fixed-binary",
            )

            def fake_call(cmd, **kwargs):
                stdout = ""
                if cmd[0] != "git":
                    stdout = json.dumps({
                        "type": "turn.completed",
                        "usage": {"input_tokens": 1000, "cached_input_tokens": 100,
                                  "output_tokens": 100},
                    }) + "\n"
                return SimpleNamespace(returncode=0, stdout=stdout, stderr="")

            with patch.object(runner, "prepare", side_effect=lambda _, dst: dst.mkdir()), \
                 patch.object(runner, "call", side_effect=fake_call):
                result = runner.evaluate(o, "search", "baseline", 1)
            self.assertEqual(result["retry_count"], 1)
            self.assertEqual(result["study"], "coding-required")
            self.assertTrue((output / "incomplete-attempts" /
                             "search-baseline-1" / "outcome.json").is_file())
            self.assertEqual(json.loads((directory / "outcome.json").read_text())
                             ["retry_count"], 1)


    def test_trace_counts_failed_and_interrupted_mcp_attempts(self):
        def item(identifier, status, result=None):
            return {"id": identifier, "type": "mcp_tool_call",
                    "server": "simple-lsp", "status": status,
                    "tool": "inspect_symbol", "result": result}

        good = {"content": [{"type": "text", "text": "def example(): pass"}]}
        events = [
            {"type": "item.started", "item": item("ok", "in_progress")},
            {"type": "item.updated", "item": item("ok", "in_progress")},
            {"type": "item.completed", "item": item("ok", "completed", good)},
            {"type": "item.started", "item": item("failed", "in_progress")},
            {"type": "item.failed", "item": item("failed", "failed")},
            {"type": "item.started", "item": item("interrupted", "in_progress")},
        ]
        attempted, successful = runner.trace_mcp_calls(events)
        self.assertEqual(len(attempted), 3)
        self.assertEqual(len(successful), 1)
        self.assertEqual(successful[0]["id"], "ok")

    def test_baseline_failed_mcp_attempt_cannot_disappear_from_audit(self):
        events = [
            {"type": "item.started", "item": {
                "id": "baseline-leak", "type": "mcp_tool_call",
                "server": "simple-lsp", "status": "in_progress"}},
            {"type": "item.failed", "item": {
                "id": "baseline-leak", "type": "mcp_tool_call",
                "server": "simple-lsp", "status": "failed"}},
        ]
        attempted, successful = runner.trace_mcp_calls(events)
        self.assertEqual(len(attempted), 1)
        self.assertFalse(successful)

    def test_grader_nonzero_exit_cannot_be_accepted_even_if_json_says_success(self):
        with tempfile.TemporaryDirectory() as tmp:
            work = Path(tmp) / "checkout"
            work.mkdir()
            row = self.trial("baseline")
            row.update(workdir=str(work), case="search", graded=False, accepted=False)
            o = SimpleNamespace(python=Path("/usr/bin/python3"), postgres_url="")

            def fake_call(argv, **kwargs):
                if "fastapi-template-grader.py" in " ".join(map(str, argv)):
                    return SimpleNamespace(returncode=1, stdout='{"success": true}', stderr="")
                return SimpleNamespace(returncode=0, stdout="", stderr="")

            with patch.object(runner, "call", side_effect=fake_call):
                runner.grade(o, [row])
            self.assertTrue(row["graded"])
            self.assertFalse(row["accepted"])


    def test_grader_timeout_is_a_recorded_rejection_not_a_crash(self):
        with tempfile.TemporaryDirectory() as tmp:
            output = Path(tmp)
            work = output / "runs" / "search-baseline-1" / "checkout"
            work.mkdir(parents=True)
            row = self.trial("baseline")
            row.update(workdir=str(work), case="search", graded=False, accepted=False)
            o = SimpleNamespace(python=Path("/usr/bin/python3"), postgres_url="")

            def fake_call(argv, **kwargs):
                if "fastapi-template-grader.py" in " ".join(map(str, argv)):
                    raise subprocess.TimeoutExpired(argv, 80)
                return SimpleNamespace(returncode=0, stdout="", stderr="")

            with patch.object(runner, "call", side_effect=fake_call):
                runner.grade(o, [row])
            self.assertTrue(row["graded"])
            self.assertFalse(row["accepted"])
            self.assertEqual(row["grader_exitcode"], 124)
            self.assertIn("timed out", row["grader"]["error"])
            saved = json.loads((work.parent / "outcome.json").read_text())
            self.assertFalse(saved["accepted"])
            runner.report(SimpleNamespace(output=output, model="fixed-model",
                                          effort="low", cases=["search"]), [row])
            self.assertTrue((output / "results.csv").is_file())

    def test_truthy_string_grader_success_is_not_accepted(self):
        with tempfile.TemporaryDirectory() as tmp:
            work = Path(tmp) / "checkout"
            work.mkdir()
            row = self.trial("baseline")
            row.update(workdir=str(work), case="search", graded=False, accepted=False)
            o = SimpleNamespace(python=Path("/usr/bin/python3"), postgres_url="")

            def fake_call(argv, **kwargs):
                if "fastapi-template-grader.py" in " ".join(map(str, argv)):
                    return SimpleNamespace(returncode=0, stdout='{"success": "false"}', stderr="")
                return SimpleNamespace(returncode=0, stdout="", stderr="")

            with patch.object(runner, "call", side_effect=fake_call):
                runner.grade(o, [row])
            self.assertTrue(row["graded"])
            self.assertFalse(row["accepted"])

    def test_syntax_check_timeout_rejects_without_losing_grader_result(self):
        with tempfile.TemporaryDirectory() as tmp:
            work = Path(tmp) / "checkout"
            work.mkdir()
            row = self.trial("baseline")
            row.update(workdir=str(work), case="search", graded=False, accepted=False)
            o = SimpleNamespace(python=Path("/usr/bin/python3"), postgres_url="")

            def fake_call(argv, **kwargs):
                cmd = " ".join(map(str, argv))
                if "fastapi-template-grader.py" in cmd:
                    return SimpleNamespace(returncode=0, stdout='{"success": true}', stderr="")
                if "compileall" in cmd:
                    raise subprocess.TimeoutExpired(argv, 60)
                return SimpleNamespace(returncode=0, stdout="", stderr="")

            with patch.object(runner, "call", side_effect=fake_call):
                runner.grade(o, [row])
            self.assertTrue(row["graded"])
            self.assertFalse(row["syntax_ok"])
            self.assertFalse(row["accepted"])
            self.assertEqual(row["grader_exitcode"], 0)


    def test_nested_mcp_errors_do_not_count_as_semantic_use(self):
        def item(result):
            return {"status": "completed", "result": result}

        bad_texts = [
            '[{"result": {"error": "symbol not found"}}]',
            '{"items": [{"isError": true}]}',
            '"Error: LSP request failed"',
            "LSP error: server unavailable",
        ]
        for value in bad_texts:
            with self.subTest(value=value):
                self.assertFalse(runner.successful_mcp_call(item({
                    "content": [{"type": "text", "text": value}],
                })))
        self.assertFalse(runner.successful_mcp_call(item({
            "structured_content": {"result": [{"error": "not found"}]},
            "content": [{"type": "text", "text": "source code"}],
        })))
        self.assertFalse(runner.successful_mcp_call(item({
            "content": [
                {"type": "text", "text": "def ok(): pass"},
                {"type": "text", "text": '{"error": "partial failure"}'},
            ],
        })))
        self.assertTrue(runner.successful_mcp_call(item({
            "content": [{"type": "text", "text": '[{"name": "read_items"}]'}],
        })))

    def test_repeated_calls_count_attempts_not_lifecycle_events(self):
        def event(kind, ident, typ, **kwargs):
            return {"type": kind, "item": {"id": ident, "type": typ, **kwargs}}

        events = [
            event("item.started", "c1", "command_execution", command="rg item"),
            event("item.completed", "c1", "command_execution", command="rg item"),
            event("item.started", "c2", "command_execution", command="rg item"),
            event("item.failed", "c2", "command_execution", command="rg item"),
            event("item.started", "m1", "mcp_tool_call", server="simple-lsp",
                  tool="inspect_symbol", arguments='{"path":"a.py","view":"edit"}'),
            event("item.completed", "m1", "mcp_tool_call", server="simple-lsp",
                  tool="inspect_symbol", arguments='{"view":"edit","path":"a.py"}'),
            event("item.started", "m2", "mcp_tool_call", server="simple-lsp",
                  tool="inspect_symbol", arguments={"view": "edit", "path": "a.py"}),
        ]
        commands, repeats = runner.trace_tool_attempts(events)
        self.assertEqual(commands, 2)
        self.assertEqual(repeats, 2)

    def test_csv_exports_uncached_input_and_repeated_calls(self):
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp)
            row = self.trial("mcp")
            row.update(uncached_input_tokens=900, repeated_tool_calls=3)
            runner.report(SimpleNamespace(output=out, model="fixed-model",
                                          effort="low", cases=["search"]), [row])
            with (out / "results.csv").open(newline="") as stream:
                saved = next(csv.DictReader(stream))
            self.assertEqual(saved["uncached_input_tokens"], "900")
            self.assertEqual(saved["repeated_tool_calls"], "3")

    def test_evaluate_records_uncached_tokens_and_attempted_commands(self):
        with tempfile.TemporaryDirectory() as tmp:
            output = Path(tmp)
            o = SimpleNamespace(
                output=output, resume=False, retry_incomplete=False,
                source=output, node=Path("/usr/bin/node"),
                codex=Path("/tmp/codex.js"), model="fixed-model", effort="low",
                mcp=Path("/tmp/simple-lsp-mcp"), lsp_bin=Path("/tmp"),
                tsserver="", timeout=10, agent_version="codex 0.147.0",
                mcp_binary_sha="fixed-binary",
            )
            trace = [
                {"type": "item.started", "item": {
                    "id": "cmd", "type": "command_execution", "command": "rg item"}},
                {"type": "turn.completed", "usage": {
                    "input_tokens": 1000, "cached_input_tokens": 800,
                    "output_tokens": 50}},
            ]

            def fake_call(cmd, **kwargs):
                stdout = ""
                if cmd[0] != "git":
                    stdout = "".join(json.dumps(event) + "\\n" for event in trace)
                return SimpleNamespace(returncode=0, stdout=stdout, stderr="")

            with patch.object(runner, "prepare", side_effect=lambda _, dst: dst.mkdir()), \\
                 patch.object(runner, "call", side_effect=fake_call):
                row = runner.evaluate(o, "search", "baseline", 1)
            self.assertEqual(row["uncached_input_tokens"], 200)
            self.assertEqual(row["command_calls"], 1)
            self.assertEqual(row["repeated_tool_calls"], 0)


if __name__ == "__main__":
    unittest.main()
