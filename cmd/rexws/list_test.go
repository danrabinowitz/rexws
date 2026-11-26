package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
		Workstreams []struct {
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
