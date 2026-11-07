#!/usr/bin/env python3
"""End-to-end Codex A/B for investigating three declarations in one Go repository.

One Codex task, one continuous conversation, three semantic targets.
Control and treatment have the same workspace-write sandbox (Go/gopls needs
writable cache) and are rejected if they modify the checked-out source.
"""
import argparse
import concurrent.futures
import hashlib
import json
import os
from pathlib import Path
import subprocess
import time

from codex_investigation_ab import clone_checkout, git, last_output, parse_json_answer
from semantic_bench_targets import resolve_go_target

POLICY = """# Read-only multi-symbol change investigation
Do not edit or generate any repository source files. The workspace-write
sandbox is ONLY to allow Go/gopls temporary build-cache files.
For three requested declarations, identify the LSP semantic references and
up to two hops of enclosing callers. Never treat same-spelled identifiers as
proof of the same symbol. Report truncation/unknowns as such.

If simple-lsp MCP is connected, call investigate_change ONCE with targets[]
containing the three exact path+symbol_path pairs, depth=2, max_nodes=30,
detail=union. Use the returned per-target file paths, counts and completeness
flags directly. Do not re-open files or run follow-up commands if successful.
Without MCP, use the standard shell and Go static-analysis utilities.

Return exactly one JSON object:
{
  "results": [
    {
      "symbol_path": "<provided function name>",
      "affected_files": ["relative/file/path.go"],
      "affected_declarations": 0,
      "direct_reference_count": 0,
      "incomplete": true
    }
  ],
  "affected_files_union": ["relative/file/path.go"]
}
Return all three results, in requested order. Sort paths. Never invent counts:
use null when the facts cannot be verified.
"""

def score(answer,truth):
    actual=answer.get("results",[])
    byname={x.get("symbol_path"):x for x in actual if isinstance(x,dict)}
    expected=truth["batch"]["results"]
    targets=truth["targets"]
    per=[]
    for i,item in enumerate(targets):
        want=expected[i]
        name=item["symbol_path"]
        got=byname.get(name,{})
        expected_paths=set(want["affected_files"])
        actual_paths=set(got.get("affected_files",[])) if isinstance(got.get("affected_files"),list) else set()
        per.append({
          "name":name,
          "path_match":actual_paths==expected_paths,
          "correct_declarations":got.get("affected_declarations")==want["affected_declarations"],
          "correct_direct_references":got.get("direct_reference_count")==want["direct_reference_count"],
          "correct_incomplete":got.get("incomplete")== (not want["meta"]["complete"]),
          "found_paths":len(actual_paths & expected_paths),
          "total_paths":len(expected_paths),
        })
    union_actual=set(answer.get("affected_files_union",[])) if isinstance(answer.get("affected_files_union"),list) else set()
    union_expected=set(truth["batch"]["affected_files_union"])
    return {
      "per_target":per,
      "union_precision":len(union_actual&union_expected)/len(union_actual) if union_actual else 0.0,
      "union_recall":len(union_actual&union_expected)/len(union_expected) if union_expected else 1.0,
      "all_semantic_facts":len(actual)==len(targets) and len(byname)==len(targets)
         and all(all(row[key] for key in
             ("path_match","correct_declarations","correct_direct_references","correct_incomplete")) for row in per)
         and union_actual==union_expected,
    }

