#!/usr/bin/env python3
"""Predeclared, task-clustered evaluation of paired coding-agent A/B trials.

Do not treat retries of the same task as independent new tasks. The declared
gates prevent significant-looking, underpowered or low-quality results from
being reported as a product advantage. No third-party dependencies.
"""
import argparse
import csv
import json
import math
import random
import statistics
from collections import defaultdict
from pathlib import Path


def wilson_lower(successes, n, z=1.96):
    if n == 0:
        return 0.0
    p = successes / n
    den = 1 + z * z / n
    return (p + z * z / (2 * n) - z * math.sqrt((p * (1 - p) + z * z / (4 * n)) / n)) / den


def quantile(sorted_values, p):
    i = p * (len(sorted_values) - 1)
    left = int(i)
    right = min(left + 1, len(sorted_values) - 1)
    f = i - left
    return sorted_values[left] * (1 - f) + sorted_values[right] * f


def parse_bool(v):
    if isinstance(v, bool):
        return v
    return str(v).lower() in ("1", "true", "yes")


def read_paired(path, study, treatment="mcp"):
    rows = list(csv.DictReader(Path(path).open(encoding="utf-8", newline="")))
    pairs = defaultdict(dict)
    for row in rows:
        if study and row.get("study") != study:
            continue
        arm = row.get("arm") or row.get("mode")
        if arm not in ("baseline", treatment):
            continue
        task = row.get("task") or row.get("case")
        rep = row.get("repeat")
        if not task or not rep:
            raise ValueError("Every trial must identify task and repeat")
        key = (task, rep)
        if arm in pairs[key]:
            raise ValueError(f"Duplicate trial: {key}, {arm}")
        if not parse_bool(row.get("completed", "true")):
            raise ValueError(f"Incomplete trial: {key}, {arm}; do not score as success")
        if not parse_bool(row.get("graded", "true")):
            raise ValueError(f"Ungraded trial: {key}, {arm}")
        for metric in ("seconds", "input_tokens"):
            try:
                n = float(row[metric])
            except (KeyError, ValueError, TypeError) as exc:
                raise ValueError(f"Missing numeric {metric} in {key}") from exc
            if not math.isfinite(n) or n <= 0:
                raise ValueError(f"Invalid {metric}={n} in {key}")
            row[metric] = n
        row["accepted"] = parse_bool(row.get("accepted", "false"))
        # Legacy pilot CSVs omit these evidence flags. Keep them analyzable,
        # but never let unverifiable trials qualify as product-grade evidence.
        row["evidence_verified"] = (
            "completed" in row and "graded" in row
            and parse_bool(row["completed"]) and parse_bool(row["graded"])
        )
        # An attempted MCP call is not evidence that the agent received useful
        # semantic information. The trace-derived successful-call count is
        # mandatory; older CSVs without it remain analyzable but cannot pass.
        row["usage_verified"] = (
            row.get("mcp_calls") not in (None, "")
            and row.get("valid_mcp_calls") not in (None, "")
        )
        row["mcp_calls"] = int(row.get("mcp_calls") or 0)
        row["valid_mcp_calls"] = int(row.get("valid_mcp_calls") or 0)
        if not 0 <= row["valid_mcp_calls"] <= row["mcp_calls"]:
            raise ValueError(f"Invalid MCP call counts in {key}, {arm}")
        pairs[key][arm] = row
    if not pairs:
        raise ValueError("No trials selected")
    incomplete = [(key, tuple(x)) for key, x in pairs.items() if set(x) != {"baseline", treatment}]
    if incomplete:
        raise ValueError(f"Unmatched pairs: {incomplete[:3]}")
    return pairs


