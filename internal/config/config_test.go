package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLoadDefaultsWithoutCreatingFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvDir, dir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DefaultProvider != "git" {
		t.Errorf("DefaultProvider = %q, want git", cfg.DefaultProvider)
	}
	if _, ok := cfg.Providers["git"]; !ok {
		t.Error("default git provider is missing")
	}
	wantStatuses := []Status{StatusInProgress, StatusBlocked, StatusInReview, StatusReady, StatusDone}
	if got := cfg.StatusIDs(); !slices.Equal(got, wantStatuses) {
		t.Errorf("StatusIDs = %v, want %v", got, wantStatuses)
	}
	if got := cfg.DefaultStatusValue(); got != StatusReady {
		t.Errorf("DefaultStatusValue = %q, want ready", got)
	}
	if got := cfg.StatusLabel(StatusInProgress); got != "In Progress" {
		t.Errorf("StatusLabel(in_progress) = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json")); !os.IsNotExist(err) {
		t.Errorf("Load created config.json: %v", err)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvDir, dir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	project, created, err := cfg.RegisterProject(repo, "repo", "", "/tmp/worktrees")
	if err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}
	if !created {
		t.Fatal("RegisterProject reported an existing project")
	}
	if project.Name != "repo" || project.Path != repo {
		t.Errorf("project = %#v", project)
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}
	if len(loaded.Projects) != 1 || loaded.Projects[0] != project {
		t.Errorf("projects = %#v, want %#v", loaded.Projects, []Project{project})
	}
	if loaded.DefaultProvider != "git" {
		t.Errorf("DefaultProvider = %q", loaded.DefaultProvider)
	}
	if len(loaded.Statuses) != len(DefaultStatuses()) || loaded.DefaultStatusValue() != StatusReady {
		t.Errorf("statuses did not round-trip: %#v default=%q", loaded.Statuses, loaded.DefaultStatusValue())
	}

	info, err := os.Stat(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("config.json mode = %o, want 600", perm)
	}
}

func TestRegisterProjectConflicts(t *testing.T) {
	cfg := Default()
	first, _, err := cfg.RegisterProject("/code/a", "a", "", "")
	if err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}
	if _, created, err := cfg.RegisterProject("/code/a", "other", "", ""); err == nil || created {
		t.Errorf("conflicting name for an existing path should fail: created=%v err=%v", created, err)
	}
	if _, created, err := cfg.RegisterProject("/code/a", "", "", ""); err != nil || created {
		t.Errorf("same path: created=%v err=%v", created, err)
	}
	if _, _, err := cfg.RegisterProject("/code/a", "", "worktrunk", ""); err == nil {
		t.Error("conflicting provider for an existing path should fail")
	}
	if _, created, err := cfg.RegisterProject("/code/b", "a", "", ""); err == nil || created {
		t.Error("duplicate name should fail")
	}
	if _, _, err := cfg.RegisterProject("/code/c", "bad/name", "", ""); err == nil {
		t.Error("name with a path separator should fail")
	}
	if first.Name != "a" {
		t.Errorf("project name = %q", first.Name)
	}
}

func TestValidateRejectsUnknownProvider(t *testing.T) {
	cfg := Default()
	cfg.Projects = []Project{{Name: "a", Path: "/code/a", Provider: "nope"}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected an error for an unknown provider")
	}
}

func TestLoadRejectsMalformedConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvDir, dir)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("expected a parse error")
	}
}

func TestLoadStateDefaultsToReady(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvDir, dir)

	state, err := LoadState(Default().StatusIDs())
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if got := state.For("/some/worktree", StatusReady); got.Status != StatusReady {
		t.Errorf("unknown path status = %q, want ready", got.Status)
	}
	if got := state.For("/some/worktree", "todo"); got.Status != "todo" {
		t.Errorf("custom fallback status = %q, want todo", got.Status)
	}
	if _, err := os.Stat(filepath.Join(dir, "state.json")); !os.IsNotExist(err) {
		t.Errorf("LoadState created state.json: %v", err)
	}
}

func TestStateRoundTripPreservesNote(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvDir, dir)

	state := NewState()
	state.Workstreams["/worktree"] = WorkstreamState{
		Status: StatusInProgress,
		Note:   "waiting on schema migration",
	}
	now := time.Date(2026, 11, 25, 22, 0, 0, 0, time.UTC)
	stored := state.SetStatus("/worktree", StatusBlocked, now)
	if stored.Note != "waiting on schema migration" {
		t.Errorf("SetStatus dropped the note: %#v", stored)
	}
	if !stored.UpdatedAt.Equal(now) {
		t.Errorf("UpdatedAt = %v, want %v", stored.UpdatedAt, now)
	}
	if err := state.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := LoadState(Default().StatusIDs())
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	got := loaded.For("/worktree", StatusReady)
	if got.Status != StatusBlocked || got.Note != "waiting on schema migration" || !got.UpdatedAt.Equal(now) {
		t.Errorf("loaded state = %#v", got)
	}
}

