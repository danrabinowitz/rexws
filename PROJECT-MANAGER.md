# Project manager handoff — rexws

Status: handoff brief for the incoming project manager / coordinator.
Snapshot: 2026-11-26. Repository: `/Users/djr/code/src/danrabinowitz/rexws`
on `main` at `f6e98b6`, clean except the intentionally untracked `TODO.md`.

## 1. Your role

You are the project manager, not the sole implementer.

- Own sequencing, delegation, integration, and reporting.
- Create a dedicated worktree and Rex session for each agent/task, then hand
  that agent a self-contained prompt.
- Keep the primary checkout on `main` and clean.
- Ask the user before destructive or outward-facing actions: removing
  worktrees/sessions, changing their live workstreams, committing, merging, or
  pushing.
- If the code and docs disagree, report it; do not silently pick one.

The user is winding down the original build session. The product is a working
alpha they are happy with; the next phase is exploratory features and TUI
refinement.

## 2. Read first

- `README.md` — what rexws is, the mental model, install/usage, project status.
- `SPEC.md` — product design (see §8 for known caveats).
- `ROADMAP.md` — the sequence; `IDEAS.md` is an unranked idea bank, not a plan.
- GitHub issues — the actionable tasks and agent briefs. Each issue carries its
  own context, scope, and acceptance criteria so it can be handed to an agent
  whole. `git log` and the docs stay the record of what shipped.
- `TUI-UX.md` — the source of truth for TUI behavior.
- `internal/tui` and the rest of the code before promising a behavior.

## 3. What rexws is

- One-liner: a small, local-first Git worktree workspace manager for Rex. It
  maps each worktree to a named Rex session and adds workflow status, agent
  activity, and window-aware switching.
- Pain solved: Rex knows sessions, windows, and blocks, not Git worktrees or
  which branch/agent/window belongs to which task. rexws derives the mapping
  from Git, persists only its own workflow metadata, and exposes a CLI, a TUI,
  and JSON.
- Primary user: one developer on one Mac. There is no team, multi-user, sync, or
  server model. It is an unofficial companion to Rex, not a Rex/Superlogical
  product.
- Primitives:
  - **Project** — a registered Git repository.
  - **Worktree** — a Git worktree; Git is the source of truth for what exists.
  - **Workstream** — rexws's row for one worktree: project, branch, path, session
    label, workflow status/note, and its live Rex session, windows, and agents.
    One worktree = one workstream. It is not a Rex or Orca object.
  - **Session** — a Rex session, 1:1 with a workstream by convention, labeled
    `<project>/<branch>`.
  - **Window** — a Rex tab inside a session.
  - **Agent** — a terminal pane reporting program status. Agents are badges, not
    a navigable level; silent shell panes are not agents.
- Boundaries: Rex owns sessions/windows/blocks/program status; rexws is a CLI
  client plus workflow metadata. Orca has no integration; Orca-created worktrees
  are just Git worktrees. No rexws daemon.

## 4. Current state

Shipped on `main` through `f6e98b6`:

- Milestones 1–4: config/state, Git worktree discovery, `project add`, `status`,
  and `list`; the Rex adapter with per-pane program status and event parsing;
  template providers with `new` and safe `rm`; and the full TUI with live
  streams, hierarchical search, status switching, and `new --switch [--client]`.
- Contract and cleanup: the shared selector grammar (including the issue #17
  hardening that keeps one unreadable worktree from breaking every selector),
  `rexws here`; the frozen `schema_version: 1` contract and `rexws schema`;
  preview-first `cleanup` with safe apply/force, orphan triage and `--fix`, and
  the TUI cleanup view.
