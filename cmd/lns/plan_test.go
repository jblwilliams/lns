package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lns/internal/projectplan"
)

func TestWritePlanJSONEmitsOnlyMachineReadablePlan(t *testing.T) {
	plan := projectplan.Plan{
		SchemaVersion: 1,
		Project: projectplan.Project{
			Name:   "demo",
			Root:   "/repo",
			Source: projectplan.SourceDiscovered,
		},
	}
	var output bytes.Buffer

	if err := writePlan(&output, plan, true); err != nil {
		t.Fatal(err)
	}

	var decoded projectplan.Plan
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not pure JSON: %v\n%s", err, output.String())
	}
	if decoded.Project.Name != "demo" {
		t.Fatalf("unexpected JSON: %s", output.String())
	}
}

func TestRunPlanDoesNotWriteProjectConfig(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{
  "name": "demo",
  "private": true,
  "scripts": {"dev": "vite"},
  "devDependencies": {"vite": "^7"}
}`), 0644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer

	if err := runPlan(&output, root, false); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(output.String(), "http://demo.localhost") {
		t.Fatalf("expected local URL in plan output:\n%s", output.String())
	}
	if _, err := os.Stat(filepath.Join(root, "lns.json")); !os.IsNotExist(err) {
		t.Fatalf("lns plan wrote repository config: %v", err)
	}
}
