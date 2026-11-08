#!/usr/bin/env python3
"""Real Codex A/B: impact understanding, 45-file interface rename, verification.

Both arms have gopls CLI; MCP arm additionally has standard coding tool catalog.
Same pinned source, same AGENTS.md, same Go caches and permissions.
Ground correctness in independently applied LSP WorkspaceEdit and git diff.
"""
import argparse
import concurrent.futures
import hashlib
import json
import os
from pathlib import Path
import subprocess
import time

from codex_investigation_ab import clone_checkout, last_output, parse_json_answer

POLICY="""# Semantic refactoring task
Do not edit AGENTS.md, dependency manifests or unrelated files.
Keep changes semantic: rename the exact Go interface method and all
implementations and call sites. Do not blindly replace same-spelled text.
Use whichever semantic code tools are available, including gopls when
appropriate; do not do redundant full-file reads if a rename tool succeeds.
Do not modify test assertions beyond references to the renamed identifier.

Verify the result with git diff --check and only these scoped compile checks:
  go test -run '^$' ./lib/events
  (cd api && go test -run '^$' ./types/events ./client)
Do not run go test ./... or install dependencies.
Do not replace GOCACHE with a fresh temporary cache: keep the inherited
per-checkout Go build cache. If build-tag dependencies are missing,
report that limitation instead of installing them.

When done return a short JSON object (without Markdown):
{"status":"completed","changed_files":0,"verification":"summary"}
"""

def run_git(args, cwd):
    return subprocess.run(["git",*args],cwd=cwd,capture_output=True,text=True,check=True).stdout

def attempt(o, arm, n, oracle):
    output=o.output/f"{arm}-{n}"
    output.mkdir(parents=True)
    work=output/"checkout"
    clone_checkout(o.source,work,POLICY)
    cache=work/".bench-cache"
    (cache/"tmp").mkdir(parents=True)
    with (work/".git"/"info"/"exclude").open("a") as f:
        f.write("\n.bench-cache/\n")
    env=os.environ.copy()
    env["PATH"]=os.pathsep.join([str(o.node.parent),str(o.lsp_bin),env.get("PATH","")])
    env["GOCACHE"]=str(cache/"go-build")
    env["XDG_CACHE_HOME"]=str(cache/"xdg")
    env["TMPDIR"]=str(cache/"tmp")
    prompt=("In this Go repository, rename the IAuditLog interface method "
        "EmitAuditEvent to RecordAuditEvent throughout the full repository. "
        "The declaration is lib/events/api.go::IAuditLog/EmitAuditEvent. "
        "It is an interface method implemented by several concrete types, "
        "including lib/events/dynamoevents/dynamoevents.go. "
        "First identify the impact, then apply a semantic rename across "
        "all implementations and call sites, then verify the result. "
        "Do not stop at modifying the single interface declaration. "
        "Preserve behavior and unrelated identifiers. No changes to dependencies. "
        "Do not run broad tests or install packages. "
        "Return only the requested JSON summary.")
    cmd=[str(o.node),str(o.codex),"exec","--ignore-user-config","--ephemeral",
         "--json","--skip-git-repo-check","--approve-for-me",
         "--cd",str(work),"--model",o.model,
         "--config",'model_reasoning_effort="'+o.effort+'"',
         "--config","shell_environment_policy.inherit=all"]
    if arm=="mcp":
        cmd.extend(["-c",'mcp_servers.simple-lsp.command="'+str(o.binary)+'"',
          "-c",'mcp_servers.simple-lsp.args=["--workspace","'+str(work)+'",'
               '"--tool-profile","coding","--result-format","text","--request-timeout","90s"]',
          "-c",'mcp_servers.simple-lsp.env.PATH="'+env["PATH"]+'"'])
    cmd.append(prompt)
    started=time.monotonic()
    try:
        result=subprocess.run(cmd,cwd=work,env=env,capture_output=True,text=True,timeout=o.timeout)
        stdout,stderr,exitcode=result.stdout,result.stderr,result.returncode
    except subprocess.TimeoutExpired as e:
        stdout,stderr=e.stdout or b"",e.stderr or b""
        if isinstance(stdout,bytes):stdout=stdout.decode(errors="replace")
        if isinstance(stderr,bytes):stderr=stderr.decode(errors="replace")
        exitcode=124
    seconds=round(time.monotonic()-started,3)
    (output/"trace.jsonl").write_text(stdout)
    (output/"stderr.txt").write_text(stderr)
    events=[]
    for line in stdout.splitlines():
        try: events.append(json.loads(line))
        except (ValueError,TypeError):pass
    usage={}
    for event in events:
        if event.get("type")=="turn.completed":usage=event.get("usage",{})
    total,cached=usage.get("input_tokens"),usage.get("cached_input_tokens")
    fresh=total-cached if isinstance(total,int) and isinstance(cached,int) and 0<=cached<=total else None
    tools=[];shell=[];environment_errors=[]
    for event in events:
        if event.get("type")!="item.completed":continue
        item=event.get("item",{})
        if item.get("type")=="mcp_tool_call":
            tools.append({"tool":item.get("tool"),"status":item.get("status"),
                "arguments":item.get("arguments"),"error":item.get("error")})
        if item.get("type")=="command_execution":
            out=item.get("aggregated_output") or ""
            shell.append({"command":item.get("command"),"exit":item.get("exit_code")})
            if any(x in out for x in ("go/packages.Load","read-only file system","initial workspace load failed")):
                environment_errors.append({"command":item.get("command"),"error_excerpt":out[:400]})
    diff=subprocess.run(["git","diff","--binary"],cwd=work,capture_output=True,check=True).stdout
    changed=run_git(["diff","--name-only"],work).strip().splitlines()
    check=subprocess.run(["git","diff","--check"],cwd=work,capture_output=True,text=True)
    sha=hashlib.sha256(diff).hexdigest()
    answer=parse_json_answer(last_output(events))
    exact=(sha==oracle["sha256"] and changed==oracle["changed_files"])
    expected_files=set(o.expected_files)
    touched=set(changed)
    bpf=(work/"lib/bpf/bpf.go").read_text()
    tagged_uses_fixed=".EmitAuditEvent(" not in bpf and "RecordAuditEvent(" in bpf
    interface_converted=all(
        "RecordAuditEvent(" in (work/rel).read_text() and
        "EmitAuditEvent(" not in (work/rel).read_text()
        for rel in ("api/types/events/api.go","lib/events/api.go")
    )
    quality_env=env.copy()
    for key in ("GOCACHE","XDG_CACHE_HOME","TMPDIR"):
        quality_env.pop(key,None)
    compile_checks={}
    if exitcode==124:
        for label in ("main","api"):
            compile_checks[label]={"success":False,"skipped":"Codex timed out; independent compilation not run"}
    else:
        for label,directory,packages in (
        ("main",work,["./lib/events"]),
        ("api",work/"api",["./types/events","./client"]),
        ):
            try:
                check_result=subprocess.run(["go","test","-run","^$",*packages],
                    cwd=directory,env=quality_env,capture_output=True,text=True,timeout=45)
                compile_checks[label]={"success":check_result.returncode==0,
                                       "error_tail":(check_result.stderr or "")[-300:]}
            except subprocess.TimeoutExpired:
                compile_checks[label]={"success":False,"error_tail":"compiler timeout"}
    substantive_success=(
        exitcode==0 and check.returncode==0 and not environment_errors
        and touched==expected_files and tagged_uses_fixed and interface_converted
        and all(v["success"] for v in compile_checks.values())
    )
    row={"arm":arm,"trial":n,"seconds":seconds,"exitcode":exitcode,
       "input_total":total,"input_cached":cached,"input_fresh":fresh,
       "output_tokens":usage.get("output_tokens"),
       "calls_mcp":tools,"calls_shell":shell,
       "environment_errors":environment_errors,
       "changed_files":changed,"changed_file_count":len(changed),
       "diff_sha256":sha,"oracle_sha256":oracle["sha256"],
       "exact_semantic_diff":exact,"diff_check":check.returncode==0,
       "expected_file_count":len(expected_files),
       "changed_files_cover_expected":touched==expected_files,
       "missing_files":sorted(expected_files-touched),
       "unexpected_files":sorted(touched-expected_files),
       "tagged_uses_fixed":tagged_uses_fixed,
       "interface_declarations_converted":interface_converted,
       "compile_checks":compile_checks,
       "accepted":substantive_success,
       "answer":answer}
    (output/"outcome.json").write_text(json.dumps(row,ensure_ascii=False,indent=2))
    print(f"{arm}/{n}: {seconds}s, files={len(changed)}, oracle={exact}, "
          f"fresh={fresh}, total={total}, accepted={substantive_success}, mcp={[x['tool'] for x in tools]}, "
          f"shell={len(shell)}, environment_errors={len(environment_errors)}",flush=True)
    return row

