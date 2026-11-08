#!/usr/bin/env python3
"""Scenario-first Codex A/B. User tickets do not mention or mandate LSP.

Control: normal shell/editor, including gopls CLI when voluntarily selected.
Treatment: identical setup plus coding-profile simple-lsp MCP.
Grade independent declared facts and report correct answers, false positives,
evidence, token cost, time and failure; don't discard losing or failed trials.
"""
from __future__ import annotations

import argparse
import concurrent.futures
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import time

from codex_investigation_ab import clone_checkout, git, last_output, parse_json_answer

POLICY = """# Repository investigation and review
Treat source as read-only. Do not change, generate, or delete tracked files.
You may use the available code-navigation, search, compile and diagnostic
tools as useful. Check exact declarations and semantic relationships, not
just matching text. Avoid running all tests or installing dependencies.
A source name alone does not prove that a method implements a contract.
If findings are uncertain, state the limitation without guessing.

Respond with ONE JSON object and no Markdown:
{"findings":[{"path":"relative/path.go","receiver":"TypeName",
"method":"MethodName","reason":"short evidence-backed explanation"}],
"limitations":["important scope/coverage cautions"]}
Omit false positives. Paths must be relative to the repository root.
"""

BAD_ENV = ("go/packages.Load", "initial workspace load failed",
           "read-only file system", "gopls cannot access its persistent index")


def normalized_finding(record: dict) -> tuple[str,str,str] | None:
    if not isinstance(record,dict):return None
    path=record.get("path")
    receiver=record.get("receiver")
    method=record.get("method")
    if not all(isinstance(x,str) for x in (path,receiver,method)):
        return None
    receiver=receiver.strip("*() ")
    return (path.replace("\\","/").removeprefix("./"),receiver,method)


def check_oracle_source(root: Path, cases: list[dict]) -> None:
    for case in cases:
        for record in case["expected_findings"]:
            path=root/record["path"]
            if not path.is_file():
                raise RuntimeError(f"{case['id']}: missing oracle file {path}")
            source=path.read_text(errors="replace")
            # Source-derived declaration, NOT the LSP output under test.
            expression=(r"func\s*\(\s*(?:\w+\s+)?\*?"+re.escape(record["receiver"])+
                        r"\s*\)\s*"+re.escape(record["method"])+r"\s*\(")
            if not re.search(expression,source):
                raise RuntimeError(f"{case['id']}: missing declared receiver method {record}")


def grade(answer: dict, scenario: dict) -> dict:
    expected={normalized_finding(x) for x in scenario["expected_findings"]}
    records=answer.get("findings",[])
    if not isinstance(records,list):records=[]
    valid=[normalized_finding(x) for x in records]
    actual={x for x in valid if x is not None}
    reason_count=sum(bool(isinstance(x,dict) and isinstance(x.get("reason"),str)
                          and x["reason"].strip()) for x in records)
    true_positive=len(actual & expected)
    missing=sorted(expected - actual)
    unexpected=sorted(actual - expected)
    precision=true_positive/len(actual) if actual else 0.0
    recall=true_positive/len(expected) if expected else 1.0
    structure_ok=(len(records)==len(actual) and reason_count==len(records)
                  and isinstance(answer.get("limitations",[]),list))
    anchors_found=expected.issubset(actual) and structure_ok
    answer_text=json.dumps(answer,ensure_ascii=False)
    review_facts_covered=(
        scenario.get("quality_mode")=="review_facts"
        and all(method in answer_text for _,_,method in expected)
        and ("テスト" in answer_text or "test" in answer_text.lower())
        and isinstance(answer.get("limitations"),list)
    )
    return {"exact_found":actual==expected and structure_ok,
            "review_facts_covered":review_facts_covered,
            "required_anchors_found":anchors_found,
            "quality_mode":scenario.get("quality_mode","exact"),
            "precision":precision,"recall":recall,
            "missing":[list(x) for x in missing],
            "unexpected":[list(x) for x in unexpected],
            "all_have_source_reasons":reason_count==len(records),
            "structure_ok":structure_ok}


