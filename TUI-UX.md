# rexws TUI UX spec

Status: **v2 — approved and implemented in Milestone 4.**
The implementation lives in `internal/tui`.

Related: `SPEC.md` (product design), `ROADMAP.md` (milestones).

## 1. Goal and scope

The TUI is a grouped, hierarchical workspace dashboard.

- **Navigable levels:** workstream (parent) → window (child).
- **Not navigable:** workflow-status group headers and agent badges.
- **Layout:** accordion / inline tree inside the existing workflow-status groups.
- **Confirmation:** `Enter` always confirms the highlighted row.
- **Hierarchy keys:** `Right` expands only; a second `Right` enters the first
  child. `Left` collapses or returns to the parent.

Goals:

- See workflow status, session state, and agent activity at a glance.
- Expand one workstream to see and switch to its windows.
- Filter the hierarchy with live search while keeping parent context.
- Preserve the existing CLI/TUI vocabulary and keybindings where possible.

Non-goals for this milestone:

- Mouse support, Kanban view, configurable keybindings.
- Window creation/renaming/closing from rexws (Rex CLI owns those).
- Multiple sessions per workstream.
- Fuzzy ranking in v1 (substring first; fuzzy can be added later).
- Per-status colors/icons (requires a config-schema change; deferred).
- Automatic Rex session reordering.
- Sticky group headers.
- An agent message/title detail pane.

## 2. Prerequisites before TUI implementation

These are implementation prerequisites, not UX. They must be done first so the
TUI can stay thin and testable.

1. **Extract shared operations out of `package main`.** Add `internal/ops` with
   functions used by both the CLI and TUI:
   - `Discover` returning per-project results and per-project errors.
   - `SetStatus` for a worktree.
   - `CreateWorktree` for the M3 provider flow, including post-create
     verification.
   - `RemoveWorkstream` including main/dirty/Rex pre-checks, session close,
     provider remove, and state cleanup.
   - `SwitchWorkstream` (or an equivalent thin wrapper) for Rex session/window
     selection.
2. **Add a `provider.Command(args, workdir) (*exec.Cmd, error)` seam.** Keep
   `provider.Run` for the CLI, but the TUI needs an `*exec.Cmd` for
   `tea.ExecProcess`. Do not run a blocking provider command directly inside
   Bubbletea.
3. **Populate `WorkflowStatusLabel` in `core.Build`** (or via a shared helper).
   The CLI currently fills it after `Build`; the TUI must not duplicate that.
4. **Add a per-window agent formatting helper** in `render`: format a window's
   agents with the same option-A rules as the parent, and return `—` when the
   window has no reporting agents.
5. **Extend the Rex event parser** for `session_view_changed` and
   `active_window_changed` (and verify any window-created/closed events with a
   live fixture). Add fixtures/tests. Without this, window rows and active
   markers are stale until `r`.
6. **Add a sanitization helper** for all externally supplied display strings
   (see §10). Use it before measuring or rendering.
7. **Add tests** for `ops`, the event parser, the per-window formatter, and the
   sanitizer.

## 3. Hierarchy and data

### 3.1 Row identity

| Row | Stable key | Notes |
| --- | --- | --- |
| Status group | Status ID | Not selectable, never in the cursor set |
| Workstream | Canonical `Path` | `project/branch` is a session identity, not a row identity |
| Window | `window_id` | Labels are display strings and may collide |

Detached worktrees can share a short SHA and therefore a `SessionName`. The TUI
keys rows by `Path`; it does not pretend the session labels are unique. If two
workstreams resolve to the same live Rex session, show a persistent warning
banner and do not invent a disambiguation. A future adoption/label feature can
resolve it.

### 3.2 Parent row

Shows, left to right:

