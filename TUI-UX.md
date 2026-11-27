# rexws TUI UX spec

Status: **v3 — approved and implemented (Milestone 4 plus the TUI reliability
pass).** The implementation lives in `internal/tui`.

Related: `SPEC.md` (product design), `ROADMAP.md` (milestones).

Visual design: [`TUI-DESIGN.md`](TUI-DESIGN.md) defines the palette, wide-terminal layout, column grid, state copy, and accessibility rules. This file remains authoritative for behavior, keybindings, search, and acceptance criteria; `TUI-DESIGN.md` notes where it supersedes a layout or color decision.

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
- Per-status colors/icons (implemented in #13; see `TUI-DESIGN.md` §2.3).
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
keys rows by `Path`; it does not pretend the session labels are unique.

Ambiguity is detected in both directions, against all live Rex sessions:

- **Two or more workstreams to one live session.** Every affected workstream
  row gets `!`, and the banner shows
  `! ambiguous: N workstreams, one live session`.
- **Two or more live sessions to one label.** Only the workstream bound to the
  first session with that label gets `!`, and the banner shows
  `! ambiguous: N live sessions, one label`.

The TUI never invents a winner: it does not blank, reorder, or suffix a row. A
live event for a session ID bound to several workstreams updates every one of
them. An unbound duplicate session is not bound or streamed; it is reported as
a warning instead. Resolution is future session adoption.

`!` is a condition, not a message: it persists until the next load stops
reporting the ambiguity.

### 3.2 Parent row

Shows, left to right:

- Selection marker.
- Expand chevron (`▸` collapsed, `▾` expanded) when the workstream has windows.
- Project name.
- Branch (flexible width; truncated with `…`).
- Aggregate agent badge (option A formatter) over all agents. Row markers
  (`!`, `‖`, `!‖`) sit at the start of the badge cell (see §10).
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

The header is one line, in this order:

1. `REXWS`.
2. Active filter chip: `filter: <query> ×`.
3. One health token (global, never filter-scoped, never truncated).
4. Counts chip: `<N> workstreams · <M> live · <K> agent-blocked`.

Counts are over the **visible** set when a filter is active, and over the full
set otherwise. The filter chip makes this clear.

- **workstreams:** workstreams in the counted set.
- **live:** workstreams with a live Rex session.
- **agent-blocked:** workstreams whose aggregate agent state is `blocked`.
  Workflow status is not included here; the group header carries it.

Health token precedence (first match wins):

| Condition | Token |
| --- | --- |
| Rex down after data was loaded | `Rex unavailable; paused (r)` |
| Rex down, never loaded | `Rex unavailable (r)` |
| Every live session stream down | `live paused (r)` |
| Some live session streams down | `N streams paused (r)` / `1 stream paused (r)` |

The health token reports stream health, not filter results, and it is never
abbreviated or cut. Conditions persist; they are not transient messages.

Counts are a **whole chip or absent**: full at 80+ columns, narrow (`ws`, no
agent-blocked) below 80, and omitted when they do not fit. When a health token
is present the filter query is capped at 12 cells at every width. Overflow is
sacrificed in this order: counts, then query width; never health.

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
  - Counts are whole or absent; never cut mid-chip.
  - Truncate the filter query to 12 cells; keep the chip.
  - A health token is always shown in full; it is never truncated.
  - Hide the window count on parent rows.
  - Hide the app name only when the label cell would fall below 12 cells; at
    supported widths it stays visible and right-aligns before the badge rail.
  - Limit the parent badge to 18 cells; limit the child badge to 18 cells.
  - Project max 10 cells; branch flex (21 cells at 60); window label at least 12.
- At 80+ columns:
  - Full header counts, still whole or absent.
  - The filter query is capped at 12 cells whenever a health token is present.
  - Parent badge max 28 cells; project max 12; branch flex.
  - Show window counts and app names.
  - The footer status suffix is right-aligned; rows keep the fixed 8-cell
    count rail.
- At 120+ columns:
  - The whole frame (header, separator, rows, banner, footer) caps at 120
    cells; above 120 it is centered and the extra columns are margins.
  - Project grows to 16 cells; the branch stays flex.
  - Header counts right-align at the content edge; every other line is the
    same 120-cell layout.
  - Markers occupy a dedicated two-cell cell before the shared badge rail.
- Overflow is sacrificed in the header in this order: counts, then query width;
  the health token is never sacrificed.
- Never wrap a row. Use `…` and `lipgloss.Width` on sanitized text.
- Footer priority: `Enter`, expand, filter, help. At 60–79 the four-key set is
  joined with single spaces; at 80+ it is joined with two spaces and adds
  `[←] back` before the optional keys. Add `s`/`n`/`d`/`r`/`q` only when they
  fit and only at 80+.
- When a filter is active, replace the `[/] filter` footer slot with
  `[Esc] clear filter` (`[Esc] clear` at 60–79).
- At 60–79 a crowded pending status (`saving status…`) keeps its label. If it
  would not fit, `[?] help` is dropped before the status, with or without an
  active filter.

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
| `c` | Open the cleanup screen: preview candidates, grouped by risk, with no mutation until confirmation |
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
- `j`/`k` and `↓`/`↑` wrap: forward from the last selectable row selects the
  first, and backward from the first selects the last. Wrapping crosses group
  headers without selecting them, so from the last row of the final group a
  forward move lands on the first row of the first group. With an existing
  selection and only one selectable row, the key is a no-op. `PgUp`/`PgDn` do
  not wrap; they stop at the edges.
- Right on a parent with no children is a no-op. Space on a child is a no-op.
- There is no `g`/`G`, `Home`, or `End` binding in v1.

Selection algorithm:

- Track the selected stable key: `Path` for a workstream, `window_id` for a
  window.
- After a refresh or filter change, select the same key if it is still visible.
- Otherwise clamp the previous row index to the new row count, then scan forward
  for the next selectable row; if none, scan backward. If no selectable row
  exists, there is no selection. This fallback does not wrap.
- `j`/`k` and `↓`/`↑` move one selectable row and wrap around the list. The
  scan is a ring over selectable rows only: group headers are never selected,
  and the starting row is never a wrap target, so an existing selection with
  fewer than two selectable rows does not move. With no selection, a forward
  move selects the first selectable row and a backward move selects the last.
  `PgUp`/`PgDn` move by a page and stop at the edges without wrapping.
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
| Cleanup list | `Space` toggle selection; `a` toggle all safe; `f` toggle force; `v` detail; `Enter` review the selection (or detail with no selection); `r` rescan; `Esc` back; `PgUp`/`PgDn` move |
| Cleanup detail | `j`/`k`, `↑`/`↓`, `PgUp`/`PgDn` move; `Space`/`a`/`f` as the list; `Enter` review; `v`/`Esc` close |
| Cleanup confirmation | `y` apply; `n` or `Esc` cancel. `Enter` is ignored: it opened the review from the list, so a held or double-tapped `Enter` can never apply before the modal paints |

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
- `d` preflights with force enabled, then shows a confirmation. Pre-checks:
  1. The main worktree cannot be removed.
  2. A dirty or detached worktree is allowed. The confirmation lists the exact
     Loss — every dirty, staged, untracked, or ignored path and every commit
     unique to a detached HEAD, or an assessment that could not be proven — so
     confirming the dialog is the consent for that loss. There is no separate
     force key. The dialog shows the same Loss, Retained, and Actions sections
     as the CLI.
  3. Rex must be available to close a live session; if not, explain and do not
     open the dialog.
  4. The project/provider must resolve.
  The TUI always retains the branch; branch deletion is CLI-only
  (`rexws rm --delete-branch`). The confirmation names the workstream and warns
  that its Rex session and windows will close. On success or failure, refresh
  and apply the cursor rule. This is intentionally different from plain
  `rexws rm`, which refuses a dirty or detached worktree unless `--force` is
  passed.
- `n` opens the new-worktree form: project, branch, provider (default from the
  project), and target-path preview. On submit, run the provider via
  `tea.ExecProcess`. After return, refresh, verify exactly one worktree exists
  for the branch, and select it by `Path`. Provider exit 0 with no worktree is an
  error. Cancel leaves the view unchanged.
- `r` refreshes Git and Rex state and retries event connections. It preserves
  query, expansion, and cursor by stable key.

### 7.4 Cleanup

`c` opens the cleanup screen. It previews every worktree and branch the CLI
cleanup would classify, grouped by risk. Nothing is mutated until the
confirmation step. Entry is refused with `Rex unavailable; press r to retry.`
exactly like switch and remove: applying requires Rex so a live session cannot
be missed. The plan is apply-capable (`Apply: true, Force: true`), so every
dirty or unmerged item already carries its loss and action; the TUI's own force
flag decides what may be selected and forwarded.

Groups, mapped from `ops.CleanupItem`:

- **Safe** — `Safe` is true: prunable records, merged branches, and
  patch-equivalent branches. Selectable without force.
- **Needs review** — `RequiresForce` is true (dirty worktree or unmerged
  branch). The group header says `force required (f)`; the row's `[-]` marker
  means the row is not currently selectable. Not selectable until `f` enables
  force; turning force off deselects them.
- **Blocked** — classification blocks removal (protected, locked, detached,
  active, unknown) or the item has no action. Never selectable, with or without
  force, and never forwarded to `ops.ApplyCleanup`.

Each row shows project/branch (or the worktree path), classification, and the
classification reason (`Detail`). The per-row detail (`v`, or `Enter` with no
selection) shows kind, action, safe/removable/requires-force, branch state, the
full reason, the exact loss for dirty/unmerged items, and any result or error.
The orphan section is part of the same report but is not fixed here; use
`rexws cleanup --fix` for stale `state.json` entries.

Keys: `j`/`k` and arrows move (group headers are skipped; does not wrap;
stops at the edges), `Space` toggles,
`a` toggles all safe, `f` toggles force, `v` opens detail, `Enter` opens review
when something is selected (otherwise detail), `PgUp`/`PgDn` page, `r` rescans
and discards the previous result, `Esc` returns to the dashboard.

The confirmation modal names the exact count and actions: worktrees removed,
branches deleted, prunable records pruned, items kept, retained branches (with
names), each selected force-required item and its loss, warnings/errors, and
the orphan count (`rexws cleanup --fix` handles orphans; this screen does not).
`y` applies the snapshotted selection; `n` or `Esc` cancels. `Enter` does not
apply: it is the key that opened the review from the list, so a held or
double-tapped `Enter` is ignored here. The snapshot is built when the modal
opens, so the applied report is exactly what was confirmed. A force selection
also gets a headline loss summary (`Force: on - 2 force item(s): 2 changed
path(s), 1 unmerged commit(s)`) that stays visible when the 60-column body
clips; the body ends with an explicit `… N more line(s) not shown` marker
instead of silently dropping lines. A refused apply (for example the Rex gate)
keeps the preview and selection and is reported as an error, never as
`Cleanup applied`.

After apply the screen stays open with per-item `removed`/`failed`/`kept`
results, the failed item's error in place of its reason, and the applied and
retained totals. The dashboard refreshes in the background and warnings reach
the banner. `r` rescans from scratch.

States: scanning shows `Scanning cleanup…`; `Esc` closes the screen while a scan
is in flight (a late result is discarded by scan token). An unreadable plan,
including Rex unavailable, shows `Cleanup unavailable` with the error and
`[r] retry  [Esc] back`; no candidates shows `No cleanup candidates.` Apply runs
in a `tea.Cmd`, so the UI stays responsive; while an apply is in flight every
key except `Ctrl+C` is ignored and the footer shows `applying…`. After apply the
detail screen keeps a loss count summary at the top and ends clipped content
with an explicit `… N more line(s) not shown` marker.

At 60 columns the preview status line puts selection and force before the group
counts (`sel 1 · force off · 2 safe · 1 review · 3 blocked`), so a long count
cannot truncate the safety controls; applied results put the result counts and
retained branches first. Rows use an 18-cell target and 11-cell classification.
The compact footer is `[sp] pick [f] force [r] rescan [Enter] review [Esc] back`
(`a` select-all-safe and `v` detail remain available and are listed above). At
80 the full footer is `[Space] pick [a] safe [f] force [v] detail [r] rescan
[Enter] review [Esc] back`, and the confirm footer is `[y] apply [n/Esc]
cancel`. There is no mouse, no wrapped row, no configurable keybinding, and no
scheduled cleanup.

### 7.5 Error banner

- Non-modal, one line above the footer. It never captures navigation keys.
- **Conditions persist** (Rex state, stream health, ambiguity, project
  discovery, an unreadable session). **Messages expire** (action errors, after
  8 seconds).
- Single precedence: newest unexpired transient error, then ambiguity, then
  project discovery, then session unreadable; within a class, newest first.
- Persistent warnings prefix `!`; transient action errors do not. Examples:
  `! project <name>: <reason>`, `! session "<label>": <reason>`, and the
  transient `switch: app client not connected`.
- `+N more` is always reserved before truncation and counts every other active
  notice.
- `x` dismisses **only the newest transient error**. It never dismisses a
  persistent warning. Persistent warnings clear when a later load stops
  reporting them.
- `?` lists every active persistent warning in full, wrapped, plus the row
  marker legend and the `x` binding.
- Stream connect/disconnect churn never enters the banner.
- Errors never crash the TUI.

## 8. Live updates

Prerequisite: the Rex event parser must expose window/view events (§2).

Streams:

- One server-wide `rex events --json` stream for session lifecycle.
- One per-session `rex events -s <session> --json` stream for program status,
  `session_view_changed`, and `active_window_changed`, for each live session.
- Start/stop per-session streams as sessions appear and disappear.
- Deliver events through a bounded, coalesced queue so a slow model never
  blocks the event scanner. Duplicate program-status or window events for the
  same target replace their queued predecessor.
- Preserve filter, expansion snapshot, and cursor by stable key across updates.

Stream health:

- Report a stream connected **only after its process actually started and
  stayed up through a short grace period**. A stream that fails immediately is
  never briefly healthy. A started process counts as an in-progress reconnect:
  it cancels a pending pause debounce but does not clear an already-visible
  pause.
- Failure retries back off 1s, 2s, 4s, and so on up to 30s. A successful connect
  resets the backoff to 1s, so the next failure retries in 1s.
- Track health per live session. The server-wide lifecycle stream does not
  decide `live paused`, and a healthy server-wide stream never sets it. It does
  act as an availability probe: a drop schedules one debounced availability
  load, and a reconnect marks Rex available and schedules one debounced reload
  to reconcile anything that happened while it was down. Probes are
  rate-limited (about one load per 5s), and a connect shortly after a healthy
  load does not cause a second load.
- A visible per-session pause is debounced by 2 seconds from the first drop:
  failing attempts re-arm the same absolute deadline rather than restarting it,
  so repeated retries cannot delay the marker. A real connect clears the pause
  immediately, and a retry attempt alone never clears it. A reconnecting
  process cancels a pending tick but keeps an already-visible pause.
- Each hub restart is a new stream generation: queued messages from the old
  generation are dropped, and health is re-learned from scratch.
- Stream connect/disconnect churn never adds a notice and never changes the
  notice count.

Header health (see §3.5) is the only global health display:

- Rex down after data was loaded: `Rex unavailable; paused (r)`.
- Rex down, never loaded: `Rex unavailable (r)`.
- Every live session stream down: `live paused (r)`.
- Some down: `N streams paused (r)` / `1 stream paused (r)`.

While any of these is shown, per-row `‖` markers are suppressed; `!` markers
stay.

Ambiguity is computed on every load from all live session labels against the
workstreams' `SessionName`s, covering both directions in §3.1. Workstreams keep
binding to the first session with their label; the UI only warns.

Handling:

- `program_status_changed`: update the matching agent in place in **every**
  workstream bound to that session; recompute the badges; no full re-list.
- `program_status_removed`: refresh that session's agents (all bound rows).
- `session_view_changed` / `active_window_changed`: refresh that session's
  windows (all bound rows: labels, counts, active marker).
- Session created/destroyed: refresh sessions and workstreams, update streams.
- Debounce window/session refreshes to about 200 ms per session.
- If the selected row disappears, apply the cursor rule.
- Do not reorder workstreams or sessions automatically.
- Auto-expand only after `Enter` creates a session for a stopped workstream.

If Rex becomes unavailable after data was loaded:

- Keep the last-known rows and badges; header shows `Rex unavailable; paused
  (r)`.
- Do not reinterpret existing live rows as `stopped`; do not show `‖`.
- Disable create/select (`Enter`) and remove (`d`), since they depend on Rex.
- `s` (state only) and `n` (worktree creation) remain available.
- `r` retries; when Rex returns, refresh and resume streams.

## 9. Errors and startup

- **Config or state load error:** full-screen error with the exact message,
  remediation (`fix config.json` / `state.json`; `rexws list`), `r` to retry, `q`
  to quit. Do not start the dashboard.
- **Per-project discovery error:** use `ops.Discover`'s per-project results. Skip
  the failed project, keep the rest usable, and show a persistent warning
  `! project <name>: <reason>`. `r` retries.
- **Skipped worktree:** `core.BuildWithWarnings` reports a worktree whose path
  cannot be canonicalized. Keep the readable worktrees and show a persistent
  warning `! project <name>: worktree <path> (<branch>) is unreadable and was
  skipped: <reason>`. `r` retries.
- **Rex unavailable at startup:** render workstreams with `?` agent badges and no
  windows; header token `Rex unavailable (r)`; disable create/select and remove;
  `r` retries.
- **No projects:** show the empty-state message.
- **Per-session unreadable:** use the existing `unknown` agent state and show a
  persistent warning `! session "<label>": <reason>`; do not fail the whole
  dashboard.
- Persistent warnings never expire. `?` lists them in full; `x` does not
  dismiss them.
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
- Row markers are text-only in a dedicated two-cell cell before the shared
  badge rail on the **parent workstream row**. Truncation keeps the cell whole.

  | Marker | Meaning |
  | --- | --- |
  | `!` | ambiguous session binding (§3.1) |
  | `‖` | live updates paused for this session |
  | `!‖` | both |

  Windows inherit the pause and are not individually marked. `‖` is suppressed
  while the header reports Rex down or every live stream paused; `!` always
  shows.
- The header has one global health token, never filter-scoped and never
  truncated, plus a counts chip that is whole or absent (§3.5).
- The banner is a single line; persistent warnings start with `!`.
- Selection: `>` plus bold/accent, not color alone.
- Expand state: `▸`/`▾`; context parent: `↳`; active window: `*` plus bold.
- Group header: `▌` static marker.
- Match highlight: bold + underline always; accent color when available. No
  color-only distinction. Limitation (pre-existing): the Ascii/`NO_COLOR`
  profile used by the tests strips bold and underline as well as color, so a
  match may not be visually distinct in that profile; the filter chip and
  context-row cues still identify the filtered view.
- Truncate with `…`; never wrap.
- `NO_COLOR` and monochrome terminals remain usable.
- The footer is always visible and mode-specific.
- Help is a bordered, scrollable panel (56/64/88 cells at 60/80/120) that fits
  at 18 rows. It lists active persistent warnings in full (wrapped), including
  each warning's detail lines (for example, the affected sessions or duplicated
  labels), then the marker legend, the `x` binding, and the key map. When the
  body is the full 15 rows at 60×18 (no banner), the first 13 content rows show
  the marker section and every key group; a warning banner reduces the body and
  the remaining rows scroll.
