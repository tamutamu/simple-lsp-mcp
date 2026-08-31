package tools

import (
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
)

func TestSymbolPathEscapesSeparatorInNames(t *testing.T) {
	if got := escapeSegment("a/b"); got != "a%2Fb" {
		t.Fatalf("escapeSegment = %q, want a%%2Fb", got)
	}
	if got := escapeSegment("100%"); got != "100%25" {
		t.Fatalf("escapeSegment = %q, want 100%%25", got)
	}
}

func TestUnescapeSegmentRoundTripsEscapedNames(t *testing.T) {
	for _, name := range []string{"a/b", "100%", "%2F", "plain", ""} {
		if got := unescapeSegment(escapeSegment(name)); got != name {
			t.Fatalf("round trip of %q = %q", name, got)
		}
	}
}

func TestWalkDocumentBuildsNestedPaths(t *testing.T) {
	text := []byte("class UserService {\n  createUser() {}\n}\n")
	vs := []protocol.DocumentSymbol{{
		Name:  "UserService",
		Kind:  5,
		Range: protocol.Range{Start: protocol.Position{}, End: protocol.Position{}},
		Children: []protocol.DocumentSymbol{{
			Name:  "createUser",
			Kind:  6,
			Range: protocol.Range{Start: protocol.Position{Line: 1}, End: protocol.Position{Line: 1}},
		}},
	}}
	nodes := walkDocument(text, "utf-16", vs, nil, "")
	if len(nodes) != 1 || nodes[0].SymbolPath != "UserService" {
		t.Fatalf("nodes = %#v", nodes)
	}
	if len(nodes[0].Children) != 1 {
		t.Fatalf("children = %#v", nodes[0].Children)
	}
	child := nodes[0].Children[0]
	if child.SymbolPath != "UserService/createUser" {
		t.Fatalf("child.SymbolPath = %q, want UserService/createUser", child.SymbolPath)
	}
	if child.ContainerName != "UserService" {
		t.Fatalf("child.ContainerName = %q, want UserService", child.ContainerName)
	}
}

func TestWalkDocumentDisambiguatesDuplicateSiblings(t *testing.T) {
	vs := []protocol.DocumentSymbol{
		{Name: "createUser", Kind: 6},
		{Name: "createUser", Kind: 6},
		{Name: "createUser", Kind: 6},
	}
	nodes := walkDocument([]byte(""), "utf-16", vs, nil, "")
	if len(nodes) != 3 {
		t.Fatalf("nodes = %#v", nodes)
	}
	want := []string{"createUser", "createUser#2", "createUser#3"}
	for i, w := range want {
		if nodes[i].SymbolPath != w {
			t.Fatalf("nodes[%d].SymbolPath = %q, want %q", i, nodes[i].SymbolPath, w)
		}
	}
}

func TestWalkDocumentNamesAnonymousSymbolsByIndex(t *testing.T) {
	vs := []protocol.DocumentSymbol{{Name: "a"}, {Name: ""}, {Name: ""}}
	nodes := walkDocument([]byte(""), "utf-16", vs, nil, "")
	if nodes[1].SymbolPath != "#1" || nodes[2].SymbolPath != "#2" {
		t.Fatalf("nodes = %#v", nodes)
	}
}

func TestWalkDocumentSkipsSymbolsWithUnconvertibleRanges(t *testing.T) {
	// A range past the end of an empty file cannot be converted.
	vs := []protocol.DocumentSymbol{{Name: "a", Range: protocol.Range{Start: protocol.Position{Line: 5}, End: protocol.Position{Line: 5, Character: 1}}}}
	nodes := walkDocument([]byte(""), "utf-16", vs, nil, "")
	if len(nodes) != 0 {
		t.Fatalf("nodes = %#v, want none", nodes)
	}
}

func TestParseSymbolPathSplitsOnSeparator(t *testing.T) {
	segments, anchored, err := parseSymbolPath("UserService/createUser")
	if err != nil {
		t.Fatal(err)
	}
	if anchored {
		t.Fatal("anchored = true, want false")
	}
	if len(segments) != 2 || segments[0] != "UserService" || segments[1] != "createUser" {
		t.Fatalf("segments = %#v", segments)
	}
}

