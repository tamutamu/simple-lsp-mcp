#!/usr/bin/env python3
"""One continuous Codex session, three sequential investigations, six targets.

Runs paired arms in isolated same-commit worktrees, identical prompts, and
scores each completed phase against a separate single-target-verified LSP oracle.
Reports only true trace token accounting. The MCP server remains connected
within each Codex exec; startup is included but not privileged in conclusions.
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
from codex_investigation_batch_ab import score

POLICY="""# Sequential source-code impact investigation
Do not change any repository source, config, or tests. A writable workspace
only permits Go/gopls build cache writes. Your task requires THREE distinct
phases in one continuous investigation, in the requested order. Each phase
contains TWO specific Go declarations and requires transitive semantic
references to depth 2, maximum 30 distinct declarations per target.
Never equate text matches with semantic references. Distinguish incomplete
analyses from proven completeness.

If an MCP server named simple-lsp exposes investigate_change, for each of the
three phases use ONE tool call with targets=[{path,symbol_path},...],
depth=2,max_nodes=30,detail=union. Reuse the same tool connection through
the whole task. Avoid redundant searches, code reads, or shell commands when
the results are complete. Without MCP, use normal code-search/gopls tools;
the request includes precise Go declaration coordinates for fair access.

Return one valid JSON object ONLY (no Markdown) after finishing all phases:
{"phases":[
  {"index":1,"results":[
      {"symbol_path":"Name","affected_files":["a.go"],
       "affected_declarations":0,"direct_reference_count":0,"incomplete":true}
    ],"affected_files_union":["a.go"]}
]}
Include phases 1, 2, 3 in order and both targets of each phase. Path values
are workspace-relative, sorted, and do not include target files unless
they are also affected. Use null for counts that cannot be verified.
"""

def capture(o, arm, trial, truth):
    out=o.output/f"{arm}-{trial}"
    out.mkdir(parents=True,exist_ok=False)
    work=out/"checkout"
    clone_checkout(o.source,work,POLICY)
    env=os.environ.copy()
    env["PATH"]=os.pathsep.join([str(o.node.parent),str(o.lsp_bin),env.get("PATH","")])
    batches=[]
    for phase in truth["phases"]:
        exact=", ".join(
            f"{t['path']}::{t['symbol_path']} (line={t['line']}, column={t['column']})"
            for t in phase["targets"])
        batches.append(f"Phase {phase['index']}: {exact}.")
    prompt=("Investigate the following THREE phases as ONE continuous read-only "
            "change-impact investigation. Complete all of them before replying.\n"+
            "\n".join(batches)+
            "\nFor every declaration find its exact affected file set, number "
            "of impacted declarations, direct LSP references, and whether the "
            "depth-limited analysis is incomplete. Return JSON as specified in "
            "AGENTS.md. Do not modify code.")
    cmd=[str(o.node),str(o.codex),"exec","--ignore-user-config","--ephemeral",
         "--json","--skip-git-repo-check","--sandbox","workspace-write",
         "--cd",str(work),"--model",o.model,"--config",'model_reasoning_effort="'+o.effort+'"']
    if arm=="mcp":
        cmd.extend(["-c",'mcp_servers.simple-lsp.command="'+str(o.binary)+'"',
            "-c",'mcp_servers.simple-lsp.args=["--workspace","'+str(work)+'",'
                 '"--tool-profile","investigate","--result-format","text",'
                 '"--request-timeout","90s"]',
            "-c",'mcp_servers.simple-lsp.env.PATH="'+env["PATH"]+'"'])
    cmd.append(prompt)
    start=time.monotonic()
    try:
        process=subprocess.run(cmd,cwd=work,env=env,capture_output=True,
                               text=True,timeout=o.timeout)
        stdout,stderr,code=process.stdout,process.stderr,process.returncode
    except subprocess.TimeoutExpired as exc:
        stdout,stderr=exc.stdout or b"",exc.stderr or b""
        if isinstance(stdout,bytes):stdout=stdout.decode(errors="replace")
        if isinstance(stderr,bytes):stderr=stderr.decode(errors="replace")
        code=124
    elapsed=round(time.monotonic()-start,3)
    (out/"trace.jsonl").write_text(stdout)
    (out/"stderr.txt").write_text(stderr)
    events=[]
    for line in stdout.splitlines():
        try:events.append(json.loads(line))
        except ValueError:continue
    usage={}
    for event in events:
        if event.get("type")=="turn.completed":
            usage=event.get("usage",{})
    total,cached=usage.get("input_tokens"),usage.get("cached_input_tokens")
    fresh=total-cached if isinstance(total,int) and isinstance(cached,int) and 0<=cached<=total else None
    mcp=[];shell=[];seen_threads=set()
    for event in events:
        if event.get("type")=="thread.started":
            seen_threads.add(event.get("thread_id"))
        if event.get("type")!="item.completed":
            continue
        item=event.get("item",{})
        if item.get("type")=="mcp_tool_call":
            mcp.append({"tool":item.get("tool"),"status":item.get("status"),
                        "arguments":item.get("arguments")})
        if item.get("type")=="command_execution":
            shell.append({"command":item.get("command"),"exit_code":item.get("exit_code")})
    answer=parse_json_answer(last_output(events))
    responses=answer.get("phases",[])
    if not isinstance(responses,list):
        responses=[]
    phase_scores=[]
    for phase in truth["phases"]:
        found=[p for p in responses if isinstance(p,dict) and p.get("index")==phase["index"]]
        if len(found)!=1:
            phase_scores.append({"index":phase["index"],"all_semantic_facts":False,
                                 "error":"missing or duplicate phase"})
            continue
        result=score(found[0],{"batch":phase["result"],"targets":phase["targets"]})
        phase_scores.append({"index":phase["index"],**result})
    no_edits=not bool(git(["status","--porcelain"],work))
    accepted=(code==0 and no_edits and len(responses)==len(truth["phases"]) and
             all(x["all_semantic_facts"] for x in phase_scores))
    row={"arm":arm,"trial":trial,"seconds":elapsed,"exitcode":code,
         "input_total":total,"input_cached":cached,"input_fresh":fresh,
         "output_tokens":usage.get("output_tokens"),
         "reasoning_tokens":usage.get("reasoning_output_tokens"),
         "mcp_calls":mcp,"shell_calls":shell,"thread_ids":sorted(t for t in seen_threads if t),
         "no_edits":no_edits,"phase_scores":phase_scores,
         "accepted":accepted,"answer":answer}
    (out/"outcome.json").write_text(json.dumps(row,ensure_ascii=False,indent=2))
    print(f"{arm}/{trial}: {elapsed}s, fresh={fresh}, total={total}, "
          f"accepted={accepted}, phases={sum(x['all_semantic_facts'] for x in phase_scores)}/3, "
          f"mcp={len(mcp)}, shell={len(shell)}",flush=True)
    return row

def main():
    p=argparse.ArgumentParser(description=__doc__)
    for name in ("source","binary","output","node","codex","lsp-bin","oracle"):
        p.add_argument("--"+name,required=True,type=Path)
    p.add_argument("--repeats",type=int,default=1)
    p.add_argument("--reverse",action="store_true",help="Reverse paired-arm start order")
    p.add_argument("--timeout",type=int,default=540)
    p.add_argument("--model",default="gpt-5.6-sol")
    p.add_argument("--effort",default="low")
    o=p.parse_args()
    for name in ("source","binary","output","node","codex","lsp_bin","oracle"):
        setattr(o,name,getattr(o,name).resolve())
    if o.output.exists():p.error("output exists")
    truth=json.loads(o.oracle.read_text())
    if not truth.get("independently_verified") or len(truth["phases"])!=3:
        p.error("requires three independently verified phases")
    for phase in truth["phases"]:
        if len(phase["targets"])!=2:
            p.error("every phase must contain two declarations")
    o.output.mkdir(parents=True)
    (o.output/"context.json").write_text(json.dumps({
        "source_commit":git(["rev-parse","HEAD"],o.source),
        "binary_sha256":hashlib.sha256(o.binary.read_bytes()).hexdigest(),
        "policy_sha256":hashlib.sha256(POLICY.encode()).hexdigest(),
        "source_files":truth["source_files"],
        "expected":truth["phases"]
    },ensure_ascii=False,indent=2))
    rows=[]
    for trial in range(1,o.repeats+1):
        arms=("baseline","mcp") if trial%2 else ("mcp","baseline")
        if o.reverse:
            arms=tuple(reversed(arms))
        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
            futures=[pool.submit(capture,o,arm,trial,truth) for arm in arms]
            for future in concurrent.futures.as_completed(futures):
                rows.append(future.result())
    (o.output/"results.json").write_text(json.dumps(rows,indent=2,ensure_ascii=False))
    print("DONE",str(o.output),flush=True)

if __name__=="__main__":
    main()