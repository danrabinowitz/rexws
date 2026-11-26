package config

import (
	"os"
	"path/filepath"
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

	state, err := LoadState()
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if got := state.For("/some/worktree"); got.Status != StatusReady {
		t.Errorf("unknown path status = %q, want ready", got.Status)
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

	loaded, err := LoadState()
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	got := loaded.For("/worktree")
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
	if _, err := LoadState(); err == nil {
		t.Fatal("expected an unknown-status error")
	}
}

func TestParseStatus(t *testing.T) {
	for _, status := range Statuses {
		got, err := ParseStatus(string(status))
		if err != nil || got != status {
			t.Errorf("ParseStatus(%q) = %q, %v", status, got, err)
		}
	}
	if _, err := ParseStatus("shipping"); err == nil {
		t.Error("ParseStatus accepted an unknown stage")
	}
}
