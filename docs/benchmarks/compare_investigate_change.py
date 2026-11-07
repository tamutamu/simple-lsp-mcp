#!/usr/bin/env python3
"""Paired real-LSP comparison: two primitive calls versus one composite call.

Both paths use the same LSP server session, workspace, symbol, depth and node
budget. Alternating order helps expose cache/order effects. Actual MCP reply
bytes are measured; this does not estimate model tokens or agent wall time.
"""
import argparse
import json
import statistics
from pathlib import Path

from direct_semantic_rename_probe import Client

def invoke(client, name, args):
    data, ms, bytes_ = client.request("tools/call", {"name": name, "arguments": args})
    if "error" in data:
        raise RuntimeError(data["error"])
    return data, ms, bytes_

def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--binary", required=True)
    ap.add_argument("--workspace", required=True)
    ap.add_argument("--path", required=True)
    ap.add_argument("--symbol", required=True)
    ap.add_argument("--depth", type=int, default=2)
    ap.add_argument("--max-nodes", type=int, default=40)
    ap.add_argument("--repeats", type=int, default=6)
    ap.add_argument("--request-timeout", type=int, default=60)
    args = ap.parse_args()
    c=Client(str(Path(args.binary).resolve()),str(Path(args.workspace).resolve()),"full",args.request_timeout)
    results={"primitives":[], "composite":[]}
    try:
        target={"path":args.path,"symbol_path":args.symbol}
        config={"depth":args.depth,"max_nodes":args.max_nodes}
        def primitive():
            src, ms1, bytes1=invoke(c,"inspect_symbol",dict(target,view="location"))
            point=src["selection_range"]["start"]
            graph, ms2, bytes2=invoke(c,"analyze_impact",dict(config,path=args.path,line=point["line"],column=point["column"]))
            return {
                "ms":round(ms1+ms2,4), "bytes":bytes1+bytes2, "calls":2,
                "affected_declarations":len(graph["nodes"])-1,
                "direct_reference_count":sum(x["reference_count"] for x in graph["edges"] if x["from"]==graph["root"]),
                "meta":graph["meta"], "affected_paths":sorted({x["path"] for x in graph["nodes"][1:]})
            }
        def composite():
            info,ms,size=invoke(c,"investigate_change",dict(config,**target))
            return {
                "ms":round(ms,4),"bytes":size,"calls":1,
                "affected_declarations":info["affected_declarations"],
                "direct_reference_count":info["direct_reference_count"],
                "meta":info["meta"],"affected_paths":[x["path"] for x in info["affected_files"]]
            }
        # Explicit cold warm-up; avoid attributing LSP startup to one arm.
        reference=primitive()
        verify=composite()
        for key in ("affected_declarations","direct_reference_count","affected_paths","meta"):
            if reference[key]!=verify[key]:
                raise RuntimeError(f"Semantic mismatch in {key}: {reference[key]!r} vs {verify[key]!r}")
        for repeat in range(args.repeats):
            order=("primitives","composite") if repeat%2==0 else ("composite","primitives")
            for name in order:
                row=primitive() if name=="primitives" else composite()
                for key in ("affected_declarations","direct_reference_count","affected_paths","meta"):
                    if row[key]!=reference[key]:
                        raise RuntimeError(f"Drift in {name}/{repeat}: {key}")
                results[name].append(row)
        outcome={
            "workspace":str(Path(args.workspace).resolve()),
            "target":target,"source_files":sum(x.suffix in (".py",".go",".ts",".tsx") for x in Path(args.workspace).rglob("*") if x.is_file()),
            "exact_semantic_equality":True,
            "affected_declarations":reference["affected_declarations"],
            "direct_reference_count":reference["direct_reference_count"],
            "affected_file_count":len(reference["affected_paths"]),
            "incomplete_traversal":not bool(reference["meta"].get("complete")),
            "baseline":{"calls":2,"median_ms":round(statistics.median(v["ms"] for v in results["primitives"]),3),
                        "median_reply_bytes":int(statistics.median(v["bytes"] for v in results["primitives"]))},
            "composite":{"calls":1,"median_ms":round(statistics.median(v["ms"] for v in results["composite"]),3),
                         "median_reply_bytes":int(statistics.median(v["bytes"] for v in results["composite"]))},
            "samples":results,
            "caveat":"Direct MCP data, not Codex token or task-level quality measurements."
        }
        print(json.dumps(outcome,ensure_ascii=False,indent=2))
    finally:
        c.close()

if __name__=="__main__":
    main()