def attempt(o,arm,n,truth):
    targetdir=o.output/f"{arm}-{n}"
    targetdir.mkdir(parents=True,exist_ok=False)
    work=targetdir/"checkout"
    clone_checkout(o.source,work,POLICY)
    env=os.environ.copy()
    env["PATH"]=os.pathsep.join([str(o.node.parent),str(o.lsp_bin),env.get("PATH","")])
    targets=truth["targets"]
    names="; ".join(f"{t['path']}::{t['symbol_path']} (declaration at line {t['line']}, column {t['column']})" for t in targets)
    prompt=("Investigate all three changes in this large codebase as ONE task: "+names+
        ". For each, identify ALL affected file paths within 2 semantic reference hops, "
        "distinct affected declarations, exact direct reference counts, and whether the "
        "bounded graph is incomplete. Include the deduplicated file union. "
        "Use the JSON format in AGENTS.md. No edits.")
    cmd=[str(o.node),str(o.codex),"exec","--ignore-user-config","--ephemeral",
         "--json","--skip-git-repo-check","--sandbox","workspace-write",
         "--cd",str(work),"--model",o.model,"--config",'model_reasoning_effort="'+o.effort+'"']
    if arm=="mcp":
        cmd.extend(["-c",'mcp_servers.simple-lsp.command="'+str(o.binary)+'"',
          "-c",'mcp_servers.simple-lsp.args=["--workspace","'+str(work)+'","--tool-profile","investigate","--result-format","text","--request-timeout","90s"]',
          "-c",'mcp_servers.simple-lsp.env.PATH="'+env["PATH"]+'"'])
    cmd.append(prompt)
    start=time.monotonic()
    try:
        proc=subprocess.run(cmd,cwd=work,env=env,capture_output=True,text=True,timeout=o.timeout)
        stdout,stderr,status=proc.stdout,proc.stderr,proc.returncode
    except subprocess.TimeoutExpired as err:
        stdout=err.stdout or b""
        stderr=err.stderr or b""
        if isinstance(stdout,bytes):stdout=stdout.decode(errors="replace")
        if isinstance(stderr,bytes):stderr=stderr.decode(errors="replace")
        status=124
    seconds=round(time.monotonic()-start,3)
    (targetdir/"trace.jsonl").write_text(stdout)
    (targetdir/"stderr.txt").write_text(stderr)
    trace=[]
    for line in stdout.splitlines():
        try:trace.append(json.loads(line))
        except (TypeError,ValueError):continue
    usage={}
    for event in trace:
        if event.get("type")=="turn.completed":usage=event.get("usage",{})
    total,cached=usage.get("input_tokens"),usage.get("cached_input_tokens")
    fresh=total-cached if isinstance(total,int) and isinstance(cached,int) and 0<=cached<=total else None
    commands=[];mcp=[]
    for event in trace:
        if event.get("type")!="item.completed":continue
        item=event.get("item",{})
        if item.get("type")=="command_execution":
            commands.append({"exit":item.get("exit_code"),"command":item.get("command")})
        if item.get("type")=="mcp_tool_call":
            mcp.append({"tool":item.get("tool"),"status":item.get("status")})
    answer=parse_json_answer(last_output(trace))
    quality=score(answer,truth)
    clean=not bool(git(["status","--porcelain"],work))
    outcome={
       "arm":arm,"repeat":n,"seconds":seconds,"exitcode":status,
       "input_total":total,"input_cached":cached,"input_fresh":fresh,
       "output_tokens":usage.get("output_tokens"),
       "reasoning_tokens":usage.get("reasoning_output_tokens"),
       "commands":commands,"mcp_tools":mcp,"no_edits":clean,
       "quality":quality, "exact_acceptance":status==0 and clean and quality["all_semantic_facts"],
       "answer":answer,
    }
    (targetdir/"outcome.json").write_text(json.dumps(outcome,ensure_ascii=False,indent=2))
    print(f"TRIAL {arm}/{n}: {seconds:.2f}s, fresh={fresh}, total={total}, "
          f"accepted={outcome['exact_acceptance']}, union_recall={quality['union_recall']:.2f}, "
          f"tools={mcp}, commands={len(commands)}",flush=True)
    return outcome

def main():
    p=argparse.ArgumentParser(description=__doc__)
    for key in ("source","binary","output","node","codex","lsp-bin","oracle"):
        p.add_argument("--"+key,required=True,type=Path)
    p.add_argument("--model",default="gpt-5.6-sol")
    p.add_argument("--effort",default="low")
    p.add_argument("--repeats",type=int,default=2)
    p.add_argument("--timeout",type=int,default=360)
    o=p.parse_args()
    for key in ("source","binary","output","node","codex","lsp_bin","oracle"):
        setattr(o,key,getattr(o,key).resolve())
    if o.output.exists():p.error("output exists")
    truth=json.loads(o.oracle.read_text())
    # Exact source-derived declaration positions help BOTH arms and avoid
    # misleading zero-reference baselines due to wrong gopls coordinates.
    truth["targets"]=[resolve_go_target(o.source,t) for t in truth["targets"]]
    o.output.mkdir(parents=True)
    (o.output/"context.json").write_text(json.dumps({
        "source_commit":git(["rev-parse","HEAD"],o.source),
        "binary_sha256":hashlib.sha256(o.binary.read_bytes()).hexdigest(),
        "policy_sha256":hashlib.sha256(POLICY.encode()).hexdigest(),
        "oracle_semantic_equality":truth.get("independent_single_graph_comparison"),
        "expected":truth["batch"],"targets":truth["targets"],
    },indent=2,ensure_ascii=False))
    results=[]
    for n in range(1,o.repeats+1):
        order=("baseline","mcp") if n%2 else ("mcp","baseline")
        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as executor:
            futures=[executor.submit(attempt,o,arm,n,truth) for arm in order]
            for future in concurrent.futures.as_completed(futures):
                results.append(future.result())
    (o.output/"results.json").write_text(json.dumps(results,ensure_ascii=False,indent=2))
    print("DONE",o.output,flush=True)

if __name__=="__main__":
    main()