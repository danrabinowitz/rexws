package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/danrabinowitz/rexws/internal/config"
)

// TestContractRealCommandOutputs captures stdout from real command runs in
// fixture repositories and validates every document against the manifest. It
// covers the states the hand-written examples cannot: attached worktrees,
// detached and forced removal, branch deletion, and new with and without
// --switch.
func TestContractRealCommandOutputs(t *testing.T) {
	manifest := loadManifest(t, frozenManifestPath(SchemaVersion))

	capture := func(t *testing.T, command string, args ...string) {
		t.Helper()
		stdout, stderr, err := runCLI(t, args...)
		if err != nil {
			t.Fatalf("%s: %v\nstderr: %s", command, err, stderr)
		}
		validateCaptured(t, manifest, command, stdout)
	}

	t.Run("list", func(t *testing.T) {
		repo := testRepo(t)
		root := filepath.Join(t.TempDir(), "worktrees")
		testConfig(t, repo, root, "")
		gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/auth", filepath.Join(root, "feat", "auth"))
		contractFailingRex(t)
		capture(t, "list", "list", "--json")
	})

	t.Run("list with detached worktree", func(t *testing.T) {
		repo := testRepo(t)
		root := filepath.Join(t.TempDir(), "worktrees")
		testConfig(t, repo, root, "")
		gitRun(t, repo, "worktree", "add", "-q", "--detach", filepath.Join(root, "detached"))
		contractFailingRex(t)
		capture(t, "list", "list", "--json")
	})

	t.Run("status", func(t *testing.T) {
		newHereFixture(t)
		capture(t, "status", "status", "repo/feat/auth", "blocked", "--json")
	})

	t.Run("switch", func(t *testing.T) {
		newHereFixture(t)
		fakeRexNew(t,
			`{"clients":[{"client_id":"client:app","info":{"kind":"app"}}]}`,
			`{"sessions":[{"session_id":"session:auth","label":"repo/feat/auth"}]}`,
		)
		capture(t, "switch", "switch", "repo/feat/auth", "--client", "client:app", "--json")
	})

	t.Run("new without switch", func(t *testing.T) {
		repo := testRepo(t)
		root := filepath.Join(t.TempDir(), "worktrees")
		testConfig(t, repo, root, "")
		capture(t, "new", "new", "repo", "feat/plain", "--json")
	})

	t.Run("new with switch", func(t *testing.T) {
		repo := testRepo(t)
		root := filepath.Join(t.TempDir(), "worktrees")
		testConfig(t, repo, root, "")
		fakeRexNew(t,
			`{"clients":[{"client_id":"client:app","info":{"kind":"app"}}]}`,
			`{"sessions":[]}`,
		)
		capture(t, "new", "new", "repo", "feat/switched", "--switch", "--json")
	})

	t.Run("here", func(t *testing.T) {
		f := newHereFixture(t)
		t.Chdir(f.target)
		capture(t, "here", "here", "--json")
	})

	t.Run("project add", func(t *testing.T) {
		repo := testRepo(t)
		t.Setenv(config.EnvDir, filepath.Join(t.TempDir(), "config"))
		capture(t, "project add", "project", "add", repo, "--json")
	})

	t.Run("rm", func(t *testing.T) {
		_, _, targetPath := rmFixture(t, "feat/rm-plain")
		capture(t, "rm", "rm", targetPath, "--json")
	})

	t.Run("rm force", func(t *testing.T) {
		_, target, targetPath := rmFixture(t, "feat/rm-force")
		if err := os.WriteFile(filepath.Join(target, "untracked.txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		capture(t, "rm", "rm", targetPath, "--force", "--yes", "--json")
	})

	t.Run("rm detached", func(t *testing.T) {
		repo := testRepo(t)
		root := filepath.Join(t.TempDir(), "worktrees")
		testConfig(t, repo, root, "")
		target := filepath.Join(root, "detached")
		gitRun(t, repo, "worktree", "add", "-q", "--detach", target)
		rmFakeRex(t)
		capture(t, "rm", "rm", mustCanonical(t, target), "--force", "--yes", "--json")
	})

	t.Run("rm delete-branch", func(t *testing.T) {
		_, _, targetPath := rmFixture(t, "feat/rm-merged")
		capture(t, "rm", "rm", targetPath, "--delete-branch", "--yes", "--json")
	})

	t.Run("cleanup", func(t *testing.T) {
		repo := cleanupRepo(t)
		root := filepath.Join(t.TempDir(), "worktrees")
		testConfig(t, repo, root, "")
		target := filepath.Join(root, "feat", "done")
		gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/done", target)
		cleanupCommit(t, target, "done.txt", "done\n", "done work")
		gitRun(t, repo, "merge", "-q", "--ff-only", "feat/done")
		cleanupFakeRex(t, `{"sessions":[]}`)
		capture(t, "cleanup", "cleanup", "--json")
	})

	t.Run("cleanup with stale state", func(t *testing.T) {
		repo := cleanupRepo(t)
		root := filepath.Join(t.TempDir(), "worktrees")
		testConfig(t, repo, root, "")
		cfg, err := config.Load()
		if err != nil {
			t.Fatal(err)
		}
		state, err := config.LoadState(cfg.StatusIDs())
		if err != nil {
			t.Fatal(err)
		}
		state.Workstreams[filepath.Join(root, "feat", "gone")] = config.WorkstreamState{
			Status:    config.StatusReady,
			UpdatedAt: time.Now().UTC(),
		}
		if err := state.Save(); err != nil {
			t.Fatal(err)
		}
		cleanupFakeRex(t, `{"sessions":[]}`)
		capture(t, "cleanup", "cleanup", "--json")
	})

	t.Run("rm partial failure", func(t *testing.T) {
		repo := testRepo(t)
		root := filepath.Join(t.TempDir(), "worktrees")
		testConfig(t, repo, root, "")
		target := filepath.Join(root, "feat", "provider-fail")
		gitRun(t, repo, "worktree", "add", "-q", "-b", "feat/provider-fail", target)
		targetPath := mustCanonical(t, target)

		cfg, err := config.Load()
		if err != nil {
			t.Fatal(err)
		}
		script := fmt.Sprintf("git -C %q worktree remove %q && exit 1", repo, targetPath)
		cfg.Providers["fake"] = config.Provider{Remove: []string{"sh", "-c", script}}
		cfg.Projects[0].Provider = "fake"
		if err := cfg.Save(); err != nil {
			t.Fatal(err)
		}
		rmFakeRex(t)

		stdout, stderr, err := runCLI(t, "rm", targetPath, "--json")
		if err == nil {
			t.Fatal("expected the provider failure")
		}
		validateCaptured(t, manifest, "rm", stdout)
		var result RmPayload
		if jsonErr := json.Unmarshal([]byte(stdout), &result); jsonErr != nil {
			t.Fatalf("parse partial JSON: %v\nstderr: %s", jsonErr, stderr)
		}
		if result.Error == "" || len(result.RetainedBranches) != 1 {
			t.Errorf("partial result = %#v, want error and the retained branch", result)
		}
	})
}

// validateCaptured validates one captured stdout document against the manifest,
// its schema version, and its payload type.
func validateCaptured(t *testing.T, manifest schemaManifest, command, stdout string) {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(stdout), &value); err != nil {
		t.Fatalf("%s wrote invalid JSON: %v\n%s", command, err, stdout)
	}
	validateCommand(t, manifest, command, value)
	assertExampleSchemaVersion(t, command, value)
	roundTripPayload(t, command, value)
}

// roundTripPayload decodes a real document into its payload type and
// re-encodes it. The two documents must be equal, so no emitted field lacks a
// typed counterpart.
func roundTripPayload(t *testing.T, command string, value any) {
	t.Helper()
	original, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	target := payloadTarget(command)
	if err := json.Unmarshal(original, target); err != nil {
		t.Fatalf("%s does not decode into its payload type: %v", command, err)
	}
	encoded, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	var before, after any
	if err := json.Unmarshal(original, &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("%s round-trip differs:\n before: %s\n after:  %s", command, original, encoded)
	}
}

func contractFailingRex(t *testing.T) {
	t.Helper()
	fake := filepath.Join(t.TempDir(), "rex")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.EnvRexBin, fake)
}
