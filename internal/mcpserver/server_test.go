package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var expectedTools = []string{
	"rename_symbol", "preview_rename", "format_document", "preview_format", "get_code_actions", "apply_code_action", "get_diagnostics", "investigate_change", "analyze_impact",
	"search_symbols", "list_workspace_symbols", "get_document_symbols", "get_hover", "inspect_symbol",
	"get_definition", "find_references", "find_implementations", "get_type_definition",
	"get_declaration", "get_incoming_calls", "get_outgoing_calls", "get_supertypes", "get_subtypes",
}

func TestMinimalLSPToolSurface(t *testing.T) {
	ds := definitions()
	if len(ds) != len(expectedTools) {
		t.Fatalf("got %d tools, want %d", len(ds), len(expectedTools))
	}
	seen := map[string]bool{}
	for i, d := range ds {
		if d.name != expectedTools[i] {
			t.Fatalf("tool[%d] = %q, want %q", i, d.name, expectedTools[i])
		}
		if seen[d.name] {
			t.Fatalf("duplicate tool: %s", d.name)
		}
		seen[d.name] = true
		if d.schema["type"] != "object" || d.call == nil {
			t.Fatalf("invalid definition: %s", d.name)
		}
	}
	for _, deleted := range []string{"find_symbol", "get_symbol_outline", "get_symbol_context", "get_semantic_slice", "impact_analysis", "verify_change", "get_symbol", "onboard"} {
		if seen[deleted] {
			t.Fatalf("obsolete tool remains: %s", deleted)
		}
	}
}

func TestInspectableLSPRelationsAreOptionalAndReadOnly(t *testing.T) {
	for _, profile := range []string{"full", "coding"} {
		var found bool
		for _, def := range definitionsForProfile(profile) {
			if def.name != "inspect_symbol" {
				continue
			}
			found = true
			if !toolAnnotations(def.name).ReadOnlyHint {
				t.Fatal("inspect_symbol must remain read-only")
			}
			properties := def.schema["properties"].(map[string]any)
			related, ok := properties["related"].(map[string]any)
			if !ok || related["type"] != "array" || related["uniqueItems"] != true {
				t.Fatalf("%s missing selected semantic relations: %#v", profile, related)
			}
			items := related["items"].(map[string]any)
			values := items["enum"].([]any)
			if len(values) != 8 {
				t.Fatalf("%s unexpectedly dropped LSP relation options: %#v", profile, values)
			}
			if _, ok := properties["relation_limit"]; !ok {
				t.Fatal("related context must expose a result cap")
			}
			if _, ok := properties["apply"]; ok {
				t.Fatal("inspection cannot request code modification")
			}
		}
		if !found {
			t.Fatalf("%s lost inspect_symbol", profile)
		}
	}
}

func TestImpactMCPOnlyNeedsPathAndLineWithConfigurableDepth(t *testing.T) {
	full, coding := definitionsForProfile("full"), definitionsForProfile("coding")
	for _, ds := range [][]definition{full, coding} {
		found := false
		for _, d := range ds {
			if d.name != "analyze_impact" {
				continue
			}
			found = true
			if !toolAnnotations(d.name).ReadOnlyHint {
				t.Fatal("impact graph must be a read-only MCP tool")
			}
			required, ok := d.schema["required"].([]string)
			if !ok || len(required) != 2 || required[0] != "path" || required[1] != "line" {
				t.Fatalf("unexpected impact required arguments: %#v", d.schema["required"])
			}
			props := d.schema["properties"].(map[string]any)
			for _, key := range []string{"path", "line", "depth", "max_nodes"} {
				if _, ok := props[key]; !ok {
					t.Fatalf("impact schema missing %s", key)
				}
			}
			if _, ok := props["column"]; !ok {
				t.Fatal("impact graph needs optional exact declaration column to avoid same-line ambiguity")
			}
		}
		if !found {
			t.Fatal("analyze_impact is missing from tool profile")
		}
	}
}

