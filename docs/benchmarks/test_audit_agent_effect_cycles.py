"""The 100-cycle auditor is fail-closed against missing agent proof."""
from __future__ import annotations
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

HERE=Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location("audit_effect",HERE/"audit_agent_effect_cycles.py")
AUDIT=importlib.util.module_from_spec(spec)
assert spec.loader is not None
spec.loader.exec_module(AUDIT)

class AuditEvidenceTests(unittest.TestCase):
    def make_trial(self,root:Path,phase:str,arm:str,*,seed:str="same",used:int=1,grade:bool=True,cached:int=60):
        folder=root/phase/"S11-demo-rep1"
        folder.mkdir(parents=True,exist_ok=True)
        total=100
        tool_calls=([{"tool":"get_outgoing_calls","status":"completed","is_error":False,"bytes":200}] if arm=="mcp" and used else [])
        value={
            "status":"completed","case_id":"S11","baseline_hash":seed,
            "grade":{"pass":grade,"checks":[{"name":"independent","ok":grade}]},
            "tokens":{"inputTokens":total,"cachedInputTokens":cached,"outputTokens":12},
            "noncached_input_tokens":total-cached,"work_seconds":9.5,
            "mcp_calls":tool_calls,"cli_calls":[],
            "model":"gpt-test","effort":"low",
        }
        (folder/(arm+"-results.json")).write_text(json.dumps(value)+"\n")
        usage={"event":{"method":"thread/tokenUsage/updated","params":{
            "tokenUsage":{"total":{"inputTokens":total,"cachedInputTokens":cached,"outputTokens":12}}}}}
        (folder/(arm+"-trace.jsonl")).write_text(json.dumps(usage)+"\n")
        return folder

    def fixtures(self,root:Path)->Path:
        for phase in ("before","after"):
            for arm in ("baseline","mcp"):
                self.make_trial(root,phase,arm)
        ledger=root/"ledger.json"
        data={"goal":200,"counted_cycles":1,"cycles":[{
            "cycle":1,"task":"S11-demo","before_run":"before","after_run":"after",
            "counted":True,"found_problems":["Actual product or evaluation defect"],
            "corrections":["Specific fix followed by fresh A/B comparison"]}]}
        ledger.write_text(json.dumps(data)+"\n")
        return ledger

    def test_accepts_genuine_matched_trials_and_cash_split(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp)
            ledger=self.fixtures(root)
            trials,pairs=AUDIT.audit(ledger,root)
            self.assertEqual((len(trials),len(pairs)),(4,2))
            self.assertEqual(pairs[-1]["mcp_tool_calls"],1)
            self.assertEqual(pairs[-1]["input_proxy_at_10pct_cache_mcp"],46.0)
            self.assertEqual(pairs[-1]["mcp_cache_share_pct"],60.0)

    def test_rejects_inconsistent_cache_accounting(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);ledger=self.fixtures(root)
            result=root/"after/S11-demo-rep1/mcp-results.json"
            data=json.loads(result.read_text())
            data["noncached_input_tokens"]=50
            result.write_text(json.dumps(data)+"\n")
            with self.assertRaisesRegex(ValueError,"mismatched noncached"):
                AUDIT.audit(ledger,root)

    def test_rejects_different_seed(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);ledger=self.fixtures(root)
            result=root/"after/S11-demo-rep1/mcp-results.json"
            data=json.loads(result.read_text());data["baseline_hash"]="other"
            result.write_text(json.dumps(data)+"\n")
            with self.assertRaisesRegex(ValueError,"different seeded"):
                AUDIT.audit(ledger,root)

    def test_rejects_missing_genuine_lsp_use(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);ledger=self.fixtures(root)
            self.make_trial(root,"after","mcp",used=0)
            with self.assertRaisesRegex(ValueError,"no successful model-initiated LSP"):
                AUDIT.audit(ledger,root)

    def test_rejects_duplicate_after_run_when_claiming_two_cycles(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);ledger=self.fixtures(root)
            record=json.loads(ledger.read_text())
            second=dict(record["cycles"][0])
            second["cycle"]=2
            record["cycles"].append(second)
            record["counted_cycles"]=2
            ledger.write_text(json.dumps(record)+"\n")
            with self.assertRaisesRegex(ValueError,"duplicate post-correction"):
                AUDIT.audit(ledger,root)

    def test_rejects_false_cycle_count(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);ledger=self.fixtures(root)
            data=json.loads(ledger.read_text());data["counted_cycles"]=100
            ledger.write_text(json.dumps(data)+"\n")
            with self.assertRaisesRegex(ValueError,"disagrees"):
                AUDIT.audit(ledger,root)

if __name__=="__main__":
    unittest.main()
