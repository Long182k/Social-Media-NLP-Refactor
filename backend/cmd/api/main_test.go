package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAPIFailFastMissingSecrets(t *testing.T) {
	binPath := filepath.Join(t.TempDir(), "api-test-bin")
	buildCmd := exec.Command("go", "build", "-o", binPath, ".")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build cmd/api failed: %v\n%s", err, string(out))
	}

	cmd := exec.Command(binPath)
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"DATABASE_URL=postgres://postgres:postgres@127.0.0.1:5432/social_media?sslmode=disable",
		"VALKEY_URL=redis://127.0.0.1:6379/0",
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit when JWT_SECRET missing; output: %s", string(out))
	}
	if !strings.Contains(string(out), "JWT_SECRET") {
		t.Fatalf("expected JWT_SECRET error message, got: %s", string(out))
	}
}
