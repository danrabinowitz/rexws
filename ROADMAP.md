# rexws roadmap

Direction, not a commitment. `SPEC.md` describes the current design; this file
tracks what we might build next. Items should graduate to a milestone when a
concrete use case appears, not just because they are possible.

Actionable tasks live in GitHub issues and carry the acceptance criteria; this
file records direction and sequence. `IDEAS.md` is the unranked idea bank.

## Shipped

- [x] Milestone 1 — config/state, Git worktree discovery, `project add`, `status`, `list`
- [x] Milestone 2 — Rex adapter: sessions, per-pane program status, event parsing, `switch`
- [x] Per-pane agent model and full `agents[]` in JSON
- [x] Per-session window model and `switch --window` (window phase 1)
- [x] Pluggable agent-column formatter (option A is the default)
- [x] Configurable workflow statuses (IDs, labels, order, default)
- [x] Configurable Rex binary path and default Rex app client
- [x] Milestone 3 — template-based providers, `rexws new`, `rexws rm`
  - Worktrunk is supported as a provider template; `wt` was not installed here
    to verify its exact flags.
- [x] Milestone 4 — TUI: hierarchical workstream → window list inside
      workflow-status groups, live Rex updates, case-insensitive substring
      search, status switcher, and `tea.ExecProcess` handoff for `new`/`rm`.
      See `TUI-UX.md`.
- [x] Window phase 2 — TUI window list: active-window highlight, window
      selection, live refresh on `session_view_changed` /
      `active_window_changed`, and per-window agent badges.

## Next

Sequenced by dependency and risk, not by appeal. Every issue link repeats its
title so this file stands alone.

### M5 · TUI reliability

Finish the in-flight live-stream rework and get the repository to a clean base.

- [#1 TUI: finish live-stream reliability (stream hub reconnect)](https://github.com/danrabinowitz/rexws/issues/1)
- [#2 Infra: add CI and push main](https://github.com/danrabinowitz/rexws/issues/2)
- [#3 Cleanup: remove stale worktrees and branches](https://github.com/danrabinowitz/rexws/issues/3)

### M6 · Contract seams

The cheap contracts every later command and agent workflow builds on.

- [#4 S1 · Selector grammar: one resolver for every command](https://github.com/danrabinowitz/rexws/issues/4)
- [#5 S7 · Context resolution: `rexws here`](https://github.com/danrabinowitz/rexws/issues/5)

### M7 · Worktree cleanup

Preview-first cleanup of worktrees, branches, and orphans, plus the frozen JSON
contract once cleanup's fields exist.

- [#6 S2 · Freeze the JSON contract: `schema_version` and `rexws schema`](https://github.com/danrabinowitz/rexws/issues/6)
- [#7 C18 · Cleanup candidate report (preview-first)](https://github.com/danrabinowitz/rexws/issues/7)
- [#8 C15 · Safe remove and explicit branch deletion](https://github.com/danrabinowitz/rexws/issues/8)
- [#9 C20 · Orphan triage: unmatched sessions, stale state, unregistered worktrees](https://github.com/danrabinowitz/rexws/issues/9)
- [#10 TUI: cleanup view with preview and bulk apply](https://github.com/danrabinowitz/rexws/issues/10)

### M8 · TUI design

Designer-led visual system, wide-terminal layout, and configurable status
colors/icons.

- [#11 TUI design brief: visual system, wide layout, hierarchy](https://github.com/danrabinowitz/rexws/issues/11)
- [#12 TUI: implement the visual system and wide-terminal layout](https://github.com/danrabinowitz/rexws/issues/12)
- [#13 Status config: per-status color and icon](https://github.com/danrabinowitz/rexws/issues/13)

### Later

- [#14 Docs: fix `SPEC.md` drift from shipped behavior](https://github.com/danrabinowitz/rexws/issues/14)
- [#15 Release: screenshots, demo GIF, site, and video; settle the name](https://github.com/danrabinowitz/rexws/issues/15)

## Future candidates

### Sessions

- [ ] Adopt existing Rex sessions: store `{id, label}` per worktree in
      `state.json`, add `rexws adopt`, resolve adopted ID → unique label →
      recreate with the adopted label. Labels are not unique in Rex, so the ID
      matters while the session is live.
- [ ] Warn or error when a label matches multiple sessions instead of silently
      taking the first.
- [ ] Optional session label template, global or per project. Adoption covers
      exceptions; a template is for a consistent convention.
- [ ] Cwd-based session association fallback. More general than labels, but
      ambiguous when a session has multiple panes.

### Windows

- [ ] Focus a specific block after selecting a window (`rex focus`), if needed.

### TUI

- [ ] Fuzzy ranking for search (v1 ships case-insensitive substring matching).
- [ ] Agent formatter choice beyond the default option-A formatter.
- [ ] Session 0 / dashboard hotkey in Rex's `init.lua` (documentation).

### Workflow

- [ ] Notes: set and clear from the CLI/TUI (`state.json` already stores them).
- [ ] Migration diagnostics/helper when a configured status ID is removed.

### CLI ergonomics

- [ ] `--project`, `--agent-state`, and `--sort` filters.
- [ ] `--config-dir` global flag (`REXWS_CONFIG_DIR` already exists).
- [ ] `rexws project set <name>` to edit provider/worktree_root on an existing
      project. Today `project add` refuses conflicting settings and config.json
      must be edited by hand.
- [ ] Richer JSON for `adopt`, `status`, and session operations.

### Environment and robustness

- [ ] Optional Rex timeout setting.
- [ ] Provider command timeouts.
- [ ] Rex version/API-revision check with a clear error.
- [ ] Config/state schema version and migration notes.

### Docs

- [ ] `docs/rex-cli.md`: verified Rex commands and JSON shapes used by rexws.
- [ ] Document the per-pane agent model and the status configuration.
- [ ] `SPEC.md` drift reconciliation is tracked as
      [#14 Docs: fix `SPEC.md` drift from shipped behavior](https://github.com/danrabinowitz/rexws/issues/14).

## Not planned (for now)

- **Custom agent state vocabulary or precedence.** These come from Rex's
  OSC 7501 protocol, not rexws.
- **Configurable Rex command flags or event names.** They are adapter internals;
  prefer a Rex version check over user configuration.
- **Configurable TUI keybindings.** Revisit after the TUI is in real use.
- **Toggling bare/prunable worktree inclusion.** Niche and easy to get wrong.
- **Multiple sessions per workstream.** The model assumes one session per
  worktree.
- **Automatic Rex session reordering when an agent becomes ready.** `rex session
  move` can put a session at the top, and the transition data already exists via
  `program_status_changed`. But session order is server-wide and shared by every
  Rex client, so automatic moves can override manual ordering, affect other
  apps, and thrash as agents move between working and idle. It needs an opt-in,
  a debounce, a clear “ready” definition (blocked/error or a finished turn, not
  every idle), and a single owner. Prefer a rexws-local “needs attention” sort
  or group in the TUI first; only move Rex sessions if the Rex sidebar itself
  must reorder.