- Selection marker.
- Expand chevron (`▸` collapsed, `▾` expanded) when the workstream has windows.
- Project name.
- Branch (flexible width; truncated with `…`).
- Aggregate agent badge (option A formatter) over all agents.
- Window count when `Windows` is non-empty:
  - No filter: `N tabs` / `1 tab`.
  - Filter, parent match (all children visible): `N tabs`.
  - Filter, context-only parent (only matching children visible): `V/N tabs`.

The workflow status label is **not** repeated on the parent row; the group header
carries it. The parent row does not show a separate session column; the session
is implied by the workstream.

A stopped workstream has no windows and no chevron. A live session with no
windows also has no chevron.

### 3.3 Child row

Shows:

- Selection marker.
- Two-space indent and a child marker (`•`).
- Active-window marker `*` plus bold label when `Window.Active` is true.
- Window label. If labels are duplicated within the session, append the last six
  characters of the window ID: `dup (76bc89)`.
- App name, chosen as:
  1. the focused block's agent app, if the focused block is an agent;
  2. otherwise the app of the agent that determines the window's highest
     severity;
  3. otherwise `—`.
- Per-window agent badge: the same option-A formatter over agents whose
  `WindowID` matches; `—` if there are none.

Window rows are ordered by Rex's window order. `Window.Active` comes from Rex's
session active-window value; it is not a per-client focus concept.

### 3.4 Status groups

- Group headers are `▌ LABEL (count)`; the `▌` is a static section marker, not a
  collapse control.
- Counts:
  - No filter: total workstreams in the group.
  - Filter active: `visible/total` (for example `(1/3)`).
- Groups with no visible workstreams are hidden.
- Group headers are skipped by navigation and are not selectable.
- Collapsing groups is out of scope.

### 3.5 Header summary

The header shows:

- `REXWS`.
- Active filter chip: `filter: <query> ×`.
- Counts: `<N> workstreams · <M> live · <K> agent-blocked`.

Counts are over the **visible** set when a filter is active, and over the full
set otherwise. The filter chip makes this clear.

- **workstreams:** workstreams in the counted set.
- **live:** workstreams with a live Rex session.
- **agent-blocked:** workstreams whose aggregate agent state is `blocked`.
  Workflow status is not included here; the group header carries it.

At narrow widths, abbreviate `workstreams` to `ws` and drop the agent-blocked
count first (see §4.3).

If no workstreams exist, show the header and:
`No workstreams. Register a repository with: rexws project add <path>`.

## 4. Layout

### 4.1 Normal, 80 columns

```text
 REXWS  [4 workstreams · 3 live · 1 agent-blocked]
──────────────────────────────────────────────────────────────────────────────
 ▌ IN PROGRESS (2)
 > ▾ rexws          m1-m2-core-rex      working · 2 tabs
       • agent       claude-code         working
       • tests       —                   —
   ▸ do-si-do       refactor-sync       blocked (permission) · 1 tab
 ▌ BLOCKED (1)
   ▸ monorepo       sparse-api-v2       idle · 3 tabs
 ▌ READY (1)
   .ai              main                stopped
──────────────────────────────────────────────────────────────────────────────
 [Enter] switch  [→] expand  [←] back  [/] filter  [s] status  [?] help
```

- `>` marks the selected row.
- `▾`/`▸` mark expansion.
- The selected row uses an accent background and bold text, not color alone.
- The active window gets `*` before its label and a bold label.
- A silent shell window shows `—` for both app and agent badge.

### 4.2 Filtered, 80 columns

```text
 REXWS  filter: tests ×  [2 workstreams · 2 live · 0 agent-blocked]
──────────────────────────────────────────────────────────────────────────────
 ▌ IN PROGRESS (2/3)
   ▾ tests-refactor  feat-x           working · 2 tabs
       • agent       claude-code      working
       • logs        —                —
 > ↳ rexws           m1-m2-core-rex   working · 1/2 tabs
       • tests       —                —
──────────────────────────────────────────────────────────────────────────────
 Search: tests█   [↑↓] move  [Enter] accept  [Esc] clear
```

