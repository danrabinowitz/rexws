package main

import (
	"strings"
	"testing"

	"github.com/danrabinowitz/rexws/internal/config"
)

func TestProjectAddWarnsAndPreservesInvalidStyle(t *testing.T) {
	repo := testRepo(t)
	testConfig(t, repo, "", "")
	writeStyledStatuses(t, []config.StatusDef{
		{ID: config.StatusReady, Label: "Ready", Color: "chartreuse", Icon: "ab"},
	})

	second := testRepo(t)
	stdout, stderr, err := runCLI(t, "project", "add", "--name", "second", second)
	if err != nil {
		t.Fatalf("project add: %v", err)
	}
	for _, want := range []string{"warning:", "invalid color", "invalid icon"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want %q", stderr, want)
		}
	}
	if strings.Contains(stdout, "warning:") {
		t.Errorf("warnings leaked to stdout: %q", stdout)
	}
	if !strings.Contains(stdout, "Registered project") {
		t.Errorf("stdout = %q", stdout)
	}

	// Save must keep the user's text so the warning repeats until they fix it.
	reloaded, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := reloaded.Statuses[0]
	if got.Color != "chartreuse" || got.Icon != "ab" {
		t.Errorf("project add rewrote the invalid style: %#v", got)
	}
	if len(reloaded.Warnings) != 2 {
		t.Errorf("warnings did not repeat: %#v", reloaded.Warnings)
	}
}

func TestHereReportsConfigWarning(t *testing.T) {
	f := newHereFixture(t)
	writeStyledStatuses(t, []config.StatusDef{
		{ID: config.StatusReady, Label: "Ready", Color: "chartreuse"},
	})

	stdout, stderr, err := runCLI(t, "here", "--dir", f.target)
	if err != nil {
		t.Fatalf("here: %v", err)
	}
	if !strings.Contains(stderr, "warning:") || !strings.Contains(stderr, "invalid color") {
		t.Errorf("stderr = %q, want the config warning", stderr)
	}
	if strings.Contains(stdout, "warning:") {
		t.Errorf("warning leaked to stdout: %q", stdout)
	}
	if !strings.Contains(stdout, "status") {
		t.Errorf("stdout = %q", stdout)
	}
}
