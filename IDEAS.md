# rexws idea bank

An unranked menu, not a plan. `ROADMAP.md` is the commitment; this file is where
ideas sit until a concrete use case promotes them. Merged from two independent
brainstorm passes (2026-11-26), with duplicates collapsed.

## How to read this

**Tiers.** Every idea is classified by *where it should live*, not by how much
it is wanted:

| Tier | Meaning |
| --- | --- |
| **Seam** | A contract `rexws` promises so other tools can be built on it. Small, load-bearing, build first. |
| **Core** | A one-shot derivation or action against Git, Rex, and `state.json`. Lives in the `rexws` binary. |
| **Satellite** | Anything with its own index, cache, schedule, network dependency, or bespoke UI. A separate binary or script that consumes `rexws`. |
| **Not ours** | Product surface that would turn a composable tool into a monolith. |
| **Gate** | Blocked on a dependency or a decision, not on effort. |

**The rule** (the composability principle): *if an idea needs its own state,
index, cache, schedule, network dependency, or dedicated UI, it is a satellite.
Otherwise it is core — provided it stays a one-shot derivation.*

**Sizes.** `[S]` under a day · `[M]` a few days · `[L]` needs design, a week or
more.

**Corollary worth internalizing:** the composability rule *shrinks* the core.
PR/CI enrichment, transcript search, port discovery, the attention inbox, usage
meters, and fleet dispatch all looked like features; as satellites they cost
`rexws` one JSON contract and one event stream.

---

## 1. Seams — build first

These are the whole reason satellites are cheap. None of them is a feature a
user sees.

- **S1 · Selector grammar** `[S]` — one resolver for `branch:`, `path:`,
  `name:`, `issue:`, `active`/`current`, and unique substring, shared by every
  command. Today each command takes a literal `project/branch` or a full path.
- **S2 · Frozen JSON contract** `[S]` — `--json` everywhere, `schema_version` in
  the payload, a documented shape per command, and `rexws schema` emitting it.
  `SPEC.md` already promises Raycast/launcher consumers; this is that promise
  made concrete.
- **S3 · Normalized event stream** `[S/M]` — `rexws events --json`: NDJSON with
  stable type names, enriched with project/branch/workstream so consumers never
  parse raw Rex events. Covers agent transitions, session lifecycle, and workflow
  changes.
- **S4 · Machine predicates** `[S]` — `rexws wait <selector> --for
  blocked|idle|done|attention|status:<id>` plus documented exit codes
  (satisfied / timeout / unavailable). Makes rexws scriptable and gives
  satellites a blocking primitive.
- **S5 · Hooks** `[S/M]` — lifecycle command templates (`after_create`,
  `before_remove`, `on_status`, `on_agent_state`) receiving structured context,
  using the same `text/template` syntax as providers.
- **S6 · Stable workstream identity** `[M]` — metadata survives branch renames
  and path moves, and a reused path does not inherit the previous occupant's
  metadata. Links, history, and attention all become untrustworthy without this.
- **S7 · Context resolution** `[S]` — `rexws here` resolves the workstream from
  `$PWD` and Rex environment variables, emitting a selector and shell-ready
  context. The hook that lets agents and prompts name themselves.
- **S8 · Extension columns** `[M]` — config-defined commands that receive the
  workstream JSON and return bounded fields (deployment state, ticket priority,
  PR state). The generic seam that keeps forge integrations *out* of core.
- **S9 · Deep-link scheme** `[S]` — `rexws link <selector>` prints a stable URI
  that opens a workstream, window, or attention item from chat, docs, or a
  notification.

---

## 2. Core — small derivations and one-shot actions

### 2.1 Orientation and navigation

- **C1 · `doctor`** `[S]` — repositories, providers, Rex binary/version, server
  reachability, app client, config validity, state-schema version, orphaned
  entries. `--fix` prunes stale state. Cheapest high-value command in the bank.
- **C2 · `here` / `current`** `[S]` — resolve the current workstream from cwd.
  Pairs with `status --self` so an agent can update its own card.
- **C3 · `explain`** `[S/M]` — why did this Rex session match this workstream,
  why does the aggregate badge say what it says, and where did each displayed
  fact come from.
- **C4 · `statusline`** `[S]` — one line for a shell prompt or Claude Code
  statusline: project/branch, agent state, workflow status.
- **C5 · Saved views** `[S/M]` — named filter + grouping + sort + column sets:
  `needs-attention`, `ready-to-review`, `remote-experiments`.
- **C6 · Quick Open** `[M]` — fuzzy search across projects, branches, sessions,
  windows, links, notes, and issue IDs, ranked by match quality then recency.
