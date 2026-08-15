package onboard

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tamutamu/simple-lsp-mcp/internal/config"
	"gopkg.in/yaml.v3"
)

// Options holds flags for onboarding tool execution.
type Options struct {
	Workspace string
	Overwrite bool
}

// Result describes the onboarding summary.
type Result struct {
	ConfigPath string
	Detected   map[string][]string // relPath -> list of detected profiles
}

var skipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	".venv":        true,
	"venv":         true,
	"target":       true,
	"dist":         true,
	"build":        true,
	".next":        true,
}

// Run scans the workspace for project structures and generates .simple-lsp.yaml.
func Run(opts Options) (*Result, error) {
	wsDir := opts.Workspace
	if wsDir == "" {
		var err error
		wsDir, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get working directory: %w", err)
		}
	}

	wsDir = filepath.Clean(wsDir)
	configPath := filepath.Join(wsDir, config.ConfigFile)

	if !opts.Overwrite {
		if _, err := os.Stat(configPath); err == nil {
			return nil, fmt.Errorf("%s already exists; use --yes to overwrite", configPath)
		}
		altPath := filepath.Join(wsDir, config.ConfigFileAlt)
		if _, err := os.Stat(altPath); err == nil {
			return nil, fmt.Errorf("%s already exists; use --yes to overwrite", altPath)
		}
	}

	detected := scanWorkspace(wsDir)

	configMap := make(map[string]map[string]config.Server)

	for relPath, profiles := range detected {
		profMap := make(map[string]config.Server)
		for _, prof := range profiles {
			if defaultSrvList, ok := config.DefaultProfiles[prof]; ok && len(defaultSrvList) > 0 {
				profMap[prof] = defaultSrvList[0]
			}
		}
		if len(profMap) > 0 {
			configMap[relPath] = profMap
		}
	}

	if len(configMap) == 0 {
		// Fallback to default profiles under root "."
		defaultProfMap := make(map[string]config.Server)
		for k, v := range config.DefaultProfiles {
			if len(v) > 0 {
				defaultProfMap[k] = v[0]
			}
		}
		configMap["."] = defaultProfMap
	}

	yamlData, err := yaml.Marshal(configMap)
	if err != nil {
		return nil, fmt.Errorf("failed to generate YAML config: %w", err)
	}

	if err := os.WriteFile(configPath, yamlData, 0644); err != nil {
		return nil, fmt.Errorf("failed to write %s: %w", configPath, err)
	}

	resDetected := make(map[string][]string)
	for relPath, profs := range configMap {
		var list []string
		for p := range profs {
			list = append(list, p)
		}
		sort.Strings(list)
		resDetected[relPath] = list
	}

	return &Result{
		ConfigPath: configPath,
		Detected:   resDetected,
	}, nil
}

func scanWorkspace(wsDir string) map[string][]string {
	detected := make(map[string]map[string]bool)

	_ = filepath.WalkDir(wsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if skipDirs[name] && path != wsDir {
				return filepath.SkipDir
			}
			return nil
		}

		rel, err := filepath.Rel(wsDir, filepath.Dir(path))
		if err != nil {
			return nil
		}

		if rel == "" {
			rel = "."
		}

		fileName := d.Name()
		if detected[rel] == nil {
			detected[rel] = make(map[string]bool)
		}

		switch fileName {
		case "pyproject.toml", "requirements.txt", "Pipfile", "setup.py":
			detected[rel]["python"] = true
		case "package.json", "tsconfig.json", "jsconfig.json":
			detected[rel]["typescript-javascript"] = true
		case "go.mod":
			detected[rel]["go"] = true
		}

		ext := strings.ToLower(filepath.Ext(fileName))
		if ext == ".html" || ext == ".htm" {
			detected[rel]["html"] = true
		}
		if ext == ".css" {
			detected[rel]["css"] = true
		}

		return nil
	})

	res := make(map[string][]string)
	for relPath, profs := range detected {
		var list []string
		for p := range profs {
			list = append(list, p)
		}
		if len(list) > 0 {
			sort.Strings(list)
			res[relPath] = list
		}
	}

	return res
}