func TestLoadStateRejectsUnknownStatus(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvDir, dir)
	data := []byte(`{"workstreams":{"/worktree":{"status":"shipping","updated_at":"2026-11-25T22:00:00Z"}}}`)
	if err := os.WriteFile(filepath.Join(dir, "state.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadState(Default().StatusIDs()); err == nil {
		t.Fatal("expected an unknown-status error")
	}
}

func TestParseStatus(t *testing.T) {
	cfg := Default()
	for _, def := range cfg.Statuses {
		for _, value := range []string{
			string(def.ID),
			strings.ToUpper(string(def.ID)),
			def.Label,
			strings.ToUpper(def.Label),
		} {
			got, err := cfg.ParseStatus(value)
			if err != nil || got != def.ID {
				t.Errorf("ParseStatus(%q) = %q, %v; want %q", value, got, err, def.ID)
			}
		}
	}
	if _, err := cfg.ParseStatus("shipping"); err == nil {
		t.Error("ParseStatus accepted an unknown stage")
	}
}

func TestCustomStatusSet(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvDir, dir)
	data := []byte(`{
		"statuses": [
			{"id": "todo", "label": "Todo"},
			{"id": "doing", "label": "Doing"},
			{"id": "blocked", "label": "Waiting"},
			{"id": "done"}
		],
		"default_status": "todo"
	}`)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	wantIDs := []Status{"todo", "doing", "blocked", "done"}
	if got := cfg.StatusIDs(); !slices.Equal(got, wantIDs) {
		t.Errorf("StatusIDs = %v, want %v", got, wantIDs)
	}
	if got := cfg.DefaultStatusValue(); got != "todo" {
		t.Errorf("DefaultStatusValue = %q, want todo", got)
	}
	if got := cfg.StatusLabel("blocked"); got != "Waiting" {
		t.Errorf("StatusLabel(blocked) = %q, want Waiting", got)
	}
	if got := cfg.StatusLabel("done"); got != "Done" {
		t.Errorf("humanized label = %q, want Done", got)
	}
	if got, err := cfg.ParseStatus("Waiting"); err != nil || got != "blocked" {
		t.Errorf("ParseStatus(Waiting) = %q, %v", got, err)
	}
	if got, err := cfg.ParseStatus("DOING"); err != nil || got != "doing" {
		t.Errorf("ParseStatus(DOING) = %q, %v", got, err)
	}
	if _, err := cfg.ParseStatus("in_progress"); err == nil {
		t.Error("ParseStatus accepted a status removed from the config")
	}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}
	if !slices.Equal(loaded.StatusIDs(), wantIDs) || loaded.DefaultStatusValue() != "todo" {
		t.Errorf("statuses did not round-trip: %#v default=%q", loaded.Statuses, loaded.DefaultStatusValue())
	}
}

func TestApplyStatusDefaults(t *testing.T) {
	cfg := Config{}
	cfg.applyStatusDefaults()
	if got := cfg.DefaultStatusValue(); got != StatusReady {
		t.Errorf("empty config default = %q, want ready", got)
	}
	if len(cfg.Statuses) != len(DefaultStatuses()) {
		t.Errorf("empty config statuses = %#v", cfg.Statuses)
	}

	custom := Config{Statuses: []StatusDef{{ID: "archive"}, {ID: "todo"}}}
	custom.applyStatusDefaults()
	if custom.Statuses[0].Label != "Archive" || custom.Statuses[1].Label != "Todo" {
		t.Errorf("labels = %#v", custom.Statuses)
	}
	if got := custom.DefaultStatusValue(); got != "archive" {
		t.Errorf("default without ready = %q, want the first status", got)
	}

	withReady := Config{Statuses: []StatusDef{{ID: "todo", Label: "To Do"}, {ID: "ready"}}}
	withReady.applyStatusDefaults()
	if got := withReady.DefaultStatusValue(); got != StatusReady {
		t.Errorf("default = %q, want ready", got)
	}
	if withReady.Statuses[0].Label != "To Do" {
		t.Errorf("existing label overwritten: %#v", withReady.Statuses)
	}
}

func TestValidateRejectsStatusProblems(t *testing.T) {
	tests := map[string]func(*Config){
		"no statuses": func(c *Config) {
			c.Statuses = nil
		},
		"duplicate id": func(c *Config) {
			c.Statuses[1].ID = c.Statuses[0].ID
		},
		"duplicate label": func(c *Config) {
			c.Statuses[1].Label = strings.ToUpper(c.Statuses[0].Label)
		},
		"bad id": func(c *Config) {
			c.Statuses[0].ID = "In Progress"
		},
		"missing default": func(c *Config) {
			c.DefaultStatus = ""
		},
		"unknown default": func(c *Config) {
			c.DefaultStatus = "shipping"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := Default()
			mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected a status validation error")
			}
		})
	}
}

func TestLoadStateRejectsRemovedStatus(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvDir, dir)
	data := []byte(`{"workstreams":{"/worktree":{"status":"in_progress","updated_at":"2026-11-25T22:00:00Z"}}}`)
	if err := os.WriteFile(filepath.Join(dir, "state.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadState([]Status{"todo", "doing", "done"})
	if err == nil {
		t.Fatal("expected an unconfigured-status error")
	}
	for _, want := range []string{"/worktree", "in_progress"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}
