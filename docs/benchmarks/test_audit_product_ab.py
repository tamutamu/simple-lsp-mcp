"""Regression: quota/failed turns cannot be mistaken for accepted coding work."""
import json
from pathlib import Path
import tempfile
import unittest

from audit_product_ab import audit, useful_completed_mcp_call


class AuditTests(unittest.TestCase):
    def test_mcp_success_needs_usable_semantic_response(self):
        good={"status":"completed", "result":{"content":[{"type":"text","text":"NewWithOptions"}], "isError": False}}
        self.assertTrue(useful_completed_mcp_call(good))
        for item in (
            {"status":"completed","result":{"content":[]}},
            {"status":"completed","result":{"content":[{"text":""}]}},
            {"status":"completed","result":{"content":[{"text":"{}"}]}},
            {"status":"completed","result":{"content":[{"text":'{"diagnostics":null,"meta":{"complete":false}}'}]}},
            {"status":"completed","result":{"content":[{"text":'{"diagnostics":[{"message":"some"}],"meta":{"complete":false}}'}]}},
            {"status":"completed","result":{"content":[{"text":'{"meta":{"complete":true}}'}]}},
            {"status":"completed","result":{"content":[{"text":"{\"error\":{\"code\":\"NO_SYMBOL\"}}"}]}},
            {"status":"completed","result":{"isError":True, "content":[{"text":"some error"}]}},
            {"status":"failed","result":{"content":[{"text":"useful"}]}},
        ):
            with self.subTest(item=item):
                self.assertFalse(useful_completed_mcp_call(item))

    def test_quota_even_if_source_grader_passed(self):
        with tempfile.TemporaryDirectory() as root:
            base = Path(root)
            directory = base / "T35-type-rep1"
            directory.mkdir()
            rows = [
                {"id": "T35", "rep": 1, "arm": "baseline", "passed": True, "seconds": 42, "mcp_calls": 0},
                {"id": "T35", "rep": 1, "arm": "mcp", "passed": True, "seconds": 1.5, "mcp_calls": 0},
            ]
            (base / "summary.json").write_text(json.dumps(rows))
            for row in rows:
                status = "completed" if row["arm"] == "baseline" else "failed"
                events = [{"event": {"method": "turn/completed", "params": {"turn": {"status": status}}}}]
                if status == "failed":
                    events.append({"event": {"method": "error", "params": {"error": {"codexErrorInfo": "usageLimitExceeded"}}}})
                (directory / f'{row["arm"]}-trace.jsonl').write_text("\n".join(json.dumps(e) for e in events))
            report = audit(base)
            self.assertEqual(report["complete_passing_pairs"], 0)
            self.assertEqual(report["verified_agent_and_grader_pass"], 1)
            self.assertEqual(report["invalid_reasons"], {"quota_exceeded": 1})


if __name__ == "__main__":
    unittest.main()
