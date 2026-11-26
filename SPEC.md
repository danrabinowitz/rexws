## 1. Architecture & Tech Stack

* **Language:** Go (single static binary, zero CGO).
* **Libraries:**
  * CLI: `[github.com/spf13/cobra](https://github.com/spf13/cobra)`
  * TUI: `[github.com/charmbracelet/bubbletea](https://github.com/charmbracelet/bubbletea)`, `lipgloss`, `bubbles`
  * Config/State: standard `encoding/json`. Both files are JSON; no YAML loader is compiled in.

* **Core Principle (Enrichment Layer):** Git and Rex are the sources of truth for what *exists*. `rexws` only persists your list of tracked projects, worktree provider configs, and human workflow metadata (`status`, `note`).
* **Frontend-Agnostic Core:** Business logic lives in `internal/core`, with one-shot operations shared through `internal/ops`. Every command that reports data supports `--json` so a future native macOS launcher (like `resh`) or Raycast extension can shell out to `rexws` without duplicating logic.
* **Composability Rule:** `rexws` is a substrate, not a platform. Code belongs in this binary only when it is a one-shot derivation from Git, Rex, and `state.json`. Anything that needs its own persistent state, index, cache, schedule, network dependency, or dedicated UI is a separate tool that consumes the versioned `rexws --json` surface; it never becomes a subsystem here. In exchange, the core owes those tools a stable contract: one selector grammar and a versioned JSON shape. A normalized public event stream and machine predicates (`rexws wait`) are future candidates, not shipped commands. A feature that needs any of the former is not rejected, only relocated. `IDEAS.md` classifies every known candidate against this rule.

```text
rexws/
├── cmd/rexws/main.go   # Cobra CLI; the root command launches the TUI
└── internal/
    ├── config/         # config.json & state.json, statuses, style validation
    ├── git/            # Worktree discovery (`git worktree list --porcelain`)
    ├── provider/       # Pluggable worktree command templates (git, wt, custom)
    ├── rex/            # Rex CLI adapter and `rex events --json` parser
    ├── core/           # Merges Git + Rex + State into Workstream structs
    ├── ops/            # Shared one-shot operations (discover, new, rm, cleanup)
    ├── selector/       # One selector grammar for switch/status/rm
    ├── render/         # Agent formatting and terminal sanitization
    ├── agentstate/     # Agent-state vocabulary and ordering
    ├── contract/       # The --json contract version constant
    ├── pathutil/       # Path canonicalization and home expansion
    └── tui/            # Bubbletea dashboard, reducers, and live stream hub

```

---

## 2. Data Model & Persistence

Store configuration and mutable state in two small, human-readable JSON files under `~/.config/rexws/`.

### `config.json` (Projects & Providers)

```json
{
  "default_provider": "git",
  "rex_bin": "rex",
  "rex_client": "",
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
      "create": ["/Users/you/bin/monorepo-wt-create.sh", "{{.RepoPath}}", "{{.Branch}}", "{{.TargetPath}}"],
      "remove": ["/Users/you/bin/monorepo-wt-remove.sh", "{{.WorktreePath}}"]
    }
  },
  "projects": [
    {
      "name": "do-si-do",
      "path": "/Users/you/code/src/do-si-do"
    },
    {
      "name": "big-monorepo",
      "path": "/Users/you/code/src/monorepo",
      "provider": "monorepo-sparse",
      "worktree_root": "/Users/you/code/worktrees/monorepo"
    }
  ],
  "statuses": [
    { "id": "in_progress", "label": "In Progress" },
    { "id": "blocked", "label": "Blocked", "color": "red", "icon": "■" },
    { "id": "in_review", "label": "In Review" },
    { "id": "ready", "label": "Ready", "color": "green" },
    { "id": "done", "label": "Done" }
  ],
  "default_status": "ready"
}

```

`rex_bin` overrides the `rex` executable and `rex_client` picks the Rex app client; both are optional. Precedence is `REXWS_REX_BIN` / `REXWS_REX_CLIENT`, then these values, then `rex` from `PATH` and the first connected Rex app.

`default_provider` is used when a project sets no `provider`. Resolution order is project-level `provider` → global `default_provider` → built-in `git`. A project's `worktree_root` is the base for new worktrees; without it, they land beside the repository at `<repo>-wt/<branch>`. The `worktrunk` entry above is an example template; `wt` was not installed when the adapter was verified, so its exact flags are unverified.

`statuses` is the configurable workflow list; IDs, labels, order, and the default are all user-defined. The built-in defaults are `in_progress`, `blocked`, `in_review`, `ready`, and `done`; a config that omits `statuses` gets those five, and a config that omits `default_status` gets `ready` when present (otherwise the first configured status). `default_status` must name a configured status. Removing a status that `state.json` still references is a load error, not a silent remap.

Each status entry may set `color` (`auto` by default) and `icon` (empty by default) for the TUI. Both are optional with `omitempty`, so older configs load unchanged. An invalid value warns and is resolved to `auto` or no icon at render time rather than failing the load; the raw text stays in `config.json`. See `README.md` and `TUI-DESIGN.md` for the color and icon grammar.

