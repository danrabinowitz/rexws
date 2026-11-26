## 1. Architecture & Tech Stack

* **Language:** Go (single static binary, zero CGO).
* **Libraries:**
* CLI: `[github.com/spf13/cobra](https://github.com/spf13/cobra)`
* TUI: `[github.com/charmbracelet/bubbletea](https://github.com/charmbracelet/bubbletea)`, `lipgloss`, `bubbles`
* Config/State: Standard `encoding/json` (or `gopkg.in/yaml.v3` for config).


* **Core Principle (Enrichment Layer):** Git and Rex are the sources of truth for what *exists*. `rexws` only persists your list of tracked projects, worktree provider configs, and human workflow metadata (`status`, `notes`).
* **Frontend-Agnostic Core:** All business logic lives in `internal/core`. Every CLI command supports `--json` so a future native macOS launcher (like `resh`) or Raycast extension can shell out to `rexws` without duplicating logic.
* **Composability Rule:** `rexws` is a substrate, not a platform. Code belongs in this binary only when it is a one-shot derivation from Git, Rex, and `state.json`. Anything that needs its own persistent state, index, cache, schedule, network dependency, or dedicated UI is a separate tool that consumes `rexws --json` and `rexws events --json`; it never becomes a subsystem here. In exchange, the core owes those tools a stable contract: one selector grammar, a versioned JSON shape, a normalized event stream, and machine predicates (`rexws wait`). A feature that needs any of the former is not rejected, only relocated. `IDEAS.md` classifies every known candidate against this rule.

```text
rexws/
├── cmd/rexws/main.go
└── internal/
    ├── config/      # Load/save ~/.config/rexws/config.json & state.json
    ├── git/         # Worktree discovery (`git worktree list --porcelain`)
    ├── provider/    # Pluggable worktree creation/removal (git, wt, custom)
    ├── rex/         # Rex CLI wrapper + `rex events --json` stream subscriber
    ├── core/        # Merges Git + Rex + State into unified Workstream structs
    └── tui/         # Bubbletea app (grouped vertical list; extensible to Kanban)

```

---

## 2. Data Model & Persistence

Store configuration and mutable state in two small, human-readable JSON files under `~/.config/rexws/`.

### `config.json` (Projects & Providers)

```json
{
  "default_provider": "git",
  "providers": {
    "git": {
      "create": ["git", "-C", "{{.RepoPath}}", "worktree", "add", "-b", "{{.Branch}}", "{{.TargetPath}}"],
      "remove": ["git", "-C", "{{.RepoPath}}", "worktree", "remove", "{{.WorktreePath}}"]
    },
    "worktrunk": {
      "create": ["wt", "switch", "--create", "{{.Branch}}"],
      "remove": ["wt", "remove", "{{.Branch}}"],
      "workdir": "{{.RepoPath}}"
    },
    "monorepo-sparse": {
      "create": ["/Users/djr/bin/monorepo-wt-create.sh", "{{.RepoPath}}", "{{.Branch}}", "{{.TargetPath}}"],
      "remove": ["/Users/djr/bin/monorepo-wt-remove.sh", "{{.WorktreePath}}"]
    }
  },
  "projects": [
    {
      "name": "do-si-do",
      "path": "/Users/djr/code/src/danrabinowitz/do-si-do"
    },
    {
      "name": "big-monorepo",
      "path": "/Users/djr/code/src/company/monorepo",
      "provider": "monorepo-sparse",
      "worktree_root": "/Users/djr/code/worktrees/monorepo"
    }
  ],
  "statuses": [
    { "id": "in_progress", "label": "In Progress" },
    { "id": "blocked", "label": "Blocked", "color": "red", "icon": "■" },
    { "id": "in_review", "label": "In Review" },
    { "id": "ready", "label": "Ready" },
    { "id": "done", "label": "Done" }
  ],
  "default_status": "ready"
}

```

`statuses` is the configurable workflow list; IDs, labels, order, and the default are all user-defined. Each entry may set `color` (`auto` by default) and `icon` (empty by default) for the TUI. Both are optional with `omitempty`, so older configs load unchanged. An invalid value warns and is resolved to `auto` or no icon at render time rather than failing the load; the raw text stays in `config.json`.

### `state.json` (Workflow Metadata)

Keyed by canonical worktree path. Missing entries default to `ready`.

```json
{
  "workstreams": {
    "/Users/djr/code/worktrees/monorepo/feat-auth": {
      "status": "in_progress",
      "note": "Waiting on schema migration",
      "updated_at": "2026-11-25T22:00:00Z"
    }
  }
}

```

### Unified In-Memory Model (`core.Workstream`)

* `Project` (string)
* `Branch` (string)
* `Path` (string, canonical)
* `SessionName` (string, `<project>/<branch>`)
* `WorkflowStatus` (configured status ID; the built-in five are `in_progress` | `blocked` | `in_review` | `ready` | `done`)
* `RexSessionID` (string, empty if stopped)
* `AgentState` (`blocked` | `error` | `working` | `done` | `idle` | `stopped`)
* `AgentDetail` (string, e.g., permission prompt kind from `OSC 7501`)

---

## 3. Pluggable Worktree Providers

Every provider defines `create` and `remove` command templates using Go's `text/template`.

* **Template Variables:**
* `{{.RepoPath}}`: Main repository path.
* `{{.Branch}}`: New or existing branch name.
* `{{.TargetPath}}`: Computed destination directory (defaults to `<worktree_root>/<branch>` or sibling `<repo>-wt/<branch>`).
* `{{.WorktreePath}}`: Existing worktree path (for `remove`).


