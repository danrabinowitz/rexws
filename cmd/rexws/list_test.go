package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danrabinowitz/rexws/internal/config"
)

func failingRex(t *testing.T) {
	t.Helper()
	fake := filepath.Join(t.TempDir(), "rex")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.EnvRexBin, fake)
}

func TestListJSONPopulatesStatusLabelAndDegradesWithoutRex(t *testing.T) {
	repo := testRepo(t)
	testConfig(t, repo, "", "")

	fake := filepath.Join(t.TempDir(), "rex")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REXWS_REX_BIN", fake)

	stdout, _, err := runCLI(t, "list", "--json")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var payload struct {
		SchemaVersion int `json:"schema_version"`
		Workstreams   []struct {
			Project             string   `json:"project"`
			Branch              string   `json:"branch"`
			WorkflowStatus      string   `json:"workflow_status"`
			WorkflowStatusLabel string   `json:"workflow_status_label"`
			AgentState          string   `json:"agent_state"`
			Warnings            []string `json:"warnings"`
		} `json:"workstreams"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("parse JSON %q: %v", stdout, err)
	}
	if len(payload.Workstreams) != 1 {
		t.Fatalf("workstreams = %#v", payload.Workstreams)
	}
	if payload.SchemaVersion != SchemaVersion {
		t.Errorf("schema_version = %d, want %d", payload.SchemaVersion, SchemaVersion)
	}
	ws := payload.Workstreams[0]
	if ws.WorkflowStatus != "ready" || ws.WorkflowStatusLabel != "Ready" {
		t.Errorf("status = (%q, %q), want (ready, Ready)", ws.WorkflowStatus, ws.WorkflowStatusLabel)
	}
	if ws.AgentState != "stopped" {
		t.Errorf("agent state = %q, want stopped", ws.AgentState)
	}
	if len(payload.Warnings) == 0 || !strings.Contains(payload.Warnings[0], "Rex unavailable") {
		t.Errorf("warnings = %#v, want a Rex-unavailable warning", payload.Warnings)
	}
}

func writeStyledStatuses(t *testing.T, statuses []config.StatusDef) {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.Statuses = statuses
	cfg.DefaultStatus = statuses[0].ID
	for _, def := range statuses {
		if def.ID == config.StatusReady {
			cfg.DefaultStatus = config.StatusReady
			break
		}
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

func TestListKeepsStatusOutputTextOnly(t *testing.T) {
	repo := testRepo(t)
	testConfig(t, repo, "", "")
	writeStyledStatuses(t, []config.StatusDef{
		{ID: config.StatusInProgress, Label: "In Progress", Color: "red", Icon: "◆"},
		{ID: config.StatusBlocked, Label: "Blocked", Color: "bright-red", Icon: "■"},
		{ID: config.StatusInReview, Label: "In Review", Color: "#808080", Icon: "中"},
		{ID: config.StatusReady, Label: "Ready", Color: "ansi256:245"},
		{ID: config.StatusDone, Label: "Done"},
	})
	failingRex(t)

	stdout, stderr, err := runCLI(t, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, unwanted := range []string{"◆", "■", "中", "\x1b"} {
		if strings.Contains(stdout, unwanted) {
			t.Errorf("list output contains style %q:\n%s", unwanted, stdout)
		}
	}
	if !strings.Contains(stdout, "Ready") {
		t.Errorf("list output lost the status label:\n%s", stdout)
	}
	if !strings.Contains(stderr, "warning: Rex unavailable") {
		t.Errorf("stderr = %q, want the Rex warning", stderr)
	}
}

func TestListWarnsAboutInvalidStatusStyle(t *testing.T) {
	repo := testRepo(t)
	testConfig(t, repo, "", "")
	writeStyledStatuses(t, []config.StatusDef{
		{ID: config.StatusReady, Label: "Ready", Color: "chartreuse"},
	})
	failingRex(t)

	stdout, stderr, err := runCLI(t, "list", "--json")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(stderr, "warning:") || !strings.Contains(stderr, "invalid color") {
		t.Errorf("stderr = %q, want a config warning", stderr)
	}
	if got := strings.Count(stderr, "invalid color"); got != 1 {
		t.Errorf("config warning printed %d times, want once:\n%s", got, stderr)
	}

	var payload struct {
		SchemaVersion int      `json:"schema_version"`
		Warnings      []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("parse JSON %q: %v", stdout, err)
	}
	if payload.SchemaVersion != SchemaVersion {
		t.Errorf("schema_version = %d, want %d", payload.SchemaVersion, SchemaVersion)
	}
	found := false
	for _, warning := range payload.Warnings {
		if strings.Contains(warning, "invalid color") {
			found = true
		}
	}
	if !found {
		t.Errorf("JSON warnings = %#v, want the config warning", payload.Warnings)
	}
}
