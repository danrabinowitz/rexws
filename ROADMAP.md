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
- [x] M5 · TUI reliability — live-stream hub reconnect (issue #1) and CI on
      every push to `main` and on pull requests (issue #2). Removing stale
      worktrees and branches (issue #3) remains open under Next.
- [x] M6 · Contract seams — one selector grammar (issue #4) and `rexws here`
      (issue #5).
- [x] M7 · Worktree cleanup — frozen JSON contract and `rexws schema` (issue
      #6), preview-first candidate report (issue #7), safe remove and explicit
      branch deletion (issue #8), orphan triage (issue #9), and the TUI cleanup
      view (issue #10).
- [x] M8 · TUI design — design brief (issue #11), visual system and
      wide-terminal layout (issue #12), and configurable per-status color and
      icon (issue #13).
- [x] Docs — README covers the concepts, install, CLI, config, selector
      grammar, cleanup, TUI, and JSON surface; `docs/json-schema.md` documents
      the frozen contract; `SPEC.md` drift from shipped behavior is reconciled
      (issue #14).

## Next

Sequenced by dependency and risk, not by appeal. Every issue link repeats its
title so this file stands alone.

### M5 follow-up · Stale worktrees

The reliability work in M5 is shipped; this cleanup task remains.

- [#3 Cleanup: remove stale worktrees and branches](https://github.com/danrabinowitz/rexws/issues/3)

### Open follow-ups

- [#18 TUI: wrap list navigation at the first and last row](https://github.com/danrabinowitz/rexws/issues/18)
- [#19 TUI cleanup: test hardening and doc nits](https://github.com/danrabinowitz/rexws/issues/19)
- [#20 TUI: surface skipped-worktree warnings from core.Build](https://github.com/danrabinowitz/rexws/issues/20)
- [#21 git: parse worktree list with -z for newline paths](https://github.com/danrabinowitz/rexws/issues/21)

### Later

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
      README already covers the per-pane agent model, status configuration,
      selector grammar, cleanup, and TUI; the Rex adapter internals are the
      remaining reference gap.
- [ ] Automated validation that every documented config example parses and
      validates against the current binary.

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
