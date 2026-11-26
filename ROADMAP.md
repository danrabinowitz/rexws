# rexws roadmap

Direction, not a commitment. `SPEC.md` describes the current design; this file
tracks what we might build next. Items should graduate to a milestone when a
concrete use case appears, not just because they are possible.

## Shipped

- [x] Milestone 1 — config/state, Git worktree discovery, `project add`, `status`, `list`
- [x] Milestone 2 — Rex adapter: sessions, per-pane program status, event parsing, `switch`
- [x] Per-pane agent model and full `agents[]` in JSON
- [x] Pluggable agent-column formatter (option A is the default)
- [x] Configurable workflow statuses (IDs, labels, order, default)
- [x] Configurable Rex binary path and default Rex app client

## Next

### Milestone 3 — worktree providers

- [ ] Template-based providers (`git`, `worktrunk`, custom)
- [ ] `rexws new <project> <branch>`
- [ ] `rexws rm <path>`: close the Rex session, run provider `remove`, clean state
- [ ] Target-path rules and `worktree_root`
- [ ] Extra template variables if needed (`{{.Project}}`, `{{.Slug}}`, …)
- [ ] Provider timeouts and clear failure reporting

### Milestone 4 — TUI

- [ ] Grouped vertical list, live Rex updates, fuzzy filter, status switcher
- [ ] `tea.ExecProcess` handoff for interactive provider scripts
- [ ] Status colors/icons from the status config
- [ ] Agent formatter choice (A remains the default)
- [ ] Session 0 / dashboard hotkey in Rex's `init.lua` (documentation)

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

### Workflow

- [ ] Notes: set and clear from the CLI/TUI (`state.json` already stores them).
- [ ] Status icons/colors (with the TUI).
- [ ] Migration diagnostics/helper when a configured status ID is removed.

### CLI ergonomics

- [ ] `--project`, `--agent-state`, and `--sort` filters.
- [ ] `--config-dir` global flag (`REXWS_CONFIG_DIR` already exists).
- [ ] Richer JSON for `adopt`, `status`, and session operations.

### Environment and robustness

- [ ] Optional Rex timeout setting.
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
