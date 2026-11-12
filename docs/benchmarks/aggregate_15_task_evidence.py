#!/usr/bin/env python3
"""Assemble independent, fail-closed evidence for all 15 defined development tasks.

This is a cross-run inventory, NOT a single controlled 15-task A/B experiment.
In particular, T46 completed arms in separate runs with different seed commit
IDs and is intentionally not counted as a prespecified pair.
"""
from __future__ import annotations

import argparse
import csv
import hashlib
import json
from pathlib import Path
import subprocess

from audit_product_ab import audit
from codex_product_ab_cases import CASES

SOURCE = {
    "T33": "product-ab-T33-2rep-20261112",
    "T34": "product-ab-literal-selector-policy-20261112",
    "T35": "product-ab-diagnostics-path-enforced-20261111",
    "T36": "product-ab-remaining-eight-pilot-20261112",
    "T37": "product-ab-T37-typescript-2rep-20261112",
    "T38": "product-ab-remaining-eight-pilot-20261112",
    "T39": "product-ab-T39-pyright-fixed-2rep-20261112",
    "T40": "product-ab-remaining-eight-pilot-20261112",
    "T41": "product-ab-last-five-resumed-20261112",
    "T42": "product-ab-T42-interface-2rep-20261112",
    "T43": "product-ab-evaluation-optimized-coding-20261111",
    "T44": "product-ab-last-five-resumed-20261112",
    "T45": "product-ab-last-five-resumed-20261112",
    "T46": None,  # two individual successes, no same-run clean pair
    "T47": "product-ab-remaining-eight-pilot-20261112",
}

def exact_trial(base: Path, task: str, repetition: int, arm: str) -> dict:
    matches=list(base.glob(f"{task}-*-rep{repetition}/{arm}-results.json"))
    if len(matches)!=1:
        raise ValueError(f"expected one result, found {len(matches)}: {task}/{repetition}/{arm} in {base}")
    return json.loads(matches[0].read_text())

def content_hash(seed: Path) -> str:
    paths=subprocess.check_output(["git","ls-files","-z"], cwd=seed).decode().split("\0")
    digest=hashlib.sha256()
    for name in sorted(filter(None,paths)):
        digest.update(name.encode()+b"\0"+hashlib.sha256((seed/name).read_bytes()).digest())
    return digest.hexdigest()