def main():
    p=argparse.ArgumentParser(description=__doc__)
    for name in ("source","binary","output","node","codex","lsp-bin","oracle"):
        p.add_argument("--"+name,type=Path,required=True)
    p.add_argument("--model",default="gpt-5.6-sol")
    p.add_argument("--effort",default="low")
    p.add_argument("--timeout",type=int,default=300)
    p.add_argument("--repeats",type=int,default=1)
    p.add_argument("--batch-result",type=Path,required=True,
        help="independent, semantically validated 48-file batch LSP oracle")
    o=p.parse_args()
    for name in ("source","binary","output","node","codex","lsp_bin","oracle"):
        setattr(o,name,getattr(o,name).resolve())
    if o.output.exists():p.error("output already exists")
    oracle=json.loads(o.oracle.read_text())
    batch=json.loads(o.batch_result.read_text())
    if batch["files"]!=48 or batch["edits"]!=162:
        p.error("wrong batch oracle result")
    o.expected_files=sorted(set(batch["paths"])|{"lib/bpf/bpf.go"})
    o.output.mkdir(parents=True)
    (o.output/"context.json").write_text(json.dumps({
        "source_commit":oracle["source_commit"],
        "oracle_sha256":oracle["sha256"],
        "oracle_file_count":oracle["file_count"],
        "oracle_edit_count":oracle["edit_count"],
        "expected_complete_file_count":len(o.expected_files),
        "expected_complete_files":o.expected_files,
        "acceptance":"all expected files, no extras, tagged references fixed, interface declarations changed, both module compiles and clean diff",
        "binary_sha256":hashlib.sha256(o.binary.read_bytes()).hexdigest(),
        "policy_sha256":hashlib.sha256(POLICY.encode()).hexdigest(),
    },indent=2))
    allrows=[]
    for n in range(1,o.repeats+1):
        arms=("baseline","mcp") if n%2 else ("mcp","baseline")
        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
            futures=[pool.submit(attempt,o,arm,n,oracle) for arm in arms]
            for future in concurrent.futures.as_completed(futures):
                allrows.append(future.result())
    (o.output/"results.json").write_text(json.dumps(allrows,indent=2,ensure_ascii=False))

if __name__=="__main__":
    main()
