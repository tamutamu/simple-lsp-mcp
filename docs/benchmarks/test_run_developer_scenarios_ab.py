"""Independent scoring and source-oracle checks for scenario-first experiments."""
import json
from pathlib import Path
import tempfile
import unittest

from run_developer_scenarios_ab import (grade, normalized_finding,
                                         check_oracle_source, verify_reported_declarations)


class SceneGraderTests(unittest.TestCase):
    def setUp(self):
        self.case={"id":"case","expected_findings":[
            {"path":"a.go","receiver":"Client","method":"Emit"},
            {"path":"b.go","receiver":"auditStream","method":"Emit"},
        ]}

    def test_correct_two_paths(self):
        answer={"findings":[
            {"path":"./a.go","receiver":"*Client","method":"Emit","reason":"API client"},
            {"path":"b.go","receiver":"auditStream","method":"Emit","reason":"streaming client"},
        ],"limitations":["dynamic dispatch not proven"]}
        result=grade(answer,self.case)
        self.assertTrue(result["exact_found"])

    def test_false_positive_not_hidden_by_recall(self):
        answer={"findings":[
            {"path":"a.go","receiver":"Client","method":"Emit","reason":"true"},
            {"path":"b.go","receiver":"auditStream","method":"Emit","reason":"true"},
            {"path":"grpc.go","receiver":"GRPCServer","method":"Emit","reason":"same name"},
        ],"limitations":[]}
        result=grade(answer,self.case)
        self.assertFalse(result["exact_found"])
        self.assertEqual(result["recall"],1)
        self.assertAlmostEqual(result["precision"],2/3)

    def test_missing_evidence_not_success(self):
        answer={"findings":[
            {"path":"a.go","receiver":"Client","method":"Emit","reason":""},
            {"path":"b.go","receiver":"auditStream","method":"Emit","reason":"stream"},
        ],"limitations":[]}
        self.assertFalse(grade(answer,self.case)["exact_found"])

    def test_fail_on_missing_declared_source_oracle(self):
        with tempfile.TemporaryDirectory() as tmp:
            folder=Path(tmp)
            (folder/"a.go").write_text("package p\nfunc (x *Client) Emit() {}\n")
            case={"id":"fixture","expected_findings":[
                {"path":"a.go","receiver":"Client","method":"Emit"}]}
            check_oracle_source(folder,[case])
            case["expected_findings"][0]["method"]="DoesNotExist"
            with self.assertRaises(RuntimeError):
                check_oracle_source(folder,[case])

    def test_workflow_scoring_accepts_verified_extra_steps(self):
        case={"quality_mode":"required_anchors","expected_findings":[
             {"path":"grpc.go","receiver":"GRPCServer","method":"GetUser"}]}
        answer={"findings":[
          {"path":"grpc.go","receiver":"GRPCServer","method":"GetUser","reason":"entry"},
          {"path":"roles.go","receiver":"ServerWithRoles","method":"GetUser","reason":"permissions"}
        ],"limitations":[]}
        scored=grade(answer,case)
        self.assertTrue(scored["required_anchors_found"])
        self.assertFalse(scored["exact_found"])
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp)
            (root/"grpc.go").write_text("package t\\nfunc (g *GRPCServer) GetUser() {}\\n")
            (root/"roles.go").write_text("package t\\nfunc (r *ServerWithRoles) GetUser() {}\\n")
            self.assertTrue(verify_reported_declarations(root,answer)["source_declarations_verified"])

    def test_source_verifier_rejects_nonexistent_extra_function(self):
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp)
            (root/"file.go").write_text("package t\\nfunc valid() {}\\n")
            result=verify_reported_declarations(root,{"findings":[
                {"path":"file.go","receiver":"","method":"imaginary","reason":"false"},
            ]})
            self.assertFalse(result["source_declarations_verified"])

    def test_test_only_methods_can_be_recognized_without_deletion(self):
        case={"quality_mode":"review_facts","expected_findings":[
            {"path":"dynamo.go","receiver":"Log","method":"deleteAllItems"},
            {"path":"dynamo.go","receiver":"Log","method":"deleteTable"}]}
        answer={"findings":[],"limitations":[
            "deleteAllItems と deleteTable はテストから使用されるため削除は危険"]}
        result=grade(answer,case)
        self.assertTrue(result["review_facts_covered"])
        self.assertFalse(result["exact_found"])

    def test_unnamed_go_receiver_in_oracle(self):
        with tempfile.TemporaryDirectory() as tmp:
            path=Path(tmp)/"emit.go"
            path.write_text("package p\\nfunc (*DiscardEmitter) Emit() {}\\n")
            case={"id":"unnamed-receiver","expected_findings":[
                {"path":"emit.go","receiver":"DiscardEmitter","method":"Emit"}]}
            check_oracle_source(Path(tmp),[case])

    def test_path_normalization(self):
        self.assertEqual(
            normalized_finding({"path":"./a\\b.go","receiver":"(*Client)",
                                "method":"Emit"}),
            ("a/b.go","Client","Emit"))


if __name__=="__main__": unittest.main()