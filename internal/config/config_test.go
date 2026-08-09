package config

import (
	"os"
	"path/filepath"
	"reflect"
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

	if !reflect.DeepEqual(runtime.Servers, DefaultProfiles) {
		t.Fatalf("expected servers = %#v, got %#v", DefaultProfiles, runtime.Servers)
	}
}

func TestLoadReadsExistingConfigFile(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, ConfigFile)
	content := `{"go":{"command":"gopls","args":[]}}`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	runtime, err := Load(Runtime{Workspace: tempDir})
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(runtime.Servers) != 1 || runtime.Servers["go"].Command != "gopls" {
		t.Fatalf("expected go profile, got %#v", runtime.Servers)
	}
}

func TestLoadRejectsInvalidConfigFiles(t *testing.T) {
	for name, value := range map[string]string{
		"empty file":      "",
		"whitespace file": "   \n",
		"invalid JSON":    `{`,
		"unknown profile": `{"rust":{"command":"rust-analyzer","args":[]}}`,
		"empty command":   `{"go":{"command":"","args":[]}}`,
		"missing args":    `{"go":{"command":"gopls"}}`,
		"non-string args": `{"go":{"command":"gopls","args":[1]}}`,
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

