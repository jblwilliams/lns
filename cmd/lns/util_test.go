package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lns/internal/config"
)

func TestStartCaddyUsesDirectExecutableWhenItCanBind(t *testing.T) {
	executable := writeExecutable(t, "#!/bin/sh\nexit 0\n")
	if err := startCaddy(context.Background(), executable, filepath.Join(t.TempDir(), "Caddyfile"), config.DefaultHTTPPort); err != nil {
		t.Fatalf("direct Caddy start failed: %v", err)
	}
}

func TestStartCaddyDoesNotPromptForSudoWithoutTerminal(t *testing.T) {
	executable := writeExecutable(t, "#!/bin/sh\necho 'listen tcp :80: bind: permission denied' >&2\nexit 1\n")
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = write.Close()
	original := os.Stdin
	os.Stdin = read
	t.Cleanup(func() {
		os.Stdin = original
		_ = read.Close()
	})

	err = startCaddy(context.Background(), executable, filepath.Join(t.TempDir(), "Caddyfile"), config.DefaultHTTPPort)
	if err == nil || !strings.Contains(err.Error(), "run `lns start` once in an interactive terminal") {
		t.Fatalf("expected non-interactive elevation guidance, got %v", err)
	}
}

func TestDevNullIsNotInteractive(t *testing.T) {
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	if isTerminal(null) {
		t.Fatal("a character device such as /dev/null must not trigger a sudo prompt")
	}
}

func writeExecutable(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "caddy")
	if err := os.WriteFile(path, []byte(contents), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}
