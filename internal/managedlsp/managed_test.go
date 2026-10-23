package managedlsp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRootOverridesRequireAbsolutePath(t *testing.T) {
	t.Setenv("SIMPLE_LSP_CACHE_DIR", "relative/path")
	if _, err := Root(); err == nil {
		t.Fatal("relative cache directory accepted")
	}
	root := t.TempDir()
	t.Setenv("SIMPLE_LSP_CACHE_DIR", root)
	got, err := Root()
	if err != nil || got != root {
		t.Fatalf("root=%s err=%v", got, err)
	}
}

func TestManagedNodePackageIsInstalledOnceWithoutModifyingPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses POSIX shell")
	}
	dir := t.TempDir()
	t.Setenv("SIMPLE_LSP_CACHE_DIR", filepath.Join(dir, "cache"))
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	npm := `#!/bin/sh
set -eu
prefix=''
while [ "$#" -gt 0 ]; do
 if [ "$1" = '--prefix' ]; then shift; prefix=$1; fi
 shift
done
mkdir -p "$prefix/node_modules/pyright"
printf 'console.log("server");\n' > "$prefix/node_modules/pyright/langserver.index.js"
echo called >> "$CALL_LOG"
`
	if err := os.WriteFile(filepath.Join(bin, "npm"), []byte(npm), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "node"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	t.Setenv("CALL_LOG", filepath.Join(dir, "calls"))
	if _, ready, _, err := Status("python"); err != nil || ready {
		t.Fatalf("unexpected ready before install: %v %v", ready, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd, args, err := Ensure(ctx, "python")
			if err == nil {
				if !strings.HasPrefix(args[0], filepath.Join(dir, "cache")) || !filepath.IsAbs(cmd) {
					err = os.ErrInvalid
				}
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	calls, err := os.ReadFile(filepath.Join(dir, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	if string(calls) != "called\n" {
		t.Fatalf("npm installed more than once: %q", calls)
	}
	_, ready, _, err := Status("python")
	if err != nil || !ready {
		t.Fatalf("not cached: %v %v", ready, err)
	}
	// npm is not needed after the first successful package installation.
	if err := os.Remove(filepath.Join(bin, "npm")); err != nil {
		t.Fatal(err)
	}
	command, args, err := Ensure(ctx, "python")
	if err != nil || !filepath.IsAbs(command) || len(args) != 2 {
		t.Fatalf("cached Node LSP required npm: command=%q args=%#v err=%v", command, args, err)
	}
}

func TestManagedGoServerRequiresToolchainButNotGlobalGopls(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses POSIX shell")
	}
	root := t.TempDir()
	t.Setenv("SIMPLE_LSP_CACHE_DIR", filepath.Join(root, "cache"))
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
set -eu
printf '#!/bin/sh\nexit 0\n' > "$GOBIN/gopls"
chmod +x "$GOBIN/gopls"
`
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	// Existing GOBIN must not move managed installations outside the cache.
	t.Setenv("GOBIN", filepath.Join(root, "user-global-bin"))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	path, args, err := Ensure(ctx, "go")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(path) || !strings.HasPrefix(path, filepath.Join(root, "cache")) || len(args) != 1 || args[0] != "serve" {
		t.Fatalf("unexpected launcher %q %#v", path, args)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+"/usr/bin:/bin")
	// gopls is cached, but Go projects still need the Go toolchain to analyze.
	path2, ready, missing, err := Status("go")
	if err != nil || !ready || path2 != path || missing != "go" {
		t.Fatalf("missing toolchain not reported: %s ready=%v missing=%s err=%v", path2, ready, missing, err)
	}
	if _, _, err := Ensure(ctx, "go"); err == nil || !strings.Contains(err.Error(), "requires go") {
		t.Fatalf("expected helpful missing-go error: %v", err)
	}
	if _, err := exec.LookPath("gopls"); err == nil {
		t.Fatal("gopls unexpectedly on PATH")
	}
}

func TestStatusDoesNotInstallAnything(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SIMPLE_LSP_CACHE_DIR", root)
	_, ready, _, err := Status("python")
	if err != nil || ready {
		t.Fatalf("unexpected status: ready=%v err=%v", ready, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("status created files: %v", entries)
	}
}

func TestHTMLAndCSSReuseManagedPackageCache(t *testing.T) {
	html, _ := Specification("html")
	css, _ := Specification("css")
	t.Setenv("SIMPLE_LSP_CACHE_DIR", t.TempDir())
	htmlDir, err := Directory(html)
	if err != nil {
		t.Fatal(err)
	}
	cssDir, err := Directory(css)
	if err != nil {
		t.Fatal(err)
	}
	if htmlDir != cssDir {
		t.Fatalf("HTML and CSS duplicate the same npm dependency: %s != %s", htmlDir, cssDir)
	}
}