* **Resolution Order:** Project-level `provider` → global `default_provider` → built-in `git`.
* **Custom Script Contract:** A custom sparse-checkout script just needs to create a valid Git worktree (so `git worktree list --porcelain` discovers it) and exit `0`. If `create` needs interactive input (e.g., selecting sparse-checkout folders), the TUI suspends via `tea.ExecProcess` so the script gets full terminal control, then resumes the TUI on exit.

---

## 4. Rex Integration

The coding harness should verify exact `rex` CLI flags against the installed binary (`rex --help`), implementing an adapter in `internal/rex`:

1. **Session Naming:** 1 Rex session per active worktree, labeled `<project>/<branch>`.
2. **Initial Discovery:** Query live sessions and their `program_status` on startup.
3. **Live Updates:** Spawn `rex events --json` in a background goroutine, parse `OSC 7501` / session lifecycle events, and send them into the Bubbletea program via `p.Send(RexEventMsg{...})`.
4. **Switch / Launch:**
* If `RexSessionID != ""` → run Rex's session-select command to jump the client to that session.
* If stopped → spawn a new Rex session rooted in `Path` with label `<project>/<branch>`, then switch to it.


5. **Session 0 Hotkey (`~/.config/rex/init.lua`):** Bind a dedicated shortcut (e.g., `cmd+0` or `cmd+shift+space`) to jump straight back to the `dashboard` session running `rexws`.

---

## 5. CLI Interface

Running `rexws` with no arguments launches the TUI. Subcommands expose the same engine for scripts and future UIs:

| Command | Purpose |
| --- | --- |
| `rexws` / `rexws tui` | Launch the interactive Bubbletea dashboard. |
| `rexws list [--json] [--status ...]` | List all discovered workstreams with merged Rex + workflow state. |
| `rexws switch <selector>` | Jump to (or create) the Rex session for a workstream. |
| `rexws status <selector> <stage>` | Set workflow status (`in_progress`, `blocked`, `in_review`, `ready`, `done`). |
| `rexws new <project> <branch> [--switch] [--client]` | Create a worktree using the project's configured provider. With `--switch`, also create or select the Rex session `<project>/<branch>` in the new worktree; `--client` chooses the Rex app client for that switch. |
| `rexws rm <selector> [--force] [--yes] [--delete-branch] [--json]` | Close Rex session (if running), re-verify the worktree, run the provider's `remove`, clean `state.json`. `--force` prints discarded paths and unique detached-HEAD commits and confirms; retained-branch state is reported separately. `--delete-branch` is the only branch-deletion path and deletes the branch only when merged or patch-equivalent to the default branch; an unmerged branch is never deleted. A provider `remove` must not delete the branch. |
| `rexws project add <path>` | Register a Git repo in `config.json`. |
| `rexws schema` | Print the machine-readable JSON contract. Needs no config, Rex, or repository. |

### JSON contract

On success, every `--json` command writes exactly one document to stdout with a
top-level `schema_version` (currently `1`). Warnings and progress go to stderr.
On failure the error goes to stderr; `rm` writes its partial-failure document
with `error` set, and `here` writes nothing outside a registered worktree. The
commands covered by the contract are `list`, `status`, `switch`, `new`, `rm`,
`project add`, `here`, and `cleanup`.
`docs/json-schema.md` documents every field, type, enum, nullable field, and
omitted field; `rexws schema` emits the same contract as a field manifest
derived from the payload types.

The version follows one rule:

- **Additive changes keep the version.** Adding a field, an optional nested
  field, an enum value, or a command does not change `schema_version`.
  Consumers must ignore unknown fields.
- **Breaking changes bump the version.** Breaking means removing or renaming a
  command, field, or type; changing a field's JSON type, array item type, or
  map value type; changing whether a field is always present or can be `null`;
  removing or narrowing an enum; adding a required field; changing the meaning
  of an existing value; or changing a fixed `const` such as `schema_version`.

A version bump adds a new frozen fixture (`cmd/rexws/testdata/schema/v<p>.json`);
existing fixtures are never edited. Golden tests validate every command's
example document against the manifest and fail on any shape change that was not
recorded deliberately. `rexws events` and satellites are outside this contract.

---

## 6. TUI Design

The TUI is an accordion inside the existing workflow-status groups. Workstreams
are the parent rows and Rex windows are their children; status group headers and
agent badges are not navigable. `Enter` confirms the highlighted row (`Right`
expands a parent, and a second `Right` enters its first child; `Left` collapses
or returns to the parent). `/` filters the hierarchy by case-insensitive
substring while keeping parent context. Live `rex events` streams update agent
badges and window state, and `s` changes a workstream's workflow status.

`TUI-UX.md` is the full UX source of truth: layout, keybindings, search
visibility rules, live-update behavior, error/startup states, and acceptance
criteria. This section intentionally does not duplicate it.

---

## 7. Implementation Milestones (For Your Coding Harness)

1. **Milestone 1 — Core + CLI (`list`, `status`, `project add`):** Config/state loader, `git worktree list --porcelain` parser, and `rexws list --json`.
2. **Milestone 2 — Rex Adapter:** Inspect local `rex` CLI flags, implement session listing, `rex events --json` stream parsing, and `rexws switch`.
3. **Milestone 3 — Pluggable Providers:** Implement template-based `git`, `worktrunk`, and `custom` providers for `rexws new` and `rexws rm`.
4. **Milestone 4 — TUI (implemented):** Accordion hierarchy inside status groups, live `rex events` updates, case-insensitive substring filter, status switcher, and `tea.ExecProcess` handoff for `new`/`rm`. The UX is specified in `TUI-UX.md`.