func TestToolCatalogStaysSmall(t *testing.T) {
	ds := definitions()
	catalog := make([]*mcp.Tool, 0, len(ds))
	for _, d := range ds {
		catalog = append(catalog, &mcp.Tool{Name: d.name, Description: d.description, InputSchema: d.schema, OutputSchema: objectSchema(), Annotations: toolAnnotations(d.name)})
	}
	encoded, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > 26*1024 {
		t.Fatalf("MCP tool catalog uses %d bytes", len(encoded))
	}
}

func TestWorkspaceListingRemainsIndependentOfSearch(t *testing.T) {
	for _, d := range definitions() {
		if d.name != "list_workspace_symbols" {
			continue
		}
		if required, _ := d.schema["required"].([]string); len(required) != 0 {
			t.Fatal("workspace listing must not require query/language")
		}
		props := d.schema["properties"].(map[string]any)
		if _, ok := props["cursor"]; !ok {
			t.Fatal("missing pagination cursor")
		}
		if _, ok := props["query"]; ok {
			t.Fatal("workspace listing must not be a search")
		}
		return
	}
	t.Fatal("workspace listing removed")
}

func TestDirectTargetToolsRetainSymbolHandlesAndPaths(t *testing.T) {
	for _, name := range []string{"get_definition", "find_references", "find_implementations", "get_type_definition", "get_declaration", "get_incoming_calls", "get_outgoing_calls", "get_supertypes", "get_subtypes", "get_hover", "inspect_symbol", "rename_symbol", "preview_rename"} {
		found := false
		for _, d := range definitions() {
			if d.name != name {
				continue
			}
			found = true
			p := d.schema["properties"].(map[string]any)
			for _, key := range []string{"symbol_id", "symbol_path", "path", "line", "column"} {
				if _, ok := p[key]; !ok {
					t.Fatalf("%s missing %s", name, key)
				}
			}
			for _, v := range append([]string(nil), func() []string { x, _ := d.schema["required"].([]string); return x }()...) {
				if v == "language" {
					t.Fatalf("%s still requires language", name)
				}
			}
		}
		if !found {
			t.Fatalf("missing %s", name)
		}
	}
}

func TestSimpleServerInstructions(t *testing.T) {
	if len(serverInstructions) > 650 {
		t.Fatalf("verbose instructions: %d", len(serverInstructions))
	}
	for _, deleted := range []string{"impact_analysis", "get_semantic_slice", "get_symbol_context", "verify_change"} {
		if strings.Contains(serverInstructions, deleted) {
			t.Fatalf("mandatory/composite instruction remains: %s", deleted)
		}
	}
	for _, wanted := range []string{"Do not invoke every tool", "preview_rename", "preview_format", "inspect_symbol", "tests"} {
		if !strings.Contains(serverInstructions, wanted) {
			t.Fatalf("instructions missing %q", wanted)
		}
	}
}

func TestWritesAreExplicitAndReadsStayReadOnly(t *testing.T) {
	writes := map[string]bool{"rename_symbol": true, "format_document": true, "apply_code_action": true}
	for _, d := range definitions() {
		a := toolAnnotations(d.name)
		if a.OpenWorldHint == nil || *a.OpenWorldHint {
			t.Fatalf("open world: %s", d.name)
		}
		if writes[d.name] {
			if a.DestructiveHint == nil || !*a.DestructiveHint || a.ReadOnlyHint {
				t.Fatalf("missing write annotation: %s", d.name)
			}
		} else if !a.ReadOnlyHint {
			t.Fatalf("read tool unmarked: %s", d.name)
		}
	}
}

