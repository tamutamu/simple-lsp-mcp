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
                "mcp_calls", "accepted", "completed", "graded",
                "valid_mcp_calls", "model", "effort", "agent_version", "mcp_binary_sha",
                "source_commit", "policy_sha", "tasks_sha", "grader_sha",
                "retry_count", "grader_exitcode"])
            w.writeheader()
            for t in range(n_tasks):
                for rep in range(1, repeats + 1):
                    base = 10 + t
                    w.writerow(dict(study="controlled", task=f"task{t}", arm="baseline",
                                    repeat=rep, seconds=base, input_tokens=base*1000,
                                    mcp_calls=0, accepted=True, completed=True, graded=True,
                                    valid_mcp_calls=0, model="model-pinned", effort="low",
                                    agent_version="codex-1", mcp_binary_sha="mcp-build", source_commit="source-commit",
                                    policy_sha="policy-digest", tasks_sha="tasks-digest", grader_sha="grader-digest",
                                    retry_count=0, grader_exitcode=0))
                    w.writerow(dict(study="controlled", task=f"task{t}", arm="mcp",
                                    repeat=rep, seconds=base*treatment_ratio,
                                    input_tokens=base*1000*treatment_ratio, mcp_calls=1,
                                    accepted=True, completed=True, graded=True,
                                    valid_mcp_calls=1, model="model-pinned", effort="low",
                                    agent_version="codex-1", mcp_binary_sha="mcp-build", source_commit="source-commit",
                                    policy_sha="policy-digest", tasks_sha="tasks-digest", grader_sha="grader-digest",
                                    retry_count=0, grader_exitcode=0))


    def test_failed_or_unverified_independent_grader_cannot_qualify(self):
        for scenario in ("missing", "nonzero", "invalid"):
            with self.subTest(scenario=scenario), tempfile.TemporaryDirectory() as td:
                p = Path(td) / "grader.csv"
                self.write_trials(p, 30, 4, 0.6)
                rows = list(csv.DictReader(p.open()))
                if scenario == "missing":
                    for row in rows:
                        row.pop("grader_exitcode")
                elif scenario == "nonzero":
                    rows[1]["grader_exitcode"] = "124"
                else:
                    rows[1]["grader_exitcode"] = "not-an-exit-code"
                with p.open("w", newline="") as f:
                    w = csv.DictWriter(f, fieldnames=rows[0].keys())
                    w.writeheader()
                    w.writerows(rows)
                if scenario == "invalid":
                    with self.assertRaisesRegex(ValueError, "Invalid grader_exitcode"):
                        ab.read_paired(p, "controlled")
                else:
                    result = ab.evaluate(ab.read_paired(p, "controlled"), bootstrap=1000)
                    self.assertFalse(result["qualified_product_efficiency"])
                    self.assertFalse(result["readiness"][
                        "independent_grader_exited_successfully_in_every_trial"])

    def test_selected_retry_cannot_qualify(self):
        with tempfile.TemporaryDirectory() as td:
            p = Path(td) / "retried.csv"
            self.write_trials(p, 30, 4, 0.6)
            rows = list(csv.DictReader(p.open()))
            rows[1]["retry_count"] = "1"
            with p.open("w", newline="") as f:
                w = csv.DictWriter(f, fieldnames=rows[0].keys())
                w.writeheader()
                w.writerows(rows)
            result = ab.evaluate(ab.read_paired(p, "controlled"), bootstrap=1000)
            self.assertFalse(result["qualified_product_efficiency"])
            self.assertFalse(result["readiness"]["all_trials_are_first_attempts_with_verified_provenance"])

    def test_missing_retry_provenance_cannot_qualify(self):
        with tempfile.TemporaryDirectory() as td:
            p = Path(td) / "legacy.csv"
            self.write_trials(p, 30, 4, 0.6)
            rows = list(csv.DictReader(p.open()))
            for row in rows:
                row.pop("retry_count")
            with p.open("w", newline="") as f:
                w = csv.DictWriter(f, fieldnames=rows[0].keys())
                w.writeheader()
                w.writerows(rows)
            result = ab.evaluate(ab.read_paired(p, "controlled"), bootstrap=1000)
            self.assertFalse(result["qualified_product_efficiency"])
            self.assertFalse(result["readiness"]["all_trials_are_first_attempts_with_verified_provenance"])

    def test_invalid_retry_count_rejected(self):
        for invalid in ("-1", "1.0", "unknown"):
            with self.subTest(value=invalid), tempfile.TemporaryDirectory() as td:
                p = Path(td) / "invalid.csv"
                self.write_trials(p, 2, 1, 0.6)
                rows = list(csv.DictReader(p.open()))
                rows[0]["retry_count"] = invalid
                with p.open("w", newline="") as f:
                    w = csv.DictWriter(f, fieldnames=rows[0].keys())
                    w.writeheader()
                    w.writerows(rows)
                with self.assertRaisesRegex(ValueError, "Invalid retry_count"):
                    ab.read_paired(p, "controlled")

    def test_invalid_repeat_identifiers_are_rejected(self):
        for invalid in ("0", "-1", "01", "1.0", "pilot"):
            with self.subTest(repeat=invalid), tempfile.TemporaryDirectory() as td:
                p = Path(td) / "invalid-repeat.csv"
                self.write_trials(p, 2, 3, 0.6)
                rows = list(csv.DictReader(p.open()))
                rows[0]["repeat"] = invalid
                with p.open("w", newline="") as f:
                    w = csv.DictWriter(f, fieldnames=rows[0].keys())
                    w.writeheader()
                    w.writerows(rows)
                with self.assertRaisesRegex(ValueError, "Invalid repeat identifier"):
                    ab.read_paired(p, "controlled")

    def test_unbalanced_extra_repeat_cannot_qualify(self):
        with tempfile.TemporaryDirectory() as td:
            p = Path(td) / "selected-repeat.csv"
            self.write_trials(p, 30, 4, 0.6)
            rows = list(csv.DictReader(p.open()))
            # One selected task gets an extra successful pair after results
            # are known; all original sample-size/effect gates still pass.
            for arm in ("baseline", "mcp"):
                extra = next(r.copy() for r in rows
                             if r["task"] == "task0" and r["arm"] == arm)
                extra["repeat"] = "5"
                rows.append(extra)
            with p.open("w", newline="") as f:
                w = csv.DictWriter(f, fieldnames=rows[0].keys())
                w.writeheader()
                w.writerows(rows)
            result = ab.evaluate(ab.read_paired(p, "controlled"), bootstrap=1000)
            self.assertFalse(result["qualified_product_efficiency"])
            self.assertFalse(result["readiness"]["balanced_contiguous_repeat_grid"])

    def test_skipped_repeat_number_cannot_qualify(self):
        with tempfile.TemporaryDirectory() as td:
            p = Path(td) / "skipped-repeat.csv"
            self.write_trials(p, 30, 4, 0.6)
            rows = list(csv.DictReader(p.open()))
            for row in rows:
                if row["repeat"] == "3":
                    row["repeat"] = "5"
            with p.open("w", newline="") as f:
                w = csv.DictWriter(f, fieldnames=rows[0].keys())
                w.writeheader()
                w.writerows(rows)
            result = ab.evaluate(ab.read_paired(p, "controlled"), bootstrap=1000)
            self.assertFalse(result["qualified_product_efficiency"])
            self.assertFalse(result["readiness"]["balanced_contiguous_repeat_grid"])

    def test_current_small_sample_never_qualifies(self):
        with tempfile.TemporaryDirectory() as td:
            p = Path(td) / "small.csv"
            self.write_trials(p, 3, 2, 0.5)
            result = ab.evaluate(ab.read_paired(p, "controlled"), bootstrap=1000)
            self.assertFalse(result["qualified_product_efficiency"])
            self.assertFalse(result["readiness"]["enough_independent_tasks"])
            self.assertFalse(result["readiness"]["quality_lower_bound_both_at_least_95_percent"])

    def test_qualification_thresholds_cannot_be_weakened(self):
        # Even perfect synthetic outcomes must not qualify after lowering
        # sample-size, effect-size or natural-adoption requirements.
        with tempfile.TemporaryDirectory() as td:
            p = Path(td) / "thresholds.csv"
            self.write_trials(p, 3, 2, 0.6)
            pairs = ab.read_paired(p, "controlled")
            for override in (
                {"min_tasks": 2},
                {"min_repeats": 2},
                {"min_time_reduction": 0.20},
                {"min_token_reduction": 0.20},
                {"selection_policy": "natural", "min_adoption": 0.50},
            ):
                with self.subTest(override=override):
                    with self.assertRaisesRegex(ValueError, "qualification requires"):
                        ab.evaluate(pairs, bootstrap=1000, **override)

    def test_larger_consistent_effect_passes_gates(self):
        with tempfile.TemporaryDirectory() as td:
            p = Path(td) / "large.csv"
            self.write_trials(p, 30, 4, 0.6)
            result = ab.evaluate(ab.read_paired(p, "controlled"), bootstrap=1000,
                                 selection_policy="natural")
            self.assertTrue(result["qualified_product_efficiency"], result)
            for metric in result["metrics"].values():
                self.assertTrue(metric["upper_below_1"])
                self.assertAlmostEqual(metric["ratio"], 0.6, places=4)
                self.assertTrue(metric["upper_below_required_ratio"])


    def test_minor_improvement_does_not_qualify_as_25_30_percent_target(self):
        with tempfile.TemporaryDirectory() as td:
            p = Path(td) / "minor.csv"
            self.write_trials(p, 30, 4, 0.8)
            result = ab.evaluate(ab.read_paired(p, "controlled"),
                                 bootstrap=1000, selection_policy="natural")
            self.assertFalse(result["qualified_product_efficiency"])
            self.assertTrue(all(v["upper_below_1"] for v in result["metrics"].values()))
            self.assertFalse(all(v["upper_below_required_ratio"]
                                 for v in result["metrics"].values()))

    def test_required_lsp_usage_is_valid_evidence_when_quality_and_cost_pass(self):
        with tempfile.TemporaryDirectory() as td:
            p = Path(td) / "forced.csv"
            self.write_trials(p, 30, 4, 0.6)
            result = ab.evaluate(ab.read_paired(p, "controlled"), bootstrap=1000,
                                 selection_policy="required")
            self.assertEqual(result["treatment_adoption_rate"], 1.0)
            self.assertTrue(result["readiness"]["treatment_mcp_usage_matches_policy"])
            self.assertTrue(result["qualified_product_efficiency"], result)

    def test_required_lsp_gate_rejects_even_one_trial_without_mcp(self):
        with tempfile.TemporaryDirectory() as td:
            p = Path(td) / "forced.csv"
            self.write_trials(p, 30, 4, 0.6)
            rows = list(csv.DictReader(p.open()))
            for row in rows:
                if row["arm"] == "mcp" and row["task"] == "task0" and row["repeat"] == "1":
                    row["mcp_calls"] = "0"
                    row["valid_mcp_calls"] = "0"
            with p.open("w", newline="") as f:
                w = csv.DictWriter(f, fieldnames=rows[0].keys())
                w.writeheader()
                w.writerows(rows)
            result = ab.evaluate(ab.read_paired(p, "controlled"), bootstrap=1000,
                                 selection_policy="required")
            self.assertFalse(result["readiness"]["treatment_mcp_usage_matches_policy"])
            self.assertFalse(result["qualified_product_efficiency"])

    def test_unverified_legacy_outcomes_are_never_qualification_evidence(self):
        with tempfile.TemporaryDirectory() as td:
            p = Path(td) / "legacy.csv"
            self.write_trials(p, 30, 4, 0.6)
            text = p.read_text()
            text = text.replace(",completed,graded", "")
            text = text.replace(",True,True,True", ",True")
            p.write_text(text)
            result = ab.evaluate(ab.read_paired(p, "controlled"),
                                 bootstrap=1000, selection_policy="natural")
            self.assertFalse(result["readiness"]["trials_have_explicit_completion_and_grading"])
            self.assertFalse(result["qualified_product_efficiency"])

    def test_quality_must_not_degrade_even_if_wilson_threshold_passes(self):
        with tempfile.TemporaryDirectory() as td:
            p = Path(td) / "quality.csv"
            self.write_trials(p, 30, 4, 0.6)
            rows = list(csv.DictReader(p.open()))
            for row in rows:
                if row["arm"] == "mcp" and row["task"] == "task0" and row["repeat"] == "1":
                    row["accepted"] = "False"
            with p.open("w", newline="") as f:
                w = csv.DictWriter(f, fieldnames=rows[0].keys())
                w.writeheader()
                w.writerows(rows)
            result = ab.evaluate(ab.read_paired(p, "controlled"),
                                 bootstrap=1000, selection_policy="natural")
            self.assertTrue(result["readiness"]["quality_lower_bound_both_at_least_95_percent"])
            self.assertFalse(result["readiness"]["treatment_quality_at_least_baseline"])
            self.assertFalse(result["qualified_product_efficiency"])


    def test_attempted_but_failed_mcp_call_cannot_prove_product_value(self):
        with tempfile.TemporaryDirectory() as td:
            p = Path(td) / "failed-mcp.csv"
            self.write_trials(p, 30, 4, 0.6)
            rows = list(csv.DictReader(p.open()))
            for row in rows:
                if row["arm"] == "mcp" and row["task"] == "task0" and row["repeat"] == "1":
                    row["valid_mcp_calls"] = "0"
            with p.open("w", newline="") as f:
                w = csv.DictWriter(f, fieldnames=rows[0].keys())
                w.writeheader()
                w.writerows(rows)
            result = ab.evaluate(ab.read_paired(p, "controlled"), bootstrap=1000)
            self.assertFalse(result["qualified_product_efficiency"])
            self.assertFalse(result["readiness"]["treatment_mcp_usage_matches_policy"])

    def test_baseline_mcp_contamination_rejected(self):
        with tempfile.TemporaryDirectory() as td:
            p = Path(td) / "contaminated.csv"
            self.write_trials(p, 30, 4, 0.6)
            rows = list(csv.DictReader(p.open()))
            rows[0]["mcp_calls"] = "1"
            rows[0]["valid_mcp_calls"] = "1"
            with p.open("w", newline="") as f:
                w = csv.DictWriter(f, fieldnames=rows[0].keys())
                w.writeheader()
                w.writerows(rows)
            result = ab.evaluate(ab.read_paired(p, "controlled"), bootstrap=1000)
            self.assertFalse(result["readiness"]["baseline_has_no_mcp_calls"])
            self.assertFalse(result["qualified_product_efficiency"])

    def test_mixed_model_or_grader_cannot_pass(self):
        for field in ("model", "effort", "agent_version", "mcp_binary_sha", "source_commit",
                      "policy_sha", "tasks_sha", "grader_sha"):
            with self.subTest(field=field), tempfile.TemporaryDirectory() as td:
                p = Path(td) / "mismatched.csv"
                self.write_trials(p, 30, 4, 0.6)
                rows = list(csv.DictReader(p.open()))
                rows[1][field] = "other"
                with p.open("w", newline="") as f:
                    w = csv.DictWriter(f, fieldnames=rows[0].keys())
                    w.writeheader()
                    w.writerows(rows)
                result = ab.evaluate(ab.read_paired(p, "controlled"), bootstrap=1000)
                self.assertFalse(result["readiness"]["same_pinned_agent_source_policy_and_grader"])
                self.assertFalse(result["qualified_product_efficiency"])

    def test_missing_trace_provenance_cannot_pass(self):
        with tempfile.TemporaryDirectory() as td:
            p = Path(td) / "unverified.csv"
            self.write_trials(p, 30, 4, 0.6)
            rows = list(csv.DictReader(p.open()))
            for row in rows:
                row.pop("valid_mcp_calls")
            with p.open("w", newline="") as f:
                w = csv.DictWriter(f, fieldnames=rows[0].keys())
                w.writeheader()
                w.writerows(rows)
            result = ab.evaluate(ab.read_paired(p, "controlled"), bootstrap=1000)
            self.assertFalse(result["readiness"]["trace_verified_mcp_call_counts"])
            self.assertFalse(result["qualified_product_efficiency"])


    def test_text_only_mcp_errors_are_not_counted_as_success(self):
        runner_path = MODULE_PATH.with_name("run_fastapi_codex_coding_required_ab.py")
        spec = importlib.util.spec_from_file_location("abrunner", runner_path)
        runner = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(runner)
        def item(result, **kwargs):
            return dict(status="completed", result=result, **kwargs)
        good = {"content": [{"type": "text", "text": '{"path":"a.py","source":"def f(): pass"}'}]}
        self.assertTrue(runner.successful_mcp_call(item(good)))
        self.assertTrue(runner.successful_mcp_call(item({"structured_content": {"source": "ok"}})))
        self.assertFalse(runner.successful_mcp_call(item(None)))
        self.assertFalse(runner.successful_mcp_call(item({})))
        self.assertFalse(runner.successful_mcp_call(item({"content": []})))
        self.assertFalse(runner.successful_mcp_call(item({"content": [{"type": "text", "text": '{"error":{"code":"INCOMPLETE_SEARCH"}}'}]})))
        self.assertFalse(runner.successful_mcp_call(item({"content": [{"type": "text", "text": "Error: LSP failed"}]})))
        self.assertFalse(runner.successful_mcp_call(item({"structuredContent": {"error": {"code": "InvalidArgument"}}})))
        self.assertFalse(runner.successful_mcp_call(item({"isError": True, **good})))
        self.assertFalse(runner.successful_mcp_call(item(good, error="transport failed")))
        self.assertFalse(runner.successful_mcp_call(dict(status="failed", result=good)))

    def test_missing_or_mixed_mcp_binary_provenance_cannot_qualify(self):
        with tempfile.TemporaryDirectory() as td:
            p = Path(td) / "build.csv"
            self.write_trials(p, 30, 4, 0.6)
            rows = list(csv.DictReader(p.open()))
            rows[1]["mcp_binary_sha"] = "other-build"
            with p.open("w", newline="") as f:
                w = csv.DictWriter(f, fieldnames=rows[0].keys())
                w.writeheader()
                w.writerows(rows)
            result = ab.evaluate(ab.read_paired(p, "controlled"), bootstrap=1000)
            self.assertFalse(result["qualified_product_efficiency"])
            self.assertFalse(result["readiness"]["same_pinned_agent_source_policy_and_grader"])

    def test_duplicate_trial_fails_closed(self):
        with tempfile.TemporaryDirectory() as td:
            p = Path(td) / "dupe.csv"
            self.write_trials(p, 2, 1, 0.9)
            with p.open("a") as f:
                f.write("controlled,task0,mcp,1,9,9000,1,true,true,true,1,model-pinned,low,codex-1,source-commit,policy-digest,tasks-digest,grader-digest\n")
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
