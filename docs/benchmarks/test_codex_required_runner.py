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


if __name__ == "__main__":
    unittest.main()
