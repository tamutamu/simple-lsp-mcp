#!/usr/bin/env python3
"""Task-oriented, independently checkable semantic development investigations.

These use a real Go project, not tiny mocked LSP API fixtures. Agents must
reason about actual source ownership and cross-package relationships.
"""
CASES = [
    {
        "id":"S04","name":"go-exhaustive-direct-outgoing-call-graph",
        "language":"go","seed":"go","read_only":True,
        "prompt":(
            "Perform an exact semantic call-graph audit for the existing "
            "(*Engine).Relationship method declared in internal/tools/engine.go. "
            "Identify every distinct directly called Go function or method, "
            "including methods with receivers in other packages. Exclude the "
            "Relationship function itself, builtin operations, and any named "
            "identifier merely appearing in comments, strings or type names. "
            "Do not edit files. Return ONLY a JSON object whose sole key "
            "is callees and whose value is a sorted array of {path,name} "
            "objects, where path is a workspace-relative file path that "
            "declares the callee and name is its plain name without receiver. "
            "No prose or Markdown fences."
        ),
        "answer_exact_callees":[{'path': 'internal/core/types.go', 'name': 'NewError'}, {'path': 'internal/lsp/session/session.go', 'name': 'Capabilities'}, {'path': 'internal/tools/engine.go', 'name': 'locationsAt'}, {'path': 'internal/tools/helpers.go', 'name': 'capable'}, {'path': 'internal/tools/helpers.go', 'name': 'limit'}, {'path': 'internal/tools/helpers.go', 'name': 'target'}, {'path': 'internal/tools/helpers.go', 'name': 'unsupported'}, {'path': 'internal/tools/input.go', 'name': 'boolValDefault'}, {'path': 'internal/tools/input.go', 'name': 'stringVal'}, {'path': 'internal/tools/reference_filter.go', 'name': 'excludeTestReferenceLocations'}, {'path': 'internal/tools/relation_preview.go', 'name': 'populateLocationPreviews'}, {'path': 'internal/tools/warm.go', 'name': 'primeProjectSemantics'}, {'path': 'internal/tools/warm.go', 'name': 'projectAlreadyWarmed'}, {'path': 'internal/tools/warm.go', 'name': 'warmNameForPosition'}, {'path': 'internal/tools/warm.go', 'name': 'warmProjectSymbolCandidates'}]
    },
    {
        "id":"S03","name":"go-exhaustive-call-hierarchy",
        "language":"go","seed":"go","read_only":True,
        "prompt":(
            "Identify every direct function or method caller of the exact "
            "document.Store.SyncBytes method in internal/document/store.go "
            "in this Go project, including ordinary code, test functions and "
            "benchmarks. Don't count the declaration or unrelated methods "
            "named SyncBytes; inspect the actual call relationships. "
            "Return ONLY a JSON object with one key callers containing "
            "a sorted list of {path,name} objects, where path is a "
            "workspace-relative Go source path and name is the direct "
            "calling function or method name (method names without receiver). "
            "No prose, no Markdown fences, and do not edit files."
        ),
        "answer_exact_callers":[
            {"path":"internal/document/store.go","name":"Sync"},
            {"path":"internal/document/store_benchmark_test.go","name":"BenchmarkWarmCandidateSync"},
            {"path":"internal/document/store_test.go","name":"TestSyncBytesUsesAlreadyReadSourceSnapshot"},
            {"path":"internal/tools/warm.go","name":"warmProjectSymbolCandidates"}
        ],
    },
    {
        "id":"S02","name":"go-exhaustive-semantic-constructor-impact",
        "language":"go","seed":"go","read_only":True,
        "prompt":(
            "We are evaluating the impact of renaming the exact constructor New "
            "declared in internal/tools/engine.go. Identify EVERY Go source file "
            "that calls that declaration, including unqualified calls within "
            "the tools package and tests, and qualified calls in other packages. "
            "Do NOT include references to unrelated symbols also named New, "
            "declarations, imports, comments or literal examples. Do not edit "
            "the source. Return ONLY a JSON object with one key named files, "
            "whose value is the sorted array of unique workspace-relative "
            "file paths with actual calls. No prose or Markdown fences."
        ),
        "answer_exact_files":['cmd/simple-lsp-mcp/doctor.go', 'cmd/simple-lsp-mcp/main.go', 'internal/tools/auto_config_real_test.go', 'internal/tools/diagnostics_integrity_test.go', 'internal/tools/document_flat_integrity_test.go', 'internal/tools/edit_assembly_test.go', 'internal/tools/edit_real_test.go', 'internal/tools/edit_test.go', 'internal/tools/engine_test.go', 'internal/tools/impact_real_test.go', 'internal/tools/location_integrity_test.go', 'internal/tools/real_lsp_test.go', 'internal/tools/reference_filter_real_test.go', 'internal/tools/relation_preview_test.go', 'internal/tools/workspace_confinement_test.go', 'internal/tools/workspace_list_test.go']
    },
    {
        "id":"S01","name":"go-interface-implementation-audit",
        "language":"go","seed":"go","read_only":True,
        "prompt":(
            "You are preparing a breaking change to the document.Notifier interface "
            "in internal/document/store.go. Identify every concrete Go type in this "
            "repository that implements this interface, including test doubles, "
            "and report exact source paths, type names, and the method/signature that "
            "satisfies the contract. Distinguish the interface declaration from its "
            "implementations. Do not edit any files. Include tests and production code."
        ),
        "answer_must_include":[
            "internal/document/store.go","Notifier",
            "internal/document/store_test.go","fakeNotifier",
            "internal/lsp/session/session.go","Session",
            "internal/lsp/transport/transport.go","Transport",
            "Notify"
        ],
    },
]


def validate() -> None:
    assert len({case["id"] for case in CASES})==len(CASES)
    for case in CASES:
        assert case["read_only"] and case["seed"] in {"go","ts","py"}
        if "answer_exact_callees" in case:
            assert len(case["answer_exact_callees"])>1
        elif "answer_exact_callers" in case:
            assert len(case["answer_exact_callers"])>1
        elif "answer_exact_files" in case:
            assert len(case["answer_exact_files"])>1
        else:
            assert len(case["answer_must_include"])>1