- TUI design: the visual system and wide-terminal layout, plus configurable
  per-status color and icon (issue #13).
- Docs: README, SPEC, ROADMAP, TUI-UX, TUI-DESIGN, and `docs/json-schema.md`
  describe the shipped surface.

See `ROADMAP.md` Shipped for the issue-level list. Known caveats are in §8.

Verification: `go test ./...`, `go test -race ./...`, `go vet ./...`, and
`gofmt -l cmd internal` pass on `main`. Rebuild `~/.local/bin/rexws` from
`main` to pick up newly shipped commands.

Repository state at handoff:

- `main` at `f6e98b6`.
- Worktrees: the primary `main` checkout plus per-task worktrees under
  `~/code/worktrees/rexws/`; use `git worktree list` for the live set.
- `TODO.md` is intentionally untracked. Do not touch it.
- Treat observed Rex sessions as user-owned; do not kill them.

## 5. Golden path and commands

```sh
# See current workstreams, sessions, windows, and agent state.
rexws list
rexws list --json

# Register a repository and choose where its worktrees live.
rexws project add ~/code/src/acme/repo --worktree-root ~/code/worktrees/repo

# Create a worktree for a new branch and open its Rex session.
rexws new rexws feat-thing --switch

# Jump to an existing workstream's session/window.
rexws switch rexws/feat-thing
rexws switch rexws/feat-thing --window tests

# Set workflow metadata and filter.
rexws status <worktree-path> in_progress
rexws list --status blocked

# Remove a worktree/session (destructive; ask first).
rexws rm <worktree-path>

# Dashboard.
rexws
rexws tui
```

Important details:

- `new` without `--switch` creates only the worktree. `--switch` creates or
  selects the session labeled `<project>/<branch>` and opens it in the app.
- `--client` chooses the Rex app client; otherwise `rex_client` config/env, then
  the first connected app.
- Target path is `<worktree_root>/<branch>` when the project sets
  `worktree_root`; otherwise a sibling `<repo>-wt/<branch>`.
- `rm` refuses the main and dirty worktrees, closes the live session, runs the
  provider remove, and clears state. It leaves the branch. It requires Rex.
- Session matching is exact `<project>/<branch>`. Existing/manual sessions with
  other labels show as stopped; session adoption is future work.
- Config: `~/.config/rexws/config.json` (projects, providers, statuses,
  `rex_bin`, `rex_client`); state: `state.json` (status/note per canonical
  worktree path). Overrides: `REXWS_CONFIG_DIR`, `REXWS_REX_BIN`,
  `REXWS_REX_CLIENT`.
- `rexws project set` does not exist yet. To change a project's
  `worktree_root`/provider, edit `config.json` (with user approval) or add the
  command as a task. Currently only `rexws` has a `worktree_root`
  (`/Users/djr/code/worktrees/rexws`); `do-si-do` and `.ai` do not.

## 6. Workspace and session conventions

- Keep the primary checkout on `main`.
- Put feature/milestone branches in their own worktrees under
  `~/code/worktrees/<repo>/<branch>`, normally created through rexws.
- One agent per worktree/session. Do not run parallel edits in the same
  worktree. If two tasks touch the same package, sequence them or make the
  overlap explicit.
- Session label is `<project>/<branch>`. Do not rename it casually; rexws matches
  on it.
- Use rexws itself for workspace creation. Do not create an Orca Workspace for
  subagent implementation; if an Orca worker is genuinely required, follow the
  user's Orca rules (parent worktree, no activation, remove when done).
- For tests and experiments, use a scratch `REXWS_CONFIG_DIR` and a scratch Rex
  session. Kill only the scratch session. Never kill the user's live sessions.
- Do not touch `TODO.md`; it is the user's scratchpad.
- Do not overwrite `~/.local/bin/rexws` without asking; build to `/tmp` for
  experiments.

### Handing a task to an agent

1. Choose project/branch and confirm the scope with the user.
2. Create the workspace/session:
   `rexws new <project> <branch> --switch`.
3. Start the agent in that worktree/session (or ask the user to).
4. Give it a self-contained prompt: repo/worktree path, docs to read, scope,
   constraints, tests, no commit/push/merge without approval, report at phase
   boundaries.
5. Track it with `rexws list --json`, `rexws status`, or the TUI.
6. Review the result; commit/merge only with user approval. Remove the workspace
   with `rexws rm` only with approval; delete the branch separately.

## 7. Architecture map

- `cmd/rexws` — Cobra CLI; root launches the TUI; commands delegate to
  `internal/ops`.
- `internal/config` — config/state load/save, configurable statuses, provider
  definitions, Rex binary/client settings.
- `internal/git` — `git worktree list --porcelain -z` parsing, branch/dirty checks.
- `internal/core` — merges worktrees, state, and Rex sessions into Workstreams;
  agent/window models and labels.
- `internal/rex` — Rex CLI adapter: sessions, program status, windows, events,
  client selection, switch.
- `internal/render` — text/JSON-facing formatting, sanitization, per-window
  agent summaries.
- `internal/ops` — shared one-shot operations used by CLI and TUI: discover,
  status, create, remove, switch.
- `internal/provider` — template command rendering and execution.
- `internal/agentstate` — shared agent state ordering/normalization.
- `internal/tui` — Bubbletea model, pure row/filter/cursor/key reducers, renderer,
  live event hub.
- `IDEAS.md` — unranked idea bank; composability rule: anything with its own
  state/index/cache/schedule/network/UI is a satellite, not core.

Sources of truth: Git and Rex for existence/state; rexws only persists config
and workflow metadata. No database/daemon/telemetry. macOS-first.

## 8. Open work and known caveats

The `SPEC.md` drift pass (issue #14) is complete: §1–§7 now match shipped
behavior (JSON-only config; configurable statuses including `rex_bin`,
`rex_client`, `statuses`, `default_status`, color, and icon; the full
template-variable and `Workstream` models; the Rex event-stream adapter;
configurable stages; and the shipped TUI). `ROADMAP.md` Docs lists
`docs/rex-cli.md` and an automated docs-example check; README covers the rest.

Known caveats:

- TUI `n`/`d` provider `tea.ExecProcess` paths are covered by tests but were not
  exercised against live data.
- The post-`r` redraw assertion was inconclusive because Bubbletea diffs
  unchanged cells; `r` completed and the active marker was verified live before.
- Worktrunk is representable as a provider template but `wt` was not installed,
  so its flags are unverified.
- No tagged release; CI runs on every push to `main` and on pull requests. Rex
  is a preview; there is no version check.

Deferred/future: fuzzy ranking, agent formatter choice, Session 0 hotkey,
session adoption/label templates, `project set`, provider timeouts, Rex version
check, config schema version, and a normalized public event stream (see
`IDEAS.md`). The new `tui-designer` agent type is available for TUI audits;
`adversarial-reviewer` is useful before merging non-trivial changes.

## 9. Coordination protocol

- Plan first for substantive work; agree with the user before editing.
- Track work in GitHub issues (`gh issue list`, `gh issue view <n>`); close one
  from the commit or pull request that satisfies its acceptance criteria. Keep
  `TODO.md` untouched.
- Use one implementation agent per worktree; use `tui-designer` for TUI layout
  audits and `adversarial-reviewer` for diffs/specs when useful.
- Report at phase boundaries: what changed, tests run, blockers, and any spec
  deviation.
- Keep the user informed of workspace/session names and paths.
- Ask before commits, merges, pushes, destructive `rm`/session kills, config
  edits, or changes to the user's live workstreams.
- If a task needs a new project or `worktree_root`, ask; editing config is a
  user-visible change.
- Do not let a subagent drift into the primary checkout or another agent's
  worktree.

## 10. Starter prompt for the project manager

The user can paste this into the new coordinator session:

```text
You are the project manager/coordinator for rexws. Start by reading
/Users/djr/code/src/danrabinowitz/rexws/PROJECT-MANAGER.md, then README.md,
SPEC.md, ROADMAP.md, TUI-UX.md, and IDEAS.md.

You own sequencing, delegation, integration, and reporting; you are not the
sole implementer. Use rexws itself to create worktrees and Rex sessions for
agents: `rexws new <project> <branch> --switch`, `rexws list --json`,
`rexws switch <project>/<branch>`, and the TUI (`rexws` / `rexws tui`).

Rules: keep the primary checkout on main; one agent per worktree/session; use
scratch configs and scratch Rex sessions for tests; never kill the user's live
sessions; do not commit, merge, push, remove worktrees/sessions, or edit config
without explicit approval; do not touch TODO.md; if docs and code disagree,
flag it.

Start by inspecting the current state (`git status`, `rexws list --json`,
`git worktree list`), then propose a short plan for the next milestone or TUI
refinement and wait for the user's sign-off. Report progress and blockers at
phase boundaries.
```
