#!/usr/bin/env python3
"""Compare Codex read-only impact analysis with and without LSP's composite tool.

This is a paired observational coding-agent study, not a direct byte-to-token
conversion. Each trial has the same task, source SHA, instructions and model.
The LSP oracle is an independently executed semantic reference graph, not an
LLM's self-reported impression of correctness.
"""
import argparse
import concurrent.futures
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import time

POLICY = """# Read-only code investigation benchmark
Do not modify, create, or remove any project source file.
Your task is to identify the functions and files potentially impacted by a
change to one exact declaration up to two semantic reference hops.
Use semantic identity, not mere matching identifier strings.

When an MCP server named simple-lsp exposes investigate_change, call this
single high-level LSP tool with the supplied path, symbol_path, depth=2 and
max_nodes=30. Use its affected_files, counts and meta as evidence; do not
reopen all affected files or repeat the same search unless the output reports
an error. This tool is read-only and reports static limitations explicitly.
Without that tool, use your ordinary code-search and static inspection tools.

Return precisely one JSON object with fields:
  affected_files: an array of workspace-relative source file paths, sorted;
  affected_declarations: integer, number of distinct related declarations;
  direct_reference_count: integer, number of direct references;
  incomplete: boolean, whether the bounded traversal has limits.
Don't guess a missing fact: use null for unknown numbers and empty array if
you cannot identify any candidate path. All counts must have evidence.
"""
SUFFIX = """Investigate how changing the declaration at {path}::{symbol} could
impact dependent code, following up to 2 LSP-reference hops and stopping at
30 unique declarations. Include the direct and indirect dependent
declarations, but NOT the original target in affected_files. Return the
actual paths, distinct declaration count, exact number of direct references
to the target, and whether traversal was bounded/incomplete. No code edits.
Only output the required JSON object."""

def git(cmd,cwd):
    return subprocess.run(["git",*cmd],cwd=cwd,capture_output=True,text=True,check=True).stdout.strip()

def clone_checkout(source,target,policy=POLICY):
    if target.exists():raise RuntimeError("refusing to overwrite "+str(target))
    target.mkdir(parents=True)
    archive=subprocess.Popen(["git","archive","HEAD"],cwd=source,stdout=subprocess.PIPE)
    copied=subprocess.run(["tar","-x","-C",str(target)],stdin=archive.stdout,capture_output=True)
    archive.stdout.close()
    if archive.wait() or copied.returncode:
        raise RuntimeError("git archive or extraction failed: "+copied.stderr.decode(errors="replace"))
    for artifact in ("AGENTS.md","CLAUDE.md",".codex",".claude",".mcp.json"):
        p=target/artifact
        if p.is_dir():
            import shutil
            shutil.rmtree(p)
        elif p.exists():p.unlink()
    (target/"AGENTS.md").write_text(policy,encoding="utf8")
    git(["init","-q"],target)
    git(["add","-A"],target)
    git(["-c","user.name=Bench","-c","user.email=bench@example.invalid","commit","-qm","fixed-source"],target)

def last_output(trace):
    agent=[]
    for item in trace:
        if item.get("type")=="item.completed" and item.get("item",{}).get("type")=="agent_message":
            agent.append(item["item"].get("text",""))
    return agent[-1] if agent else ""

def parse_json_answer(raw):
    value=raw.strip()
    if value.startswith("```"):
        value=re.sub(r"^```(?:json)?\\s*|\\s*```$", "",value).strip()
    try:
        answer=json.loads(value)
    except json.JSONDecodeError:
        return {"raw":value[:500], "parsed":False}
    return answer if isinstance(answer,dict) else {"raw":value[:500],"parsed":False}