func TestRenamePreviewIsReadOnlyAndRetainsExplicitWriteOperation(t *testing.T) {
	tools := definitions()
	var preview, rename *definition
	for i := range tools {
		if tools[i].name == "preview_rename" {
			preview = &tools[i]
		}
		if tools[i].name == "rename_symbol" {
			rename = &tools[i]
		}
	}
	if preview == nil || rename == nil {
		t.Fatal("missing separate rename preview and apply-capable tool")
	}
	if !toolAnnotations("preview_rename").ReadOnlyHint {
		t.Fatal("preview should not require destructive-tool approval")
	}
	if !*toolAnnotations("rename_symbol").DestructiveHint {
		t.Fatal("real rename must be marked destructive")
	}
	if _, ok := preview.schema["properties"].(map[string]any)["apply"]; ok {
		t.Fatal("read-only preview must never accept apply")
	}
	if _, ok := rename.schema["properties"].(map[string]any)["apply"]; !ok {
		t.Fatal("existing apply-capable rename contract lost")
	}
	detail, ok := preview.schema["properties"].(map[string]any)["preview_detail"].(map[string]any)
	if !ok {
		t.Fatal("preview_rename missing full-detail opt-in")
	}
	values, _ := detail["enum"].([]any)
	if len(values) != 2 || values[0] != "summary" || values[1] != "full" {
		t.Fatalf("unexpected preview detail values: %#v", values)
	}
	if _, ok := rename.schema["properties"].(map[string]any)["preview_detail"]; ok {
		t.Fatal("write-capable rename unexpectedly changed its stable schema")
	}
}

func TestFormatPreviewCannotRequestWrites(t *testing.T) {
	var preview, formatter *definition
	ds := definitions()
	for i := range ds {
		if ds[i].name == "preview_format" {
			preview = &ds[i]
		}
		if ds[i].name == "format_document" {
			formatter = &ds[i]
		}
	}
	if preview == nil || formatter == nil {
		t.Fatal("expected read-only preview_format and write-capable format_document")
	}
	if !toolAnnotations("preview_format").ReadOnlyHint {
		t.Fatal("preview formatting is incorrectly marked destructive")
	}
	if !*toolAnnotations("format_document").DestructiveHint {
		t.Fatal("formatting application needs destructive authorization")
	}
	if _, ok := preview.schema["properties"].(map[string]any)["apply"]; ok {
		t.Fatal("read-only formatting preview must not expose apply")
	}
	if _, ok := formatter.schema["properties"].(map[string]any)["apply"]; !ok {
		t.Fatal("format_document must preserve backward-compatible apply")
	}
}

