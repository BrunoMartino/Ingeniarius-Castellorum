package tools

import (
	"os"
	"testing"
)

func readAudit(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading audit log %s: %v", path, err)
	}
	return string(b)
}
