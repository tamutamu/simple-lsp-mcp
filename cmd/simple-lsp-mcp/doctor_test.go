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
	if !strings.Contains(out.String(), "configuration not found") {
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
	if !report.OK || len(report.Servers) != 1 || !report.Servers[0].Available {
		t.Fatalf("report: %+v", report)
	}
}
