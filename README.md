# rexws

**A small, local-first Git worktree workspace manager for Rex.**

`rexws` maps each Git worktree to a named Rex session and adds the layer Rex
doesn't have: which branch belongs to which task, what state that work is in,
and which agent needs attention. It derives the mapping from Git, persists only
your own workflow metadata, and stays out of the way.

> `rexws` is an unofficial personal companion to Rex. It is not affiliated with,
> endorsed by, or supported by Rex or Superlogical.

**Status:** pre-1.0, active development. The CLI and the interactive TUI both
work; there is no tagged release and no CI yet. See
[Project status](#project-status).

---

## The problem

Rex knows about sessions, windows, and blocks. It does not know about Git
worktrees, or which branch, agent, and tab belong together.

So the mapping lives in your head:

- You name sessions by hand, and the names drift.
- You remember which tab is running the agent you care about.
- You rebuild the same mental index every morning, and again after lunch.
- Nothing tells you, at a glance, which of a dozen branches is blocked, which
  is waiting on review, and which is finished but unmerged.

`rexws` derives the session name from the worktree (`<project>/<branch>`),
merges it with Rex's live session and agent state, and keeps a small amount of
workflow metadata of its own. You get one list that answers "where is everything,
and what needs me next?"

It is built for one developer on one machine who works in many worktrees, many
Rex sessions, and several coding agents at once. It is configurable enough for
another individual to adopt, but there is no team, multi-user, sync, or server
model — and there won't be.

---

## Core concepts

| Term | Meaning |
| --- | --- |
| **Project** | A Git repository registered in `config.json`. |
| **Worktree** | A Git worktree discovered by `git worktree list --porcelain`. Git is the source of truth for what exists. |
| **Workstream** | `rexws`'s row for one worktree: project, branch, path, session label, workflow status and note, and the live Rex session, agents, and windows. **One worktree is one workstream.** It is not a Rex object or an Orca object. |
| **Session** | A Rex session, one per workstream by convention, labeled `<project>/<branch>`. Rex owns it. |
| **Window** | A Rex tab inside a session. A child of a workstream. Selecting a window by label or ID is supported; creating and closing windows from `rexws` is not. |
| **Agent** | A terminal pane that reports program status (`blocked`, `error`, `working`, `done`, `idle`). Agents are badges on a workstream, not a navigable level. A silent shell pane is not an agent. |
| **Status** | *Your* workflow stage for a workstream, not anything Rex knows about. Configurable; defaults to `in_progress`, `blocked`, `in_review`, `ready`, `done`. |

### How they fit together

```
Project (registered repo)
└── Worktree (Git)
    └── Workstream (rexws row)
        ├── Status + note          ← rexws metadata
        └── Rex session (1:1 by convention)
            ├── Window (tab)
            └── Agent (pane reporting program status)
```

Three sources of truth, and each owns one thing:

- **Git** owns worktrees and branches.
- **Rex** owns sessions, windows, blocks, and program status.
- **`rexws`** owns only your list of projects, provider configuration, and
  per-workstream workflow metadata (`status`, `note`).

There is no database, index, or cache. Every command recomputes the view from
Git and Rex, then joins in the small JSON state file.

### Boundary with Rex

`rexws` is a CLI client of Rex. It reads Rex's session and program-status data
and issues commands to Rex; it never runs a server or background process. Rex
already runs the server, and `rexws` is a one-shot client of it.

### Boundary with Orca

None, deliberately. Orca-created worktrees are just Git worktrees to `rexws`,
and `rexws` does not manage Orca workspaces or integrate with Orca. The two
tools can coexist without knowing about each other.

---

## The golden path

Build from source (there are no releases yet):

```sh
go build -o ~/.local/bin/rexws ./cmd/rexws
```

Register a repository and give it a home for worktrees:

```sh
rexws project add ~/code/src/acme/repo --worktree-root ~/code/worktrees/repo
```

Create a worktree for a branch, then open its Rex session:

```sh
rexws new repo feat/auth --switch
```

`new` only creates the worktree unless you pass `--switch`, which also creates
(or selects) the Rex session `<project>/<branch>` in the new worktree; `--client`
chooses the Rex app client for that switch. To return to an existing workstream
later, use `rexws switch repo/feat/auth`. Without `--worktree-root`, the
worktree lands beside the repository at `<repo>-wt/<branch>`.

Move the work forward and see where everything stands:

```sh
rexws status <path> in_progress
rexws list
```

`status` takes the worktree path that `list` prints. When you're done, `rm`
closes the session, runs the provider's remove command, and clears the
workstream's metadata — but leaves the branch alone:

```sh
rexws rm <path>
```

`rm` refuses the main worktree and refuses dirty worktrees. It has no `--force`,
and it currently requires Rex to be reachable.

### Commands

| Command | Purpose |
| --- | --- |
| `rexws` / `rexws tui` | Launch the interactive dashboard. |
| `rexws list [--json] [--status <id>]` | List discovered workstreams with merged Rex and workflow state. |
| `rexws project add <path> [--name ...] [--provider ...] [--worktree-root ...]` | Register a Git repository. |
| `rexws new <project> <branch> [--json] [--switch] [--client ...]` | Create a worktree with the project's provider; with `--switch`, also create or select its Rex session. |
| `rexws switch <project/branch> [--window ...] [--client ...] [--json]` | Show the workstream's Rex session, creating it if needed; optionally select a window. |
| `rexws status <path> <stage> [--json]` | Set a workstream's workflow status, by ID or label. |
| `rexws rm <path> [--json]` | Close the session, remove the worktree, clear its metadata. |

### TUI

Run `rexws` with no arguments, or `rexws tui`, to open the interactive
dashboard. It keeps the workflow-status groups from `list` and adds an accordion
inside them: each workstream expands to its Rex windows. `Enter` confirms the
highlighted row (switch to the session, or to the selected window). `Right`
expands a parent; a second `Right` moves into its first child, and `Left`
collapses or returns to the parent. `/` filters by case-insensitive substring
across project, branch, workflow status, agent state, and windows while keeping
parent context.
Live Rex streams update agent badges and window state as they change. Press `?`
for the full key map and `s` to change a workstream's status.

See `TUI-UX.md` for the full UX design.

Every command that reports data accepts `--json` for scripts and future
launchers. The JSON shapes are usable but **not frozen** — there is no
`schema_version` yet, so treat them as unstable.

---

## Configuration

Everything lives in two human-editable JSON files under `~/.config/rexws/`
(respecting `XDG_CONFIG_HOME`, or overridable with `REXWS_CONFIG_DIR`).

### `config.json`

```json
{
  "default_provider": "git",
  "rex_bin": "rex",
  "rex_client": "",
  "providers": {
    "git": {
      "create": ["git", "-C", "{{.RepoPath}}", "worktree", "add", "-b", "{{.Branch}}", "{{.TargetPath}}"],
      "remove": ["git", "-C", "{{.RepoPath}}", "worktree", "remove", "{{.WorktreePath}}"]
    }
  },
  "statuses": [
    { "id": "in_progress", "label": "In Progress" },
    { "id": "blocked", "label": "Blocked" },
    { "id": "in_review", "label": "In Review" },
    { "id": "ready", "label": "Ready" },
    { "id": "done", "label": "Done" }
  ],
  "default_status": "ready",
  "projects": [
    {
      "name": "repo",
      "path": "/absolute/path/to/repo",
      "provider": "git",
      "worktree_root": "/absolute/path/to/worktrees/repo"
    }
  ]
}
```

A missing file is not an error: built-in defaults are used, and nothing is
written to disk until you register a project.

**Providers** are command templates using Go's `text/template`. They are
rendered with `{{.Project}}`, `{{.RepoPath}}`, `{{.RepoName}}`, `{{.Branch}}`,
`{{.Slug}}`, `{{.TargetPath}}`, and `{{.WorktreePath}}`; an unknown template
field is an error, not a literal. A custom provider only has to leave a valid
Git worktree behind so discovery finds it. Worktrunk is representable as a
provider template, but its exact flags were never verified here — treat that as
an example, not a supported integration.

**Statuses** are entirely yours to define: IDs, labels, order, and default.
Removing a status that `state.json` still references is a loud error rather than
a silent remap.

### `state.json`

```json
{
  "workstreams": {
    "/absolute/path/to/worktrees/repo/feat-auth": {
      "status": "in_progress",
      "note": "Waiting on schema migration",
      "updated_at": "2026-01-01T00:00:00Z"
    }
  }
}
```

Entries are keyed by canonical worktree path. A workstream with no entry gets
the configured default status. `note` is stored but has no editing command yet.

---

## Design principles

These are constraints the project is designed around, not aspirations:

1. **No daemon.** Commands are one-shot. The TUI is an interactive client, not
   a service. Rex already runs the server.
2. **Git and Rex are the sources of truth.** `rexws` persists only its own
   configuration and workflow metadata. No shadow database, no index, no cache.
3. **Local-first.** No network service, cloud sync, or telemetry. It talks to
   the local Rex CLI and nothing else. macOS is the tested platform; other
   platforms are untested.
4. **One static Go binary.** No CGO, minimal dependencies. The CLI uses Cobra;
   the TUI uses the Charm stack.
5. **Plain text, human-editable.** JSON under a standard XDG config directory,
   written atomically with `0600` permissions.
6. **Convention over configuration.** Session labels (`<project>/<branch>`),
   the default Git provider, and the default statuses all work without any
   configuration. Config exists for the exceptions.
7. **Frontend-agnostic core.** Business logic lives outside the CLI so other
   frontends can consume `--json`. The boundary is still being extracted, so
   don't over-read it yet.
8. **Substrate, not platform.** If an idea needs its own state, index, cache,
   schedule, network dependency, or dedicated UI, it belongs in a separate
   tool that consumes `rexws`. Only one-shot derivations against Git, Rex, and
   `state.json` belong in this binary. `IDEAS.md` classifies candidates against
   this rule.
9. **Loud data integrity.** Invalid configuration, conflicting projects, and
   references to removed statuses are errors, not silent fixes.

---

## Goals and non-goals

### Goals

- One list that shows every worktree across every registered project, joined
  with its Rex session, agent state, and your workflow status.
- Zero-maintenance naming: derive session labels from Git instead of asking you
  to type them.
- A scriptable surface: every command has `--json`, so launchers, editors, and
  small scripts can build on `rexws` without duplicating its logic.
- Extensibility through command templates, not a plugin system.
- A codebase small enough to read in an afternoon.

### Non-goals

Stated now so they stop being relitigated:

- **Not a Rex replacement or a terminal multiplexer.** `rexws` does not own
  terminals, sessions, or panes.
- **Not a Git GUI.** No merge, rebase, review, commit, or push workflows.
- **No team, multi-user, daemon, cloud, or remote backend.**
- **No Orca integration.**
- **No native GUI, menu bar app, or Raycast extension** as part of this
  project. If those exist, they consume the JSON surface.
- **No multiple sessions per workstream**, and no automatic reordering of Rex
  sessions.
- **No PR/CI enrichment in core.** Those are satellite ideas, not features of
  this binary.
- **No automatic branch deletion.** `rm` removes the worktree and leaves the
  branch.
- **No Kanban view, mouse support, or configurable keybindings.**
- **No window create/rename/close from `rexws`.**
- **No session adoption yet.** Matching is exact on `<project>/<branch>`, so
  sessions you named by hand show as stopped. Adoption is a future candidate.
- **No notes UX yet.** `state.json` stores notes; nothing edits them.
- **No frozen JSON schema, Rex version check, config-schema version, or
  provider timeouts yet.**

---

## Differentiation / prior art

- **Rex itself** already lists sessions and shows program status. It does not
  know about Git worktrees, branch-level workflow state, or the relationship
  between a branch and a task. `rexws` is an overlay on top of Rex, not a
  competitor to it: it derives the missing mapping and hands navigation back to
  Rex.
- **Worktree managers** such as Worktrunk or plain `git worktree` + scripts
  create and remove worktrees well. They have no concept of a Rex session, an
  agent's state, or your review status. `rexws` uses those tools as providers
  and adds the session and workflow layer.
- **Terminal/session managers** such as tmuxinator or zellij manage sessions
  and layouts. They don't map sessions to Git branches, and they don't know
  what an agent inside a pane is doing. `rexws` doesn't manage layouts at all.
- **Orca** is a full environment with its own workspace and agent model.
  `rexws` is not an Orca client and does not manage Orca workspaces; Orca
  worktrees are simply Git worktrees here.
- **A shell script** could assemble this view. `rexws` exists because the
  joining, the exact-label matching, the provider abstraction, and the JSON
  surface are the parts that get re-written badly every time.

---

## Project status

`rexws` is in early, active development. The interface and the on-disk formats
may change without migration support.

**Working today:**

- Config and state, with configurable workflow statuses and environment
  overrides.
- Git worktree discovery, `project add`, `list` (table and JSON), and `status`.
- Rex integration: session listing, per-pane program status, session lifecycle
  and status event parsing, and `switch` for sessions and windows.
- Template-based worktree providers, the built-in Git provider, `new` (including
  `--switch`), and `rm` with main-worktree, dirty-worktree, and Rex pre-checks.
- The interactive TUI (`rexws` or `rexws tui`): workflow-status groups, an
  expandable workstream → window hierarchy, substring search, status switching,
  and live Rex updates.

**Planned:**

- Session adoption and label templates, a notes editing path, selector grammar,
  a normalized event stream, and machine-readable predicates.

**Not yet:**

- No tagged release and no CI. Windows and Linux are untested.
- Verified against Rex `0.1.0-preview.25` (API revision 24). There is no Rex
  version check, so a preview drift can break the adapter.
- The JSON contract is not frozen and has no `schema_version`.

See `ROADMAP.md` for what is committed next, `SPEC.md` for the current design,
`IDEAS.md` for the unranked idea bank, and `TUI-UX.md` for the TUI UX.

---

## License

MIT — see [LICENSE](LICENSE).
