package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lns/internal/config"
)

func TestExecuteResetRepoRemovesLocalConfigOnly(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	t.Setenv("HOME", home)

	repoConfigPath := filepath.Join(repo, "lns.json")
	if err := os.WriteFile(repoConfigPath, []byte("{}\n"), 0644); err != nil {
		t.Fatalf("write repo config: %v", err)
	}
	if err := os.MkdirAll(config.GetConfigDir(), 0755); err != nil {
		t.Fatalf("mkdir global config: %v", err)
	}

	summary, err := executeReset(resetScopeRepo, repo)
	if err != nil {
		t.Fatalf("executeReset(repo): %v", err)
	}
	if !summary.repoRemoved {
		t.Fatal("expected repo config to be removed")
	}
	if _, err := os.Stat(repoConfigPath); !os.IsNotExist(err) {
		t.Fatalf("expected repo config to be deleted, got %v", err)
	}
	if _, err := os.Stat(config.GetConfigDir()); err != nil {
		t.Fatalf("expected global config dir to remain, got %v", err)
	}
}

func TestExecuteResetAllRemovesRepoAndGlobalState(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	t.Setenv("HOME", home)

	repoConfigPath := filepath.Join(repo, "lns.json")
	if err := os.WriteFile(repoConfigPath, []byte("{}\n"), 0644); err != nil {
		t.Fatalf("write repo config: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(config.GetCaddyConfigDir(), "nested"), 0755); err != nil {
		t.Fatalf("mkdir global nested dir: %v", err)
	}
	if err := os.WriteFile(config.GetSettingsPath(), []byte("{}\n"), 0644); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	summary, err := executeReset(resetScopeAll, repo)
	if err != nil {
		t.Fatalf("executeReset(all): %v", err)
	}
	if !summary.repoRemoved || !summary.globalRemoved {
		t.Fatalf("expected both repo and global state removed, got %#v", summary)
	}
	if _, err := os.Stat(repoConfigPath); !os.IsNotExist(err) {
		t.Fatalf("expected repo config to be deleted, got %v", err)
	}
	if _, err := os.Stat(config.GetConfigDir()); !os.IsNotExist(err) {
		t.Fatalf("expected global config dir to be deleted, got %v", err)
	}
}

func TestPromptResetApprovalRequiresConfirm(t *testing.T) {
	approved, err := promptResetApproval(bufio.NewReader(strings.NewReader("repo\n")))
	if err != nil {
		t.Fatalf("promptResetApproval non-confirm: %v", err)
	}
	if approved {
		t.Fatal("expected non-confirm input to cancel reset")
	}

	approved, err = promptResetApproval(bufio.NewReader(strings.NewReader("confirm\n")))
	if err != nil {
		t.Fatalf("promptResetApproval confirm: %v", err)
	}
	if !approved {
		t.Fatal("expected confirm input to continue reset")
	}
}