def collect(scratch: Path) -> tuple[list[dict],dict]:
    catalog={x["id"]:x for x in CASES}
    assert set(catalog)==set(SOURCE)
    reports={}
    output=[]
    for case_id,source in SOURCE.items():
        if source is None:
            output.append({"id":case_id,"name":catalog[case_id]["name"],"language":catalog[case_id]["language"],
                           "pairs":0,"baseline_pass":0,"mcp_pass":0,"mcp_actual_calls":0,
                           "mcp_successful_calls":0,"status":"not_paired","source":"separate attempts"})
            continue
        base=scratch/source
        if source not in reports:
            reports[source]=audit(base)
        report=reports[source]
        pairs=[x for x in report["pairs"] if x["id"]==case_id]
        if not pairs:
            raise ValueError(f"no verified accepted pair for {case_id} from {base}")
        baseline_times=[]
        mcp_times=[]
        baseline_noncached=0
        mcp_noncached=0
        mcp_calls=0
        successful_calls=0
        for pair in pairs:
            rep=pair["rep"]
            left=exact_trial(base,case_id,rep,"baseline")
            right=exact_trial(base,case_id,rep,"mcp")
            if left["baseline_hash"]!=right["baseline_hash"]:
                raise AssertionError(f"seed Git SHA differs within A/B pair: {case_id}/{rep}")
            for arm,row in (("baseline",left),("mcp",right)):
                if row["status"]!="completed" or not row["grade"]["pass"] or any(not c["ok"] for c in row["grade"]["checks"]):
                    raise AssertionError(f"invalid purported success: {case_id}/{rep}/{arm}")
            if pair["mcp_calls"]!=len(right["mcp_calls"]):
                raise AssertionError(f"disagreement between audited logs and result: {case_id}/{rep}")
            baseline_times.append(left["work_seconds"])
            mcp_times.append(right["work_seconds"])
            baseline_noncached+=left["noncached_input_tokens"]
            mcp_noncached+=right["noncached_input_tokens"]
            mcp_calls+=pair["mcp_calls"]
            successful_calls+=pair["successful_mcp_calls"]
        mean_a=round(sum(baseline_times)/len(baseline_times),3)
        mean_b=round(sum(mcp_times)/len(mcp_times),3)
        output.append({
            "id":case_id,"name":catalog[case_id]["name"],"language":catalog[case_id]["language"],
            "pairs":len(pairs),"baseline_pass":len(pairs),"mcp_pass":len(pairs),
            "mean_baseline_seconds":mean_a,
            "mean_mcp_seconds":mean_b,
            "time_change_pct":round((mean_b/mean_a-1)*100,1),
            "noncached_baseline_tokens":baseline_noncached,
            "noncached_mcp_tokens":mcp_noncached,
            "mcp_actual_calls":mcp_calls,"mcp_successful_calls":successful_calls,
            "status":"paired_verified",
            "source":source,
            "tool_profile":json.loads((base/"manifest.json").read_text()).get("tool_profile","unrecorded"),
        })
    assert len(output)==15
    partial=scratch/"product-ab-remaining-eight-pilot-20261112"
    resumed=scratch/"product-ab-last-five-resumed-20261112"
    t46b=exact_trial(resumed,"T46",1,"baseline")
    t46m=exact_trial(partial,"T46",1,"mcp")
    if not (t46b["status"]=="completed" and t46b["grade"]["pass"] and t46m["status"]=="completed" and t46m["grade"]["pass"]):
        raise AssertionError("T46 individual arm result was not successful")
    seeds=[]
    for b in (partial,resumed):
        result=list(b.glob("T46-*-rep1/frozen-task-seed"))
        if len(result)!=1:raise AssertionError("missing T46 frozen seed")
        seeds.append(content_hash(result[0]))
    if seeds[0]!=seeds[1]:
        raise AssertionError("T46 arms didn't even receive identical source content")
    if t46b["baseline_hash"]==t46m["baseline_hash"]:
        raise AssertionError("T46 appears paired; update inventory rather than mark separate")
    summary={
        "task_count":len(output),
        "tasks_with_verified_pairs":sum(r["pairs"]>0 for r in output),
        "verified_pairs":sum(r["pairs"] for r in output),
        "verified_agent_trials":2*sum(r["pairs"] for r in output),
        "tasks_with_mcp_calls":sum(r["mcp_actual_calls"]>0 for r in output),
        "actual_mcp_calls":sum(r["mcp_actual_calls"] for r in output),
        "successful_mcp_calls":sum(r["mcp_successful_calls"] for r in output),
        "t46_unpaired":{
            "baseline_seconds":t46b["work_seconds"],
            "mcp_seconds":t46m["work_seconds"],
            "baseline_noncached_tokens":t46b["noncached_input_tokens"],
            "mcp_noncached_tokens":t46m["noncached_input_tokens"],
            "mcp_calls":len(t46m["mcp_calls"]),
            "same_tracked_source_hash":seeds[0],
            "baseline_seed_commit":t46b["baseline_hash"],
            "mcp_seed_commit":t46m["baseline_hash"],
            "paired":False,
            "caution":"Post hoc, separate runs and Git commits. Do not use for a controlled difference estimate.",
        },
    }
    return output,summary

def main() -> None:
    parser=argparse.ArgumentParser()
    parser.add_argument("--scratch",type=Path,required=True)
    parser.add_argument("--csv",type=Path,required=True)
    parser.add_argument("--json",type=Path,required=True)
    args=parser.parse_args()
    output,summary=collect(args.scratch)
    fields=["id","name","language","pairs","baseline_pass","mcp_pass","mean_baseline_seconds",
            "mean_mcp_seconds","time_change_pct","noncached_baseline_tokens","noncached_mcp_tokens",
            "mcp_actual_calls","mcp_successful_calls","status","source","tool_profile"]
    with args.csv.open("w",newline="",encoding="utf8") as target:
        w=csv.DictWriter(target,fieldnames=fields,extrasaction="ignore")
        w.writeheader()
        w.writerows(output)
    args.csv.write_bytes(args.csv.read_bytes().replace(b"\r\n",b"\n"))
    args.json.write_text(json.dumps(summary,indent=2,ensure_ascii=False)+"\n",encoding="utf8")
    print(json.dumps(summary,indent=2,ensure_ascii=False))
    for row in output:
        print(row["id"],row["pairs"],row.get("time_change_pct"),
              row["mcp_actual_calls"],row["status"])

if __name__=="__main__":
    main()