### `state.json` (Workflow Metadata)

Keyed by canonical worktree path. Missing entries use the configured `default_status`; with the built-in statuses that is `ready`.

```json
{
  "workstreams": {
    "/Users/you/code/worktrees/monorepo/feat-auth": {
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
* `WorkflowStatus` (`config.Status`; a configured stage ID, not a fixed enum)
* `WorkflowStatusLabel` (string, display label for that stage)
* `Note` (string, workflow note; omitted from JSON when empty)
* `RexSessionID` (string, empty if stopped)
* `AgentState` (`blocked` | `error` | `working` | `unknown` | `done` | `idle` | `stopped`)
* `AgentDetail` (string, e.g., a permission prompt kind reported by Rex)
* `Agents` (`[]Agent`, one per terminal block that reports program status)
* `Windows` (`[]Window`, Rex windows in order)
* `Detached` (bool, true when the worktree has a detached HEAD; omitted when false)

`Agent` carries the block and window identity, reported app, state, detail, message, title, timestamp, and any read error. `Window` carries its ID, label, active flag, focused block, and block IDs. The complete field, type, enum, and nullability contract is in `docs/json-schema.md`; this list is the model, not a second copy of the JSON contract.

---

## 3. Pluggable Worktree Providers

Every provider defines `create` and `remove` command templates using Go's `text/template`.

* **Template Variables:**
  * `{{.Project}}`: Registered project name.
  * `{{.RepoPath}}`: Main repository path.
  * `{{.RepoName}}`: Base directory name of the main repository.
  * `{{.Branch}}`: New or existing branch name.
  * `{{.Slug}}`: Branch name flattened for a directory (`feat/auth` → `feat-auth`).
  * `{{.TargetPath}}`: Computed destination directory (defaults to `<worktree_root>/<branch>` or sibling `<repo>-wt/<branch>`).
  * `{{.WorktreePath}}`: Existing worktree path (for `remove`).

An unknown template field is an error, not a literal. `TargetPath` is populated for `create`; `WorktreePath` is populated for `remove`.

* **Resolution Order:** Project-level `provider` → global `default_provider` → built-in `git`.
* **Custom Script Contract:** A custom sparse-checkout script just needs to create a valid Git worktree (so `git worktree list --porcelain` discovers it) and exit `0`. If `create` needs interactive input (e.g., selecting sparse-checkout folders), the TUI suspends via `tea.ExecProcess` so the script gets full terminal control, then resumes the TUI on exit.

---

## 4. Rex Integration

The adapter lives in `internal/rex` and is verified against Rex `0.1.0-preview.25`
(API revision 24). There is no Rex version check yet, so a preview drift can
break the adapter.

1. **Session Naming:** one Rex session per active worktree, labeled `<project>/<branch>`.
2. **Initial Discovery:** list live sessions (`rex session ls --json`), then read each session's terminal blocks and their program-status records, plus its windows. A block with no program-status records is not an agent, so a session can have fewer agents than blocks. A session whose blocks or windows cannot be listed is surfaced as `unknown` with a warning instead of being dropped.
3. **Live Updates:** the TUI's stream hub owns one server-wide `rex events --json` stream for session lifecycle and one `rex events -s <session_id> --json` stream per live session. `internal/rex` parses newline-delimited JSON and normalizes `session_created`, `session_destroyed`, `program_status_changed`, `program_status_removed`, `session_view_changed`, `active_window_changed`, `window_created`, `window_closed`, and `window_label_changed`. Events reach Bubbletea through a bounded, coalesced queue so a slow model cannot block the scanner. A started process counts as connected only after a short grace period; failures retry with 1s, 2s, 4s, and so on up to 30s; a visible per-session pause is debounced by 2s; each hub restart is a new stream generation and drops queued messages from the old one. Program status arrives as JSON records derived from Rex's OSC 7501 protocol; rexws does not sniff terminal escape sequences itself.
4. **Switch / Launch:** `switch` resolves the session by exact label. When it is absent and no window was requested, it creates the session with `rex session new <label> -c <dir> --json`. It resolves the app client from `--client`, then `REXWS_REX_CLIENT`/`rex_client`, then the first connected Rex app (`rex client ls --json`), and selects with `rex -C <client> do session.select session_id=... [window_id=...]`. With `--window`, the session must already exist; the window resolves by exact ID, unique label, or unique ID suffix. If creation succeeds but selection fails, the error names the created session.

A Session 0 / dashboard hotkey in Rex's `init.lua` is not implemented; it remains
a future documentation candidate in `ROADMAP.md`.

---

## 5. CLI Interface

Running `rexws` with no arguments launches the TUI. Subcommands expose the same engine for scripts and future UIs:

| Command | Purpose |
| --- | --- |
| `rexws` / `rexws tui` | Launch the interactive Bubbletea dashboard. |
| `rexws list [--json] [--status <id-or-label>]...` | List all discovered workstreams with merged Rex + workflow state. `--status` is repeatable or comma-separated. |
| `rexws here [--dir <path>] [--json]` | Show the workstream whose worktree contains the current directory (or `--dir`). |
| `rexws switch <selector> [--window <label-or-id>] [--client <client>] [--json]` | Jump to (or create) the Rex session for a workstream; `--window` selects one of its windows. |
| `rexws status <selector> <stage> [--json]` | Set a workstream's configured workflow status, by ID or label. |
| `rexws new <project> <branch> [--switch] [--client] [--json]` | Create a worktree using the project's configured provider. With `--switch`, also create or select the Rex session `<project>/<branch>` in the new worktree; `--client` chooses the Rex app client for that switch. |
| `rexws rm <selector> [--force] [--yes] [--delete-branch] [--json]` | Close the Rex session (if running), re-verify the worktree, run the provider's `remove`, and clear `state.json`; the branch is retained by default. `--force` permits a dirty or detached worktree after printing `LOSS`, `RETAINED`, and `ACTIONS`, and asks only when loss is non-empty or the branch state could not be assessed; `--json` with `--force` requires `--yes`. `--delete-branch` is the only branch-deletion path: the worktree goes first, then the branch only when freshly proven merged or patch-equivalent to the default branch. Protected or unmerged branches are never deleted. A provider `remove` must not delete the branch. |
| `rexws cleanup [--project <name>] [--json] [--apply] [--force] [--fix [--yes]]` | Preview-first cleanup of worktrees, branches, and orphans; `--apply` removes only the provably safe set, `--force` names its loss, and `--fix` prunes only provably stale `state.json` entries. |
| `rexws project add <path> [--name ...] [--provider ...] [--worktree-root ...]` | Register a Git repo in `config.json`. |
| `rexws schema` | Print the machine-readable JSON contract. Needs no config, Rex, or repository. |

`switch`, `status`, and `rm` share one selector grammar: exact
`<project>/<branch>`, `branch:<name>`, `path:<path>`, `name:<label>`,
`active`/`current`, and a unique case-insensitive substring, with stable exit
codes `2` (no match), `3` (ambiguous), and `4` (outside every workstream).
`here` uses the same containment rule for the current directory. `README.md`
holds the user-facing reference for the grammar; `internal/selector` is the one
implementation.

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
recorded deliberately. A future public `rexws events` stream and satellites are
outside this contract.

---

## 6. TUI Design

The TUI is a Bubbletea dashboard. Workstreams are grouped by configured workflow
status, and each group header shows the configured label plus its optional
status icon and color. Workstreams are the parent rows and Rex windows are their
children; status group headers and agent badges are not navigable. `Enter`
confirms the highlighted row (switch to the session, or to the selected window).
`Right` expands a parent and a second `Right` enters its first child; `Left`
collapses or returns to the parent. `/` filters the hierarchy by
case-insensitive substring across project, branch, workflow status, agent state,
and window labels while keeping parent context. Live Rex streams update agent
badges and window state as they change.

Normal-mode actions are `s` (status selector), `n` (new-worktree flow),
`d` (remove after main-worktree and Rex pre-checks, with a confirmation showing
the Loss, Retained, and Actions sections the CLI reports),
`c` (cleanup preview grouped by risk, with bulk apply after review), `r`
(refresh and retry event connections), and `?` (help). `new` and `rm` suspend
the TUI through `tea.ExecProcess` when a provider needs the terminal. Status colors and icons
come from `config.json`; an invalid value warns in the banner and falls back
instead of failing the load.

`TUI-UX.md` is the behavior source of truth: layout, keybindings, search
visibility rules, live-update behavior, error/startup states, cleanup flow, and
acceptance criteria. `TUI-DESIGN.md` is the visual source. This section
intentionally does not duplicate either.

---

## 7. Implementation State

All of the planned implementation phases have shipped on `main`:

1. **Core + CLI:** config/state loader, `git worktree list --porcelain` parser, `project add`, `status`, `list` (table and JSON).
2. **Rex Adapter:** session listing, per-pane program status, event parsing, and `switch` for sessions and windows.
3. **Pluggable Providers:** template-based providers for `new` and `rm`, including the built-in `git` provider and the safe-remove path.
4. **TUI:** the accordion hierarchy inside status groups, live streams, substring search, status switcher, cleanup view, and `tea.ExecProcess` handoff for `new`/`rm`.
5. **Contract seams:** one selector grammar shared by `switch`, `status`, and `rm`, plus `rexws here`.
6. **Worktree cleanup:** the preview-first candidate report, safe `--apply`/`--force` sets, orphan triage with `--fix`, and the TUI cleanup screen.
7. **JSON contract:** `schema_version: 1`, `rexws schema`, frozen fixtures and golden tests, and `docs/json-schema.md`.
8. **TUI design:** the visual system and wide-terminal layout, plus configurable per-status color and icon.

There is no daemon, database, or scheduled cleanup. Open follow-ups and
direction are tracked in GitHub issues and `ROADMAP.md`; this document does not
commit a new milestone.
