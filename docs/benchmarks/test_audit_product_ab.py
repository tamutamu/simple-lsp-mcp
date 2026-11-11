"""Regression: quota/failed turns cannot be mistaken for accepted coding work."""
import json
from pathlib import Path
import tempfile
import unittest

from audit_product_ab import audit


class AuditTests(unittest.TestCase):
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
