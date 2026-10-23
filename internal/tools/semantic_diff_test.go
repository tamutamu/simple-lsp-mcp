package tools

import "testing"

func TestDiffSemanticRelationsReportsCountChanges(t *testing.T) {
	before := map[string]semanticRelation{
		relationKey("outgoing_call", "audit.go", "Audit", "Audit"): {Kind: "outgoing_call", Path: "audit.go", SymbolPath: "Audit", Name: "Audit", Count: 1},
		relationKey("reference", "use.go", "", ""):                 {Kind: "reference", Path: "use.go", Count: 2},
	}
	after := map[string]semanticRelation{
		relationKey("reference", "use.go", "", ""): {Kind: "reference", Path: "use.go", Count: 1},
	}
	removed, added := diffSemanticRelations(before, after, map[string]bool{})
	if len(added) != 0 {
		t.Fatalf("added = %#v", added)
	}
	if len(removed) != 2 {
		t.Fatalf("removed = %#v", removed)
	}
	var call, ref *semanticRelationDelta
	for i := range removed {
		switch removed[i].Kind {
		case "outgoing_call":
			call = &removed[i]
		case "reference":
			ref = &removed[i]
		}
	}
	if call == nil || call.BeforeCount != 1 || call.AfterCount != 0 || call.TargetChanged {
		t.Fatalf("call delta = %#v", call)
	}
	if ref == nil || ref.BeforeCount != 2 || ref.AfterCount != 1 {
		t.Fatalf("reference delta = %#v", ref)
	}
}

func TestDiffSemanticRelationsMarksChangedTargets(t *testing.T) {
	key := relationKey("implementation", "impl.go", "", "")
	before := map[string]semanticRelation{key: {Kind: "implementation", Path: "impl.go", Count: 1}}
	removed, _ := diffSemanticRelations(before, map[string]semanticRelation{}, map[string]bool{"impl.go": true})
	if len(removed) != 1 || !removed[0].TargetChanged {
		t.Fatalf("removed = %#v", removed)
	}
}

func TestReviewWorthyRemovedRelationAvoidsReferenceNoise(t *testing.T) {
	if !reviewWorthyRemovedRelation("outgoing_call") || !reviewWorthyRemovedRelation("incoming_call") || !reviewWorthyRemovedRelation("implementation") {
		t.Fatal("call and implementation removals must be review-worthy")
	}
	if reviewWorthyRemovedRelation("reference") {
		t.Fatal("reference count changes should not create standalone warnings")
	}
}
