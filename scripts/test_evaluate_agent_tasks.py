"""Offline harness tests: no agent account or network required."""
import importlib.util
import sys
import tempfile
import unittest
from pathlib import Path

MODULE_PATH = Path(__file__).with_name("evaluate-agent-tasks.py")
spec = importlib.util.spec_from_file_location("simple_lsp_agent_eval", MODULE_PATH)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class AgentEvaluationTests(unittest.TestCase):
    def test_manifest_and_dry_run(self):
        tasks = module.read_tasks((MODULE_PATH.parent.parent / "testdata/evaluation/tasks.json").resolve())
        self.assertEqual(tasks[0]["id"], "go-add-bug")
        self.assertEqual(len(tasks[0]["overlays"]), 1)
        self.assertEqual(module.main(["--manifest", str(MODULE_PATH.parent.parent / "testdata/evaluation/tasks.json"),
                                     "--baseline", '["nonexistent-agent"]', "--mcp", '["nonexistent-agent"]']), 0)

    def test_offline_pairing_uses_hidden_oracle(self):
        tasks = module.read_tasks((MODULE_PATH.parent.parent / "testdata/evaluation/tasks.json").resolve())
        noop = [sys.executable, "-c", "pass"]
        fix = [sys.executable, "-c", "from pathlib import Path; p=Path('calc.go'); p.write_text(p.read_text().replace('return a - b', 'return a + b'))"]
        report = module.evaluate(tasks, noop, fix, "", 2, 123, 20, 60, None)
        self.assertEqual(report["summary"]["baseline"], {"attempts": 2, "passed": 0})
        self.assertEqual(report["summary"]["mcp"], {"attempts": 2, "passed": 2})
        self.assertEqual(len(report["results"]), 4)

    def test_oracle_catches_visible_test_tampering(self):
        tasks = module.read_tasks((MODULE_PATH.parent.parent / "testdata/evaluation/tasks.json").resolve())
        tamper = [sys.executable, "-c", "from pathlib import Path; Path('calc_test.go').write_text('package calc\\n')"]
        with tempfile.TemporaryDirectory() as tmp:
            result = module.run_trial(tasks[0], "baseline", tamper, "", Path(tmp), 20, 60)
            self.assertFalse(result["passed"])

    def test_oracle_rejects_symlinked_destination(self):
        tasks = module.read_tasks((MODULE_PATH.parent.parent / "testdata/evaluation/tasks.json").resolve())
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "calc_oracle_test.go").symlink_to(root / "elsewhere.go")
            with self.assertRaises(RuntimeError):
                module.apply_oracle(tasks[0], root)

    def test_identical_agent_commands_cannot_claim_comparison(self):
        manifest = str(MODULE_PATH.parent.parent / "testdata/evaluation/tasks.json")
        with self.assertRaises(SystemExit) as raised:
            module.main(["--manifest", manifest, "--baseline", '["agent"]', "--mcp", '["agent"]', "--execute"])
        self.assertEqual(raised.exception.code, 2)

    def test_commands_reject_invalid_argv(self):
        for value in ('{}', '[]', '["", "bad"]', '[17]'):
            with self.assertRaises(ValueError):
                module.argv_argument(value)


if __name__ == "__main__":
    unittest.main()