- Minimum size 60×18; smaller shows a centered `terminal too small` message.

## 11. Technical architecture

- Add `github.com/charmbracelet/bubbletea`, `lipgloss`, and `bubbles`
  (`textinput`, `viewport`). The help panel is rendered directly.
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
- [ ] The layout remains usable at 60×18 and never splits into panes. Above
  120 columns the frame caps at 120 cells, centers, and never overflows.
- [ ] `PgUp`/`PgDn` move the selection, and the cursor is always visible.
- [ ] If the selected row disappears, selection moves by the defined algorithm;
  with no selection, row actions are no-ops.
- [ ] A per-session disconnect shows `‖` on that row and `N streams paused (r)`
  in the header within 2s. A drop and reconnect under 2s produces no visible
  change. A healthy server-wide stream never sets `live paused`.
- [ ] A stream that fails every retry never flashes healthy; after a successful
  connect, the next failure retries in 1s.
- [ ] Rex loaded then unavailable shows `Rex unavailable; paused (r)`, keeps
  last-known badges, shows no `‖`, and refuses `Enter`/`d`. Rex that never
  loaded shows `Rex unavailable (r)` and `?` badges.
- [ ] When every live session stream is down the header shows `live paused (r)`
  and no `‖` markers remain.
- [ ] Two workstreams sharing one session get `!` and the persistent
  `! ambiguous: N workstreams, one live session` banner; a live event updates
  both rows.
