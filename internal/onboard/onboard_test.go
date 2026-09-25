package onboard

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/config"
)

func TestOnboardScansMonorepoStructure(t *testing.T) {
	tempDir := t.TempDir()

	// Create apps/frontend/package.json
	frontendDir := filepath.Join(tempDir, "apps", "frontend")
	if err := os.MkdirAll(frontendDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(frontendDir, "package.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create apps/backend/pyproject.toml
	backendDir := filepath.Join(tempDir, "apps", "backend")
	if err := os.MkdirAll(backendDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backendDir, "pyproject.toml"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	// Create root go.mod
	if err := os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte("module test"), 0644); err != nil {
		t.Fatal(err)
	}

	res, err := Run(Options{Workspace: tempDir, Overwrite: true})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if _, err := os.Stat(res.ConfigPath); err != nil {
		t.Fatalf("expected config file to be generated at %s, got error: %v", res.ConfigPath, err)
	}

	runtime, err := config.Load(config.Runtime{Workspace: tempDir})
	if err != nil {
		t.Fatalf("failed to load generated config: %v", err)
	}

	if len(runtime.Servers["go"]) == 0 {
		t.Errorf("expected go server in root")
	}

	pyServer := config.SelectServer(runtime.Servers["python"], "apps/backend/main.py")
	if pyServer.Directory != "apps/backend" {
		t.Errorf("expected python Directory = apps/backend, got %s", pyServer.Directory)
	}

	tsServer := config.SelectServer(runtime.Servers["typescript-javascript"], "apps/frontend/src/index.ts")
	if tsServer.Directory != "apps/frontend" {
		t.Errorf("expected typescript Directory = apps/frontend, got %s", tsServer.Directory)
	}
}

func TestOnboardPreventsOverwriteWithoutFlag(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, config.ConfigFile)
	if err := os.WriteFile(configPath, []byte("existing"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Run(Options{Workspace: tempDir, Overwrite: false})
	if err == nil {
		t.Fatal("expected error when file exists and Overwrite is false")
	}

	res, err := Run(Options{Workspace: tempDir, Overwrite: true})
	if err != nil {
		t.Fatalf("expected success with Overwrite=true, got %v", err)
	}
	if res.ConfigPath != configPath {
		t.Errorf("ConfigPath = %s, want %s", res.ConfigPath, configPath)
	}
}

func TestScanWorkspaceProfiles(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	detected := scanWorkspace(tempDir)
	expected := map[string][]string{
		".": {"go"},
	}

	if !reflect.DeepEqual(detected, expected) {
		t.Errorf("detected = %#v, want %#v", detected, expected)
	}
}

// A dangling or external symlink must never be followed or replaced by an
// onboarding request, even when the caller explicitly opts into overwrite.
func TestOnboardNeverFollowsConfigurationSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "external.yaml")
	original := []byte("do not overwrite me\n")
	if err := os.WriteFile(target, original, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, config.ConfigFile)
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}
	for _, overwrite := range []bool{false, true} {
		if _, err := Run(Options{Workspace: root, Overwrite: overwrite}); err == nil {
			t.Fatalf("accepted config symlink, overwrite=%v", overwrite)
		}
		actual, err := os.ReadFile(target)
		if err != nil || string(actual) != string(original) {
			t.Fatalf("external config changed: %q, %v", actual, err)
		}
	}
}

func TestOnboardOverwriteReplacesRegularConfig(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, config.ConfigFile)
	if err := os.WriteFile(path, []byte("old content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(Options{Workspace: root, Overwrite: true}); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(config.Runtime{Workspace: root})
	if err != nil || len(loaded.Servers["go"]) != 1 {
		t.Fatalf("generated configuration could not be loaded: %#v %v", loaded.Servers, err)
	}
}