- `tests-refactor` matched the filter itself, so all its children are visible.
- `rexws` is context only: it is dimmed, prefixed with `↳`, force-expanded, and
  shows only the matching child. The `↳` replaces the expand chevron on a
  context parent.
- The matched substring would be bold + underlined (plus accent when color is
  available). The plain-text mockup cannot show that.
- Counts are visible counts: 2 workstreams, 2 live, 0 agent-blocked.
### 4.3 Narrow widths

- Supported minimum: **60 columns × 18 rows**. Below that, show a centered
  `terminal too small` message.
- Never use a split-pane layout.
- At 60–79 columns:
  - Header uses `ws` and drops the agent-blocked count.
  - Truncate the filter query to 12 cells; keep the chip.
  - Hide the window count on parent rows.
  - Hide the app name on child rows before truncating the window label.
  - Limit the parent badge to 18 cells; limit the child badge to 18 cells.
  - Project max 10 cells; branch keeps at least 10; window label at least 12.
- At 80+ columns:
  - Full header counts.
  - Parent badge max 28 cells; project max 12; branch flex.
  - Show window counts and app names.
- Never wrap a row. Use `…` and `lipgloss.Width` on sanitized text.
- Footer priority: `Enter`, expand/back, filter, help. Add
  `s`/`n`/`d`/`r`/`q` only when they fit.
- When a filter is active, replace the `[/] filter` footer slot with
  `[Esc] clear filter`.

Compact 60-column shape:

```text
 REXWS filter: tests × [2 ws · 2 live]
──────────────────────────────────────────────
 ▌ IN PROGRESS (2/3)
   ▾ tests-refactor feat-x  working
       • agent claude-code  working
 > ↳ rexws m1-m2-core-rex  working
       • tests —            —
──────────────────────────────────────────────
 [Enter] switch [→] expand [/] filter [?] help
```

### 4.4 Scrolling

- The body is a viewport. The cursor is always kept visible.
- `PgUp` / `PgDn` move the **selection** by one page; the viewport follows.
  They do not scroll independently of the cursor.
- Group headers scroll with content; sticky headers are out of scope.
- If the cursor is not visible, scroll the minimum amount to reveal it.

## 5. Keybindings

### 5.1 Normal mode

| Key | Action |
| --- | --- |
| `j` / `k`, `↓` / `↑` | Move through visible rows; skip group headers |
| `l` / `→` | Collapsed parent: expand only, cursor stays. Expanded parent: move to its first visible child. Child: no-op |
| `h` / `←` | Child: move to its parent. Expanded parent: collapse, cursor stays. Collapsed parent: no-op |
| `Space` | Toggle expand/collapse without moving the cursor; child: no-op |
| `Enter` | Confirm the highlighted row |
| `/` | Enter search mode |
| `s` | Open the status selector for the selected row's workstream |
| `n` | New worktree flow |
| `d` | Remove the selected row's workstream after pre-checks and confirmation |
| `r` | Refresh Git and Rex state; retry Rex/event connections |
| `PgUp` / `PgDn` | Move the selection by one page |
| `q` | Quit |
| `?` | Help |
| `Esc` | Clear an active filter; otherwise no-op |
| `Ctrl+C` | Quit from any mode |

Rules:

- `Enter` never drills down. Use `Right`/`Space` to reveal children.
- A second `Right` is what enters the first child; the first `Right` only
  expands. This round-trips with `Left`.
- Group headers are skipped. Collapsed children are hidden.
- Navigation does not wrap at the edges.
- Right on a parent with no children is a no-op. Space on a child is a no-op.
- There is no `g`/`G`, `Home`, or `End` binding in v1.

Selection algorithm:

- Track the selected stable key: `Path` for a workstream, `window_id` for a
  window.
- After a refresh or filter change, select the same key if it is still visible.
- Otherwise clamp the previous row index to the new row count, then scan forward
  for the next selectable row; if none, scan backward. If no selectable row
  exists, there is no selection.
