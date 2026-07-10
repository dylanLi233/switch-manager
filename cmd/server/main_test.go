package main

import (
	"bytes"
	"path/filepath"
	"testing"
)

func TestRunRejectsMissingConfigFile(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := run([]string{"-config", filepath.Join(t.TempDir(), "missing.yaml")}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
}

func TestRunRejectsUnknownFlag(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := run([]string{"-unknown"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("code = %d", code)
	}
}