def attempt(o,arm,n,expected):
    output=o.output/f"{arm}-{n}"
    output.mkdir(parents=True,exist_ok=False)
    work=output/"checkout"
    clone_checkout(o.source,work)
    env=os.environ.copy()
    env["PATH"]=os.pathsep.join([str(o.node.parent),str(o.lsp_bin),env.get("PATH","")])
    # The agent must not edit source, but gopls/Go need temp build-cache writes.
    # Both arms get the same sandbox and must pass the git-clean condition.
    prompt=SUFFIX.format(path=o.path,symbol=o.symbol)
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
        result=subprocess.run(cmd,cwd=work,env=env,capture_output=True,text=True,timeout=o.timeout)
        stdout,stderr,exitcode=result.stdout,result.stderr,result.returncode
    except subprocess.TimeoutExpired as err:
        stdout=err.stdout or b""
        stderr=err.stderr or b""
        if isinstance(stdout,bytes):stdout=stdout.decode(errors="replace")
        if isinstance(stderr,bytes):stderr=stderr.decode(errors="replace")
        exitcode=124
    elapsed=round(time.monotonic()-start,3)
    (output/"trace.jsonl").write_text(stdout)
    (output/"stderr.txt").write_text(stderr)
    trace=[]
    for line in stdout.splitlines():
        try: trace.append(json.loads(line))
        except (ValueError,TypeError): continue
    usage={}
    for item in trace:
        if item.get("type")=="turn.completed":usage=item.get("usage",{})
    total=usage.get("input_tokens")
    cached=usage.get("cached_input_tokens")
    fresh=total-cached if isinstance(total,int) and isinstance(cached,int) and 0<=cached<=total else None
    commands=[]
    tools=[]
    for item in trace:
        if item.get("type")!="item.completed":continue
        rec=item.get("item",{})
        if rec.get("type")=="command_execution":commands.append(rec.get("command"))
        if rec.get("type")=="mcp_tool_call":
            tools.append({"tool":rec.get("tool"),"status":rec.get("status"),"server":rec.get("server")})
    answer=parse_json_answer(last_output(trace))
    paths=answer.get("affected_files",[])
    if not isinstance(paths,list):paths=[]
    truth=set(expected["paths"])
    candidate=set(str(x) for x in paths)
    precision=len(candidate&truth)/len(candidate) if candidate else (1 if not truth else 0)
    recall=len(candidate&truth)/len(truth) if truth else (1 if not candidate else 0)
    counts_correct={name:answer.get(name)==expected[name] for name in
          ("direct_reference_count","affected_declarations","incomplete")}
    no_edits=not git(["status","--porcelain"],work)
    row={
        "arm":arm,"repeat":n,"seconds":elapsed,"exitcode":exitcode,
        "input_total":total,"input_cached":cached,"input_fresh":fresh,
        "output_tokens":usage.get("output_tokens"),
        "reasoning_tokens":usage.get("reasoning_output_tokens"),
        "commands":len(commands),"mcp_tools":tools,"no_edits":no_edits,
        "path_precision":precision,"path_recall":recall,
        "counts_correct":counts_correct,"exact_acceptance":
           exitcode==0 and no_edits and candidate==truth and all(counts_correct.values()),
        "answer":answer,
    }
    (output/"outcome.json").write_text(json.dumps(row,ensure_ascii=False,indent=2))
    print(f"TRIAL {arm} {n} {elapsed}s total={total} fresh={fresh} "
          f"accept={row['exact_acceptance']} precision={precision:.2f} recall={recall:.2f} tools={tools}",flush=True)
    return row

def main():
    ap=argparse.ArgumentParser()
    for k in ("source","binary","output","node","codex","lsp-bin","oracle"):
        ap.add_argument("--"+k,required=True,type=Path)
    ap.add_argument("--path",required=True)
    ap.add_argument("--symbol",required=True)
    ap.add_argument("--model",default="gpt-5.6-sol")
    ap.add_argument("--effort",default="low")
    ap.add_argument("--repeats",type=int,default=1)
    ap.add_argument("--timeout",type=int,default=240)
    args=ap.parse_args()
    o=args
    for k in ("source","binary","output","node","codex","lsp_bin","oracle"):
        setattr(o,k,getattr(o,k).resolve())
    if o.output.exists():ap.error("output directory already exists")
    truth=json.loads(o.oracle.read_text())
    sample=truth["samples"]["composite"][0]
    expected={
        "paths":sample["affected_paths"],
        "direct_reference_count":truth["direct_reference_count"],
        "affected_declarations":truth["affected_declarations"],
        "incomplete":truth["incomplete_traversal"],
    }
    o.output.mkdir(parents=True)
    (o.output/"context.json").write_text(json.dumps({
        "source_commit":git(["rev-parse","HEAD"],o.source),
        "model":o.model,"policy_sha256":hashlib.sha256(POLICY.encode()).hexdigest(),
        "binary_sha256":hashlib.sha256(o.binary.read_bytes()).hexdigest(),
        "expected":expected,
    },ensure_ascii=False,indent=2))
    allrows=[]
    # Pair arms concurrently within each repeat, alternate start order.
    for n in range(1,o.repeats+1):
        order=("baseline","mcp") if n%2 else ("mcp","baseline")
        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
            futures=[pool.submit(attempt,o,arm,n,expected) for arm in order]
            allrows.extend(f.result() for f in concurrent.futures.as_completed(futures))
    (o.output/"results.json").write_text(json.dumps(allrows,ensure_ascii=False,indent=2))
    print("DONE",o.output,flush=True)

if __name__=="__main__":
    main()