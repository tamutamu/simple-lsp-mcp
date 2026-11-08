import json
import tempfile
import unittest
from pathlib import Path
from audit_codex_trace import audit

class AuditTests(unittest.TestCase):
    def check(self, events):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "trace.jsonl"
            path.write_text("\n".join(json.dumps(e) for e in events), encoding="utf-8")
            return audit(path)

    def test_two_turns(self):
        result = self.check([
            {"type": "turn.completed", "usage": {"input_tokens": 100, "cached_input_tokens": 80, "output_tokens": 4}},
            {"type": "turn.completed", "usage": {"input_tokens": 40, "cached_input_tokens": 20, "output_tokens": 3}},
        ])
        self.assertEqual(result["usage_total"]["input_tokens"], 140)
        self.assertEqual(result["usage_total"]["uncached_input_tokens"], 40)

    def test_missing_cache(self):
        result = self.check([{"type": "turn.completed", "usage": {"input_tokens": 100}}])
        self.assertIsNone(result["usage_total"]["uncached_input_tokens"])

    def test_deduplicated_tool_lifecycle(self):
        item = {"id": "one", "type": "mcp_tool_call", "server": "simple-lsp"}
        result = self.check([{"type": kind, "item": item} for kind in
                             ("item.started", "item.updated", "item.completed")])
        self.assertEqual(result["tool_attempts"]["simple_lsp_attempts"], 1)

    def test_invalid_cache_is_unknown(self):
        result = self.check([{"type": "turn.completed", "usage": {"input_tokens": 5, "cached_input_tokens": 6}}])
        self.assertIsNone(result["usage_total"]["uncached_input_tokens"])

if __name__ == "__main__":
    unittest.main()
