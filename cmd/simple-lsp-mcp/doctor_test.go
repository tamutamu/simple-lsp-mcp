package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/config"
)

func TestDoctorDoesNotCreateConfiguration(t *testing.T) {
	root := t.TempDir()
	var out bytes.Buffer
	if err := runDoctor([]string{"--workspace", root}, &out); err == nil {
		t.Fatal("expected missing config error")
	}
	if _, err := os.Stat(filepath.Join(root, config.ConfigFile)); !os.IsNotExist(err) {
		t.Fatalf("doctor wrote configuration: %v", err)
	}
	if !strings.Contains(out.String(), "no supported language project detected") {
		t.Fatalf("report: %s", out.String())
	}
}

func TestDoctorReportsAvailableExecutable(t *testing.T) {
	root := t.TempDir()
	file := "go:\n  command: " + os.Args[0] + "\n  args: []\n"
	if err := os.WriteFile(filepath.Join(root, config.ConfigFile), []byte(file), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runDoctor([]string{"--workspace", root}, &out); err != nil {
		t.Fatal(err)
	}
	var report doctorReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if !report.OK || report.Version == "" || report.Binary == "" || len(report.Servers) != 1 || !report.Servers[0].Available {
		t.Fatalf("report: %+v", report)
	}
}

func TestDoctorAutoDetectedWorkspaceDoesNotCreateConfig(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/test\ngo 1.26.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	// Missing executable is allowed here: doctor reports availability instead of downloading.
	_ = runDoctor([]string{"--workspace", root}, &out)
	var report doctorReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Config != "auto-detected (in memory)" || len(report.Servers) != 1 || report.Servers[0].Profile != "go" || report.Servers[0].Directory != "." {
		t.Fatalf("wrong auto-detected profile: %+v", report)
	}
	if _, err := os.Lstat(filepath.Join(root, config.ConfigFile)); !os.IsNotExist(err) {
		t.Fatalf("doctor created config: %v", err)
	}
}