def evaluate(pairs, treatment="mcp", min_tasks=20, min_repeats=3,
             bootstrap=8000, seed=20261101, min_adoption=0.8,
             selection_policy="required", min_time_reduction=0.25,
             min_token_reduction=0.30):
    if selection_policy not in ("required", "natural"):
        raise ValueError("selection_policy must be required or natural")
    for threshold in (min_time_reduction, min_token_reduction):
        if not 0 < threshold < 1:
            raise ValueError("minimum reductions must be within (0, 1)")
    by_task = defaultdict(list)
    for (task, rep), arms in pairs.items():
        by_task[task].append(arms)
    reps = {task: len(x) for task, x in by_task.items()}
    n_tasks = len(by_task)
    treatments = [a[treatment] for a in pairs.values()]
    baselines = [a["baseline"] for a in pairs.values()]
    n = len(pairs)
    acceptance = {
        arm: {"accepted": sum(a["accepted"] for a in data), "n": n,
              "wilson_lower_95": wilson_lower(sum(a["accepted"] for a in data), n)}
        for arm, data in [("baseline", baselines), (treatment, treatments)]
    }
    adoption = sum(a["valid_mcp_calls"] > 0 for a in treatments) / n
    # All runs must use the same agent/model, instructions, pinned source and
    # independent grader. Without this evidence a CSV can claim a spurious
    # advantage by mixing models, graders, or task revisions across arms.
    provenance_fields = ("model", "effort", "agent_version", "source_commit",
                         "policy_sha", "grader_sha")
    provenance = {
        field: {a.get(field, "") for a in treatments + baselines}
        for field in provenance_fields
    }
    provenance_verified = all(
        len(values) == 1 and all(values) for values in provenance.values()
    )
    readiness = {
        "enough_independent_tasks": n_tasks >= min_tasks,
        "enough_repeats_each_task": all(rep >= min_repeats for rep in reps.values()),
        "quality_lower_bound_both_at_least_95_percent": all(
            v["wilson_lower_95"] >= 0.95 for v in acceptance.values()),
        "treatment_quality_at_least_baseline": (
            acceptance[treatment]["accepted"] >= acceptance["baseline"]["accepted"]),
        "trials_have_explicit_completion_and_grading": all(
            a["evidence_verified"] for a in (treatments + baselines)),
        "same_pinned_agent_source_policy_and_grader": provenance_verified,
        "trace_verified_mcp_call_counts": all(
            a["usage_verified"] for a in (treatments + baselines)),
        "baseline_has_no_mcp_calls": all(
            a["mcp_calls"] == 0 for a in baselines),
        # The product is explicitly deployed with AGENTS.md/CLAUDE.md.
        # Requiring natural/voluntary adoption as a pass gate rejects the
        # exact usage contract being evaluated. In 'required' trials *every*
        # treatment must use MCP; in 'natural' trials the predeclared
        # adoption threshold applies. Never score an MCP-free arm as proof
        # that mandatory LSP improved coding efficiency.
        "treatment_mcp_usage_matches_policy": (
            adoption == 1.0 if selection_policy == "required"
            else adoption >= min_adoption
        ),
    }
    metrics = {}
    rng = random.Random(seed)
    keys = sorted(by_task)
    targets = {"seconds": 1 - min_time_reduction,
               "input_tokens": 1 - min_token_reduction}
    for metric in ("seconds", "input_tokens"):
        # Each independent task has equal weight, irrespective of its raw size
        # or the number of repeated stochastic Codex executions.
        task_log_ratios = [
            statistics.mean(math.log(x[treatment][metric] / x["baseline"][metric])
                            for x in by_task[t])
            for t in keys
        ]
        mean_log_ratio = statistics.mean(task_log_ratios)
        boot = sorted(statistics.mean(
            task_log_ratios[rng.randrange(n_tasks)] for _ in range(n_tasks))
            for _ in range(bootstrap))
        # Simultaneous 95%-family-wise confidence for two co-primary outcomes:
        # Bonferroni alpha/2 per metric, two-sided 97.5% interval.
        lo, hi = quantile(boot, 0.0125), quantile(boot, 0.9875)
        metrics[metric] = {
            "ratio": round(math.exp(mean_log_ratio), 6),
            "change_percent": round((math.exp(mean_log_ratio) - 1) * 100, 2),
            "ci_familywise_95_ratio": [round(math.exp(lo), 6), round(math.exp(hi), 6)],
            "upper_below_1": hi < 0,
            "required_ratio": targets[metric],
            # The complete CI must clear the declared 25% / 30% target,
            # not merely show any nonzero improvement.
            "upper_below_required_ratio": math.exp(hi) <= targets[metric],
        }
    qualified = all(readiness.values()) and all(
        v["upper_below_required_ratio"] for v in metrics.values())
    return {
        "qualified_product_efficiency": qualified,
        "study_design": {
            "independent_tasks": n_tasks,
            "paired_trials": n,
            "repeats_per_task": reps,
            "task_equal_weight": True,
            "bootstrap_samples": bootstrap,
            "seed": seed,
            "two_co_primary_metrics_bonferroni": True,
            "selection_policy": selection_policy,
            "minimum_time_reduction": min_time_reduction,
            "minimum_total_input_reduction": min_token_reduction,
        },
        "readiness": readiness,
        "acceptance": acceptance,
        "treatment_adoption_rate": round(adoption, 4),
        "treatment_adoption_definition": "at least one successful MCP call per trial",
        "metrics": metrics,
        "interpretation": (
            "Qualified under the predeclared gates; replicate on a held-out codebase."
            if qualified else
            "NOT significant product evidence: design, quality, adoption or effect gates failed."
        ),
    }


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--csv", required=True, type=Path)
    p.add_argument("--study", required=True)
    p.add_argument("--treatment", default="mcp")
    p.add_argument("--bootstrap", type=int, default=8000)
    p.add_argument("--min-tasks", type=int, default=20)
    p.add_argument("--min-repeats", type=int, default=3)
    p.add_argument("--seed", type=int, default=20261101)
    p.add_argument("--selection-policy", choices=("natural", "required"),
                   default="required",
                   help="Evaluate either mandatory AGENTS.md LSP usage or natural discovery; both are valid study designs")
    p.add_argument("--require-qualified", action="store_true")
    args = p.parse_args()
    if args.bootstrap < 1000 or args.min_tasks < 2 or args.min_repeats < 1:
        p.error("Invalid power/design parameters")
    pairs = read_paired(args.csv, args.study, args.treatment)
    report = evaluate(pairs, treatment=args.treatment, min_tasks=args.min_tasks,
                      min_repeats=args.min_repeats, bootstrap=args.bootstrap,
                      seed=args.seed, selection_policy=args.selection_policy)
    print(json.dumps(report, ensure_ascii=False, indent=2))
    if args.require_qualified and not report["qualified_product_efficiency"]:
        raise SystemExit(1)


if __name__ == "__main__":
    main()
