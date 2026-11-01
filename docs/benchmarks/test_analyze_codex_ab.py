"""Regression tests for statistically honest, matched agent evaluation."""
import csv
import importlib.util
import tempfile
import unittest
from pathlib import Path

MODULE_PATH = Path(__file__).with_name("analyze_codex_ab.py")
SPEC = importlib.util.spec_from_file_location("abstats", MODULE_PATH)
ab = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(ab)


class EvaluationTest(unittest.TestCase):
    def write_trials(self, path, n_tasks, repeats, treatment_ratio):
        with path.open("w", newline="") as f:
            w = csv.DictWriter(f, fieldnames=[
                "study", "task", "arm", "repeat", "seconds", "input_tokens",
                "mcp_calls", "accepted", "completed", "graded"])
            w.writeheader()
            for t in range(n_tasks):
                for rep in range(1, repeats + 1):
                    base = 10 + t
                    w.writerow(dict(study="controlled", task=f"task{t}", arm="baseline",
                                    repeat=rep, seconds=base, input_tokens=base*1000,
                                    mcp_calls=0, accepted=True, completed=True, graded=True))
                    w.writerow(dict(study="controlled", task=f"task{t}", arm="mcp",
                                    repeat=rep, seconds=base*treatment_ratio,
                                    input_tokens=base*1000*treatment_ratio, mcp_calls=1,
                                    accepted=True, completed=True, graded=True))

    def test_current_small_sample_never_qualifies(self):
        with tempfile.TemporaryDirectory() as td:
            p = Path(td) / "small.csv"
            self.write_trials(p, 3, 2, 0.5)
            result = ab.evaluate(ab.read_paired(p, "controlled"), bootstrap=1000)
            self.assertFalse(result["qualified_product_efficiency"])
            self.assertFalse(result["readiness"]["enough_independent_tasks"])
            self.assertFalse(result["readiness"]["quality_lower_bound_both_at_least_95_percent"])

    def test_larger_consistent_effect_passes_gates(self):
        with tempfile.TemporaryDirectory() as td:
            p = Path(td) / "large.csv"
            self.write_trials(p, 30, 4, 0.8)
            result = ab.evaluate(ab.read_paired(p, "controlled"), bootstrap=1000)
            self.assertTrue(result["qualified_product_efficiency"], result)
            for metric in result["metrics"].values():
                self.assertTrue(metric["upper_below_1"])
                self.assertAlmostEqual(metric["ratio"], 0.8, places=4)

    def test_duplicate_trial_fails_closed(self):
        with tempfile.TemporaryDirectory() as td:
            p = Path(td) / "dupe.csv"
            self.write_trials(p, 2, 1, 0.9)
            with p.open("a") as f:
                f.write("controlled,task0,mcp,1,9,9000,1,true,true,true\n")
            with self.assertRaisesRegex(ValueError, "Duplicate trial"):
                ab.read_paired(p, "controlled")

    def test_unmatched_trial_fails_closed(self):
        with tempfile.TemporaryDirectory() as td:
            p = Path(td) / "unmatched.csv"
            self.write_trials(p, 2, 1, 0.9)
            contents = p.read_text().splitlines()
            p.write_text("\n".join(contents[:-1]) + "\n")
            with self.assertRaisesRegex(ValueError, "Unmatched pairs"):
                ab.read_paired(p, "controlled")


if __name__ == "__main__":
    unittest.main()
