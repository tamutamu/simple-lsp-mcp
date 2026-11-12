#!/usr/bin/env python3
"""Fail-closed reconciliation of the genuine agent-level iteration ledger.

Each recorded iteration must contain both arms of a before/after Codex A/B.
Track all input tokens, including cached tokens, and the actual model call count.
Input-token-cost proxies are scenarios, not Codex API charge assertions.
"""
from __future__ import annotations

import argparse
import csv
import json
from pathlib import Path
import sys

def require(condition: bool, message: str) -> None:
    if not condition:
        raise ValueError(message)

def read_json(path: Path) -> dict:
    require(path.is_file(),f"missing {path}")
    return json.loads(path.read_text(encoding="utf8"))

def verify_trial(root: Path, arm: str, allow_corrected: bool) -> dict:
    paths=list(root.glob(f"*/{arm}-results.json"))
    require(len(paths)==1,f"{root}: expected one {arm} result, found {len(paths)}")
    path=paths[0]
    raw=read_json(path)
    require(raw.get("status")=="completed",f"{path}: agent did not complete successfully")
    grade=raw.get("grade") or {}
    corrected=path.with_name(arm+"-corrected-grade.json")
    if allow_corrected and corrected.is_file():
        grade=read_json(corrected)
    require(grade.get("pass") is True,f"{path}: failed independent grade")
    checks=grade.get("checks")
    require(isinstance(checks,list) and all(check.get("ok") is True for check in checks),
        f"{path}: one or more independent grade checks failed")
    tokens=raw.get("tokens")
    require(isinstance(tokens,dict),f"{path}: missing usage")
    total=int(tokens["inputTokens"])
    cached=int(tokens["cachedInputTokens"])
    fresh=total-cached
    require(0<=cached<=total,f"{path}: invalid cached accounting")
    require(fresh==raw["noncached_input_tokens"],f"{path}: mismatched noncached input tokens")
    if "input_tokens" in raw:
        require(total==raw["input_tokens"],f"{path}: mismatched total input tokens")
    output=int(tokens["outputTokens"])
    require(output>=0,f"{path}: invalid output usage")
    trace=path.with_name(arm+"-trace.jsonl")
    require(trace.is_file(),f"{path}: trace missing")
    usage_updates=[]
    for line in trace.read_text(encoding="utf8").splitlines():
        try: event=json.loads(line)["event"]
        except (ValueError,KeyError):continue
        if event.get("method")=="thread/tokenUsage/updated":
            usage_updates.append(event["params"]["tokenUsage"])
    require(len(usage_updates)>=1,f"{path}: missing model usage events")
    last=usage_updates[-1]["total"]
    require(int(last["inputTokens"])==total and int(last["cachedInputTokens"])==cached and int(last["outputTokens"])==output,
        f"{path}: trace and result model usage disagree")
    mcp=raw.get("mcp_calls") or []
    successful=sum(call.get("status")=="completed" and not call.get("is_error",False) for call in mcp)
    return {
        "case_id":raw["case_id"],
        "initial_seed_commit":raw["baseline_hash"],
        "seconds":float(raw["work_seconds"]),
        "fresh_input":fresh,
        "cached_input":cached,
        "total_input":total,
        "output":output,
        "model_calls":len(usage_updates),
        "cached_share_pct":round(100*cached/total,2) if total else 0,
        "model":raw.get("model"),
        "effort":raw.get("effort"),
        "mcp_calls":len(mcp),
        "mcp_successful":successful,
        "mcp_tools":",".join(x["tool"] for x in mcp),
        "shell_calls":len(raw.get("cli_calls") or []),
        "mcp_response_bytes":sum(int(x.get("bytes") or 0) for x in mcp),
        "shell_output_bytes":sum(int(x.get("bytes") or 0) for x in raw.get("cli_calls") or []),
        "pass":True,
        "source":str(path),
    }

def input_equivalent(row: dict, cache_multiplier: float) -> float:
    return row["fresh_input"]+cache_multiplier*row["cached_input"]