- With no selection, `Enter`, `s`, `d`, `Right`, and `Space` are no-ops.
  `/`, `n`, `r`, `PgUp`/`PgDn`, `q`, and `?` still work.

### 5.2 Search mode

Entered with `/`. The query input is focused and filters live.

| Key | Action |
| --- | --- |
| Printable characters, including `Space` | Append to the query |
| `Backspace`, `Delete` | Edit the query |
| `←` / `→`, `Home` / `End` | Move the text cursor inside the query |
| `Ctrl+u` | Clear the query |
| `↓` / `↑`, `Ctrl+n` / `Ctrl+p` | Move the selection among visible rows |
| `Enter` | Accept the filter and return to normal mode; a non-empty filter remains active |
| `Esc` | Clear the filter and return to normal mode |
| `Ctrl+C` | Quit |

Decisions:

- Search mode is a **text input**. `Space` types; `Left`/`Right` edit the query;
  no tree expansion keys are active during search.
- `j`/`k` type characters. Use arrows or `Ctrl+n`/`Ctrl+p` to move the selection.
- `Enter` accepts the filter; it does not switch. A second `Enter` in normal mode
  switches the highlighted row.
- If the query becomes empty, the filter is cleared immediately, the pre-search
  expansion state is restored, and the input stays focused so the user can type
  again. `Enter`/`Esc` then exit search.
- The footer switches to search-specific hints.

### 5.3 Modal modes and priority

Modals capture keys before search or normal mode.

| Mode | Keys |
| --- | --- |
| Status selector | `↑`/`↓` move; `1..9` apply immediately where available; `Enter` apply the highlighted status; `Esc` cancel |
| Remove confirmation | `y` or `Enter` confirm; `n` or `Esc` cancel |
| Help | Scroll with `↑`/`↓`/`PgUp`/`PgDn`; `?` or `Esc` close |
| New worktree | Form keys; `Enter` continue; `Esc` cancel; provider scripts via `tea.ExecProcess` |

Priority: **modal → search → normal**.

- `Esc` closes the modal, then clears search, then clears an active filter, then
  no-ops.
- `Esc` never changes hierarchy level. `Left`/`h` does that.
- `q` quits only in normal mode. In search it types; in a modal it is handled by
  that modal if bound.
- `Ctrl+C` always quits.
- The error banner is **not** a modal (§9); it does not capture keys.

Status selector details:

- The current status is highlighted.
- Applying the current status is a no-op and does not change `updated_at`.
- `1..9` apply the corresponding status immediately, as a shortcut. With more
  than nine statuses, number keys cover the first nine; use arrows for the rest.
- `Enter` applies the highlighted status (after moving with `↑`/`↓` or a number
  key).
- The selector applies to the workstream of the selected row.

## 6. Search

### 6.1 Input model

- Explicit `/` trigger. Typing does not filter until search mode is entered.
- Case-insensitive substring matching in v1. Fuzzy subsequence matching can be
  added later; it must expose match positions for highlighting.
- Live filtering on every keystroke. The set is small; no debounce is required.
- A non-empty filter persists after `Enter`. It is cleared by `Esc`, by emptying
  the query, or by the normal-mode `Esc` when a filter is active.
- Refocusing search with `/` puts the text cursor at the end and does not take a
  new expansion snapshot.

### 6.2 Search fields

Match against these fields independently:

- **Status group:** label, ID.
- **Workstream:** project, branch, and each agent's state/detail. Do not match
  the reduced aggregate separately; matching any agent makes the workstream
  visible.
- **Window:** label, displayed app, and each agent in that window's
  state/detail.
- Do not match: full paths, notes, agent `msg`/`title`, hidden
  `effective_app` values, or timestamps.

Search uses the displayed value for app: if `App` is set, that is the value; if
not, the displayed fallback is `EffectiveApp`. A hidden `EffectiveApp` is not
searchable.

