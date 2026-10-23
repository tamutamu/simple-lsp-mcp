package config

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

var discoverySkipDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true, ".local": true, ".scratch": true, ".claude": true, ".codex": true, ".agents": true,
	".venv": true, "venv": true, "node_modules": true, "target": true,
	"dist": true, "build": true, ".next": true, ".turbo": true,
	".cache": true, "coverage": true, "__pycache__": true,
}

// DetectWorkspace locates project roots without writing files or following symlinks.
// Asset-only HTML/CSS directories share their nearest project server instead of
// spawning one server per component folder. The same rules serve onboard and startup.
func DetectWorkspace(root string) map[string][]string {
	projects := map[string]map[string]bool{}
	assets := map[string]bool{}
	add := func(dir, profile string) {
		if projects[dir] == nil {
			projects[dir] = map[string]bool{}
		}
		projects[dir][profile] = true
	}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && discoverySkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		dir, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return nil
		}
		switch d.Name() {
		case "pyproject.toml", "requirements.txt", "Pipfile", "setup.py":
			add(dir, "python")
		case "package.json", "tsconfig.json", "jsconfig.json":
			add(dir, "typescript-javascript")
		case "go.mod":
			add(dir, "go")
		}
		switch strings.ToLower(filepath.Ext(d.Name())) {
		case ".html", ".htm":
			assets["html\x00"+dir] = true
		case ".css":
			assets["css\x00"+dir] = true
		}
		return nil
	})
	for key := range assets {
		pair := strings.SplitN(key, "\x00", 2)
		profile, dir := pair[0], pair[1]
		nearest := "."
		for candidate := range projects {
			if candidate == "." || dir == candidate || strings.HasPrefix(dir, candidate+string(filepath.Separator)) {
				if candidate != "." && len(candidate) > len(nearest) {
					nearest = candidate
				}
			}
		}
		add(nearest, profile)
	}
	result := make(map[string][]string, len(projects))
	for dir, profiles := range projects {
		for profile := range profiles {
			result[dir] = append(result[dir], profile)
		}
		sort.Strings(result[dir])
	}
	return result
}