def verify_reported_declarations(root: Path, answer: dict) -> dict:
    """Reject made-up method locations, independently of the LSP response.

    This proves that the declared function/method exists, not that the
    explanation is true or that the function belongs to the call path.
    """
    root=root.resolve()
    invalid=[]
    records=answer.get("findings",[])
    if not isinstance(records,list):records=[]
    for item in records:
        f=normalized_finding(item)
        if f is None:
            invalid.append(["invalid",str(item)[:120]])
            continue
        path,receiver,method=f
        file=(root/path).resolve()
        if not file.is_relative_to(root) or not file.is_file() or file.suffix!=".go":
            invalid.append([path,receiver,method])
            continue
        source=file.read_text(errors="replace")
        if receiver:
            expr=(r"func\s*\(\s*(?:\w+\s+)?\*?"+
                  re.escape(receiver)+r"\s*\)\s*"+re.escape(method)+r"\s*\(")
        else:
            expr=r"func\s+"+re.escape(method)+r"\s*\("
        if not re.search(expr,source):
            invalid.append([path,receiver,method])
    return {"source_declarations_verified":len(invalid)==0,"invalid":invalid}


def attempt(o, scenario:dict, arm:str, trial:int) -> dict:
    out=o.output/scenario["id"]/f"{arm}-{trial}"
    out.mkdir(parents=True,exist_ok=False)
    work=out/"checkout"
    clone_checkout(o.source,work,POLICY)
    cache=work/".bench-cache"
    for name in ("go-build","xdg","tmp"):
        (cache/name).mkdir(parents=True,exist_ok=True)
    with (work/".git/info/exclude").open("a",encoding="utf8") as f:
        f.write("\n.bench-cache/\n")
    env=os.environ.copy()
    env["PATH"]=os.pathsep.join([str(o.node.parent),str(o.lsp_bin),env.get("PATH","")])
    env["HOME"]=str(o.home)
    env["GOCACHE"]=str(cache/"go-build")
    env["XDG_CACHE_HOME"]=str(cache/"xdg")
    env["TMPDIR"]=str(cache/"tmp")
    cmd=[str(o.node),str(o.codex),"exec","--ignore-user-config","--ephemeral",
         "--json","--skip-git-repo-check","--sandbox","workspace-write",
         "--cd",str(work),"--model",o.model,
         "--config",f'model_reasoning_effort="{o.effort}"',
         "--config","shell_environment_policy.inherit=all"]
    if arm=="mcp":
        cmd.extend(["-c",'mcp_servers.simple-lsp.command="'+str(o.binary)+'"',
           "-c",'mcp_servers.simple-lsp.args=["--workspace","'+str(work)+
                 '","--tool-profile","'+o.mcp_profile+
                 '","--result-format","text","--request-timeout","90s"]',
           "-c",'mcp_servers.simple-lsp.env.PATH="'+env["PATH"]+'"'])
    cmd.append(scenario["user_ticket"]+
        "\n回答はAGENTS.mdのJSON形式に従い、証拠に基づく実装箇所だけを挙げてください。")
    started=time.monotonic()
    try:
        process=subprocess.run(cmd,cwd=work,env=env,capture_output=True,text=True,timeout=o.timeout)
        stdout,stderr,code=process.stdout,process.stderr,process.returncode
    except subprocess.TimeoutExpired as error:
        stdout,stderr=error.stdout or b"",error.stderr or b""
        if isinstance(stdout,bytes):stdout=stdout.decode(errors="replace")
        if isinstance(stderr,bytes):stderr=stderr.decode(errors="replace")
        code=124
    elapsed=round(time.monotonic()-started,3)
    (out/"trace.jsonl").write_text(stdout)
    (out/"stderr.txt").write_text(stderr)
    events=[]
    for line in stdout.splitlines():
        try:events.append(json.loads(line))
        except (ValueError,TypeError):pass
    usage={}
    for event in events:
        if event.get("type")=="turn.completed":usage=event.get("usage",{})
    total,cached=usage.get("input_tokens"),usage.get("cached_input_tokens")
    fresh=(total-cached if isinstance(total,int) and isinstance(cached,int)
           and 0<=cached<=total else None)
    mcp=[];shell=[];environment_errors=[]
    for event in events:
        if event.get("type")!="item.completed":continue
        item=event.get("item",{})
        if item.get("type")=="mcp_tool_call":
            mcp.append({"name":item.get("tool"),"status":item.get("status"),
                        "arguments":item.get("arguments")})
        if item.get("type")=="command_execution":
            output=item.get("aggregated_output") or ""
            shell.append({"command":item.get("command"),"exit_code":item.get("exit_code"),
                          "output_bytes":len(output.encode("utf8"))})
            if any(bad in output for bad in BAD_ENV):
                environment_errors.append({"command":item.get("command"),
                                           "excerpt":output[:300]})
    answer=parse_json_answer(last_output(events))
    score=grade(answer,scenario)
    source_checks=verify_reported_declarations(work,answer)
    clean=not bool(git(["status","--porcelain"],work))
    mode=scenario.get("quality_mode","exact")
    required=(score["required_anchors_found"] if mode=="required_anchors" else
              score["review_facts_covered"] if mode=="review_facts" else
              score["exact_found"])
    acceptance=(code==0 and clean and not environment_errors and required
                and source_checks["source_declarations_verified"])
    row={"scenario":scenario["id"],"arm":arm,"trial":trial,
         "seconds":elapsed,"exitcode":code,
         "input_total":total,"input_cached":cached,"input_fresh":fresh,
         "output_tokens":usage.get("output_tokens"),
         "commands":shell,"mcp_calls":mcp,"no_source_edits":clean,
         "environment_errors":environment_errors,
         "quality":score,"source_checks":source_checks,
         "human_review_required":bool(scenario.get("human_review_facts")),
         "accepted":acceptance,"answer":answer}
    (out/"outcome.json").write_text(json.dumps(row,ensure_ascii=False,indent=2))
    print(f"{scenario['id']} {arm}/{trial}: {elapsed}s, fresh={fresh}, total={total}, "
          f"pass={acceptance}, recall={score['recall']:.2f}, precision={score['precision']:.2f}, "
          f"shell={len(shell)}, mcp={len(mcp)}",flush=True)
    return row