### 6.3 Visibility rules

For each workstream, compute:

- `groupMatch`: the query matches the status group's label or ID.
- `parentMatch`: the query matches a workstream field, or `groupMatch` is true.
- `childMatches`: windows whose child fields match.
- `childMatch`: `len(childMatches) > 0`.

Rules:

1. **Parent matches, no child matches:** show the parent. When expanded, show
   **all** of its children. Do not auto-expand a parent-only match.
2. **Parent matches and at least one child matches:** show the parent and **all**
   children; force the parent expanded; highlight matching children.
3. **Parent does not match, at least one child matches:** show the parent as
   context, force it expanded, and show **only** the matching children. Mark the
   parent with `↳`.
4. **Neither:** hide the workstream.
5. **No windows:** rules 1 and 4 apply.

A group match sets `parentMatch` for every workstream in that group. This is how
workflow-status search works without a per-row status label. The group header is
visible and its matched label substring is highlighted.

Why parent matches show all children:

- A workstream match means the whole workstream is relevant; its windows are its
  tabs. An expanded parent that shows nothing is confusing.
- A child-only match is focused, so its context parent shows only the matching
  children.

### 6.4 Expansion snapshot and forced expansion

- Take **one snapshot** of the expansion state when the query transitions from
  empty to non-empty. Do not take a new snapshot when `/` is pressed again.
- Hold the snapshot until the filter is cleared. Clearing the filter restores it.
- While the filter is active, force-expand context parents (rule 3) and parents
  with matching children (rule 2).
- Forced expansion is **sticky for the filter session**: once a parent is forced
  open by a child match, it stays open while the filter remains active, even if
  the query changes. This avoids expand/collapse flicker while typing.
- Parent-only matches use the snapshot expansion, plus sticky forcing if that
  parent was previously forced open.
- No manual tree toggles occur during search; the tree is derived from the
  snapshot, sticky forcing, and the rules.
- After accepting the filter, normal-mode toggles are allowed, but they do not
  update the snapshot. Clearing the filter restores the original snapshot.
- If a selected row disappears after a query change, apply the cursor algorithm
  in §5.1.

`BuildRows` inputs must include the workstreams, status definitions, snapshot,
sticky forced set, and query. A precomputed `effectiveExpanded` set is acceptable
if it is pure and tested.

### 6.5 Visual distinction

- Highlight every matched substring in rendered direct fields with bold +
  underline, plus accent color when available.
- Do not synthesize highlight offsets inside an aggregate agent badge. Agent
  state/detail matches make the row/window visible; the badge itself is not
  span-highlighted.
- A context parent is dimmed and prefixed with `↳`; it replaces the expand
  chevron. Do not rely on color alone.
- A parent that matched itself uses normal styling.
- The filter chip stays visible when the filter is active but search is not
  focused.
- The search bar shows the query, visible counts, and keys, for example:
  `Search: tests█   [↑↓] move  [Enter] accept  [Esc] clear`.
- Match counts are counts of visible workstreams and windows, not raw field
  matches.

### 6.6 Empty and edge states

- Empty query: no filter; all rows per the normal or restored expansion state.
- No matches: `No matches for "<query>". Press Esc to clear.` The query remains
  editable.
- Backspace to empty: clear the filter, restore the snapshot, keep search
  focused.
- A context parent with one matching child shows `1/N tabs`.
- Status groups with no visible workstreams are hidden.

## 7. Actions

### 7.1 Single-flight actions

- At most one mutating action (`Enter`, `s`, `d`, `n`) is pending at a time.
- While an action is pending, ignore further mutating keys and show a spinner or
  `working…` in the footer/status line.
- Navigation and filtering remain available. `r` is ignored while an action is
  pending, to avoid racing a refresh with the action. When the TUI is suspended
  by `tea.ExecProcess`, all keys go to the child process.
