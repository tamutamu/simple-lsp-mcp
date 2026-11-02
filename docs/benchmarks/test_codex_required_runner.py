"""Regression tests for the Codex A/B runner's qualification provenance."""
import csv
import json
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
                    return SimpleNamespace(returncode=1, stdout='{"success": true}\\n', stderr="")
                return SimpleNamespace(returncode=0, stdout="", stderr="")

            with patch.object(runner, "call", side_effect=fake_call):
                runner.grade(o, [row])
            self.assertTrue(row["graded"])
            self.assertFalse(row["accepted"])


if __name__ == "__main__":
    unittest.main()