def pair_differences(left: dict, right: dict) -> dict:
    require(left["case_id"]==right["case_id"],"different tasks in pair")
    require(left["initial_seed_commit"]==right["initial_seed_commit"],"different seeded sources in pair")
    require(left["pass"] and right["pass"],"independent grader failed in pair")
    require(left["model"]==right["model"] and left["effort"]==right["effort"],
        "model or effort differs between A/B arms")
    require(left["mcp_calls"]==0,"baseline contains MCP calls")
    noncached_diff=right["fresh_input"]-left["fresh_input"]
    cached_diff=right["cached_input"]-left["cached_input"]
    break_even=None
    if cached_diff!=0:
        break_even=-noncached_diff/cached_diff
    return {
        "quality_both_pass":True,
        "baseline_seconds":left["seconds"],"mcp_seconds":right["seconds"],
        "time_change_percent":round((right["seconds"]/left["seconds"]-1)*100,2),
        "baseline_input":left["total_input"],"mcp_input":right["total_input"],
        "total_input_change_percent":round((right["total_input"]/left["total_input"]-1)*100,2),
        "baseline_fresh":left["fresh_input"],"mcp_fresh":right["fresh_input"],
        "fresh_input_change_percent":round((right["fresh_input"]/left["fresh_input"]-1)*100,2),
        "baseline_cached":left["cached_input"],"mcp_cached":right["cached_input"],
        "baseline_model_calls":left["model_calls"],"mcp_model_calls":right["model_calls"],
        "baseline_cache_share_pct":left["cached_share_pct"],
        "mcp_cache_share_pct":right["cached_share_pct"],
        "model":left["model"],"effort":left["effort"],
        "mcp_tool_calls":right["mcp_calls"],"mcp_successful_tool_calls":right["mcp_successful"],
        "mcp_response_bytes":right["mcp_response_bytes"],
        "baseline_shell_output_bytes":left["shell_output_bytes"],
        "mcp_shell_output_bytes":right["shell_output_bytes"],
        # These hypothetical weights do not include output-token charges.
        # They are NOT billing estimates: Codex CLI data gives no dollar price.
        "input_proxy_at_10pct_cache_baseline":round(input_equivalent(left,.1),1),
        "input_proxy_at_10pct_cache_mcp":round(input_equivalent(right,.1),1),
        "input_proxy_at_10pct_cache_change_percent":round(
            (input_equivalent(right,.1)/input_equivalent(left,.1)-1)*100,2),
        "input_cache_break_even_weight":round(break_even,4) if break_even is not None else None,
    }

def audit(ledger_path: Path, scratch: Path) -> tuple[list[dict],list[dict]]:
    ledger=read_json(ledger_path)
    cycles=ledger["cycles"]
    require(ledger["counted_cycles"]==len(cycles),"counted_cycles disagrees with records")
    require([c["cycle"] for c in cycles]==list(range(1,len(cycles)+1)),"cycle numbering invalid")
    require(len(cycles)<=ledger["goal"],"goal exceeded")
    trials=[]
    pairs=[]
    for cycle in cycles:
        no=cycle["cycle"]
        require(cycle.get("counted") is True,f"cycle {no}: no counted approval")
        require(bool(cycle.get("found_problems")) and bool(cycle.get("corrections")),
            f"cycle {no}: no causal diagnosis or correction")
        for phase in ("before","after"):
            root=scratch/cycle[phase+"_run"]
            allow_corrected=no==1 and phase=="before"
            b=verify_trial(root,"baseline",allow_corrected)
            m=verify_trial(root,"mcp",allow_corrected)
            diff=pair_differences(b,m)
            if phase=="after":
                require(m["mcp_successful"]>0,
                    f"cycle {no}: no successful model-initiated LSP use after correction")
            pairs.append({"cycle":no,"task":cycle["task"],"phase":phase,**diff})
            for arm,record in [("baseline",b),("mcp",m)]:
                trials.append({"cycle":no,"task":cycle["task"],"phase":phase,"arm":arm,**record})
    return trials,pairs

def main() -> None:
    parser=argparse.ArgumentParser()
    parser.add_argument("--ledger",type=Path,default=Path("docs/benchmarks/agent-effect-100-cycles-ledger.json"))
    parser.add_argument("--scratch",type=Path,default=Path(".scratch"))
    parser.add_argument("--output",type=Path,required=True)
    args=parser.parse_args()
    trials,pairs=audit(args.ledger,args.scratch)
    args.output.mkdir(parents=True,exist_ok=True)
    for name,rows in (("trials.csv",trials),("pairs.csv",pairs)):
        require(bool(rows),"empty audit")
        path=args.output/name
        with path.open("w",encoding="utf8",newline="") as f:
            writer=csv.DictWriter(f,fieldnames=list(rows[0]))
            writer.writeheader()
            writer.writerows(rows)
        path.write_bytes(path.read_bytes().replace(b"\r\n",b"\n"))
    print(json.dumps({
        "verified_cycles":len(trials)//4,
        "independent_agent_trials":len(trials),
        "verified_matched_pairs":len(pairs),
        "post_correction_mcp_calls":sum(r["mcp_tool_calls"] for r in pairs if r["phase"]=="after"),
        "post_correction_mcp_successes":sum(r["mcp_successful_tool_calls"] for r in pairs if r["phase"]=="after"),
    },ensure_ascii=False))

if __name__=="__main__":
    main()