func TestResultStructuredAndTextContentMatch(t *testing.T) {
	v := map[string]any{"symbols": []string{"one"}}
	r := result(v, false)
	if r.StructuredContent == nil || len(r.Content) != 1 {
		t.Fatal("missing result content")
	}
	b, err := json.Marshal(r.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"symbols":["one"]}` {
		t.Fatalf("unexpected content: %s", b)
	}
}

func TestEnumeratedInputsUseJSONSchemaEnums(t *testing.T) {
	all := allProperties()
	for _, name := range []string{"language", "kinds", "severities"} {
		spec := all[name].(map[string]any)
		schema := spec
		if name != "language" {
			schema = spec["items"].(map[string]any)
		}
		if len(schema["enum"].([]any)) == 0 {
			t.Fatalf("missing enum: %s", name)
		}
	}
}

func TestCodingProfileSubsetAndCatalogSize(t *testing.T) {
	full := definitionsForProfile("full")
	coding := definitionsForProfile("coding")
	if len(full) != 23 || len(coding) != 15 {
		t.Fatalf("unexpected catalog sizes full=%d coding=%d", len(full), len(coding))
	}
	exposed := map[string]bool{}
	for _, d := range coding {
		exposed[d.name] = true
		switch d.name {
		case "format_document", "get_code_actions", "apply_code_action":
			t.Fatalf("unrelated write tool exposed in coding profile: %s", d.name)
		}
	}
	if !exposed["preview_rename"] || !exposed["rename_symbol"] {
		t.Fatal("coding profile cannot perform semantic rename")
	}
	if !toolAnnotations("preview_rename").ReadOnlyHint || !*toolAnnotations("rename_symbol").DestructiveHint {
		t.Fatal("rename preview/write annotations are incorrect")
	}
	catalog := make([]*mcp.Tool, 0, len(coding))
	for _, d := range coding {
		catalog = append(catalog, &mcp.Tool{Name: d.name, Description: d.description, InputSchema: d.schema, OutputSchema: objectSchema(), Annotations: toolAnnotations(d.name)})
	}
	data, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) >= 18*1024 {
		t.Fatalf("coding catalog still too large: %d bytes", len(data))
	}
}

func TestInvestigationInputSchemaMatchesAcceptedArguments(t *testing.T) {
	schema := investigateInputSchema()
	fields := schema["properties"].(map[string]any)
	detail := fields["detail"].(map[string]any)["enum"].([]any)
	if len(detail) != 3 || detail[0] != "files" || detail[1] != "union" || detail[2] != "graph" {
		t.Fatalf("invalid enum advertised to coding agents: %#v", detail)
	}
	targets := fields["targets"].(map[string]any)
	if targets["minItems"] != 2 || targets["maxItems"] != 6 {
		t.Fatalf("incorrect batch size limits: %#v", targets)
	}
	depth := fields["depth"].(map[string]any)
	nodes := fields["max_nodes"].(map[string]any)
	if depth["maximum"] != 16 || nodes["maximum"] != 200 {
		t.Fatalf("invalid tool bounds: depth=%#v nodes=%#v", depth, nodes)
	}
}

func TestInvestigateProfileContainsOnlyReadOnlyHighLevelTool(t *testing.T) {
	items := definitionsForProfile("investigate")
	if len(items) != 1 || items[0].name != "investigate_change" {
		t.Fatalf("wrong investigate tool catalog: %#v", items)
	}
	if !toolAnnotations("investigate_change").ReadOnlyHint {
		t.Fatal("investigate_change must not permit source writes")
	}
	coding := definitionsForProfile("coding")
	var found bool
	for _, d := range coding {
		if d.name == "investigate_change" {
			found = true
		}
	}
	if !found {
		t.Fatal("coding profile must expose the high-level investigation tool")
	}
}

func TestCodingCatalogGuidesInterfaceRenameDiscovery(t *testing.T) {
	var previewFound, renameFound bool
	for _, d := range definitionsForProfile("coding") {
		switch d.name {
		case "preview_rename":
			previewFound = true
			if !strings.Contains(d.description, "unrenamed_method_candidates") {
				t.Fatal("Go interface rename preview no longer advertises independent method candidates")
			}
		case "rename_symbol":
			renameFound = true
			fields := d.schema["properties"].(map[string]any)
			if _, ok := fields["targets"]; !ok {
				t.Fatal("coding profile lost atomic multi-interface rename")
			}
		}
	}
	if !previewFound || !renameFound || !strings.Contains(serverInstructions, "rename_symbol(targets=") {
		t.Fatal("coding profile cannot guide an agent from preview to one batch rename")
	}
}

func TestRefactorProfileRetainsOnlyFourSemanticWorkflowTools(t *testing.T) {
	tools := definitionsForProfile("refactor")
	want := map[string]bool{
		"inspect_symbol": true, "investigate_change": true,
		"preview_rename": true, "rename_symbol": true,
	}
	if len(tools) != len(want) {
		t.Fatalf("refactor profile exposed %d tools, want %d", len(tools), len(want))
	}
	for _, tool := range tools {
		if !want[tool.name] {
			t.Fatalf("refactor profile unexpectedly exposed %q", tool.name)
		}
	}
	if !toolAnnotations("preview_rename").ReadOnlyHint ||
		!toolAnnotations("investigate_change").ReadOnlyHint ||
		!*toolAnnotations("rename_symbol").DestructiveHint {
		t.Fatal("refactor profile lost read/write approval hints")
	}
	catalog, err := json.Marshal(renameCatalogForTest(tools))
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) > 8*1024 {
		t.Fatalf("task-specific profile too large: %d bytes", len(catalog))
	}
}

func TestRefactorCatalogAvoidsLargeDetailOutputsWithoutChangingOtherProfiles(t *testing.T) {
	var foundPreview, foundImpact bool
	for _, tool := range definitionsForProfile("refactor") {
		properties := tool.schema["properties"].(map[string]any)
		switch tool.name {
		case "preview_rename":
			foundPreview = true
			if _, ok := properties["preview_detail"]; ok {
				t.Fatal("refactor profile still invites full multi-file edit payloads")
			}
			if !strings.Contains(tool.description, "unrenamed_method_candidates") {
				t.Fatal("refactor preview must retain independent method discovery")
			}
		case "investigate_change":
			foundImpact = true
			detail, ok := properties["detail"].(map[string]any)
			if !ok {
				t.Fatal("impact detail schema missing")
			}
			values := detail["enum"]
			encoded, _ := json.Marshal(values)
			if string(encoded) != `["files","union"]` {
				t.Fatalf("unexpected compact-only detail choices: %s", encoded)
			}
		}
	}
	if !foundPreview || !foundImpact {
		t.Fatal("refactor tools missing")
	}
	for _, profile := range []string{"coding", "full"} {
		for _, tool := range definitionsForProfile(profile) {
			properties := tool.schema["properties"].(map[string]any)
			if tool.name == "preview_rename" {
				if _, ok := properties["preview_detail"]; !ok {
					t.Fatalf("%s lost deliberate full rename inspection", profile)
				}
			}
			if tool.name == "investigate_change" {
				detail := properties["detail"].(map[string]any)
				encoded, _ := json.Marshal(detail["enum"])
				if !strings.Contains(string(encoded), "graph") {
					t.Fatalf("%s lost exact LSP graph mode", profile)
				}
			}
		}
	}
}

func TestRenameProfileIsCompactAndSupportsWriteWithApproval(t *testing.T) {
	rename := definitionsForProfile("rename")
	if len(rename) != 2 {
		t.Fatalf("rename profile tool count=%d, want 2", len(rename))
	}
	got := map[string]bool{}
	for _, tool := range rename {
		got[tool.name] = true
	}
	if !got["preview_rename"] || !got["rename_symbol"] {
		t.Fatalf("missing semantic rename operations: %#v", got)
	}
	if !toolAnnotations("preview_rename").ReadOnlyHint || !*toolAnnotations("rename_symbol").DestructiveHint {
		t.Fatal("rename tool approval hints are incorrect")
	}
	catalog, err := json.Marshal(renameCatalogForTest(rename))
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) > 4000 {
		t.Fatalf("rename-only tool catalog not compact: %d bytes", len(catalog))
	}
}

func renameCatalogForTest(ds []definition) []*mcp.Tool {
	result := make([]*mcp.Tool, 0, len(ds))
	for _, d := range ds {
		result = append(result, &mcp.Tool{Name: d.name, Description: d.description,
			InputSchema: d.schema, OutputSchema: objectSchema(), Annotations: toolAnnotations(d.name)})
	}
	return result
}

func TestTextOnlyResultAvoidsDuplicateStructuredPayload(t *testing.T) {
	payload := map[string]any{"symbols": []string{"one", "two"}, "truncated": false}
	both := resultWithFormat(payload, false, "both")
	textOnly := resultWithFormat(payload, false, "text")
	if both.StructuredContent == nil || textOnly.StructuredContent != nil {
		t.Fatal("response format did not control redundant structuredContent")
	}
	if len(textOnly.Content) != 1 {
		t.Fatalf("unexpected text-only content: %#v", textOnly.Content)
	}
	if _, ok := textOnly.Content[0].(*mcp.TextContent); !ok {
		t.Fatalf("expected JSON text block, got %T", textOnly.Content[0])
	}
	wireBoth, err := json.Marshal(both)
	if err != nil {
		t.Fatal(err)
	}
	wireText, err := json.Marshal(textOnly)
	if err != nil {
		t.Fatal(err)
	}
	if len(wireText) >= len(wireBoth) {
		t.Fatalf("text-only did not shrink wire payload: both=%d text=%d", len(wireBoth), len(wireText))
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(textOnly.Content[0].(*mcp.TextContent).Text), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded["symbols"].([]any)) != 2 {
		t.Fatalf("text content changed: %#v", decoded)
	}
	if outputSchemaForFormat("text") != nil || outputSchemaForFormat("both") == nil {
		t.Fatal("text-only must not advertise a structured output schema")
	}
	if !resultWithFormat(map[string]any{"error": "bad"}, true, "text").IsError {
		t.Fatal("text-only must retain tool error status")
	}
}
