package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
)

type changedLineRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type changedFile struct {
	Path       string             `json:"path"`
	BasePath   string             `json:"base_path,omitempty"`
	Ranges     []changedLineRange `json:"ranges,omitempty"`
	BaseRanges []changedLineRange `json:"base_ranges,omitempty"`
	Deleted    bool               `json:"deleted,omitempty"`
	Untracked  bool               `json:"untracked,omitempty"`
}

type gitChangeSet struct {
	Base  string
	Files []changedFile
}

var diffHunkPattern = regexp.MustCompile(`^@@ -([0-9]+)(?:,([0-9]+))? \+([0-9]+)(?:,([0-9]+))? @@`)

// gitChanges reads the working tree relative to base without modifying Git
// state. `git diff <base>` includes both staged and unstaged tracked changes;
// untracked files are added separately so newly-created source files are not
// invisible to verify_change.
func (e *Engine) gitChanges(ctx context.Context, base string) (gitChangeSet, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "HEAD"
	}
	if strings.HasPrefix(base, "-") || strings.ContainsAny(base, "\r\n\x00") {
		return gitChangeSet{}, core.NewError(core.InvalidArgument, "base must be a Git revision, not an option")
	}
	if err := runGitVerify(ctx, e.WS.Root(), base); err != nil {
		return gitChangeSet{}, core.WithCause(core.InvalidArgument, "verify_change base revision is not available", err)
	}
	cmd := exec.CommandContext(ctx, "git", "-c", "core.quotePath=false", "-C", e.WS.Root(), "diff", "--no-ext-diff", "--no-color", "--unified=0", "--no-prefix", base, "--")
	out, err := cmd.Output()
	if err != nil {
		return gitChangeSet{}, core.WithCause(core.InternalError, "verify_change could not read git diff", err)
	}
	files, err := parseUnifiedDiff(out)
	if err != nil {
		return gitChangeSet{}, err
	}

	untrackedCmd := exec.CommandContext(ctx, "git", "-C", e.WS.Root(), "ls-files", "--others", "--exclude-standard", "-z")
	untracked, err := untrackedCmd.Output()
	if err != nil {
		return gitChangeSet{}, core.WithCause(core.InternalError, "verify_change could not list untracked files", err)
	}
	byPath := make(map[string]changedFile, len(files))
	for _, file := range files {
		if pathExcludedFromWorkspace(file.Path) {
			continue
		}
		byPath[file.Path] = file
	}
	for _, raw := range bytes.Split(untracked, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		path := filepath.ToSlash(string(raw))
		if pathExcludedFromWorkspace(path) {
			continue
		}
		if _, exists := byPath[path]; exists {
			continue
		}
		full, resolveErr := e.WS.Resolve(path)
		if resolveErr != nil {
			return gitChangeSet{}, resolveErr
		}
		content, readErr := os.ReadFile(full)
		if readErr != nil {
			return gitChangeSet{}, core.WithCause(core.InternalError, "verify_change could not read untracked file "+path, readErr)
		}
		lines := 1
		if len(content) > 0 {
			lines = bytes.Count(content, []byte{'\n'}) + 1
		}
		byPath[path] = changedFile{Path: path, Ranges: []changedLineRange{{Start: 1, End: lines}}, Untracked: true}
	}

	files = files[:0]
	for _, file := range byPath {
		files = append(files, file)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return gitChangeSet{Base: base, Files: files}, nil
}

func pathExcludedFromWorkspace(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if workspaceListExcludedDirs[part] {
			return true
		}
	}
	return false
}

func runGitVerify(ctx context.Context, root, base string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--verify", "--end-of-options", base+"^{commit}")
	return cmd.Run()
}

// parseUnifiedDiff extracts only the current-worktree line ranges needed for
// semantic symbol mapping. It deliberately does not interpret patch content.
func parseUnifiedDiff(data []byte) ([]changedFile, error) {
	var files []changedFile
	var current *changedFile
	var oldPath string
	seenHunk := false
	flush := func() {
		if current == nil || current.Path == "" {
			return
		}
		current.Ranges = mergeChangedRanges(current.Ranges)
		current.BaseRanges = mergeChangedRanges(current.BaseRanges)
		files = append(files, *current)
	}
	for _, raw := range bytes.Split(data, []byte{'\n'}) {
		line := string(raw)
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flush()
			current = &changedFile{}
			oldPath = ""
			seenHunk = false
		case current != nil && !seenHunk && strings.HasPrefix(line, "--- "):
			oldPath = strings.TrimPrefix(line, "--- ")
			if oldPath != "/dev/null" {
				current.BasePath = filepath.ToSlash(oldPath)
			}
		case current != nil && !seenHunk && strings.HasPrefix(line, "+++ "):
			path := strings.TrimPrefix(line, "+++ ")
			if path == "/dev/null" {
				current.Path = filepath.ToSlash(oldPath)
				current.Deleted = true
			} else {
				current.Path = filepath.ToSlash(path)
			}
		case current != nil && !seenHunk && strings.HasPrefix(line, "rename from "):
			current.BasePath = filepath.ToSlash(strings.TrimPrefix(line, "rename from "))
		case current != nil && !seenHunk && strings.HasPrefix(line, "rename to "):
			current.Path = filepath.ToSlash(strings.TrimPrefix(line, "rename to "))
		case current != nil && strings.HasPrefix(line, "@@ "):
			seenHunk = true
			m := diffHunkPattern.FindStringSubmatch(line)
			if m == nil {
				return nil, core.NewError(core.InternalError, "verify_change could not parse git diff hunk: "+line)
			}
			oldStart, _ := strconv.Atoi(m[1])
			oldCount := 1
			if m[2] != "" {
				oldCount, _ = strconv.Atoi(m[2])
			}
			newStart, _ := strconv.Atoi(m[3])
			newCount := 1
			if m[4] != "" {
				newCount, _ = strconv.Atoi(m[4])
			}
			if oldCount > 0 {
				current.BaseRanges = append(current.BaseRanges, anchoredChangedRange(oldStart, oldCount))
			}
			current.Ranges = append(current.Ranges, anchoredChangedRange(newStart, newCount))
		}
	}
	flush()
	for i := range files {
		if strings.ContainsAny(files[i].Path, "\r\n\x00") {
			return nil, core.NewError(core.InvalidPath, fmt.Sprintf("unsupported Git path in diff: %q", files[i].Path))
		}
	}
	return files, nil
}

func anchoredChangedRange(start, count int) changedLineRange {
	if start < 1 {
		start = 1
	}
	if count <= 0 {
		return changedLineRange{Start: start, End: start}
	}
	return changedLineRange{Start: start, End: start + count - 1}
}

func mergeChangedRanges(ranges []changedLineRange) []changedLineRange {
	if len(ranges) < 2 {
		return ranges
	}
	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].Start != ranges[j].Start {
			return ranges[i].Start < ranges[j].Start
		}
		return ranges[i].End < ranges[j].End
	})
	out := []changedLineRange{ranges[0]}
	for _, r := range ranges[1:] {
		last := &out[len(out)-1]
		if r.Start <= last.End+1 {
			if r.End > last.End {
				last.End = r.End
			}
			continue
		}
		out = append(out, r)
	}
	return out
}
