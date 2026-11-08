#!/usr/bin/env python3
"""Compare transactionally applied multi-interface rename with Codex's edit diff."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
from direct_semantic_rename_probe import Client

def checked(command,cwd,stdin=None):
    return subprocess.run(command,cwd=cwd,input=stdin,capture_output=True,check=True)

def main():
    p=argparse.ArgumentParser()
    p.add_argument("--source",type=Path,required=True)
    p.add_argument("--binary",type=Path,required=True)
    p.add_argument("--work",type=Path,required=True)
    p.add_argument("--output",type=Path,required=True)
    o=p.parse_args()
    source=o.source.resolve(); dst=o.work.resolve()
    if dst.exists():p.error("workdir already exists")
    dst.mkdir(parents=True)
    checked(["tar","-x","-C",str(dst)],source,checked(["git","archive","HEAD"],source).stdout)
    for cmd in (["git","init","-q"],["git","add","-A"],
                ["git","-c","user.name=Bench","-c","user.email=bench@example.invalid","commit","-qm","base"]):
        checked(cmd,dst)
    targets=[
        {"path":"api/client/client.go","symbol_path":"Client/EmitAuditEvent"},
        {"path":"api/types/events/api.go","symbol_path":"Emitter/EmitAuditEvent"},
        {"path":"lib/events/api.go","symbol_path":"IAuditLog/EmitAuditEvent"},
    ]
    client=Client(str(o.binary.resolve()),str(dst),"coding",90)
    try:
        result,ms,size=client.request("tools/call",{
            "name":"rename_symbol","arguments":{
                "targets":targets,"new_name":"RecordAuditEvent","apply":True,
            }
        })
        if result.get("applied") is not True:
            raise RuntimeError("batch edit was not applied")
        diff=checked(["git","diff","--binary"],dst).stdout
        checked(["git","diff","--check"],dst)
        paths=checked(["git","diff","--name-only"],dst).stdout.decode().splitlines()
        output={"time_ms":round(ms,2),"response_bytes":size,
             "files":result["file_count"],"edits":result["edit_count"],
             "per_target":result.get("target_edit_counts"),
             "paths":paths,
             "diff_sha256":hashlib.sha256(diff).hexdigest()}
        o.output.write_text(json.dumps(output,ensure_ascii=False,indent=2))
        print(json.dumps({k:v for k,v in output.items() if k!="paths"},indent=2),flush=True)
    finally:
        client.close()

if __name__=="__main__":main()