def main():
    p=argparse.ArgumentParser(description=__doc__)
    for key in ("source","binary","output","node","codex","lsp-bin"):
        p.add_argument("--"+key,required=True,type=Path)
    p.add_argument("--catalog",type=Path,default=Path(__file__).with_name("developer_scenarios.json"))
    p.add_argument("--scenarios",default="all",help="comma-separated IDs or 'all'")
    p.add_argument("--repeats",type=int,default=1)
    p.add_argument("--timeout",type=int,default=240)
    p.add_argument("--model",default="gpt-5.6-sol")
    p.add_argument("--effort",default="low")
    p.add_argument("--mcp-profile",choices=("coding","full"),default="coding")
    o=p.parse_args()
    for key in ("source","binary","output","node","codex","lsp_bin","catalog"):
        setattr(o,key,getattr(o,key).resolve())
    o.home=Path.home()
    if o.output.exists():p.error("output directory already exists")
    all_cases=json.loads(o.catalog.read_text())["scenes"]
    chosen=all_cases if o.scenarios=="all" else [
        item for item in all_cases if item["id"] in o.scenarios.split(",")]
    if not chosen or len(chosen)!=len(o.scenarios.split(",")) and o.scenarios!="all":
        p.error("unknown or duplicate scene ID")
    check_oracle_source(o.source,chosen)
    o.output.mkdir(parents=True)
    (o.output/"context.json").write_text(json.dumps({
        "source_commit":git(["rev-parse","HEAD"],o.source),
        "model":o.model,"effort":o.effort,"profile":o.mcp_profile,
        "policy_sha256":hashlib.sha256(POLICY.encode()).hexdigest(),
        "binary_sha256":hashlib.sha256(o.binary.read_bytes()).hexdigest(),
        "catalog_sha256":hashlib.sha256(o.catalog.read_bytes()).hexdigest(),
        "scenario_ids":[s["id"] for s in chosen],
        "comparison":"normal shell/gopls-cli vs normal shell/gopls-cli + optional MCP",
        "no_mandatory_lsp":True
    },ensure_ascii=False,indent=2))
    results=[]
    for scenario in chosen:
        for n in range(1,o.repeats+1):
            order=("baseline","mcp") if n%2 else ("mcp","baseline")
            with concurrent.futures.ThreadPoolExecutor(max_workers=2) as executor:
                futures=[executor.submit(attempt,o,scenario,arm,n) for arm in order]
                results.extend(f.result() for f in concurrent.futures.as_completed(futures))
    (o.output/"results.json").write_text(json.dumps(results,indent=2,ensure_ascii=False))
    print("DONE",o.output,flush=True)

if __name__=="__main__":
    main()