- [ ] Two live sessions sharing one label get the second banner copy and `!`
  only on the bound row.
- [ ] With a transient error and two persistent warnings, the banner shows the
  transient with `+2 more`; `x` reveals the highest-priority warning and never
  dismisses a persistent one; `?` lists every persistent warning in full.
- [ ] Stream failures add no transient notice and do not change the notice
  count.
- [ ] At 60 columns the worst-case header (long health token plus a capped
  filter query) is at most 60 cells, never truncates health, and keeps counts
  whole or absent. At 80 columns a long query drops the counts chip before the
  query is truncated.
- [ ] Semantic tokens resolve to the documented ANSI-16, 256-color, or
  truecolor value for the active profile and light/dark background; ANSI-16
  selection uses reverse.
- [ ] Help is a bordered panel at 56/64/88 cells for 60/80/120 (and centered in
  the 120-cell block at 200). With the full 15-row body at 60×18 (no banner),
  its first 13 content rows include the marker legend and every key group; a
  warning banner reduces the body and the rest scrolls.
- [ ] Long banner messages at 60 columns always retain `+N more` and every line
  stays within 60 cells.
- [ ] The rendered screen is exactly the terminal height before and after a
  notice expires.
- [ ] Under ASCII/`NO_COLOR`, `!` and `‖` carry their meaning with no color or
  ANSI escapes. `REXWS_ASCII` (or a non-UTF-8 locale) selects the ASCII glyph
  set for every structural glyph, including expansion, groups, and rules.
