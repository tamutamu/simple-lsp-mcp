package config

import (
	"github.com/tamutamu/simple-lsp-mcp/internal/managedlsp"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"
)

func TestLoadWithoutConfigIsReadOnlyAndLeavesProfilesUnconfigured(t *testing.T) {
	tempDir := t.TempDir()
	runtime, err := Load(Runtime{Workspace: tempDir})
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	for _, name := range []string{ConfigFile, ConfigFileAlt} {
		if _, err := os.Lstat(filepath.Join(tempDir, name)); !os.IsNotExist(err) {
			t.Fatalf("Load unexpectedly created %s: %v", name, err)
		}
	}
	if len(runtime.Servers) != 0 {
		t.Fatalf("configuration was implicitly enabled: %#v", runtime.Servers)
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

func TestLoadAutoDetectsMonorepoWithoutWritingConfig(t *testing.T) {
	root := t.TempDir()
	for _, entry := range []string{"backend/pyproject.toml", "frontend/package.json", "go.mod"} {
		full := filepath.Join(root, entry)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("test"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := Load(Runtime{Workspace: root})
	if err != nil {
		t.Fatal(err)
	}
	for profile, directory := range map[string]string{
		"go": ".", "python": "backend", "typescript-javascript": "frontend",
	} {
		if len(cfg.Servers[profile]) != 1 || cfg.Servers[profile][0].Directory != directory {
			t.Errorf("profile %s: %#v", profile, cfg.Servers[profile])
		}
	}
	if _, err := os.Lstat(filepath.Join(root, ConfigFile)); !os.IsNotExist(err) {
		t.Fatalf("automatic detection wrote configuration: %v", err)
	}
}

func TestAutomaticDetectionSkipsIgnoredDirectoriesAndSymlinks(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"node_modules", ".scratch", ".local"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, directory, "go.mod"), []byte("module x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "node_modules", "go.mod"), filepath.Join(root, "go.mod")); err != nil {
		t.Skip(err)
	}
	if got := DetectWorkspace(root); len(got) != 0 {
		t.Fatalf("detected ignored project files: %#v", got)
	}
}

func TestAutomaticServerDoesNotDownloadWithoutOptIn(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PATH", root)
	for _, profile := range []string{"go", "python", "typescript-javascript", "html", "css"} {
		srv := automaticServer(profile, false)
		if srv.Managed || srv.Command == "npx" || srv.Command == "go" {
			t.Errorf("unexpected managed launcher for %s: %#v", profile, srv)
		}
	}
}

func TestAutomaticServerManagedModeDoesNotDependOnLSPInPath(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PATH", root)
	for _, profile := range []string{"go", "python", "typescript-javascript", "html", "css"} {
		srv := automaticServer(profile, true)
		if !srv.Managed || srv.Command != "managed:"+profile || len(srv.Args) != 0 {
			t.Fatalf("%s: %#v", profile, srv)
		}
	}
}

func TestDetectWorkspaceGroupsAssetsUnderNearestProject(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{
		"frontend/package.json", "frontend/src/parts/view.css", "frontend/src/parts/page.html",
		"backend/pyproject.toml", "frontend/node_modules/fake/tsconfig.json", ".claude/skills/fake/package.json",
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got := DetectWorkspace(root)
	if len(got) != 2 {
		t.Fatalf("unexpected roots: %#v", got)
	}
	want := []string{"css", "html", "typescript-javascript"}
	if !reflect.DeepEqual(got["frontend"], want) {
		t.Fatalf("frontend profiles = %#v want %#v", got["frontend"], want)
	}
	if !reflect.DeepEqual(got["backend"], []string{"python"}) {
		t.Fatalf("backend = %#v", got["backend"])
	}
}

func TestLoadExplicitConfigOverridesAutoDetectAndManagedMode(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ConfigFile), []byte("go:\n  command: custom-gopls\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(Runtime{Workspace: root, AutoInstallLSP: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Servers) != 1 || len(cfg.Servers["go"]) != 1 || cfg.Servers["go"][0].Command != "custom-gopls" {
		t.Fatalf("explicit config was changed: %#v", cfg.Servers)
	}
}

func TestManagedLauncherUsesPinnedToolchainPackages(t *testing.T) {
	for _, tc := range []struct{ profile, dependency string }{
		{"python", "pyright@1.1.413"},
		{"typescript-javascript", "typescript@5.9.3"},
		{"html", "vscode-langservers-extracted@4.10.0"},
		{"css", "vscode-langservers-extracted@4.10.0"},
		{"go", "golang.org/x/tools/gopls@v0.24.0"},
	} {
		srv := automaticServer(tc.profile, true)
		if !srv.Managed || srv.Command != "managed:"+tc.profile {
			t.Fatalf("%s: wrong managed launcher: %#v", tc.profile, srv)
		}
		spec, ok := managedlsp.Specification(tc.profile)
		if !ok || !(slices.Contains(spec.Packages, tc.dependency) || spec.GoPackage == tc.dependency) {
			t.Fatalf("%s: missing pinned dependency %#v", tc.profile, spec)
		}
	}
}

func TestManagedDefaultTimeoutRespectsCallerOverride(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	cfg, err := Load(Runtime{Workspace: root, AutoInstallLSP: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Servers["go"]) != 1 || !cfg.Servers["go"][0].Managed || cfg.RequestTimeout != 180*time.Second {
		t.Fatalf("managed defaults: %+v", cfg)
	}
	custom, err := Load(Runtime{Workspace: root, AutoInstallLSP: true, RequestTimeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if custom.RequestTimeout != 3*time.Second {
		t.Fatalf("explicit timeout was lost: %s", custom.RequestTimeout)
	}
}
