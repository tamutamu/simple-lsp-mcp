#!/usr/bin/env python3
"""Re-grade preserved Codex traces after correcting a mistaken ground truth.

Never modifies original trace.jsonl or original outcome.json.
For C02 additionally emits safety observations that need human adjudication.
"""
import argparse
import json
from pathlib import Path

from run_developer_scenarios_ab import grade, verify_reported_declarations


def regrade(outcome: dict, case: dict, checkout: Path) -> dict:
    old=outcome["quality"]
    corrected=grade(outcome["answer"],case)
    source_check=verify_reported_declarations(checkout,outcome["answer"])
    mode=case.get("quality_mode","exact")
    required=(corrected["required_anchors_found"] if mode=="required_anchors"
              else corrected["review_facts_covered"] if mode=="review_facts"
              else corrected["exact_found"])
    result={"scene":case["id"],"arm":outcome["arm"],
            "seconds":outcome["seconds"],
            "input_total":outcome["input_total"],
            "input_fresh":outcome["input_fresh"],
            "original_grading":old,
            "corrected_grading":corrected,
            "source_checks":source_check,
            "manual_review_required":bool(case.get("human_review_facts")),
            "candidate_set_accepted": (
                outcome["exitcode"]==0 and outcome["no_source_edits"]
                and not outcome["environment_errors"] and required
                and source_check["source_declarations_verified"]),
            "raw_trace_preserved":True}
    if case["id"]=="C02-dynamodb-unused-helpers":
        text=json.dumps(outcome["answer"],ensure_ascii=False)
        mentions=all(name in text for name in ("deleteAllItems","deleteTable"))
        test_mention=any(word in text.lower() for word in ("_test.go","テスト","test"))
        result["manual_review_observations"]={
            "both_candidate_names_mentioned_anywhere":mentions,
            "tests_mentioned":test_mention,
            "source_unchanged":outcome["no_source_edits"],
            "production_unused_vs_test_used_needs_semantic_review":True,
            "reason":"gopls unusedfunc does not mean the method has no _test.go callers"
        }
    return result


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--catalog",required=True,type=Path)
    parser.add_argument("--results",required=True,type=Path)
    parser.add_argument("--output",required=True,type=Path)
    args=parser.parse_args()
    data=json.loads(args.catalog.read_text())
    scored=[]
    for case in data["scenes"]:
        for path in sorted((args.results/case["id"]).glob("*/outcome.json")):
            record=json.loads(path.read_text())
            scored.append(regrade(record,case,path.parent/"checkout"))
    if not scored:
        parser.error("no existing evidence files; refuse empty report")
    args.output.write_text(json.dumps(scored,ensure_ascii=False,indent=2)+"\n")
    for item in scored:
        q=item["corrected_grading"]
        print(item["scene"],item["arm"],
              "findings_recall",round(q["recall"],3),
              "extra_reported_declarations",len(q["unexpected"]),
              "review_facts_covered",q["review_facts_covered"],
              "automated_criteria_pass",item["candidate_set_accepted"],
              "human_review_required",item["manual_review_required"],
              "original",item["original_grading"]["exact_found"])


if __name__=="__main__":
    main()