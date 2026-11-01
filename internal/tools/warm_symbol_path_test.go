package tools

import (
    "reflect"
    "testing"
)

// LSP documentSymbol sibling paths and flat receiver-qualified names are
// addresses, not the identifier spelling that appears at cross-file call sites.
func TestTargetWarmNameNormalizesSyntheticSymbolPaths(t *testing.T) {
    cases := []struct {
        path string
        want string
    }{
        {"UserService/updateUser", "updateUser"},
        {"UserService/updateUser#2", "updateUser"},
        {"UserService/updateUser#12", "updateUser"},
        {"/UserService/updateUser#3", "updateUser"},
        {"(*Engine).FindSymbol", "FindSymbol"},
        {"pkg/(*Engine).FindSymbol#2", "FindSymbol"},
        {"pkg/%25name#2", "%name"},
        {"pkg/foo#bar", "foo#bar"},
        {"pkg/#2", ""},
    }
    e := &Engine{}
    for _, tc := range cases {
        t.Run(tc.path, func(t *testing.T) {
            got := e.targetWarmName(map[string]any{"symbol_path": tc.path})
            if got != tc.want {
                t.Fatalf("candidate name for %q = %q, want %q", tc.path, got, tc.want)
            }
        })
    }
}

func TestSyntheticSymbolPathsStillWarmRealCrossFileCandidates(t *testing.T) {
    e, p, root := warmIndexFixture(t)
    warmWrite(t, root, "decl.ts", "export function updateUser() {}\n")
    warmWrite(t, root, "caller.ts", "import { updateUser } from './decl';\nupdateUser();\n")
    warmWrite(t, root, "other.ts", "export function unrelated() {}\n")

    for _, symbolPath := range []string{"updateUser#2", "UserService/updateUser#3"} {
        name := e.targetWarmName(map[string]any{"symbol_path": symbolPath})
        found, stats := warmScan(t, e, p, root, name)
        want := []string{"caller.ts", "decl.ts"}
        if !reflect.DeepEqual(found, want) || stats.Matches != len(want) {
            t.Fatalf("symbol_path %q: candidates=%v stats=%+v; want %v",
                symbolPath, found, stats, want)
        }
    }
}