- **C7 · Frecency** `[S]` — `switch --last`, a recent-workstreams list, and
  ranking informed by both recency and frequency.
- **C8 · `path` / `exec` / `open`** `[S]` — navigation without switching: print
  the worktree path, run a command in it, or open it in `$EDITOR`.
- **C9 · Command palette** `[M]` — contextual actions in the TUI without
  committing to configurable keybindings (which the roadmap defers).

### 2.2 Worktree lifecycle

- **C10 · Rich start-from choices** `[S/M]` — default base, local or remote
  branch, commit, PR, issue, or another workstream. Extends the Milestone 3
  provider templates with `{{.BaseRef}}` and friends.
- **C11 · Setup hooks** `[S/M]` — per-repo after-create commands (dependency
  install, `.env` wiring, `direnv allow`) run inside a labeled Rex block so the
  output is visible, with `--setup run|skip|inherit`.
- **C12 · Layout recipes** `[M]` — declarative windows/blocks/commands per
  project, applied through Rex on create. Includes *capturing* a live session's
  layout into a recipe (structure only, never terminal contents).
- **C13 · Background creation** `[M]` — provider progress and logs without
  blocking the dashboard, with retry and cancel.
- **C14 · Shared-path policies** `[M]` — symlink dependency and cache
  directories from the primary checkout, and copy explicitly configured
  gitignored files such as `.env`. The single biggest quality-of-life win for
  large repos.
- **C15 · Safe remove** `[S]` — refuse on a dirty worktree unless `--force`, and
  say exactly what would be lost.
- **C16 · Archive vs delete** `[S/M]` — archive closes Rex resources and hides
  the workstream but keeps the checkout; delete separately chooses whether to
  retain the branch.
- **C17 · Sleep / wake** `[S]` — stop an idle resumable agent, keep the
  workstream, relaunch its conversation later. Distinct from archive and delete.
- **C18 · Cleanup candidate report** `[S/M]` — merged, clean, inactive, stopped,
  or ephemeral workstreams, each with the reason listed, honoring protections
  for pinned, dirty, unmerged, and actively reviewed work. Preview first, never
  automatic.
- **C19 · Retained-branch report** `[S]` — after removing a checkout, list
  branches that remain because they may hold unmerged work.
- **C20 · Orphan triage** `[S]` — unmatched Rex sessions, stale state entries,
  unregistered worktrees, duplicate or ambiguous associations.
- **C21 · Ephemeral marking** `[S]` — flag experiments so they resurface later
  as cleanup candidates instead of silently accumulating.
- **C22 · Project discovery** `[S/M]` — scan a directory of repositories,
  bulk-register, and group projects.

### 2.3 Git health and shipping

- **C23 · Git health** `[S]` — dirty/staged/untracked, ahead/behind, unpushed
  commits, conflicts, base freshness, diff size.
- **C24 · Saved comparison bases** `[S]` — preserve the start-from ref and
  display `branch → base`; compare against another branch or commit on demand.
- **C25 · External review launchers** `[S]` — open the diff in a difftool,
  `$EDITOR`, lazygit, or a browser.
- **C26 · Visible Git operations** `[S]` — commit, push, sync, and PR creation
  run in labeled Rex blocks rather than hidden behind an internal Git
  implementation.
- **C27 · Staging delegate** `[S]` — hand hunk- or line-level staging to an
  existing Git UI, keeping rexws as the entry point.
- **C28 · Predicate-based filters** `[S]` — filter on any extension column (S8):
  draft reviews, requested changes, failing checks, conflicts, merged-but-not-
  cleaned.

### 2.4 Sessions and agents

- **C29 · Terminal peek** `[S/M]` — `rex capture` excerpt in a TUI detail pane
  and as a CLI command, so you can tell "waiting on a permission prompt" from
  "thinking" without switching. Lazy-load; do not poll every block.
- **C30 · `send` / `key`** `[S]` — thin, selector-addressed wrappers over
  `rex send` / `rex send-key`. Human-initiated; never an unattended loop.
- **C31 · Suggested status** `[S]` — derive a proposal from agent state
  (working → in_progress, done → in_review) and show it as a hint. Never
  auto-write human metadata; one keystroke accepts.
- **C32 · Attention predicate** `[S]` — `rexws attention --json` and
  `wait --for attention`: does anything need a person, and where exactly
  (session, window, block). The *predicate* is core; acknowledgment state is a
  satellite.
- **C33 · State ages** `[S]` — "working 14m", "waiting 2h", "finished 8m", with
  sort and filter by duration. Needs C38.