- Every action, on success or failure, triggers a refresh when it returns.
- `tea.ExecProcess` returns to the TUI only after the child exits; refresh
  immediately afterward.

### 7.2 Enter

- **Live workstream:** select the session. If a window is marked active, pass its
  `window_id`; otherwise select the session without a window. This makes the
  landing tab deterministic.
- **Stopped workstream:** create the session in the worktree directory and select
  it. Refresh afterwards, expand that parent once, and keep the cursor on the
  parent's `Path`. If creation succeeds but selection fails, show a banner and
  still refresh (the session exists).
- **Window:** select the window by `window_id`. If the session is gone, show a
  banner and refresh.
- **Rex unavailable:** do not create or select. Show `Rex unavailable; press r to
  retry.`
- If the workstream has no live session and Rex is reachable, creation is
  allowed. If the workstream has a live session but the app client cannot be
  resolved, show the error and refresh.

### 7.3 Status, remove, new

- `s` opens the status selector for the selected row's workstream. Applying a
  status refreshes; the cursor follows the workstream's `Path`, even if it moves
  to another group.
- `d` runs these pre-checks **before** showing confirmation:
  1. The main worktree cannot be removed.
  2. A dirty worktree cannot be removed; show Git's reason.
  3. Rex must be available to close a live session; if not, explain and do not
     open the dialog.
  4. The project/provider must resolve.
  The confirmation names the workstream and warns that its Rex session and
  windows will close. On success or failure, refresh and apply the cursor rule.
- `n` opens the new-worktree form: project, branch, provider (default from the
  project), and target-path preview. On submit, run the provider via
  `tea.ExecProcess`. After return, refresh, verify exactly one worktree exists
  for the branch, and select it by `Path`. Provider exit 0 with no worktree is an
  error. Cancel leaves the view unchanged.
- `r` refreshes Git and Rex state and retries event connections. It preserves
  query, expansion, and cursor by stable key.

### 7.4 Error banner

- Non-modal status line above the footer. It never captures navigation keys.
- `x` dismisses it; it auto-dismisses after 8 seconds. `Esc` is not used to
  dismiss it.
- Show the newest error; if others are pending, indicate `+N more`.
- Errors never crash the TUI.

## 8. Live updates

Prerequisite: the Rex event parser must expose window/view events (§2).

Streams:

- One server-wide `rex events --json` stream for session lifecycle.
- One per-session `rex events -s <session> --json` stream for program status,
  `session_view_changed`, and `active_window_changed`, for each live session.
- Start/stop per-session streams as sessions appear and disappear.
- Reconnect with exponential backoff (1s, 2s, 4s, up to 30s). Show `live updates
  paused` in the header/footer; `r` reconnects immediately.
- Do not let a slow handler block the event scanner. Send events to a buffered
  channel and coalesce updates.

Handling:

- `program_status_changed`: update the matching agent in place; recompute the
  workstream and window badges; no full re-list.
- `program_status_removed`: refresh that session's agents.
- `session_view_changed` / `active_window_changed`: refresh that session's
  windows (labels, counts, active marker).
- Session created/destroyed: refresh sessions and workstreams, update streams.
- Debounce window/session refreshes to about 200 ms per session.
- Preserve filter, expansion snapshot, and cursor by stable key.
- If the selected row disappears, apply the cursor rule.
- Do not reorder workstreams or sessions automatically.
- Auto-expand only after `Enter` creates a session for a stopped workstream.

If Rex becomes unavailable after data was loaded:

- Keep the last-known rows and badges, but mark the header `Rex unavailable; live
  updates paused`.
- Do not reinterpret existing live rows as `stopped`.
- Disable create/select (`Enter`) and remove (`d`), since they depend on Rex.
- `s` (state only) and `n` (worktree creation) remain available.
- `r` retries; when Rex returns, refresh and resume streams.

## 9. Errors and startup

- **Config or state load error:** full-screen error with the exact message,
  remediation (`fix config.json` / `state.json`; `rexws list`), `r` to retry, `q`
  to quit. Do not start the dashboard.
