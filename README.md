# rexws

**A small, local-first Git worktree workspace manager for Rex.**

`rexws` maps each Git worktree to a named Rex session and adds the layer Rex
doesn't have: which branch belongs to which task, what state that work is in,
and which agent needs attention. It derives the mapping from Git, persists only
your own workflow metadata, and stays out of the way.

> `rexws` is an unofficial personal companion to Rex. It is not affiliated with,
> endorsed by, or supported by Rex or Superlogical.

**Status:** pre-1.0, active development. The CLI and the interactive TUI both
work; CI runs on every push to `main` and on pull requests, and there is no
tagged release yet. See [Project status](#project-status).

**Requirements:** Git 2.36 or newer (`git worktree list --porcelain -z`).
`cleanup`'s patch-equivalence proof additionally requires Git 2.38 or newer
(`git merge-tree --write-tree`).

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
| **Worktree** | A Git worktree discovered by `git worktree list --porcelain -z`. Git is the source of truth for what exists. |
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
rexws status <selector> in_progress
rexws list
```

`status` takes a selector (see [Selecting a workstream](#selecting-a-workstream));
the worktree path that `list` prints still works. When you're done, `rm`
resolves the selector, prints the resolved session label and path, then closes
the session, runs the provider's remove command, and clears the workstream's
metadata. The branch is retained by default:

```sh
rexws rm <selector>
```

`rm` refuses the main worktree, a dirty worktree, and a detached HEAD. It
requires Rex to be reachable so its session pre-check and close are real. A
plain `rm` does not need a resolvable default branch; only `--force` (best
effort) and `--delete-branch` (required) compare the branch against it. If the
branch state cannot be proved under `--force`, `rm` says so and treats it as a
loss to confirm; `--delete-branch` refuses.

Remove a dirty or detached worktree anyway:

```sh
rexws rm <selector> --force
```

Before acting, `--force` prints `LOSS`, `RETAINED`, and `ACTIONS` to stderr.
`LOSS` is only what is truly discarded: every uncommitted, staged, untracked,
and ignored path (the whole directory is deleted) and every commit unique to a
detached HEAD that no branch, tag, or remote contains. `RETAINED` describes the
branch that stays — merged, patch-equivalent, or unmerged — and its commits
that are on no remote-tracking ref. Retained commits are not lost and do not
prompt. `--force` asks for confirmation only when `LOSS` is non-empty or the
branch state could not be assessed. Scripts and `--json` runs must pass
`--yes`; `--json` always requires `--yes` with `--force`, so stdout stays a
single document and no prompt can hang a pipeline. The built-in `git`
provider's `worktree remove` gets `--force` only for a dirty worktree; a custom
provider must handle a dirty removal itself.

Delete the branch as part of the removal:

```sh
rexws rm <selector> --delete-branch
```

The worktree is removed first. The branch is deleted only when a fresh proof
shows it is merged (an ancestor of the project's default branch) or
patch-equivalent (merging it back is a clean no-op). Merged branches try
`git branch -d`; if `-d` refuses because the main checkout's HEAD is elsewhere,
`rm` re-proves the base-relative ancestry and deletes atomically with
`git update-ref -d refs/heads/<branch> <sha>`, so a branch that moved cannot be
deleted. Patch-equivalent branches use the same atomic deletion only after a
fresh merge-tree proof immediately before deletion. Protected branches and an
unmerged branch are never deleted, with or without `--force`;
`--delete-branch` refuses and explains before removing anything. Remote
branches are never touched. `--yes` is accepted with `--delete-branch` even
though branch deletion alone never prompts.

After removal, `rm` reports the branch it retained and whether it still holds
unmerged commits:

```text
Retained branch feat/auth: unmerged, 2 commit(s) not in main.
```

The `--json` result carries `schema_version`, `deleted_branch`,
`retained_branches` (`branch`, `state`, `default_branch`, `commits`, `error`),
`warnings`, `retained` when a branch is kept (plan-time `branch`, `state`,
`default_branch`, `unpushed_commits`, `unmerged_commits`), and, for `--force`,
`loss`. `loss` is only what is discarded: `dirty_files`, `detached`,
`detached_commits`, and `assessment_error`. The CLI's `LOSS` and `RETAINED`
sections use the same split. A partial failure after the worktree was removed
still emits the JSON document with `schema_version` and `error` set and
`retained_branches` populated, then exits nonzero.

Provider contract: a remove command must deregister the worktree and must not
delete the branch. `--delete-branch` is the only branch-deletion path. If a
provider deregisters the worktree but leaves its directory behind, state is
cleared and the leftover directory is reported as a warning; if the worktree is
still registered, removal fails and state is kept. If a provider deletes the
branch anyway, `rm` warns that the branch disappeared and names the commits
that may now be unreachable.

Before each mutation, `rm` re-verifies the plan, and it checks the worktree
before killing the Rex session: the worktree must still be clean (or still have
exactly the paths the loss report named), its branch and HEAD must not have
moved, a detached HEAD's unique-commit set is recomputed (so a ref moved during
confirmation cannot strand a commit silently), no new live Rex session may have
appeared, and the branch proof is re-run immediately before deletion. A stale
proof refuses and keeps the worktree or branch.

### Commands

| Command | Purpose |
| --- | --- |
| `rexws` / `rexws tui` | Launch the interactive dashboard. |
| `rexws list [--json] [--status <id-or-label>]...` | List discovered workstreams with merged Rex and workflow state. `--status` is repeatable or comma-separated. |
| `rexws here [--dir <path>] [--json]` | Show the workstream containing the current directory (or `--dir`). |
| `rexws project add <path> [--name ...] [--provider ...] [--worktree-root ...]` | Register a Git repository. |
| `rexws new <project> <branch> [--json] [--switch] [--client ...]` | Create a worktree with the project's provider; with `--switch`, also create or select its Rex session. |
| `rexws switch <selector> [--window ...] [--client ...] [--json]` | Show the workstream's Rex session, creating it if needed; optionally select a window. |
| `rexws status <selector> <stage> [--json]` | Set a workstream's workflow status, by ID or label. |
| `rexws rm <selector> [--json] [--force] [--yes] [--delete-branch]` | Close the session, remove the worktree, clear its metadata; with `--delete-branch`, delete the branch only when merged or patch-equivalent. |
| `rexws cleanup [--project <name>] [--json] [--apply] [--force] [--fix [--yes]]` | Report every worktree, branch, and orphan with a reason; remove only the provably safe set and prune only provably stale `state.json` entries. |
| `rexws schema` | Print the machine-readable JSON contract; needs no config, Rex, or repository. |

### Selecting a workstream

`switch`, `status`, and `rm` take one selector. The grammar is the same for
every command, and a selector resolves to exactly one workstream or fails.

| Selector | Meaning |
| --- | --- |
| `<project>/<branch>` | Exact workstream identity. The first segment is the project and the rest is the branch, so branch names may contain `/`. |
| `branch:<name>` | Exact branch name. A detached worktree matches its abbreviated HEAD commit. |
| `path:<path>` | Exact worktree path. Relative paths resolve from the current directory; a leading `~/` expands to home. |
| `name:<label>` | Exact Rex session label, `<project>/<branch>`. It is the explicit form of the bare identity and is never treated as a path or substring. |
| `active`, `current` | The workstream whose worktree contains the current directory, including any subdirectory of it. |
| `<substring>` | A case-insensitive substring of the project name, branch, or worktree path. It must match exactly one workstream. |

Selector names and branch names are case-sensitive. A `path:` follows the
filesystem's own case rules, so a wrong-case spelling of the same directory
resolves on macOS but not on Linux. Only the substring fallback ignores case.
A bare selector is resolved as exact `<project>/<branch>` first, then as a
literal path. An input that exists as a path but is not a worktree is an error,
not a substring match; only nonexistent inputs reach the substring rule. An
ambiguous selector is an error that lists every candidate and suggests `path:`;
it never picks the first. Detached worktrees are identified by their
abbreviated HEAD commit, so two detached worktrees at the same commit share an
identity by construction; choose one with `path:`. `rexws here` uses the same
containment rule for the current directory; see
[Resolving the current workstream](#resolving-the-current-workstream).

A worktree whose path cannot be resolved — missing, unreadable, or behind a
broken symlink — is skipped instead of failing every selector or list.
`list` and `here` always print a warning on stderr naming every skipped
worktree's project and path, because both build the full workstream view;
`list --json` also includes them in its `warnings` array. `switch` and `status`
print the warning only when the skipped worktree could have matched the
selection; an unrelated skipped worktree stays quiet there. `--json` output
keeps stdout a single document.

`rm` refuses a selector when an unreadable project or a skipped worktree could
change which workstream it names. The refusal names the unreadable project or
worktree on stderr and asks for a `path:<path>` selector. A `path:` selector that
resolves among readable worktrees is always allowed because it names one exact
directory; an exact `<project>/<branch>` or `name:` also stays allowed when
only an unrelated project is unreadable. An allowed removal still prints every
skipped-worktree warning on stderr. When the selector matches nothing, `rm`
prints the skipped-worktree warnings before the create-or-register hint, so the
hint cannot hide them. `rm` never removes a workstream on a silently incomplete
candidate set.

`git worktree list --porcelain -z` preserves a path containing a newline, so
such a worktree is discovered, listed, and selectable like any other.

Selector failures have stable exit codes so scripts can branch without parsing
messages:

| Exit code | Meaning |
| --- | --- |
| `0` | Success. |
| `1` | Any other error, including a `--dir` that does not exist. |
| `2` | The selector matched no workstream. |
| `3` | The selector matched more than one workstream, or a directory is claimed by more than one registered workstream. |
| `4` | `active` or `current` — or `rexws here` — was used outside every workstream. |

### Resolving the current workstream

`rexws here` resolves the workstream whose worktree contains the current
directory, including any subdirectory, using the same containment rule as
`active`. `--dir <path>` resolves from another directory instead; the path may
be relative, symlinked, end with `/`, or start with `~/`. An empty `--dir` is an
error.

```sh
$ cd ~/code/worktrees/repo/feat-auth/internal
$ rexws here
project   repo
branch    feat/auth
path      /Users/you/code/worktrees/repo/feat-auth
session   repo/feat/auth
status    In Progress
selector  repo/feat/auth
```

`selector` is the value to hand to `switch`, `status`, or `rm`. It always
resolves to exactly this workstream: the exact `<project>/<branch>` label when
that label is unique and passes through the selector grammar unchanged, and
`path:<path>` otherwise — a detached worktree, a branch checked out in more than
one worktree, or a project name containing `:` or surrounding whitespace.
`session_name` is the human and Rex label; another worktree can share it, a
detached worktree's label is derived from HEAD and changes when HEAD moves, and
it is not unique in those cases. `--json` adds the workflow status ID and the
display label that `list` and `status` also use, plus `"detached": true` for a
detached worktree:

```json
{
  "schema_version": 1,
  "project": "repo",
  "branch": "feat/auth",
  "path": "/Users/you/code/worktrees/repo/feat-auth",
  "session_name": "repo/feat/auth",
  "workflow_status": "in_progress",
  "workflow_status_label": "In Progress",
  "selector": "repo/feat/auth"
}
```

Outside every registered worktree, `here` fails with exit code `4` rather than
printing an empty result. A `--dir` that does not exist is ordinary bad input
and exits `1`, with the flag and path named in the error.

### Cleanup candidates

`rexws cleanup` reports every worktree and every local branch that is not
checked out in one, across all registered projects by default, with a stable
reason for each. It changes no refs, worktrees, branches, or state unless
`--apply` is passed, and
`--project <name>` narrows the scan to one registered repository.

The classification is the primary blocking reason. `branch_state` states the
branch's relationship to the project's default branch independently, so a
dirty worktree on a merged branch has `"classification": "dirty"` and
`"branch_state": "merged"`.

| Classification | Meaning | `--apply` | `--force` |
| --- | --- | --- | --- |
| `prunable` | The worktree directory is missing and Git marks the entry prunable. | Removes the bookkeeping with `git worktree prune`; the branch is left for a later run, matching the two-pass scripts. A missing directory Git did not mark prunable is reported but left alone. | Same. |
| `merged` | The branch is an ancestor of the project's default branch and the worktree is clean. | Removes the worktree, then deletes the branch with `git branch -d` after the ancestry proof. | Same. |
| `patch-equivalent` | Merging the branch into the default branch is a clean no-op, so its changes are already there by rebase or cherry-pick. A clean empty commit is a no-op and qualifies. | Removes the worktree, then deletes the branch with `git branch -D` after the merge-tree proof. | Same. |
| `unmerged` | The branch has commits or changes that are not in the default branch. | Kept. | Removes after printing each commit that would be lost. |
| `dirty` | The worktree has modified, staged, untracked, or ignored paths. | Kept. | Removes after printing every status line that would be lost. |
| `protected` | The branch is `main`, `master`, `develop`, `HEAD`, or the project's default branch; or this is the main checkout. | Never removed. | Never removed. |
| `locked` | The worktree is locked. | Never removed. | Never removed. |
| `detached` | The worktree has a detached HEAD. | Never removed. | Never removed. |
| `active` | A live Rex session is labeled `<project>/<branch>`. | Never removed. | Never removed. |
| `unknown` | Git reported neither a branch nor a detached state, or the branch proof could not be evaluated (no common ancestor or a Git error). | Never removed. | Never removed. |

Safety rules:

- A preview (`cleanup` or `cleanup --json`) changes no refs, worktrees,
  branches, or `state.json`, and kills no session. It reads worktree and branch
  listings, `merge-base`, `git status` (including ignored paths), `log`, Rex
  sessions, and runs a merge-tree proof. The proof can write unreachable tree
  objects into Git's object database; Git GC reclaims them, and no ref or
  worktree points at them.
- Patch equivalence uses `git merge-tree --write-tree` (Git 2.38 or newer)
  rather than `git cherry`: it requires a clean merge whose result is exactly
  the default branch's tree. That rejects an evil merge with unique
  conflict-resolution content and a patch that was later reverted upstream,
  both of which cherry would call equivalent.
- `--apply` removes only `prunable`, `merged`-and-clean, and
  `patch-equivalent` items. `--force` additionally permits `dirty` and
  `unmerged` items, and nothing else. Protected, locked, detached, and active
  items are never removed, with or without `--force`.
- Before each removal, apply re-lists worktrees and re-lists Rex again
  immediately before the mutation loop, re-reads status including ignored
  paths, checks the branch SHA, and re-runs the ancestry or merge-tree proof.
  If anything changed since the plan, the item is kept and reported failed;
  rerun `cleanup`.
- Merged branches try `git branch -d`; if the main checkout's HEAD does not
  contain the merge, apply re-proves the base-relative ancestry and falls back
  to `-D`. Patch-equivalent branches and explicitly forced unmerged branches use
  `-D` only after the same re-proof, including a final re-check after the
  worktree is removed.
- Before a forced removal, `cleanup --apply --force` prints each item, its
  changed, untracked, or ignored paths, and its commits not in the default
  branch to stderr. `cleanup --force` without `--apply` prints the same
  prospective loss but changes nothing. With `--json`, stdout stays a single
  parseable document. A later re-check can still refuse the removal, so a LOSS
  line describes the plan, not a completed deletion.
- Ignored files count as dirty. `git worktree remove` deletes the whole
  directory, including ignored content such as a local `.env`, so a worktree
  holding only ignored files requires `--force` and names those paths in LOSS.
- Active sessions are checked through Rex. A preview continues with a warning
  when Rex cannot be reached; `--apply` refuses, because it cannot prove no
  session is active. `session_id` is the first match when Rex has duplicate
  labels; the detail names every matching session ID.
- The default branch is `origin/HEAD`'s target (its local branch when present,
  otherwise the remote-tracking ref), then local `main`, then local `master`,
  then `origin/main` or `origin/master`. A repository with none of these is
  reported as a project error and is not touched. The local default is
  preferred over a newer remote-tracking branch, so a branch merged only
  upstream can be reported `unmerged`; that is conservative and never removes
  it.
- The scan is sequential per worktree and branch: each item costs a few Git
  calls (`merge-base`, `merge-tree`, `log`, `status`). Cost grows linearly with
  the number of items, which is why apply re-verification favors correctness
  over batching.
- Worktree removal also clears that path's `state.json` entry. Branch-only
  deletions have no state.
- `cleanup --fix` changes only `state.json` and is covered under
  [Orphan triage](#orphan-triage).

#### Orphan triage

The same report names every inconsistency between Rex sessions, `state.json`,
and Git worktrees. Reporting is read-only; only `--fix` changes anything.

| Kind | Meaning | `--fix` |
| --- | --- | --- |
| `unmatched-session` | A live Rex session's label matches no worktree in the registered projects. | Reported only. rexws never adopts, renames, or closes sessions. |
| `stale-state` | A `state.json` key's path is not a current Git worktree. | Pruned only when the path is absent and provably names no worktree. |
| `unregistered-worktree` | A worktree inside a registered project's checkout but outside that project's `worktree_root`. | Reported only. rexws does not register it. |
| `session-label-conflict` | More than one live session shares a label, or one label matches more than one worktree, such as two detached worktrees at the same commit. | Reported only. rexws never closes or renames sessions. |

Detection rules:

- A path is a current Git worktree when a registered project's
  `git worktree list --porcelain` reports it, including a prunable record, or
  when the path exists and is the top level of a Git worktree. The second rule
  keeps state for a worktree of an unregistered repository; rexws does not
  treat a path as stale merely because the project is not registered.
- A `stale-state` entry is fixable only when its key is absolute, the path does
  not exist, and no re-listed project has a worktree there. A path that still
  exists but is not a worktree is reported and retained, as are prunable
  worktrees, which `cleanup --apply` owns.
- Fixability is computed over every registered project's worktree list, which
  is the same set `--fix` re-lists. If any registered project cannot be listed,
  stale entries are not checked at all, so a preview never advertises a fix
  that would refuse. A project whose default branch cannot be resolved still
  contributes its already-listed worktrees to session matching and
  unregistered-worktree reports.
- Session labels are `<project>/<branch>`; detached worktrees use the
  abbreviated HEAD commit. Branches may contain `/`. A session for an
  unregistered worktree still matches, so it is not reported as unmatched.
  Only labels whose first segment names a registered project are considered
  rexws-managed; a hand-named or unknown-project session is left alone. With
  no projects registered, session triage is skipped with a warning.
- A project with no `worktree_root` is skipped for `unregistered-worktree`,
  because there is no configured convention to violate. Set one to make
  out-of-root worktrees visible.
- `--project` narrows session items by label prefix, unregistered worktrees to
  that project, and state entries to paths under its checkout or
  `worktree_root`, even when only one project is registered. The state proof
  still reads every registered project so preview and fix agree.

`--fix` confirmation:

- `cleanup --fix` lists the candidates on stderr and asks on a terminal.
  Anything other than `y` or `yes` changes nothing and returns success unless
  the report itself has errors.
- A non-interactive run, and any `--json` run with candidates, must pass
  `--yes`; without it, the report is still printed but the command exits
  nonzero and changes no state. With no candidates there is nothing to confirm:
  `--fix` is an authorized no-op that validates `state.json` and reports
  `"fixed": true`.
- `--fix` cannot be combined with `--apply` or `--force`. It never kills or
  renames a Rex session, registers a project, adopts a session, or touches a
  worktree or branch.
- A `state.json` read failure is a warning in a preview and an error for
  `--fix`; a preview therefore still exits zero when the rest of the report is
  clean, while JSON stays a single parseable document in both cases.
- Before saving, fix re-lists every registered project, reloads `state.json`,
  and re-proves each target. If a path reappeared or became a worktree, nothing
  is pruned and that entry is reported `failed`. If a project cannot be
  re-read, the fix refuses before the per-item loop, so results stay empty.

`--json` emits a `schema_version: 1` document. The shape is frozen; see
[JSON contract](#json-contract). It is fixed because every item carries every
key. Projects are sorted by name and items by branch, kind, and path, so two
reports of the same repositories are byte-identical. Every worktree or branch
item carries `project`, `kind` (`worktree` or `branch`), `path`,
`branch`, `head`, `classification`, `branch_state`, `safe`, `removable`,
`requires_force`, `dirty`, `locked`, `detached`, `active_session`,
`session_id`, `action`, `result`, `detail`, `loss`, and `error`. Empty values
are `""`, `false`, or `null`; keys are never omitted. `loss` is `null` when
nothing would be lost, or an object whose `dirty_files` and `commits` are
always arrays. `totals.by_classification` always includes every classification
key, including zero counts. `action` is what apply would do; `result` is empty
in a preview and `kept`, `removed`, or `failed` after apply; `error` is empty
unless an item was kept or failed during apply.

The orphan section is additive and does not change `projects` or `totals`:

```json
{
  "orphans": {
    "fixed": false,
    "complete": true,
    "items": [
      {
        "kind": "stale-state",
        "project": "repo",
        "label": "",
        "path": "/absolute/path/to/worktrees/repo/feat-gone",
        "session_ids": [],
        "detail": "state.json entry points at a path that no longer exists and is not a current Git worktree",
        "fixable": true,
        "result": "",
        "error": ""
      }
    ],
    "totals": {
      "scanned": 1,
      "fixable": 1,
      "removed": 0,
      "failed": 0,
      "kept": 0,
      "by_kind": {
        "unmatched-session": 0,
        "stale-state": 1,
        "unregistered-worktree": 0,
        "session-label-conflict": 0
      }
    }
  }
}
```

`orphans.items` is sorted by kind, project, label, path, and session IDs.
`session_ids` is always an array. `fixed` means an authorized fix pass ran: it
is `false` in a preview, or when the fix was refused or failed before running,
and `true` after a fix pass, including a zero-candidate no-op. `complete` means
the stale-state proof covered every registered project, loaded `state.json`,
and checked every entry in scope; it is `false` for a preview or fix where no
projects are registered, a project’s worktrees could not be listed, `state.json`
could not be loaded, or an entry could not be checked. Scripts must treat
`"complete": false` as “stale state was not examined”, not as clean. `result`
stays empty in a preview or a fix that refused before the per-item loop; after
an authorized fix pass every item is `kept`, `removed`, or `failed`, so the
report names exactly what was pruned and what was retained.
`orphans.totals.by_kind` always includes all four kinds. This section is
additive: it does not change `projects` or `totals`.

Cleanup is deliberately not scheduled and does not kill Rex sessions. It also
never deletes a branch it cannot prove merged or patch-equivalent without
`--force`, and even `--force` will not touch a protected, locked, detached, or
active item.

### TUI

Run `rexws` with no arguments, or `rexws tui`, to open the interactive
dashboard. It groups workstreams by configured workflow status, with each
group header showing its configured label, icon, and color, and adds an
accordion inside them: each workstream expands to its Rex windows. `Enter`
confirms the highlighted row (switch to the session, or to the selected
window). `Right` expands a parent; a second `Right` moves into its first child,
and `Left` collapses or returns to the parent. `/` filters by case-insensitive
substring across project, branch, workflow status, agent state, and windows
while keeping parent context.
Live Rex streams update agent badges and window state as they change. Press `?`
for the full key map and `s` to change a workstream's status. `c` opens the
cleanup screen, which previews every worktree and branch grouped by risk and
applies only the selection you review. `d` opens the remove confirmation, which
shows the same Loss, Retained, and Actions sections the CLI would print. After
the provider removes the worktree, the dashboard notifies you of the retained
branch and whether it is unmerged, and warns if a provider deregistered the
worktree but left its directory.

See `TUI-UX.md` for the behavior source of truth and `TUI-DESIGN.md` for the
visual system.

## JSON contract

Every command that reports data accepts `--json` for scripts and future
launchers. Every payload carries `"schema_version": 1` and is frozen: see
`docs/json-schema.md` for every field, type, enum, nullable field, and omitted
field. `rexws schema` prints the same contract as a machine-readable field
manifest without needing config, Rex, or a repository. Field changes track the
payload types automatically; enum and command drift requires a deliberate
canonical-slice and command-coverage test update. `cmd/rexws` golden tests
compare the CLI output with a pinned fixture and validate one example per
command.

Additive fields keep the version. Removing or renaming a field, changing a
field's type or presence, removing an enum value, or adding a required field is
breaking and bumps `schema_version`. Consumers must ignore unknown fields.

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
    { "id": "in_review", "label": "In Review", "icon": "◆" },
    { "id": "ready", "label": "Ready", "color": "green" },
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

Each status may also set an optional `color` and `icon` for the TUI:

- `color` is `auto` (the default), a named adaptive ANSI color (`black`, `red`,
  `green`, `yellow`, `blue`, `magenta`, `cyan`, `white`, or `bright-<name>`),
  `#RGB`/`#RRGGBB`, or `ansi256:0-255`.
- `icon` is one grapheme cluster occupying one or two display cells, with no
  control characters. Icons are opt-in: an empty value renders nothing.
- The five built-in IDs resolve their colors without configuration
  (`in_progress` blue, `blocked` red, `in_review` purple, `ready` green,
  `done` muted). A custom ID gets a deterministic color from the accent,
  purple, cyan, and amber hues. A configured color wins.
- An invalid color or icon prints a `warning:` and falls back to `auto` or no
  icon instead of failing the load. The TUI shows the same warning in its
  banner. This is the deliberate exception to principle #9 below.
- A fixed hex or `ansi256` color that falls below 3:1 contrast on an assumed
  light or dark background also warns. The configured color only decorates the
  group marker and icon, never body text, so the label stays readable.
- Icons are rendered as-is. Emoji presentation and width depend on the terminal
  and font; ASCII mode translates structural glyphs but not your icons.

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
`rexws cleanup` reports entries whose path is no longer a current Git worktree;
`cleanup --fix` prunes only the provably stale ones after confirmation.

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
   references to removed statuses are errors, not silent fixes. Cosmetic style
   fields (`statuses[].color` and `statuses[].icon`) are the deliberate
   exception: an invalid value prints a `warning:` and falls back (`auto` or no
   icon) at render time, because a typo there cannot corrupt data. The value
   stays untouched in `config.json`, so the warning repeats until you fix it;
   it is never a silent rewrite.

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
- **No automatic or scheduled branch deletion.** `rm` removes the worktree and
  leaves the branch unless `--delete-branch` is explicit, and that flag deletes
  only a branch proven merged or patch-equivalent to the default branch;
  `cleanup --apply` deletes only branches proven merged or patch-equivalent, or
  explicitly forced after naming the loss.
- **No Kanban view, mouse support, or configurable keybindings.**
- **No window create/rename/close from `rexws`.**
- **No session adoption yet.** Matching is exact on `<project>/<branch>`, so
  sessions you named by hand show as stopped. Adoption is a future candidate.
- **No notes UX yet.** `state.json` stores notes; nothing edits them.
- **No JSON Schema document.** The JSON contract is frozen as a field manifest
  (`rexws schema`), not JSON Schema. Also no Rex version check, config-schema
  version, or provider timeouts yet.

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
- One selector grammar for `switch`, `status`, and `rm`: exact
  `<project>/<branch>`, `branch:`, `path:`, `name:`, `active`/`current`, and
  unique case-insensitive substrings; `here` resolves the workstream containing
  the current directory.
- `cleanup`: a preview-first, cross-project classification of every worktree
  and branch, with a safe `--apply` set and a force path that names its loss.
- Orphan triage in `cleanup`: unmatched Rex sessions, stale `state.json`
  entries, worktrees outside their project's `worktree_root`, and duplicate or
  ambiguous session labels; `--fix` prunes only provably stale state after
  confirmation.
- The interactive TUI (`rexws` or `rexws tui`): workflow-status groups with
  configured labels, colors, and icons; an expandable workstream → window
  hierarchy; substring search; status switching; live Rex updates; and the
  cleanup screen with preview and bulk apply.
- `rexws schema` and the frozen, versioned JSON contract; `docs/json-schema.md`
  documents every field and `cleanup` reports orphans additively.

**Planned:**

- Session adoption and label templates, a notes editing path, a normalized
  event stream, and machine-readable predicates.

**Not yet:**

- No tagged release yet. CI runs formatting, vet, and tests, including the race
  detector, on Linux; Windows is untested.
- Verified against Rex `0.1.0-preview.25` (API revision 24). There is no Rex
  version check, so a preview drift can break the adapter.
- The JSON contract is frozen at `schema_version: 1`. Additive changes keep the
  version; breaking changes bump it. See [JSON contract](#json-contract).

See `ROADMAP.md` for what is committed next, `SPEC.md` for the current design,
`IDEAS.md` for the unranked idea bank, and `TUI-UX.md` for the TUI UX.

---

## License

MIT — see [LICENSE](LICENSE).