- [ ] Control characters in labels, warnings, or errors cannot reach the
  banner, header, or help, and cannot inject markers.
- [ ] The cleanup screen groups candidates as safe, needs review, and blocked,
  shows each item's classification reason, and previews without mutating.
- [ ] No cleanup mutation happens before the confirmation step; only `y`
  applies, and a held or repeated `Enter` never does. The confirm names the
  exact item/action counts, retained branches, force-required loss, warnings,
  and orphans.
- [ ] Blocked items are never selectable; force-required items are selectable
  only after `f` enables force and are dropped when force is turned off.
- [ ] Applying reports per-item `removed`/`failed`/`kept`, retained branches,
  and warnings, then refreshes the dashboard without blocking input.
- [ ] The cleanup list, detail, and confirm layouts fit at 60×18 and 80×24.
- [ ] No configurable keybindings, no mouse, unchanged footer priority,
  unchanged 60×18 minimum, no wrapped rows.
- [ ] Unit tests cover visibility, sticky expansion, match spans, the key
  reducer, the cursor algorithm, sanitization, per-window badges, rendering at
  60/80/120/200 columns under fixed ASCII and truecolor profiles, the ANSI-16
  and 256-color token matrix for both backgrounds, the 120-cell cap and column
  grid, marker cells in both glyph sets, help completeness, middle truncation,
  the ASCII glyph fallback on every screen, stream health and backoff, ambiguity
  in both directions, and banner/notice precedence.

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
- Status colors/icons are implemented in #13; see `TUI-DESIGN.md` §8.2.
- Fuzzy search is deferred.
- Minimum size is 60×18.
- Ambiguity is warned in both directions; the first session with a label stays
  bound and no row is invented or reordered.
