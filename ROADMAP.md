# rexws roadmap

Direction, not a commitment. `SPEC.md` describes the current design; this file
tracks what we might build next. Items should graduate to a milestone when a
concrete use case appears, not just because they are possible.

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

No milestone is committed yet. Candidates below graduate when a concrete use
case appears.

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
- [ ] Status icons/colors from the status config (text-only badges in v1).
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

- [ ] README: install, CLI reference, config examples, Rex integration.
- [ ] `docs/rex-cli.md`: verified Rex commands and JSON shapes used by rexws.
- [ ] Document the per-pane agent model and the status configuration.

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
