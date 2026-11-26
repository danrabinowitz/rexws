# rexws TUI design brief — visual system, wide-terminal layout, hierarchy

**Status:** approved design for issues [#11](https://github.com/danrabinowitz/rexws/issues/11), [#12](https://github.com/danrabinowitz/rexws/issues/12), [#13](https://github.com/danrabinowitz/rexws/issues/13); #11 decisions confirmed 2026-11-26.
**Repository:** `/Users/djr/code/src/danrabinowitz/rexws` at `c4d2477`. Paths in citations are under that root.
**Baseline:** assumes the issue #1 reliability UX has landed: header health token, `!`/`‖`/`!‖` row markers, banner precedence, 60×18 minimum. That work is in flight and is not in `main` yet; where this brief depends on it, it says so.

## 0. Key decisions

- **Color is a role system, not a palette of hexes.** Every semantic has a non-color carrier; ANSI-16 is the compatibility floor, 256-color and truecolor are enhancements. `live` is deliberately uncolored; stale is muted plus `‖`.
- **Content width caps at 120 cells and centers above that.** The whole frame (header, separator, body, banner, footer) is composed at `W = min(terminalWidth, 120)`; excess columns are margins. Above 120 the layout is byte-for-byte the 120 layout. No split panes, no new navigable level, no new metadata columns.
- **A fixed column grid replaces ad-hoc flexing.** Fixed tree and marker cells; project 10/12/16 cells; branch flex; badge 18/28; a right-aligned 8-cell count rail at ≥80; the child app sits in a right-aligned flex cell so parent and child badges share one rail.
- **#12 ships built-in status colors by status ID; #13 makes them configurable.** `StatusDef` gains optional `color` and `icon`; invalid values warn and fall back, never fail the load; no config schema version is needed.
- **Document path:** `TUI-DESIGN.md` at the repository root; pointer text for `TUI-UX.md` is in §9.

---

## 1. Scope, baseline, and conflicts

### 1.1 In scope

Palette roles and mappings; wide-terminal width cap and alignment; hierarchy and density; empty/loading/fatal/outage/no-match/too-small copy; wireframes at 60/80/120/200; accessibility and glyph fallbacks; implementation checklists for #12 and #13.

### 1.2 Out of scope

Code changes; Kanban; mouse; configurable keybindings; agent message/title panes; new interactions; theme editor beyond per-status entries.

### 1.3 Issue #1 baseline assumed here

- Header health token, global and never truncated: `Rex unavailable (r)`, `Rex unavailable; paused (r)`, `live paused (r)`, `N streams paused (r)`.
- Marker cell before the badge: `!` ambiguous binding, `‖` updates paused, `!‖` both. Parent-only; children inherit.
- Banner precedence: newest transient error > ambiguity > project discovery > session unreadable; `+N more` reserved; `?` lists every persistent warning.
- Current main still has the old copy (`internal/tui/render.go:89-117`) and no per-session health; the in-flight issue #1 changes that. This brief draws the post-#1 tokens.

**Refinement to #1:** if #1 ships the marker as a badge prefix, #12 moves it into a dedicated 2-cell marker cell (§3.2). The visual is identical (`branch  ‖ blocked`), but the marker no longer consumes badge content. This is a layout refinement, not a behavior change.

### 1.4 Conflicts with `TUI-UX.md` (explicit, not silent)

| TUI-UX rule | This brief | Why |
|---|---|---|
| §1/§13/§15: “Per-status colors/icons … deferred” (`TUI-UX.md:32,690,718`) | #13 schedules them; §8.2 defines the schema | The design system now exists; deferral is obsolete |
| §4.3: at 80+, project max 12, badge max 28, branch flex (`TUI-UX.md:220-223`) | Adds a ≥120 tier: project 16, marker cell, badge/count rail, branch flex; 60–79 gets a single-space footer | Wide terminals need a rail and an explicit cap |
| §4.1/§4.3: full-width separator and rows | Separator and all frame lines are capped at 120 and centered above 120 | A 200-column rule under a 200-column header was the sparseness defect |
| §3.2: count after badge (`TUI-UX.md:86-106`) | Order unchanged; the count moves to a right-aligned rail at ≥80 | Alignment, not data |
| §4.3: hide app at 60–79 before truncating labels (`TUI-UX.md:217`) | App is shown when the label cell remains ≥12; hidden otherwise | Same rule, applied to the new grid |
| §10: text-only agent badges (`TUI-UX.md:587-589`) | Retained; color is additive, never the carrier | No change |
| §3.4: `▌ LABEL (count)` (`TUI-UX.md:127-136`) | Adds optional status icon and status-colored `▌`; label text remains | #13 extension |

Behavior, keybindings, search, row identity, and scrolling rules are unchanged.

---

## 2. Visual system

### 2.1 Principles

1. **Terminal-native.** Never paint a full-screen background. Use the terminal's default foreground and background; set colors only on text and the selection bar.
2. **Restraint.** Bright/saturated color marks focus, active state, warning/error, and agent urgency. Structural chrome is default or muted.
3. **Never color alone.** Every role has a glyph, word, weight, or position redundant with its color.
4. **Light/dark adaptive.** Use `lipgloss.AdaptiveColor`; resolve with the detected background. ANSI-16 is the floor, 256/truecolor are enhancements.
5. **No new hue for status.** Status colors reuse the semantic palette; a custom status gets a deterministic fallback from a reserved four-hue set, never the blocked-red or ready-green.
6. **Live is not green.** Green means done/ready; “live” is a count word and a health token, not a color. Stale is muted plus `‖`.

### 2.2 Semantic tokens

Mappings are light / dark. ANSI-16 named pairs are adaptive: `blue` means ANSI 4 on light and 12 on dark, not a raw fixed index. The truecolor pairs are the current accent/notice hues or close derivatives.

| Token | Role | ANSI-16 | 256 | Truecolor | Non-color carrier |
|---|---|---|---|---|---|
| `text` | body, project, branch, labels | default | default | default | — |
| `muted` | metadata: counts, app, group count, `+N more` | 8 / 8 | 240 / 245 | `#475569` / `#94A3B8` | position (right rail, parentheses) |
| `faint` | context parent only | SGR 2 | SGR 2 | SGR 2 | `↳` + dimmed row |
| `accent` | brand, focus, active, in-progress | 4 / 12 | 25 / 111 | `#1D4ED8` / `#7AA2F7` | bold title, `>`, `*` |
| `selection.fg` | selected row text | default | default | default | `>` + bold |
| `selection.bg` | selected row bar | reverse | 254 / 24 | `#DCE6FF` / `#2A3F5F` | `>` + bold |
| `match` | search match | 4 / 12 + bold + underline | 25 / 111 | `#1D4ED8` / `#7AA2F7` | underline + bold |
| `info` | pending/refreshing | 6 / 14 | 37 / 81 | `#0E7490` / `#22D3EE` | `refreshing…` text |
| `warning` | persistent warning, health paused, ambiguity marker | 3 / 11 | 130 / 216 | `#9A3412` / `#FDBA74` | `!` prefix |
| `error` | transient action failure, Rex down, blocked | 1 / 9 | 160 / 203 | `#B91C1C` / `#F87171` | wording (`switch: …`), badge text |
| `danger` | destructive confirmation | 1 / 9 + bold | 124 / 217 | `#991B1B` / `#FCA5A5` | question + explicit verbs |
| `status.in_progress` | group accent | accent | accent | accent | label |
| `status.blocked` | group accent | error | error | error | label |
| `status.in_review` | group accent | 5 / 13 | 61 / 141 | `#6D28D9` / `#C084FC` | label |
| `status.ready` | group accent | 2 / 10 | 28 / 114 | `#15803D` / `#4ADE80` | label |
| `status.done` | group accent | muted | muted | muted | label |
| `agent.blocked` | badge | error | error | error | text `blocked (detail)` |
| `agent.error` | badge | 5 / 13 | 125 / 211 | `#BE123C` / `#FB7185` | text `error` |
| `agent.working` | badge | info | info | info | text `working` |
| `agent.done` | badge | ready | ready | ready | text `done` |
| `agent.idle` | badge | text | text | text | text `idle` |
| `agent.stopped` | badge | muted | muted | muted | text `stopped` |
| `agent.unknown` | badge | 3 / 11 | 136 / 179 | `#B45309` / `#FBBF24` | text `unknown`, `?` |
| `live` | live count | text | text | text | word `live` |
| `stale` | stale data | muted | muted | muted | `‖` + health token |
| `marker.ambiguous` | `!` marker | warning | warning | warning | `!` |
| `marker.paused` | `‖` marker | muted | muted | muted | `‖` |

The current renderer has only `accentColor`, `selectBG`, `matchStyle`, `dimStyle`, `groupStyle`, `titleStyle`, `noticeStyle` (`internal/tui/render.go:18-27`). #12 replaces these with theme lookups.

### 2.3 Status map

Defaults for the five built-in IDs; custom IDs get `auto`:

- `in_progress` → accent blue. `blocked` → error red. `in_review` → purple. `ready` → green. `done` → muted.
- `auto` for a custom ID: deterministic pick from `[accent, in_review purple, info cyan, agent.unknown amber]` by FNV hash of the ID. Never blocked-red or ready-green.
- In #12, IDs outside the five built-ins render muted; the deterministic hash resolver above is #13’s extension.
- Group header: `▌` in the status color; label bold `text`; count `muted`; optional icon in the status color before the label.
- The parent row never repeats the status (`TUI-UX.md:100-102`). The status selector shows the same icon/color beside each label.
- Color is redundant: the label is always present, and the group header’s `▌` is a static shape.

Icons default to empty (decision, see §10). An icon is one grapheme, 1–2 cells, sanitized; invalid → omitted with a warning.

### 2.4 Agent map and badge coloring

Severity order is `blocked > error > working > unknown > done > idle > stopped` (`internal/agentstate/agentstate.go:20-37`). The badge is a text string from `render.Default.Format` (`internal/render/agent.go`), or `?` when Rex has never loaded (`internal/tui/state.go:109-113`, `internal/tui/filter.go:129-132`).

- Color the **whole badge** by its highest-severity state. The text still lists every state (`blocked (permission) ×1, working ×1`); per-token coloring is noise and complexity.
- Window badges use the window’s highest-severity agent; `—` stays `muted`.
- `stopped` and `idle` are not urgent; `stopped` is muted, `idle` is base text.
- On a selected row, all fg switches to `selection.fg`; the background, `>`, and bold carry selection. Semantic hue is suppressed there for contrast.

### 2.5 Notices, danger, live/stale

- Persistent warning: `warning` + `!` prefix. Transient error: `error`, no prefix. The `+N more` suffix is `muted`.
- Danger: `danger` + bold on the remove question/title; the footer keeps base colors; explicit `[y/Enter] confirm` wording.
- Health token: `error` for Rex unavailable; `warning` for paused/partial. The marker cell: `!` warning, `‖` muted; both take `!` (warning) for the cell.
- `live` has no color. `stale` is muted plus `‖`; the header count remains the global truth.
- Selection: `selection.bg` + bold + `>`; never accent text on selection. Match: bold + underline + accent (or selection fg on a selected row).
- `faint` only for context parents; never for essential data, because Faint can be invisible on some terminals.

### 2.6 Contrast and color blindness

Assumed backgrounds for truecolor: `#FFFFFF` (light) and `#1E1E1E` (dark). Computed WCAG ratios:

| Pair | Ratio | Pair | Ratio |
|---|---|---|---|
| accent light | 6.70:1 | accent dark | 6.62:1 |
| muted light | 7.58:1 | muted dark | 6.50:1 |
| warning light | 7.31:1 | warning dark | 9.89:1 |
| error light | 6.47:1 | error dark | 6.03:1 |
| in_review light | 7.10:1 | in_review dark | 6.31:1 |
| ready light | 5.02:1 | ready dark | 9.57:1 |
| info light | 5.36:1 | info dark | 9.22:1 |
| agent.error light | 6.29:1 | agent.error dark | 6.19:1 |
| agent.unknown light | 5.02:1 | agent.unknown dark | 9.99:1 |
| danger light | 8.31:1 | danger dark | 8.78:1 |
| black on selection light | 16.81:1 | white on selection dark | 10.63:1 |

Targets: normal text ≥4.5:1; bold/large ≥3:1; non-text boundaries ≥3:1. ANSI-16/256 depend on the user’s theme; the text/glyph carriers are mandatory there. `AdaptiveColor` chooses light/dark using the terminal background; tests pin the profile.

Color-blind rules:
- Red/green is never the only difference: blocked/error vs ready/done have different words and group labels.
- Orange/red: persistent `!` and wording vs transient action errors.
- Blue/cyan: `in_progress`/accent vs `working`/info appear in different cells (group vs badge) and with text.
- The palette avoids a red/green pair for related controls; the `▌` hue is decorative.

### 2.7 NO_COLOR and monochrome

`termenv.Ascii` (already used by `internal/tui/render_test.go:17-20`) removes all SGR. The design remains unambiguous because:
- selection: `>` + bold + background; in monochrome, `>` + bold.
- active window: `*` + bold.
- expansion: `▸`/`▾`; context: `↳`.
- groups: `▌` + uppercase; children: indent + `•`.
- notices: `!` prefix for persistent; transient wording.
- reliability: `!`, `‖`, `!‖`; Rex unknown `?`.
- matches: underline (plus bold); never color-only.

Monochrome terminals without bold/underline still have all the glyphs, words, and indentation.

---

## 3. Wide-terminal behavior

### 3.1 Content width and alignment

- `W = min(terminalWidth, 120)`.
- Above 120, center the entire frame: left margin = `(terminalWidth - W) / 2`; the right margin absorbs the remainder. The header, separator, every row, the banner, and the footer are all composed at `W`, so margins are symmetric and the frame is one block.
- At ≤120, the frame uses the full width with its existing 1-cell leading gutter.
- The minimum-size check uses the real terminal: `< 60×18` → centered “terminal too small” (`internal/tui/render.go:38-40`).
- Rationale for centering: a left-aligned 120 block on a 300-column terminal reads as a rendering bug; centering makes the cap intentional. The cost is a shifting left edge when resizing across 120; acceptable for a dashboard. If the team prefers a fixed left edge, use a 2-cell left gutter and leave the right margin empty; the grid is unchanged.

### 3.2 Column grid

All cells are fixed or flex per the table; every row is padded to `W` and never wraps.

| Column | 60–79 | 80–119 | 120+ | Notes |
|---|---|---|---|---|
| left gutter | 1 | 1 | 1 | leading space |
| selection | 2 | 2 | 2 | `> ` / `  ` |
| tree | 2 | 2 | 2 | `▸ `, `▾ `, `↳ `, or `  `; always reserved, so stopped rows align |
| project | 10 | 12 | 16 | truncated with `…` |
| gap | 2 | 2 | 2 | |
| branch | flex | flex | flex | `B = W - 11 - P - R - C`, minus the 2-cell count gap when present; fills remaining (21 at 60, 19 at 80, 55 at 120) |
| gap | 1 | 1 | 1 | |
| marker | 2 | 2 | 2 | `  `, `! `, `‖ `, `!‖` |
| gap | 1 | 1 | 1 | |
| badge | 18 | 28 | 28 | parent and child share this rail |
| gap | 0 | 2 | 2 | |
| count | — | 8 | 8 | right-aligned; blank rail on rows without a count |
| header counts | follows chips | follows chips | right-aligned at `W` | |
| footer status | appended | right-aligned | right-aligned | `refreshing…`, `switching…` |

Child rows share the same trailing rail: label cell `L = max(12, min(longest label, 24))`; app cell flex, **right-aligned**, then blank marker (2), badge (18/28), blank count (0/8). This aligns every badge with the parent badge and clusters app+badge at the right as a status readout.

At 60–79 the footer uses single spaces and short labels (`[Esc] clear`); at 80+ it uses two spaces and full labels. Optional keys are included only as whole items, never truncated (`internal/tui/render.go:535-542`).

### 3.3 How extra columns are used (no split panes, no new level)

Above 120, extra columns are **only margins**. Between 80 and 120, the additions are:
1. branch grows (parent identity),
2. the child app cell grows (app right-aligned before the badge rail),
3. the header counts right-align at ≥120,
4. the footer status suffix right-aligns at ≥80.

No second pane, no new column of data, no new navigable level. Rows never stretch past 120. Long branches truncate at the branch cell; the cursor and actions still use the canonical path.

At 200, the block is 120 wide with 40-cell margins. Draw the normal dashboard once in full; every other state is the same 120 block (see §6 convention).

---

## 4. Hierarchy and density

### 4.1 Vertical rhythm

- Frame: header (1), separator (1), body (viewport), optional one-line banner, footer (1). No extra blank lines between groups; group headers are the hard breaks.
- One-cell leading gutter; the separator is a single `─` rule at `W`.
- Selection bar spans the full content width, including the rails.
- At 18 rows, body = 15 (no banner) or 14 (banner). Density does not drop at wide widths; wide mode changes columns, not line rhythm.
- Group headers scroll with the content; no sticky headers (`TUI-UX.md:244-250`).

### 4.2 Group headers

` ▌ LABEL (count)`:
- `▌` = status color; optional icon before the label, also status-colored.
- Label uppercase, bold, `text`; count `muted`.
- Counts: `(N)` normally; `(V/N)` under a filter; groups with no visible workstreams are hidden (`TUI-UX.md:127-136`).
- Not selectable, not collapsible; `▌` is a static mark, not a control.

### 4.3 Parent rows

Order: selection, tree, project, branch, marker, badge, count.
- Project `text`, truncated at 10/12/16; branch `text`, flex, truncated with `…`.
- Marker cell carries `!`/`‖`/`!‖`; otherwise blank.
- Badge: text-only, aggregate color, truncated at 18/28.
- Count: `2 tabs` / `1 tab` / `1/2 tabs`, right-aligned in the 8-cell rail; hidden below 80.
- Stopped workstreams have no chevron but keep the 2-cell tree cell, so badges align.
- Status is not repeated.

### 4.4 Child rows

Indent 2, bullet `•`, active `*` + bold label, label cell `max(12, min(longest, 24))`, app right-aligned in its flex cell (muted), blank marker, shared badge rail, blank count rail.
- App is hidden only when the label cell would fall below 12 (`TUI-UX.md:217`).
- A `—` app or badge is `muted`.
- Duplicate window labels keep the last-six-ID suffix (`internal/tui/filter.go:207-219`).

### 4.5 Density by width

| Width | Content | Project | Branch | Badge | Count | App | Footer |
|---|---|---|---|---|---|---|---|
| 60–79 | 60–79 | 10 | flex (21) | 18 | hidden | shown (label ≥12) | single space; `[Esc] clear`; 4 priority keys |
| 80–119 | 80–119 | 12 | flex (19) | 28 | 8 rail | shown | two spaces; optional keys |
| 120 | 120 | 16 | flex (55) | 28 | 8 rail | right-aligned flex | counts right; status right |
| 200 | 120 centered | 16 | 55 | 28 | 8 rail | right-aligned flex | identical to 120; 40-cell margins |

No per-status icons are required for hierarchy; the group label is the hierarchy. Icons are an optional accent.

---

## 5. States and copy

Exact copy; `…` marks truncation. Comments in brackets are not rendered.

### 5.1 Empty

- ≥80, one line: `No workstreams. Register one with: rexws project add <path>`
- 60–79, two lines: `No workstreams.` / `Register one: rexws project add <path>`
- Suppress the zero counts chip (`[0 workstreams · 0 live]`) when the total is 0; the message is the content.
- Current copy at `internal/tui/render.go:151-153` is 66 cells and truncates at 60; this fixes it.

### 5.2 Loading

- Startup body: `Loading…` (base), plus at ≥80 a muted second line `Reading Git worktrees and Rex sessions`. At 60, one line.
- No spinner (avoids tick/test churn); the footer remains the normal key bar.
- The loading state must not claim Rex is unavailable (`internal/tui/render_test.go:44-55`).

### 5.3 Refresh and pending actions

- Refresh keeps every row and badge; footer right: `refreshing…` (`info`). No layout shift, no blanking.
- Pending action labels already exist (`internal/tui/model.go:146-167`): `switching…`, `saving status…`, `checking…`, `removing…`, `preparing…`, `creating…`, `verifying…`. Render them `info` + bold in the footer’s right rail; navigation and filtering remain available; mutating keys are ignored (`TUI-UX.md:467-477`).
- No modal spinner; the footer is the single-flight indicator.

### 5.4 Fatal config/state

Full-screen, composed at `W` (max 100 for prose), left-aligned:
```
 REXWS

 Cannot load config.json            [or state.json; use fatalKind]

 <exact error, sanitized and wrapped>

 Fix ~/.config/rexws/config.json, then press r to retry.
 rexws list shows the same error outside the TUI.

 [r] retry  [q] quit
```
- Use `fatalKind` (`internal/tui/model.go:204,208,240-242`); the current renderer has one combined remedy (`internal/tui/render.go:67-87`) and truncates the message.
- Wrapping is allowed here: this is prose, not a row.
- Title `REXWS` bold; heading `danger`; remedy `text`; hint `muted`.

### 5.5 Rex unavailable (post-#1 baseline)

- Never loaded: header `Rex unavailable (r)`; badges `?`; no windows; Enter/d refuse with `Rex unavailable; press r to retry.` (`internal/tui/reduce.go:367-369,424-425`).
- Loaded then lost: header `Rex unavailable; paused (r)`; keep last-known badges; no `‖` row markers; Enter/d refuse.
- The current long copy (`internal/tui/render.go:100-106`) is replaced by the post-#1 tokens; never render live workstreams as stopped (`internal/tui/render_test.go:193-205` must keep passing).

### 5.6 Filtering with no matches

- ≥80: `No matches for "agent". Press Esc to clear.`
- 60–79: `No matches for "agent".` / `Press Esc to clear.`
- Quote the query with `%q`; sanitize it; keep the search input focused and editable (`TUI-UX.md:455-463`).

### 5.7 Too small

Centered, no colors required:
```
terminal too small
need 60x18, have 42x10
```
(second line muted). Current copy is only `terminal too small` (`internal/tui/render.go:38-40`).

### 5.8 Notices and stacking

Post-#1 precedence (transient > ambiguity > project > session unreadable), `+N more` always reserved, persistent `!`, `?` lists all persistent warnings. The banner is one line; conditions (health, markers) live outside it. The current banner is newest-only and appends `+N more` before truncation (`internal/tui/render.go:119-135`); the design reserves the suffix. Color: persistent `warning`, transient `error`, suffix `muted`.

---

## 6. Wireframes

Conventions:
- Frames show the content rows and omit blank viewport padding; every line is ≤ the stated width. Frames are ASCII-art with the real glyphs; bold, underline, dim, and color are not visible (they are noted below each state).
- At 120 columns the content is 120; at 200, the entire 120 block is centered with 40 cells of margin on each side. The normal dashboard is drawn at 200; the other 200 states are the same 120 block (construction: prefix every 120 line with 40 spaces, add 40 trailing cells). Above 120, no column changes.
- Matching substrings are bold + underlined (e.g., `agent` in the filtered frame).
- Post-#1 markers are shown as `!`/`‖`; the marker cell is 2 cells, so `!` has a space after it before the badge.
- Sample data: 4 workstreams, 3 live, 2 agent-blocked.

### 6.1 Normal dashboard

**60 columns**
```text
 REXWS  [4 ws · 3 live]
────────────────────────────────────────────────────────────
 ▌ IN PROGRESS (2)
 > ▾ rexws       m1-m2-core-rex         blocked (permissi…
     • *agent            claude-code    working
     • tests                       —    blocked (permissi…
   ▸ do-si-do    refactor-sync          blocked (permissi…
 ▌ BLOCKED (1)
   ▸ monorepo    sparse-api-v2          idle
 ▌ READY (1)
     .ai         main                   stopped
 [Enter] switch [→] expand [←] back [/] filter [?] help
```

**80 columns**
```text
 REXWS  [4 workstreams · 3 live · 2 agent-blocked]
────────────────────────────────────────────────────────────────────────────────
 ▌ IN PROGRESS (2)
 > ▾ rexws         m1-m2-core-rex         blocked (permission) ×1, wo…    2 tabs
     • *agent              claude-code    working
     • tests                         —    blocked (permission)
   ▸ do-si-do      refactor-sync          blocked (permission)             1 tab
 ▌ BLOCKED (1)
   ▸ monorepo      sparse-api-v2          idle                            3 tabs
 ▌ READY (1)
     .ai           main                   stopped
 [Enter] switch  [→] expand  [←] back  [/] filter  [?] help  [s] status  [n] new
```

**120 columns**
```text
 REXWS                                                                        [4 workstreams · 3 live · 2 agent-blocked]
────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
 ▌ IN PROGRESS (2)
 > ▾ rexws             m1-m2-core-rex                                             blocked (permission) ×1, wo…    2 tabs
     • *agent                                                      claude-code    working
     • tests                                                                 —    blocked (permission)
   ▸ do-si-do          refactor-sync                                              blocked (permission)             1 tab
 ▌ BLOCKED (1)
   ▸ monorepo          sparse-api-v2                                              idle                            3 tabs
 ▌ READY (1)
     .ai               main                                                       stopped
 [Enter] switch  [→] expand  [←] back  [/] filter  [?] help  [s] status  [n] new
```

**200 columns** (40-cell margin, then the 120 block)
```text
                                         REXWS                                                                        [4 workstreams · 3 live · 2 agent-blocked]
                                        ────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
                                         ▌ IN PROGRESS (2)
                                         > ▾ rexws             m1-m2-core-rex                                             blocked (permission) ×1, wo…    2 tabs
                                             • *agent                                                      claude-code    working
                                             • tests                                                                 —    blocked (permission)
                                           ▸ do-si-do          refactor-sync                                              blocked (permission)             1 tab
                                         ▌ BLOCKED (1)
                                           ▸ monorepo          sparse-api-v2                                              idle                            3 tabs
                                         ▌ READY (1)
                                             .ai               main                                                       stopped
                                         [Enter] switch  [→] expand  [←] back  [/] filter  [?] help  [s] status  [n] new
```

Styling: group `▌` in the status color (in-progress blue, blocked red, ready green); group label bold; counts muted; selected row `>` + bold + selection bar; active `*agent` bold; badges colored by aggregate (blocked red, idle base, stopped muted); active/in-progress blue; branch/project base.

### 6.2 Filtered (query `agent`)

The context parent is dim + `↳`; the matching `agent` is bold + underlined; counts are visible counts.

**60 columns**
```text
 REXWS  filter: agent ×  [1 ws · 1 live]
────────────────────────────────────────────────────────────
 ▌ IN PROGRESS (1/2)
 > ↳ rexws       m1-m2-core-rex         blocked (permissi…
     • *agent            claude-code    working
 [Enter] switch [→] expand [←] back [Esc] clear [?] help
```

**80 columns**
```text
 REXWS  filter: agent ×  [1 workstream · 1 live · 1 agent-blocked]
────────────────────────────────────────────────────────────────────────────────
 ▌ IN PROGRESS (1/2)
 > ↳ rexws         m1-m2-core-rex         blocked (permission) ×1, wo…  1/2 tabs
     • *agent              claude-code    working
 [Enter] switch  [→] expand  [←] back  [Esc] clear filter  [?] help  [s] status
```

**120 columns**
```text
 REXWS  filter: agent ×                                                        [1 workstream · 1 live · 1 agent-blocked]
────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
 ▌ IN PROGRESS (1/2)
 > ↳ rexws             m1-m2-core-rex                                             blocked (permission) ×1, wo…  1/2 tabs
     • *agent                                                      claude-code    working
 [Enter] switch  [→] expand  [←] back  [Esc] clear filter  [?] help  [s] status  [n] new
```

At 200: the 120 block with 40-cell margins. `↳` and the faint row remain the non-color context cue; the match underline is additive.

### 6.3 Per-session stream paused (one of three live sessions)

**60 columns**
```text
 REXWS  1 stream paused (r)  [4 ws · 3 live]
────────────────────────────────────────────────────────────
 ▌ IN PROGRESS (2)
 > ▾ rexws       m1-m2-core-rex      ‖  blocked (permissi…
     • *agent            claude-code    working
     • tests                       —    blocked (permissi…
   ▸ do-si-do    refactor-sync          blocked (permissi…
 ▌ BLOCKED (1)
   ▸ monorepo    sparse-api-v2          idle
 ▌ READY (1)
     .ai         main                   stopped
 [Enter] switch [→] expand [←] back [/] filter [?] help
```

**80 columns**
```text
 REXWS  1 stream paused (r)  [4 workstreams · 3 live · 2 agent-blocked]
────────────────────────────────────────────────────────────────────────────────
 ▌ IN PROGRESS (2)
 > ▾ rexws         m1-m2-core-rex      ‖  blocked (permission) ×1, wo…    2 tabs
     • *agent              claude-code    working
     • tests                         —    blocked (permission)
   ▸ do-si-do      refactor-sync          blocked (permission)             1 tab
 ▌ BLOCKED (1)
   ▸ monorepo      sparse-api-v2          idle                            3 tabs
 ▌ READY (1)
     .ai           main                   stopped
 [Enter] switch  [→] expand  [←] back  [/] filter  [?] help  [s] status  [n] new
```

**120 columns**
```text
 REXWS  1 stream paused (r)                                                   [4 workstreams · 3 live · 2 agent-blocked]
────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
 ▌ IN PROGRESS (2)
 > ▾ rexws             m1-m2-core-rex                                          ‖  blocked (permission) ×1, wo…    2 tabs
     • *agent                                                      claude-code    working
     • tests                                                                 —    blocked (permission)
   ▸ do-si-do          refactor-sync                                              blocked (permission)             1 tab
 ▌ BLOCKED (1)
   ▸ monorepo          sparse-api-v2                                              idle                            3 tabs
 ▌ READY (1)
     .ai               main                                                       stopped
 [Enter] switch  [→] expand  [←] back  [/] filter  [?] help  [s] status  [n] new
```

At 200: same block with margins. `‖` is `marker.paused` (muted); the header health token is `warning`; children inherit the pause but are not individually marked (post-#1 rule). If #1 ships the marker inside the badge, #12 moves it to the marker cell without changing the visual.

### 6.4 Ambiguous live session

Two workstreams share one live session; both rows carry `!`; no suffix, reorder, or blanking invents a winner.

**60 columns**
```text
 REXWS  [4 ws · 3 live]
────────────────────────────────────────────────────────────
 ▌ IN PROGRESS (2)
 > ▸ rexws       m1-m2-core-rex      !  blocked (permissi…
   ▸ rexws       m1-m2-core-rex      !  blocked (permissi…
 ▌ BLOCKED (1)
   ▸ monorepo    sparse-api-v2          idle
 ▌ READY (1)
     .ai         main                   stopped
 ! ambiguous: 2 workstreams, one live session
 [Enter] switch [→] expand [←] back [/] filter [?] help
```

**80 columns**
```text
 REXWS  [4 workstreams · 3 live · 2 agent-blocked]
────────────────────────────────────────────────────────────────────────────────
 ▌ IN PROGRESS (2)
 > ▸ rexws         m1-m2-core-rex      !  blocked (permission)             1 tab
   ▸ rexws         m1-m2-core-rex      !  blocked (permission)             1 tab
 ▌ BLOCKED (1)
   ▸ monorepo      sparse-api-v2          idle                            3 tabs
 ▌ READY (1)
     .ai           main                   stopped
 ! ambiguous: 2 workstreams, one live session
 [Enter] switch  [→] expand  [←] back  [/] filter  [?] help  [s] status  [n] new
```

**120 columns**
```text
 REXWS                                                                        [4 workstreams · 3 live · 2 agent-blocked]
────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
 ▌ IN PROGRESS (2)
 > ▸ rexws             m1-m2-core-rex                                          !  blocked (permission)             1 tab
   ▸ rexws             m1-m2-core-rex                                          !  blocked (permission)             1 tab
 ▌ BLOCKED (1)
   ▸ monorepo          sparse-api-v2                                              idle                            3 tabs
 ▌ READY (1)
     .ai               main                                                       stopped
 ! ambiguous: 2 workstreams, one live session
 [Enter] switch  [→] expand  [←] back  [/] filter  [?] help  [s] status  [n] new
```

At 200: same block with margins. Banner `warning` + `!`; row marker `!`. For two live sessions sharing one label, the banner copy is `! ambiguous: 2 live sessions, one label` and only the bound workstream carries `!`.

### 6.5 Help

The help is a bordered, scrollable panel: body-height box, 13 visible content rows at 18×18; more content scrolls. The header and footer remain. At 60/80/120 the box is 56/64/88 cells wide, centered in the content block.

Implemented order follows `TUI-UX.md` §10: warnings, then the marker legend and `x` binding, then the key map. The sample frames below list Keys before Markers for compactness; the content and panel geometry are the same.

**60 columns**
```text
  ╭─ Help ───────────────────────────────────────────╮
  │ Keys                                             │
  │  j/k, ↓/↑     move selection                     │
  │  →/l          expand / first child               │
  │  ←/h          back / collapse                    │
  │  space        toggle expansion                   │
  │  enter        switch session/window              │
  │  s n d r      status/new/remove/refresh          │
  │  /            search                             │
  │  ? q          help / quit                        │
  │ Markers                                          │
  │  > selected    * active window                   │
  │  ▸▾ collapsed/expanded    ↳ context              │
  │  ! ambiguous   ‖ paused   ? unknown   — none     │
  ╰──────────────────────────────────────────────────╯
 [↑↓] scroll  [PgUp/PgDn] page  [?/Esc] close
```

**80 columns**
```text
        ╭─ Help ───────────────────────────────────────────────────────╮
        │ Keys                                                         │
        │  j/k, ↓/↑     move selection                                 │
        │  →/l          expand / first child                           │
        │  ←/h          back / collapse                                │
        │  space        toggle expansion                               │
        │  enter        switch session/window                          │
        │  s n d r      status/new/remove/refresh                      │
        │  /            search                                         │
        │  ? q          help / quit                                    │
        │ Markers                                                      │
        │  > selected    * active window                               │
        │  ▸▾ collapsed/expanded    ↳ context                          │
        │  ! ambiguous   ‖ paused   ? unknown   — none                 │
        ╰──────────────────────────────────────────────────────────────╯
 [↑↓] scroll  [PgUp/PgDn] page  [?/Esc] close
```

**120 columns**
```text
                ╭─ Help ───────────────────────────────────────────────────────────────────────────────╮
                │ Keys                                                                                 │
                │  j/k, ↓/↑     move selection                                                         │
                │  →/l          expand / first child                                                   │
                │  ←/h          back / collapse                                                        │
                │  space        toggle expansion                                                       │
                │  enter        switch session/window                                                  │
                │  s n d r      status/new/remove/refresh                                              │
                │  /            search                                                                 │
                │  ? q          help / quit                                                            │
                │ Markers                                                                              │
                │  > selected    * active window                                                       │
                │  ▸▾ collapsed/expanded    ↳ context                                                  │
                │  ! ambiguous   ‖ paused   ? unknown   — none                                         │
                ╰──────────────────────────────────────────────────────────────────────────────────────╯
 [↑↓] scroll  [PgUp/PgDn] page  [?/Esc] close
```

At 200: the 88-cell box is centered in the 120 block, which is centered in 200 (56-cell left margin, 56-cell right). The box height is body height; the untouched keys (optional `q`, `Ctrl+C`, page keys) scroll below the fold. In ASCII mode, `│`→`|`, `╭╮╰╯`→`+`, `▸▾`→`>v`, `‖`→`||`, `…`→`~`.

### 6.6 Modals at 80

Bordered inset panels, centered horizontally and vertically in the body; no overlay/dimming in v1 (the body is cleared). Footer remains mode-specific. Width 64 at 80 columns (`min(W-6, 64)`); at 60, 54. Danger copy uses `danger` + bold.

**Status selector**
```text
 REXWS  [4 workstreams · 3 live · 2 agent-blocked]
────────────────────────────────────────────────────────────────────────────────
        ╭─ Status ─────────────────────────────────────────────────────╮
        │ rexws/m1-m2-core-rex                                         │
        │                                                              │
        │  > 1  In Progress  (current)                                 │
        │    2  Blocked                                                │
        │    3  In Review                                              │
        │    4  Ready                                                  │
        │    5  Done                                                   │
        ╰──────────────────────────────────────────────────────────────╯
 [↑↓] move  [1-9] shortcut  [Enter] apply  [Esc] cancel
```
With #13 icons/colors, each row gains the configured icon before the label (e.g., `> 1  ◐ In Progress  (current)`); the icon uses the status color; the label stays text. The `(current)` text is the non-color current marker.

**Remove confirmation**
```text
 REXWS  [4 workstreams · 3 live · 2 agent-blocked]
────────────────────────────────────────────────────────────────────────────────
        ╭─ Remove workstream ──────────────────────────────────────────╮
        │ Remove rexws/m1-m2-core-rex?                                 │
        │                                                              │
        │ Worktree: /wt/rexws                                          │
        │                                                              │
        │ This closes its Rex session and windows,                     │
        │ runs the provider remove command, and                        │
        │ deletes its workflow state.                                  │
        ╰──────────────────────────────────────────────────────────────╯
 [y/Enter] confirm  [n/Esc] cancel
```
The title and question are `danger` + bold; the destructive verb and the two-line consequence are explicit; no default action is implied.

**New worktree**
```text
 REXWS  [4 workstreams · 3 live · 2 agent-blocked]
────────────────────────────────────────────────────────────────────────────────
        ╭─ New worktree ───────────────────────────────────────────────╮
        │ > Project:  rexws          (←/→)                             │
        │   Branch:   feat/new-ui█                                     │
        │   Provider: git                                              │
        │   Target:   /wt/rexws/feat/new-ui                            │
        ╰──────────────────────────────────────────────────────────────╯
 [Tab] field  [←/→] project  [Enter] create  [Esc] cancel
```
`>` marks the active field (non-color); the text cursor is the terminal cursor or `█`; provider/target are `muted`; the target path truncates in the middle at 60, preserving the basename.

---

## 7. Accessibility

### 7.1 Non-color carriers (mandatory)

| Semantic | Carrier besides color |
|---|---|
| selection | `>` + bold + bar |
| active window | `*` + bold label |
| expanded/collapsed | `▾`/`▸` |
| context parent | `↳` + indent + faint |
| group | `▌` + uppercase label + count |
| agent state | full text (`blocked (permission)`, `working`, `stopped`, `unknown`, `—`) |
| match | bold + underline |
| warning persistent | `!` prefix + wording |
| error transient | action context (`switch:`, `status:`, provider labels) |
| danger | explicit question + confirm wording |
| live | word `live` + count |
| stale | `‖` + header health token |
| ambiguity | `!` + banner |
| Rex unknown | `?` + “Rex unavailable” |
| current status | `(current)` text |

### 7.2 Weight and decoration

- Bold: title, group label, selection, active window label, danger question, pending footer suffix. Bold is never the sole carrier.
- Underline: matches only (plus bold); never for status.
- Faint: context parents only; never for essential data.
- No italic, no blink, no reverse except ANSI-16 selection fallback.
- Selected rows suppress semantic fg (use `selection.fg`); bold + `>` + bar carry selection.

### 7.3 Glyphs and terminals without Unicode

| Unicode | ASCII | Use |
|---|---|---|
| `‖` | `||` | updates paused |
| `!‖` | `!|` | ambiguous + paused |
| `▸` | `>` | collapsed |
| `▾` | `v` | expanded |
| `↳` | `>` | context parent (dim/indent disambiguates) |
| `•` | `-` | child marker |
| `▌` | `|` | group marker |
| `—` | `-` | empty value |
| `…` | `~` | truncation |
| `×` | `x` | filter chip clear |
| `·` | `-` | header count separator |
| `─` | `-` | rule |
| `╭╮╰╯│` | `+ + + + |` | modal borders |
| `↑↓←→` | `^v<>` | key labels in footers and help |
| `! ? * > [ ]` | unchanged | already ASCII |

Mechanism: `REXWS_ASCII=1` forces the ASCII set; otherwise auto-detect a UTF-8 locale (`LC_ALL`, then `LC_CTYPE`, then `LANG` containing `UTF-8`/`utf8`). If unset, default to Unicode (current behavior). The glyph set is a small struct used by every renderer; tests force both. `‖` is 1 cell in `lipgloss.Width` (verified), so the 2-cell marker cell holds `!‖` and the ASCII `!|`.

### 7.4 Contrast, NO_COLOR, monochrome

Covered in §2.6–§2.7. Tests pin `termenv.Ascii` for no-color and `termenv.TrueColor` for the palette; a palette lint asserts the truecolor ratios in §2.6 are ≥4.5:1 for text roles.

---

## 8. Implementation checklist

### 8.1 #12 — visual system and wide layout

Ordered; each step leaves tests green.

1. **Theme layer.** New `internal/tui/theme.go`: semantic tokens from §2.2, `Theme` with `Status(def)`, `Agent(state)`, `Notice(kind)`, `Glyphs` (Unicode/ASCII), `NewTheme(profile)`; light/dark via `AdaptiveColor`. Add a contrast test helper. Replace the package vars at `internal/tui/render.go:18-27`.
2. **Apply roles.** `renderGroupRow` (`internal/tui/render.go:281-288`): status-colored `▌`, bold label, muted count, optional icon. `renderWorkstreamRow`/`renderWindowRow` (`internal/tui/render.go:290-359`): muted app/count, agent-colored badge, selection overrides. Notices (`internal/tui/render.go:119-135`): warning/error roles, muted `+N more`. Remove modal (`internal/tui/render.go:597-610`): danger. Match/selection keep bold/underline/`>`.
3. **Frame width.** In `Render` (`internal/tui/render.go:29-65`): `W = min(width,120)`, margins for >120, compose header/separator/body/notice/footer at `W`, prefix the margin. Keep the real-terminal minimum check (`:38-40`) and fatal (`:41-43`). The separator is `─` × `W` (`:46`).
4. **Grid.** Replace `computeWidths` (`internal/tui/render.go:186-262`) with one `Grid` type: project 10/12/16, branch flex, marker 2, badge 18/28, count 0/8, fixed tree cell, child app flex right-aligned. Parent and child badges share the rail. Update the row renderers to use it; keep `truncate`/`lipgloss.Width` and never wrap.
5. **Header/footer alignment.** Header counts right-aligned at ≥120; footer status right-aligned at ≥80; 60–79 single-space footer with `[Esc] clear`; optional keys whole-only (existing loop `internal/tui/render.go:535-542`). Keep the #1 health tokens untruncated.
6. **State copy.** Empty (two lines at 60), loading (two lines at ≥80), fatal (use `fatalKind`; wrap prose; separate config/state remedy), no matches (two lines at 60), too-small (add current size), pending suffix roles. Add a `wrap` helper for prose only.
7. **Glyphs.** Introduce the glyph set from §7.3; replace hardcoded `▌`, `▸`, `▾`, `↳`, `•`, `—`, `…`, `·`, `×`, and box characters. Keep sanitization before measuring (`internal/render/sanitize.go`); sanitize icons.
8. **Tests.** `internal/tui/render_test.go`: 60/80/120/200 with `Ascii` and `TrueColor`; assert no line exceeds width (`assertScreen`, `:30-42`), exact columns, whole chips, no color-only cues, and the state copy. Add `theme_test.go` for tokens/contrast and `glyph` tests for ASCII. Update `TestRender80Columns`/`60` if counts/copy change. Keep reducer/cursor tests untouched.
9. **Docs.** `TUI-DESIGN.md`; pointer in `TUI-UX.md`; §4.3/§10 cross-links; README TUI link.

**#12 acceptance:** no overflow or sparse rows at 200 (the 120 block centered); everything readable with color disabled; existing render/reducer tests pass; new width tests cover 60/80/120/200; TUI-UX criteria updated. The post-#1 health token and markers render in the new grid.

### 8.2 #13 — per-status color and icon

Ordered; depends on #12’s theme.

1. **Schema.** `internal/config/status.go:22-26`: `StatusDef` gains
   ```go
   Color string `json:"color,omitempty"`
   Icon  string `json:"icon,omitempty"`
   ```
   Empty = `auto` / no icon. `DefaultStatuses()` (`:28-37`) stays empty so old configs and `rexws list` are unchanged; the built-in five resolve by ID in the theme.
   - `color` grammar: `auto` (default), a named adaptive color (`black|red|green|yellow|blue|magenta|cyan|white|bright-<name>`), `#RGB`/`#RRGGBB`, or `ansi256:0-255`.
   - `icon`: one grapheme, 1–2 display cells, no C0/C1 controls, no ESC; trimmed; empty = none.
2. **Lenient validation and warnings.** Add `Warnings []string `json:"-"`` to `Config` (`internal/config/config.go:34-42`), populated after load by a style pass (called from `Load` at `:84` and `RegisterProject`). Invalid color → warning + `auto`; invalid icon → warning + none. Never fail the load. Confirmed 2026-11-26: this deliberately relaxes `README.md` principle #9 for cosmetic style fields; the warning is visible, never silent. Surface warnings: CLI `warning:` on stderr (alongside existing Rex warnings), TUI persistent banner (config class, after project discovery). Cite `internal/tui/model.go:219-226` for the warning aggregation.
3. **Resolver.** In `theme.go`: `auto` = built-in IDs by §2.3, custom IDs by deterministic hash; named = adaptive pair; hex/256 = fixed. Optional contrast warning when a fixed color is <3:1 against both `#FFF` and `#1E1E1E`.
4. **Render.** Group header `▌ <icon> LABEL (count)`; icon/`▌` in the status color, label base bold, count muted. Status selector rows get the icon and color; `(current)` remains the text marker. Parent rows stay status-free (`TUI-UX.md:100-102`).
5. **CLI unchanged.** `cmd/rexws/list.go:68-74` keeps `WorkflowStatusLabel` and `render.Default.Format`; add a test asserting no icon/color in `rexws list`. The JSON shape is unchanged.
6. **Schema version.** None. The fields are optional and additive; old configs load unchanged; `Default()` emits no color/icon. ROADMAP #6’s `schema_version` is for CLI JSON, not config. Revisit if a `theme` block or a non-additive color grammar appears.
7. **Docs.** README config example (`README.md:174-219`), SPEC §2 model (`SPEC.md:86-96`; note #14’s fixed-enum drift), TUI-UX §13/§15 mark scheduled, TUI-DESIGN §2.3.
8. **Tests.** `internal/config`: defaults, named/hex/ansi256, invalid values → warnings + fallback, icon sanitization, old config loads. `internal/tui`: group header and selector render icons/colors under TrueColor; Ascii still readable; width tests. CLI unchanged.
9. **Supersession.** `TUI-UX.md:32` (non-goal), `:690` (resolved decision), `:718` (out of scope), and ROADMAP M8 (`ROADMAP.md:61-68`) are superseded by #13. Amend them to “scheduled in #13; see TUI-DESIGN.md” when #13 lands.

**#13 acceptance:** old configs load with no migration; invalid values warn, never crash or silently miscolor; the TUI renders configured icon/color; `rexws list` unchanged; tests cover defaults, overrides, invalid input.

---

## 9. Document location and TUI-UX pointer

**Suggested path:** `/Users/djr/code/src/danrabinowitz/rexws/TUI-DESIGN.md`. Root, next to `TUI-UX.md`; there is no `docs/` directory yet and one file does not justify creating one (ROADMAP’s `docs/rex-cli.md` is separate).

**Pointer text** — add after the “Related” line (`TUI-UX.md:6`):

> Visual design: [`TUI-DESIGN.md`](TUI-DESIGN.md) defines the palette, wide-terminal layout, column grid, state copy, and accessibility rules. This file remains authoritative for behavior, keybindings, search, and acceptance criteria; `TUI-DESIGN.md` notes where it supersedes a layout or color decision.

Also add:
- §4.3 after the 80+ rules: “At ≥120 columns, see `TUI-DESIGN.md` §3 for the 120-cell content cap and column grid.”
- §10 after the glyph list: “Color roles, glyph fallbacks, and wide-layout rules are defined in `TUI-DESIGN.md` §2 and §7.”
- §13/§15: change the per-status color/icon entries from “deferred/out of scope” to “scheduled in #13; see `TUI-DESIGN.md` §8.2.”
- README TUI section (`README.md:161`): add “Visual system and wide-terminal layout: `TUI-DESIGN.md`.”

---

## 10. Decisions taken and open questions

Decisions taken (revisit only with evidence):
- **120-cell cap, centered (confirmed).** 100 would be denser: ~35-cell branch and 50-cell margins at 200.
- **Counts right-align at ≥120, not at 80.** At 80 the header stays a left-to-right chip row; right-aligning it there creates a sparse gap.
- **Child app right-aligned in its flex cell.** This clusters app+badge as a right status readout and aligns badges; the alternative (app immediately after the label) loses badge alignment.
- **Default icons empty.** The issue’s “built-in defaults” is satisfied by colors; shipping icons by default would change every existing TUI and double-mark groups. Icons are opt-in.
- **No config schema version for #13.** Additive optional fields; old configs unchanged.
- **Bordered modals at all widths.** Centered inset panels, no overlay/dimming in v1; body is cleared. If #12 needs to be smaller, the borders can be deferred without changing the copy or keys.
- **ASCII mode via `REXWS_ASCII=1` plus locale auto-detection.** No keybinding, no config schema change.

Coordinator decisions (2026-11-26):
1. **Width:** 120-cell cap, centered above 120.
2. **Invalid color/icon:** warn and fall back; the warning surfaces in the CLI and TUI. This deliberately relaxes `README.md` principle #9 for cosmetic style fields — visibly, not silently.
3. **Modal borders:** included in #12.
4. **Warning plumbing:** `Config.Warnings []string` (`json:"-"`), matching the existing `(Config, error)` API.
5. **`live` count color:** uncolored; the word `live` and the header health token carry the meaning.

---

## Summary of key decisions

- **Role-based palette** with ANSI-16/256/truecolor mappings, light/dark adaptive, ≥5:1 truecolor contrast against assumed `#FFF`/`#1E1E1E`, and a mandatory non-color carrier for every semantic. `live` is uncolored; stale is muted + `‖`; selection is `>` + bold + bar; matches are bold + underline.
- **120-cell content cap, centered**, with one grid for all widths: fixed tree/marker cells, project 10/12/16, badge 18/28, right-aligned count rail at ≥80, child app right-aligned before the shared badge rail. No split panes, no new navigable level, no new metadata columns; above 120 only margins change.
- **The post-#1 reliability UX is the baseline**: health token, `!`/`‖`/`!‖` marker cell, banner precedence. #12 refines the marker into its own cell; #1 behavior is unchanged.
- **#12 then #13**: built-in status colors by ID in #12; optional `color`/`icon` in `StatusDef` in #13 with lenient validation, warnings, and no config schema version. `rexws list` output stays text-only and unchanged.
- **Document:** `TUI-DESIGN.md` at the repo root, linked from `TUI-UX.md` and the README, with the explicit supersession notes in §1.4 and §8.2.
