package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempYAML(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, ".daffodil.yml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write temp yaml: %v", err)
	}
	return p
}

func TestRunFromYAMLRequiresConfig(t *testing.T) {
	err := runFromYAML([]string{})
	if err == nil {
		t.Fatal("expected error for missing --config")
	}
	if !strings.Contains(err.Error(), ".daffodil.yml") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunFromYAMLNoHosts(t *testing.T) {
	cfg := writeTempYAML(t, `
steps:
  - name: Build
    type: local
    command: echo hello
`)
	err := runFromYAML([]string{"--config", cfg})
	if err == nil {
		t.Fatal("expected missing hosts error")
	}
	if !strings.Contains(err.Error(), "hosts[]") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunFromYAMLUnsupportedStepType(t *testing.T) {
	cfg := writeTempYAML(t, `
hosts:
  - name: web1
    host: 127.0.0.1
    user: deploy
steps:
  - name: Invalid
    type: unknown
`)
	err := runFromYAML([]string{"--config", cfg})
	if err == nil {
		t.Fatal("expected unsupported step type error")
	}
	if !strings.Contains(err.Error(), "unsupported step type") {
		t.Fatalf("unexpected error: %v", err)
	}
}
