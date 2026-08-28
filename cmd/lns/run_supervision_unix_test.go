//go:build !windows

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestExecuteRunsStopsWholeProcessGroupWhenChildFails(t *testing.T) {
	root := t.TempDir()
	pids := filepath.Join(root, "pids")
	ready := filepath.Join(root, "ready")
	worker := writeSupervisorFixture(t, root)

	err := executeRuns(context.Background(), []serviceRun{
		{Name: "worker", Root: root, Command: []string{worker, pids}, Env: append(os.Environ(), "READY_FILE="+ready)},
		{Name: "failure", Root: root, Command: []string{"sh", "-c", fmt.Sprintf("while [ ! -f %q ]; do sleep 0.01; done; exit 7", ready)}, Env: os.Environ()},
	})
	if err == nil || !strings.Contains(err.Error(), "failure exited") {
		t.Fatalf("expected the failing child to be reported, got %v", err)
	}
	assertFixtureProcessesStopped(t, pids)
}

func TestExecuteRunsReapsStartedChildrenWhenLaterStartFails(t *testing.T) {
	root := t.TempDir()
	pids := filepath.Join(root, "pids")
	ready := filepath.Join(root, "ready")
	worker := writeSupervisorFixture(t, root)
	calls := 0
	factory := func(name string, args ...string) *exec.Cmd {
		calls++
		if calls == 2 {
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				if _, err := os.Stat(ready); err == nil {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			return exec.Command(filepath.Join(root, "does-not-exist"))
		}
		return exec.Command(name, args...)
	}
	err := executeRunsWithFactory(context.Background(), []serviceRun{
		{Name: "worker", Root: root, Command: []string{worker, pids}, Env: append(os.Environ(), "READY_FILE="+ready)},
		{Name: "missing", Root: root, Command: []string{"missing"}, Env: os.Environ()},
	}, factory)
	if err == nil || !strings.Contains(err.Error(), "start missing") {
		t.Fatalf("expected the later start failure, got %v", err)
	}
	assertFixtureProcessesStopped(t, pids)
}

func TestExecuteRunsReturnsCleanlyOnInterrupt(t *testing.T) {
	root := t.TempDir()
	pids := filepath.Join(root, "pids")
	ready := filepath.Join(root, "ready")
	worker := writeSupervisorFixture(t, root)
	done := make(chan error, 1)
	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stopSignals()
	go func() {
		done <- executeRuns(ctx, []serviceRun{{
			Name: "worker", Root: root, Command: []string{worker, pids},
			Env: append(os.Environ(), "READY_FILE="+ready),
		}})
	}()
	waitForFile(t, ready)
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("interrupt should be a clean shutdown, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("executeRuns did not stop after interrupt")
	}
	assertFixtureProcessesStopped(t, pids)
}

func writeSupervisorFixture(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "worker.sh")
	contents := `#!/bin/sh
trap 'exit 0' INT TERM
sleep 60 &
child=$!
printf '%s %s\n' "$$" "$child" > "$1"
: > "$READY_FILE"
wait "$child"
`
	if err := os.WriteFile(path, []byte(contents), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func assertFixtureProcessesStopped(t *testing.T, path string) {
	t.Helper()
	waitForFile(t, path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range strings.Fields(string(data)) {
		pid, err := strconv.Atoi(value)
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(time.Second)
		for processAliveForTest(pid) && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if processAliveForTest(pid) {
			t.Fatalf("process %d survived LNS cleanup", pid)
		}
	}
}

func processAliveForTest(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || !errors.Is(err, syscall.ESRCH)
}