- **Per-project discovery error:** use `ops.Discover`'s per-project results. Skip
  the failed project, keep the rest usable, and show a persistent warning banner
  naming the project and error. `r` retries.
- **Rex unavailable at startup:** render workstreams with `?` agent badges and no
  windows; header banner `Rex unavailable`; disable create/select and remove; `r`
  retries.
- **No projects:** show the empty-state message.
- **Per-session unreadable:** use the existing `unknown` agent state and show a
  warning; do not fail the whole dashboard.
- **Status/state mismatch:** `LoadState` already rejects statuses not in config;
  surface the error in the full-screen config/state error path.

## 10. Visual and accessibility rules

- **Sanitize everything external before rendering.** Strip C0 controls
  (`0x00–0x1F`), C1 controls (`0x80–0x9F`), and ESC sequences from window labels,
  agent app/effective-app/kind/msg/title, session labels, and any other
  Rex-derived string. Replace control runs with `?`; never allow a newline or
  tab inside a row. Measure and truncate after sanitizing.
- Agent badges are text-only in v1: use the option-A formatter for parents and
  per-window summaries for children. Do not claim symbols that are not
  implemented.
- Selection: `>` plus bold/accent, not color alone.
- Expand state: `▸`/`▾`; context parent: `↳`; active window: `*` plus bold.
- Group header: `▌` static marker.
- Match highlight: bold + underline always; accent color when available. No
  color-only distinction.
- Truncate with `…`; never wrap.
- `NO_COLOR` and monochrome terminals remain usable.
- The footer is always visible and mode-specific.
- Help is a scrollable overlay; it must fit at 18 rows.
- Minimum size 60×18; smaller shows a centered `terminal too small` message.

## 11. Technical architecture

- Add `github.com/charmbracelet/bubbletea`, `lipgloss`, and `bubbles`
  (`textinput`, `viewport`, `key`, `help`).
- Do not use `bubbles/list` for the tree; it is flat and its filter does not
  preserve hierarchy.
- Build a custom `[]Row` view model:
  - `Kind`: group, workstream, window.
  - `Depth`, stable key, display fields, `Matched`, `ContextMatch`, match spans.
- Keep filter/visibility in pure functions and unit-test them:
  - `BuildRows(workstreams, statuses, snapshot, forced, query) []Row`.
  - Visibility rules from §6.3.
  - Match spans for rendered direct fields.
- TUI state:
  - `expanded map[path]bool`.
  - `preSearchExpanded map[path]bool` (one snapshot per filter session).
  - `forcedExpanded map[path]bool` (sticky for the filter session).
  - `query`, `mode` (normal/search/modal), `cursorKey`, `viewport`.
  - `pendingAction` for single-flight.
  - `rexState` (available/unavailable) and `projectErrors`.
- Key handling is a pure reducer: `Reduce(state, key) (state, effects)` so key
  tests do not need Bubbletea. Effects include refresh, switch, open modal, etc.
- `viewport` is render-only; it must not receive `Update` messages, or it will
  interpret `PgUp`/`Up`/`Down` independently of the cursor rules.
- `textinput` handles only text-editing keys in search mode; the parent model
  handles `Up`/`Down`, `Ctrl+n`/`Ctrl+p`, `Enter`, `Esc`, and `Ctrl+C`.
- Rendering uses sanitized strings and `lipgloss.Width`; no ANSI-aware manual
  truncation.
- Parent agent summary uses `render.Default`; add `render.FormatWindowAgents` or
  equivalent.
- Switching uses the extracted ops/Rex wrapper.
- New/remove use `tea.ExecProcess` with `provider.Command`.
- Test render output at 60 and 80 columns with a fixed color profile.

## 12. Acceptance criteria

- [ ] A live session's workstream expands, showing windows in Rex order.
- [ ] `Enter` on a workstream selects its active window if known; otherwise the
  session. `Enter` on a window selects it by ID.