func TestParseSymbolPathRoundTripsEscapedNames(t *testing.T) {
	segments, _, err := parseSymbolPath("a%2Fb/plain")
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 2 || segments[0] != "a/b" || segments[1] != "plain" {
		t.Fatalf("segments = %#v", segments)
	}
}

func TestParseSymbolPathDetectsAnchor(t *testing.T) {
	segments, anchored, err := parseSymbolPath("/createUser")
	if err != nil {
		t.Fatal(err)
	}
	if !anchored {
		t.Fatal("anchored = false, want true")
	}
	if len(segments) != 1 || segments[0] != "createUser" {
		t.Fatalf("segments = %#v", segments)
	}
}

func TestParseSymbolPathRejectsEmptySegment(t *testing.T) {
	for _, raw := range []string{"A//B", "", "/", "A/ /B"} {
		if _, _, err := parseSymbolPath(raw); err == nil {
			t.Fatalf("parseSymbolPath(%q) = nil error, want one", raw)
		}
	}
}

func TestParseSymbolPathTrimsSurroundingSpace(t *testing.T) {
	segments, _, err := parseSymbolPath(" UserService / createUser ")
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 2 || segments[0] != "UserService" || segments[1] != "createUser" {
		t.Fatalf("segments = %#v", segments)
	}
}

func documentSymbolNodes() []symbolNode {
	text := []byte("class App {\n class UserService {\n  createUser() {}\n }\n}\n")
	vs := []protocol.DocumentSymbol{{
		Name: "App",
		Kind: 5,
		Children: []protocol.DocumentSymbol{{
			Name: "UserService",
			Kind: 5,
			Children: []protocol.DocumentSymbol{{
				Name: "createUser",
				Kind: 6,
			}},
		}},
	}}
	return walkDocument(text, "utf-16", vs, nil, "")
}

func TestMatchNodesPrefersExactOverSuffix(t *testing.T) {
	nodes := documentSymbolNodes()
	got := matchNodes(nodes, []string{"App", "UserService", "createUser"}, false)
	if len(got) != 1 || got[0].SymbolPath != "App/UserService/createUser" {
		t.Fatalf("matchNodes = %#v", got)
	}
}

func TestMatchNodesMatchesTrailingSegments(t *testing.T) {
	nodes := documentSymbolNodes()
	got := matchNodes(nodes, []string{"createUser"}, false)
	if len(got) != 1 || got[0].SymbolPath != "App/UserService/createUser" {
		t.Fatalf("matchNodes = %#v", got)
	}
	got = matchNodes(nodes, []string{"UserService", "createUser"}, false)
	if len(got) != 1 || got[0].SymbolPath != "App/UserService/createUser" {
		t.Fatalf("matchNodes = %#v", got)
	}
}

func TestMatchNodesAnchoredRejectsSuffixOnlyMatch(t *testing.T) {
	nodes := documentSymbolNodes()
	got := matchNodes(nodes, []string{"createUser"}, true)
	if len(got) != 0 {
		t.Fatalf("matchNodes = %#v, want none", got)
	}
}

func TestMatchNodesReturnsEveryCandidateWhenAmbiguous(t *testing.T) {
	text := []byte("class A {\n createUser() {}\n}\nclass B {\n createUser() {}\n}\n")
	vs := []protocol.DocumentSymbol{
		{Name: "A", Kind: 5, Children: []protocol.DocumentSymbol{{Name: "createUser", Kind: 6}}},
		{Name: "B", Kind: 5, Children: []protocol.DocumentSymbol{{Name: "createUser", Kind: 6}}},
	}
	nodes := walkDocument(text, "utf-16", vs, nil, "")
	got := matchNodes(nodes, []string{"createUser"}, false)
	if len(got) != 2 {
		t.Fatalf("matchNodes = %#v, want two candidates", got)
	}
}

func TestMatchNodesReturnsNoneWhenNothingMatches(t *testing.T) {
	nodes := documentSymbolNodes()
	if got := matchNodes(nodes, []string{"doesNotExist"}, false); len(got) != 0 {
		t.Fatalf("matchNodes = %#v, want none", got)
	}
}
