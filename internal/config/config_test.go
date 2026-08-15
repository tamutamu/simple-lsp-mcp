package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCreatesDefaultConfigFileWhenMissing(t *testing.T) {
	tempDir := t.TempDir()
	runtime, err := Load(Runtime{Workspace: tempDir})
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	configPath := filepath.Join(tempDir, ConfigFile)
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("expected config file %s to be created, got %v", configPath, err)
	}

	if len(runtime.Servers) == 0 {
		t.Fatalf("expected default servers to be loaded, got empty map")
	}
}

func TestLoadReadsExistingYamlConfigFile(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, ConfigFile)
	content := `
.:
  go:
    command: gopls
    args: []
apps/backend:
  python:
    command: pyright-langserver
    args: ["--stdio"]
    env:
      PYTHONPATH: "apps/backend"
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	runtime, err := Load(Runtime{Workspace: tempDir})
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(runtime.Servers["go"]) != 1 || runtime.Servers["go"][0].Command != "gopls" || runtime.Servers["go"][0].Directory != "." {
		t.Fatalf("expected go profile, got %#v", runtime.Servers["go"])
	}

	pyServers := runtime.Servers["python"]
	if len(pyServers) != 1 || pyServers[0].Directory != "apps/backend" || pyServers[0].Env["PYTHONPATH"] != "apps/backend" {
		t.Fatalf("expected python profile with directory and env under apps/backend, got %#v", pyServers)
	}
}

func TestSelectServer(t *testing.T) {
	servers := []Server{
		{
			Directory: ".",
			Command:   "pyright-root",
		},
		{
			Directory: "apps/backend",
			Command:   "pyright-backend",
		},
	}

	matchedRoot := SelectServer(servers, "main.py")
	if matchedRoot.Command != "pyright-root" {
		t.Errorf("expected pyright-root, got %s", matchedRoot.Command)
	}

	matchedBackend := SelectServer(servers, "apps/backend/src/main.py")
	if matchedBackend.Command != "pyright-backend" {
		t.Errorf("expected pyright-backend, got %s", matchedBackend.Command)
	}
}

func TestLoadRejectsInvalidConfigFiles(t *testing.T) {
	for name, value := range map[string]string{
		"empty file":      "",
		"whitespace file": "   \n",
		"invalid YAML":    "foo: [",
		"unknown profile": ".:\n  rust:\n    command: rust-analyzer\n",
		"empty command":   ".:\n  go:\n    command: \"\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			tempDir := t.TempDir()
			configPath := filepath.Join(tempDir, ConfigFile)
			if err := os.WriteFile(configPath, []byte(value), 0644); err != nil {
				t.Fatal(err)
			}

			if _, err := Load(Runtime{Workspace: tempDir}); err == nil {
				t.Fatal("Load succeeded for invalid config")
			}
		})
	}
}