- [ ] A stopped workstream has no chevron; `Enter` creates and selects its
  session, refreshes, and shows the initial window.
- [ ] First `Right` expands a collapsed parent without moving the cursor; second
  `Right` moves to the first visible child; `Left` round-trips.
- [ ] Search mode accepts spaces and edits the query with `Left`/`Right`;
  `j`/`k` type; arrows navigate; `Enter` accepts; a second `Enter` switches.
- [ ] A parent matching the filter shows all children when expanded.
- [ ] A context parent is dimmed with `↳`, auto-expanded, and shows only matching
  children.
- [ ] Search matches per-agent states, so a state visible in a multi-state badge
  is not hidden.
- [ ] A group-label match makes its workstreams visible and highlights the group
  label.
- [ ] Clearing the filter restores the pre-search expansion snapshot.
- [ ] `Esc` closes a modal, then clears search, then clears an active filter;
  it never changes hierarchy level.
- [ ] `s` and `d` on a window target its workstream.
- [ ] `d` refuses the main worktree, a dirty worktree, and a Rex-unavailable
  state before showing the confirmation.
- [ ] Duplicate window labels display a short ID suffix and switch by ID.
- [ ] A Rex outage never renders live workstreams as `stopped` and never offers
  create-on-`Enter`.
- [ ] Double `Enter` cannot create duplicate sessions; mutating actions are
  single-flight.
- [ ] Rex-derived control characters cannot reach the terminal or corrupt width.
- [ ] The layout remains usable at 60×18 and never splits into panes.
- [ ] `PgUp`/`PgDn` move the selection, and the cursor is always visible.
- [ ] If the selected row disappears, selection moves by the defined algorithm;
  with no selection, row actions are no-ops.
- [ ] Unit tests cover visibility, sticky expansion, match spans, the key
  reducer, the cursor algorithm, sanitization, per-window badges, and rendering
  at 60/80 columns.

## 13. Resolved decisions

- Layout: accordion inside status groups.
- `Enter` confirms; `Right` expands only; second `Right` enters the first child.
- Search mode is a text input; `Space` types; no tree keys while searching.
- Header counts are visible counts under a filter; `agent-blocked` means the
  aggregate agent state is blocked.
- Window live updates require extending the Rex event parser.
- Workstream rows are keyed by `Path`; windows by `window_id`.
- Text badges in v1; symbols deferred.
- `d` runs main/dirty/Rex pre-checks before confirmation.
- `Enter` on a live workstream selects its active window if known.
- Single-window parents show their child row.
- Group counts are `visible/total` when filtering.
- Group labels/IDs are searchable.
- Agent states/details are matched per agent.
- Matches are always bold + underlined; accent color is additive.
- Status colors/icons are deferred.
- Fuzzy search is deferred.
- Minimum size is 60×18.

## 14. Docs updates after sign-off

Completed with the Milestone 4 docs pass:

- `SPEC.md` §6 replaced the original flat TUI design with a concise summary of
  this hierarchy and a link here.
- `ROADMAP.md` moved Milestone 4 and window phase 2 to Shipped, checked the
  implemented items, and moved the deferred items to Future candidates.
- `README.md` documents the TUI and the `rexws` / `rexws tui` launch paths.
- This document is linked from `SPEC.md` and `ROADMAP.md`.

Remaining drift outside that pass: `SPEC.md` §2 still describes workflow
statuses as a fixed enum rather than the configurable `statuses` list.

## 15. Out of scope

- Mouse interactions.
- Kanban columns.
- Configurable keybindings.
- Window creation, rename, move, or close from rexws.
- Multiple sessions per workstream.
- Automatic Rex session reordering.
- Sticky group headers.
- Fuzzy ranking in v1.
- Per-status colors/icons.
- Agent message/title detail panes.
- `g`/`G`, `Home`/`End`.
