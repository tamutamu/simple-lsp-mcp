// Package managedlsp provisions pinned language servers in a user-owned cache.
// A language server is never installed into PATH or into the user's repository.
package managedlsp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Spec contains the exact version and entrypoint of a managed language server.
type Spec struct {
	Profile    string
	Version    string
	Packages   []string
	Entrypoint string
	GoPackage  string
}

func Specification(profile string) (Spec, bool) {
	switch profile {
	case "python":
		return Spec{Profile: profile, Version: "1.1.413", Packages: []string{"pyright@1.1.413"}, Entrypoint: "pyright/langserver.index.js"}, true
	case "typescript-javascript":
		return Spec{Profile: profile, Version: "5.3.0", Packages: []string{"typescript@5.9.3", "typescript-language-server@5.3.0"}, Entrypoint: "typescript-language-server/lib/cli.mjs"}, true
	case "html":
		return Spec{Profile: profile, Version: "4.10.0", Packages: []string{"vscode-langservers-extracted@4.10.0"}, Entrypoint: "vscode-langservers-extracted/bin/vscode-html-language-server"}, true
	case "css":
		return Spec{Profile: profile, Version: "4.10.0", Packages: []string{"vscode-langservers-extracted@4.10.0"}, Entrypoint: "vscode-langservers-extracted/bin/vscode-css-language-server"}, true
	case "go":
		return Spec{Profile: profile, Version: "0.24.0", GoPackage: "golang.org/x/tools/gopls@v0.24.0"}, true
	}
	return Spec{}, false
}

func Root() (string, error) {
	if override := os.Getenv("SIMPLE_LSP_CACHE_DIR"); override != "" {
		if !filepath.IsAbs(override) {
			return "", fmt.Errorf("SIMPLE_LSP_CACHE_DIR must be absolute")
		}
		return filepath.Clean(override), nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "simple-lsp-mcp", "servers"), nil
}

func Directory(spec Spec) (string, error) {
	root, err := Root()
	if err != nil {
		return "", err
	}
	// Both profile and version are compile-time fixed strings, not user inputs.
	category := spec.Profile
	// HTML and CSS are entrypoints in the same pinned npm package.
	if category == "html" || category == "css" {
		category = "html-css"
	}
	return filepath.Join(root, category, spec.Version), nil
}

func binaryPath(dir string, spec Spec) string {
	if spec.GoPackage != "" {
		name := "gopls"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		return filepath.Join(dir, name)
	}
	return filepath.Join(dir, "node_modules", filepath.FromSlash(spec.Entrypoint))
}

func cached(spec Spec) (string, bool, error) {
	dir, err := Directory(spec)
	if err != nil {
		return "", false, err
	}
	path := binaryPath(dir, spec)
	if _, err := os.Stat(filepath.Join(dir, ".ready")); err != nil {
		return path, false, nil
	}
	st, err := os.Stat(path)
	return path, err == nil && st.Mode().IsRegular(), nil
}

// Status does not create files, launch tools or access the network.
func Status(profile string) (string, bool, string, error) {
	spec, ok := Specification(profile)
	if !ok {
		return "", false, "", fmt.Errorf("unsupported managed LSP %q", profile)
	}
	path, ready, err := cached(spec)
	if err != nil {
		return "", false, "", err
	}
	// The host language toolchain may still be required to run or analyze
	// source, but the managed LSP binary itself never needs to be on PATH.
	runtimeTool := "node"
	if spec.GoPackage != "" {
		runtimeTool = "go"
	}
	if _, err := exec.LookPath(runtimeTool); err != nil {
		return path, ready, runtimeTool, nil
	}
	if ready {
		return path, true, "", nil
	}
	if spec.GoPackage == "" {
		if _, err := exec.LookPath("npm"); err != nil {
			return path, false, "npm", nil
		}
	}
	return path, false, "", nil
}

// Ensure returns an absolute executable plus arguments; no LSP entry in PATH
// is read or written. It only installs when the pinned cache is missing.
func Ensure(ctx context.Context, profile string) (string, []string, error) {
	spec, ok := Specification(profile)
	if !ok {
		return "", nil, fmt.Errorf("unsupported managed LSP %q", profile)
	}
	path, ready, missing, err := Status(profile)
	if err != nil {
		return "", nil, err
	}
	if missing != "" {
		return "", nil, fmt.Errorf("managed %s requires %s for installation or language analysis: %w", profile, missing, exec.ErrNotFound)
	}
	if !ready {
		if err := provision(ctx, spec); err != nil {
			return "", nil, err
		}
		path, ready, err = cached(spec)
		if err != nil {
			return "", nil, err
		}
		if !ready {
			return "", nil, fmt.Errorf("managed %s installation missing executable", profile)
		}
	}
	if spec.GoPackage != "" {
		return path, []string{"serve"}, nil
	}
	node, err := exec.LookPath("node")
	if err != nil {
		return "", nil, fmt.Errorf("managed %s requires node to run: %w", profile, err)
	}
	return node, []string{path, "--stdio"}, nil
}

func provision(ctx context.Context, spec Spec) error {
	dest, err := Directory(spec)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
		return err
	}
	// Lock per profile/version across concurrent LSP sessions and MCP processes.
	lock := dest + ".lock"
	for {
		if _, ready, _ := cached(spec); ready {
			return nil
		}
		err = os.Mkdir(lock, 0700)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		// Crashed installers leave a lock. Remove only after a generous expiry.
		if info, statErr := os.Stat(lock); statErr == nil && time.Since(info.ModTime()) > 15*time.Minute {
			_ = os.Remove(lock)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	defer os.Remove(lock)
	if _, ready, _ := cached(spec); ready {
		return nil
	}
	stage, err := os.MkdirTemp(filepath.Dir(dest), ".simple-lsp-stage-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	installerTimeout := 3 * time.Minute
	installCtx, cancel := context.WithTimeout(ctx, installerTimeout)
	defer cancel()
	var cmd *exec.Cmd
	if spec.GoPackage != "" {
		goBin, err := exec.LookPath("go")
		if err != nil {
			return err
		}
		cmd = exec.CommandContext(installCtx, goBin, "install", spec.GoPackage)
		cmd.Env = append(os.Environ(), "GOBIN="+stage)
	} else {
		npmBin, err := exec.LookPath("npm")
		if err != nil {
			return err
		}
		args := []string{"install", "--prefix", stage, "--no-save", "--no-package-lock", "--ignore-scripts", "--no-audit", "--no-fund"}
		args = append(args, spec.Packages...)
		cmd = exec.CommandContext(installCtx, npmBin, args...)
	}
	cmd.Dir = stage
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("install managed %s (%s): %w: %s", spec.Profile, spec.Version, err, strings.TrimSpace(string(output)))
	}
	if info, err := os.Stat(binaryPath(stage, spec)); err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("managed %s installer did not produce expected entrypoint: %s", spec.Profile, binaryPath(stage, spec))
	}
	if err := os.WriteFile(filepath.Join(stage, ".ready"), []byte(spec.Version+"\n"), 0600); err != nil {
		return err
	}
	// Install into a new versioned directory; failed temp installs never mark ready.
	if _, err := os.Stat(dest); err == nil {
		// The lock is held; an unready version dir is from an interrupted process.
		if err := os.RemoveAll(dest); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(stage, dest); err != nil {
		return err
	}
	return nil
}