- **C34 · Launch profiles** `[S/M]` — named combinations of backend, binary,
  arguments, model, effort, permission mode, and a non-secret account label.
- **C35 · Resume / fork** `[M]` — relaunch the backend's own resume or fork
  mechanism in a new Rex block where supported, keeping the worktree
  association.
- **C36 · Provenance metadata** `[M]` — associate workstreams and commits with
  the agent, launch profile, conversation ID, and initial task. No line-level
  authorship claims.
- **C37 · Workspace sets** `[S/M]` — open a related collection of workstreams
  and selected windows in one action, such as API + frontend + docs.

### 2.5 State, history, and hygiene

- **C38 · Status transition history** `[S]` — append `{status, at}` instead of
  overwriting `updated_at`. Enables ages, flow metrics, and a timeline view.
- **C39 · Progress checkpoints** `[S]` — append timestamped "what changed / what
  is next" entries rather than overwriting the only note.
- **C40 · Decision log** `[S]` — decisions, rejected alternatives, constraints,
  and open questions alongside the status note.
- **C41 · Mutation receipts** `[S/M]` — record requested targets, actual
  actions, results, and failures for bulk and provider operations.
- **C42 · Git-backed restore points** `[M]` — explicit named refs or commits
  that can be listed, diffed, and restored.
- **C43 · Conflict rehearsal** `[S]` — test a merge or rebase in a temporary
  worktree and report likely conflicts without touching the real checkout. Pure
  Git, unusually high value for the effort.
- **C44 · Scratch-task inbox** `[S/M]` — capture a task or URL before it
  deserves a worktree; promote it into one later.
- **C45 · Intent lint** `[S]` — flag aging workstreams with no task, note,
  recent activity, or evident next action.
- **C46 · Next-action hints** `[S]` — deterministic suggestions: "review
  completed agent", "push three commits", "resolve conflicts", "merged; candidate
  for cleanup".
- **C47 · Definition-of-done checklist** `[S/M]` — per-project checks such as
  pushed, reviewed, CI green, docs updated, cleanup done.
- **C48 · WIP budget and timeboxes** `[S]` — soft limits that call attention to
  excessive active workstreams, and experiments that exceeded an investigation
  budget. Warn; never block or stop.
- **C49 · Focus mode** `[S]` — one workstream, its dependencies, and anything
  demanding input.
- **C50 · Prewarmed pool** `[M/L]` — clean bases and dependency caches prepared
  ahead of a task arriving.

### 2.6 Presentation state

Storage is core; the views that use it may be satellites or the TUI.

- **C51 · Independent presentation state** `[S]` — pins, favorites, parking,
  custom titles, colors, manual groups, and per-client presentation targets.
  Deliberately not overloaded onto workflow status.
- **C52 · Query filter grammar** `[S/M]` — `status:blocked agent:working
  dirty:true`, matching the selector grammar of S1.
- **C53 · Bulk selection** `[S/M]` — multi-select in the TUI for sleep, close,
  or status changes.
- **C54 · Workstream hierarchies** `[M]` — parent/child organization for
  subtasks, including across repositories.
- **C55 · True stack relationships** `[M/L]` — model stacked branches and
  reviews, as distinct from cosmetic nesting.
- **C56 · Portfolio view** `[M]` — group by goal across repositories rather than
  by repository.
- **C57 · Templates from history** `[S/M]` — start from a previous workstream's
  checklist, recipe, links, and structure, but not its code or conversation.

---

## 3. Satellites — one tool each

Each consumes `rexws --json`, the event stream (S3), and the selector grammar
(S1). None of them should ever be merged into the core binary.

