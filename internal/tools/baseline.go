package tools

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tamutamu/simple-lsp-mcp/internal/config"
	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/workspace"
)

const (
	baseSnapshotMaxFiles = 20000
	baseSnapshotMaxBytes = 512 << 20
)

// baseEngine materializes one committed Git revision into a temporary
// directory and starts an isolated Engine over that snapshot. This lets
// verify_change compare diagnostics without mutating the user's worktree or
// Git metadata. The caller must invoke cleanup.
func (e *Engine) baseEngine(ctx context.Context, base string) (*Engine, func(), error) {
	root, err := os.MkdirTemp("", "simple-lsp-base-")
	if err != nil {
		return nil, nil, core.WithCause(core.InternalError, "could not create base snapshot directory", err)
	}
	cleanup := func() { _ = os.RemoveAll(root) }

	cmd := exec.CommandContext(ctx, "git", "-C", e.WS.Root(), "archive", "--format=tar", base)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cleanup()
		return nil, nil, core.WithCause(core.InternalError, "could not open git archive stream", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		cleanup()
		return nil, nil, core.WithCause(core.InternalError, "could not start git archive", err)
	}
	extractErr := extractBaseArchive(stdout, root)
	waitErr := cmd.Wait()
	if extractErr != nil {
		cleanup()
		return nil, nil, extractErr
	}
	if waitErr != nil {
		cleanup()
		message := "git archive failed"
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			message += ": " + detail
		}
		return nil, nil, core.WithCause(core.InternalError, message, waitErr)
	}

	ws, err := workspace.Open(root)
	if err != nil {
		cleanup()
		return nil, nil, core.WithCause(core.InternalError, "could not open base snapshot workspace", err)
	}
	cfg := cloneRuntime(e.runtime)
	cfg.Workspace = ws.Root()
	baseEngine := New(ws, cfg)
	return baseEngine, func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), e.Timeout)
		defer cancel()
		baseEngine.Sessions.Shutdown(shutdownCtx)
		cleanup()
	}, nil
}

func extractBaseArchive(r io.Reader, root string) error {
	tr := tar.NewReader(r)
	files := 0
	var total int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return core.WithCause(core.InternalError, "could not read git archive", err)
		}
		name := filepath.Clean(filepath.FromSlash(h.Name))
		if name == "." || filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return core.NewError(core.InvalidPath, "git archive contained an unsafe path")
		}
		if pathExcludedFromWorkspace(filepath.ToSlash(name)) {
			continue
		}
		target := filepath.Join(root, name)
		rel, err := filepath.Rel(root, target)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return core.NewError(core.InvalidPath, "git archive path escaped the base snapshot")
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return core.WithCause(core.InternalError, "could not create base snapshot directory", err)
			}
		case tar.TypeReg, tar.TypeRegA:
			files++
			total += h.Size
			if files > baseSnapshotMaxFiles || total > baseSnapshotMaxBytes {
				return core.NewError(core.IncompleteSearch, fmt.Sprintf("base snapshot exceeds verification budget (%d files or %d MiB)", baseSnapshotMaxFiles, baseSnapshotMaxBytes>>20))
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return core.WithCause(core.InternalError, "could not create base snapshot parent", err)
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
			if err != nil {
				return core.WithCause(core.InternalError, "could not create base snapshot file", err)
			}
			_, copyErr := io.CopyN(f, tr, h.Size)
			closeErr := f.Close()
			if copyErr != nil {
				return core.WithCause(core.InternalError, "could not extract base snapshot file", copyErr)
			}
			if closeErr != nil {
				return core.WithCause(core.InternalError, "could not close base snapshot file", closeErr)
			}
		case tar.TypeSymlink, tar.TypeLink:
			return core.NewError(core.IncompleteSearch, "base snapshot contains symlinks or hard links; baseline diagnostics cannot be verified safely")
		default:
			// Ignore metadata-only tar entries. Git archives should not contain
			// devices or sockets, but they are not needed for language analysis.
		}
	}
}

func cloneRuntime(in config.Runtime) config.Runtime {
	out := in
	out.Servers = make(map[string][]config.Server, len(in.Servers))
	for key, servers := range in.Servers {
		out.Servers[key] = append([]config.Server(nil), servers...)
	}
	return out
}