- One global health token leads the header, before counts and after the filter
  chip; health is never truncated.
- Row markers are text-only, inside the badge cell: `!`, `‖`, `!‖`.
- Conditions persist; messages expire. `x` dismisses only transient errors.
- Per-session stream pause is debounced 2s and cleared by a real connect.

## 14. Docs updates after sign-off

Completed with the Milestone 4 docs pass:

- `SPEC.md` §6 replaced the original flat TUI design with a concise summary of
  this hierarchy and a link here.
- `ROADMAP.md` moved Milestone 4 and window phase 2 to Shipped, checked the
  implemented items, and moved the deferred items to Future candidates.
- `README.md` documents the TUI and the `rexws` / `rexws tui` launch paths.
- This document is linked from `SPEC.md` and `ROADMAP.md`.

Completed with the SPEC drift pass (issue #14):

- `SPEC.md` §1–§7 now match shipped behavior: JSON-only config, configurable
  statuses with `rex_bin` and `rex_client`, the full template-variable and
  `Workstream` models, the JSON event-stream adapter, configurable status
  stages, the shipped TUI actions including the cleanup screen, and the current
  implementation state.
- `README.md` and `ROADMAP.md` reflect the same shipped surface. The remaining
  Docs candidates are `docs/rex-cli.md` and an automated docs-example check.
- No known behavior drift remains in `SPEC.md`.

## 15. Out of scope

- Mouse interactions.
- Kanban columns.
- Configurable keybindings.
- Window creation, rename, move, or close from rexws.
- Multiple sessions per workstream.
- Automatic Rex session reordering.
- Sticky group headers.
- Fuzzy ranking in v1.
- Per-status colors/icons beyond the optional `color`/`icon` fields on
  configured statuses (implemented in #13; see `TUI-DESIGN.md` §8.2).
- Agent message/title detail panes.
- `g`/`G`, `Home`/`End`.