| Tool | Idea | Why it cannot be core | Consumes |
| --- | --- | --- | --- |
| **T1 · `wsfind`** | Per-workstream and cross-workstream transcript search; index local agent histories by worktree, branch, title, model, first prompt, and recent messages | Needs a persistent full-text index | `list --json`, S1, `here` |
| **T2 · `wsnotify`** | Attention inbox, read/dismissed/snoozed/muted state, notification rules (desktop, webhook, chat), catch-up digest | Owns acknowledgment state across restarts; long-lived process | `events --json`, `attention --json` |
| **T3 · `wspr`** | Forge enrichment: draft/open/merged, review decision, comments, checks, mergeability, merge queue; issue enrichment with cached title, status, assignee, priority, labels | Network, auth, rate limits, caching | S8 column protocol |
| **T4 · `wsdig`** | Activity journal, unread feed, flow metrics (time-in-status, blocked transitions, review latency, age) | Appends its own durable log | `events --json`, C38 |
| **T5 · `wsports`** | Port and preview registry: Rex forwards, detected listeners, advertised dev-server URLs, "open preview" | Platform-specific scanning, a live cache | `list --json` |
| **T6 · `wsusage`** | Provider usage, reset times, balances, warning thresholds | Reads provider state; changes without notice | `list --json` |
| **T7 · `wsrace`** | Race N worktrees × agents/models from one base; candidate scorecards on diff size, tests, findings, runtime, usage | Coordinates many long-lived sessions and stores results | `new`, `list --json`, S5 |
| **T8 · `wsfleet`** | Dispatch a prompt or command to selected workstreams, each visible in its own Rex block; local concurrency queues | Scheduler and queue state | S1, S3, `send` |
| **T9 · `wsreview`** | Review-comment packets and failed-check repair packets, delivered as one structured revision prompt | Assembles multi-source context; may call a forge | T3, `send` |
| **T10 · Frontends** | `fzf` selector, `list --watch`, menu-bar client, Raycast extension | Their own rendering and event loop | `list --json`, S3 |
| **T11 · `wshandoff`** | Agent handoff (end one agent, start another with explicit context) and portable handoff bundles: non-secret manifest of task context, notes, links, base ref, recipe, optional patch | Packaging, transport, and cross-agent coordination | `list --json`, C34, C57 |

Two ideas that *looked* like satellites but are better as core: capturing a live
layout back into a recipe (C12 — no separate state, just Rex reads), and forge
data shown as a column (S8) if it were ever cheap enough to compute on demand.
A satellite wins only when caching or rate-limit handling is genuinely required.

---

## 4. Gate — waiting on a dependency or a decision

- **D1 · Forge CLI availability** — does `gh` (and `glab`) exist and stay
  authenticated on this machine? Decides whether T3 is worth building at all.
- **D2 · Multi-host inventory** — Rex already has `-S/--server`, `hosts`,
  `forward`, and `cp`. Open questions: namespace session identity by host, show
  the last successful inventory with an explicit "last seen" for a disconnected
  host, keep one unreachable host from hiding everything else, and decide
  whether remote *execution* is in scope (probably not). `rex cp` is the natural
  path for pulling logs and screenshots.
- **D3 · Provider usage exposure** — unverified whether Claude/Codex expose local
  usage or rate-limit state. Check before promising T6.
- **D4 · Notification channels** — which of desktop, webhook, and chat you
  actually want, before T2 grows options.
- **D5 · Cost of `rex capture`** — how expensive a per-block capture is decides
  whether C29 can refresh freely or must be strictly lazy.

---

## 5. Not ours

Named so they stop being relitigated:

- Durable orchestration service: runs, task dispatches, heartbeats, retries,
  nested workers, federation.
- Workstream mailbox, decision gates, dependency graphs, and cross-workstream
  workflow pauses. Local concurrency queues (T8) are the acceptable slice.
- Native structured chat, native diff/review UI, line-level attribution.
- Embedded editor, Markdown/Mermaid/PDF/image viewers.
- Per-workstream browser, browser automation, computer use, emulators.
- Persistent bot platform (Slack, Gmail, Calendar, Linear connectors) and a
  managed credential vault.
- Multi-user mobile/remote backend: device pairing, remote approvals, access
  control, shared servers.
- Plugin host and plugin catalogs. Provider templates plus hooks (S5) are the
  whole extensibility story.
- A scheduling engine with run history. A cron entry plus `--dry-run` cleanup
  (C18) covers the real need; write it as a recipe, not a subsystem.

Still out, per the existing roadmap: automatic Rex session reordering,
configurable TUI keybindings, multiple sessions per workstream, a custom agent
state vocabulary, and toggling bare/prunable worktree inclusion.

---

## 6. Suggested sequence

The point is not to work through the list. It is to buy the ability to build
from it cheaply.

1. **Seams first** — S1 selectors, S2 JSON contract, S7 context resolution. One
   sitting, and every later idea gets cheaper.
2. **Five small core commands** — C1 `doctor`, C2 `here`, C38 transition
   history, S3 events, S4 predicates. This is the spine.
3. **One satellite as proof** — T1 `wsfind`. It is the idea that most clearly
   *cannot* be core, so it validates the contract under real pressure.
4. **Then pick by use case, not by list order.** Best value per unit of effort
   in core: C14 shared paths, C23 git health, C29 terminal peek, C43 conflict
   rehearsal.
5. **One satellite in flight at a time.** Two is how a composable toolbox turns
   back into a monolith.

This section should migrate into `ROADMAP.md` as milestones once a use case
appears; the composability rule itself belongs in `SPEC.md` as a stated design
principle.
