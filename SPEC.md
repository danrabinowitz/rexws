## 1. Architecture & Tech Stack

* **Language:** Go (single static binary, zero CGO).
* **Libraries:**
* CLI: `[github.com/spf13/cobra](https://github.com/spf13/cobra)`
* TUI: `[github.com/charmbracelet/bubbletea](https://github.com/charmbracelet/bubbletea)`, `lipgloss`, `bubbles`
* Config/State: Standard `encoding/json` (or `gopkg.in/yaml.v3` for config).


* **Core Principle (Enrichment Layer):** Git and Rex are the sources of truth for what *exists*. `rexws` only persists your list of tracked projects, worktree provider configs, and human workflow metadata (`status`, `notes`).
* **Frontend-Agnostic Core:** All business logic lives in `internal/core`. Every CLI command supports `--json` so a future native macOS launcher (like `resh`) or Raycast extension can shell out to `rexws` without duplicating logic.

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
  ]
}

```

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
* `WorkflowStatus` (`in_progress` | `blocked` | `in_review` | `ready` | `done`)
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
| `rexws switch <project/branch>` | Jump to (or create) the Rex session for a workstream. |
| `rexws status <path> <stage>` | Set workflow status (`in_progress`, `blocked`, `in_review`, `ready`, `done`). |
| `rexws new <project> <branch>` | Create a worktree using the project's configured provider. |
| `rexws rm <path>` | Close Rex session (if running), run provider's `remove`, clean `state.json`. |
| `rexws project add <path>` | Register a Git repo in `config.json`. |

---

## 6. TUI Design (Grouped Vertical List)

Start with a **grouped vertical list**—it handles long branch names, project tags, and agent badges cleanly, and is straightforward to extend with a Kanban column view later.

### Layout

```text
 REXWS  [Filter: /]  [3 active · 1 blocked]
────────────────────────────────────────────────────────────────────
 ▼ IN PROGRESS (2)
 > billing      feat-oauth-pkce          ▲ blocked (permission)
   do-si-do     refactor-sync            ● working

 ▼ BLOCKED (1)
   monorepo     sparse-api-v2            ○ idle

 ▼ IN REVIEW (1)
   local-llms   bench-q4                 ✓ done

 ▼ READY (3)
   .ai          main                     — stopped
   ...
────────────────────────────────────────────────────────────────────
 [Enter] switch  [s] status  [n] new  [d] remove  [/] filter  [?] help

```

### Keybindings

* `j` / `k` or `↑` / `↓`: Navigate workstreams (skips group headers).
* `Enter`: Switch to Rex session (or create + switch if stopped).
* `s`: Open quick status selector (`1` In Progress, `2` Blocked, `3` In Review, `4` Ready, `5` Done).
* `n`: Create new worktree (pick project → enter branch name → runs provider via `tea.ExecProcess`).
* `d`: Archive/remove worktree (with confirmation prompt; closes Rex session + runs provider `remove`).
* `/`: Live fuzzy filter across project, branch, workflow status, and agent state (e.g., typing `blocked` shows both `Blocked` workflow items and `▲ blocked` agents).
* `r`: Force refresh Git worktrees and Rex sessions.
* `q`: Quit.

---

## 7. Implementation Milestones (For Your Coding Harness)

1. **Milestone 1 — Core + CLI (`list`, `status`, `project add`):** Config/state loader, `git worktree list --porcelain` parser, and `rexws list --json`.
2. **Milestone 2 — Rex Adapter:** Inspect local `rex` CLI flags, implement session listing, `rex events --json` stream parsing, and `rexws switch`.
3. **Milestone 3 — Pluggable Providers:** Implement template-based `git`, `worktrunk`, and `custom` providers for `rexws new` and `rexws rm`.
4. **Milestone 4 — Bubbletea TUI:** Grouped vertical list, live `rex events` updates, fuzzy filter, status switcher, and `tea.ExecProcess` handoff for `new`/`rm`.
