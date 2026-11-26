# JSON contract

Every command documented below accepts `--json` and, on success, writes exactly
one JSON document to stdout. Warnings and progress stay on stderr, so stdout can
be piped directly into `jq`, a launcher, or a Raycast extension. On failure, the
error goes to stderr and no stdout document is written, with two exceptions:
`rm` writes its partial-failure document (with `error` set) when the worktree
was already removed, and `here` writes nothing outside every registered
worktree.

The contract is versioned. Every payload carries a top-level
`"schema_version": 1`. The full machine-readable shape is available without
config, Rex, or a repository:

```sh
rexws schema
```

`rexws schema` emits a **field manifest** (format
`rexws-field-manifest`), not JSON Schema. It describes every command, its root
type, and every named type reachable from it: primitive type, array items, map
values, enum values, nullability, whether the field may be omitted, and the
fixed `schema_version` value. Field changes are reflected automatically from
the payload types that produce the output. Enum values and the command list are
tied to canonical slices and a command-coverage test, so drift there requires
updating those deliberately in the same change. Golden tests in `cmd/rexws`
compare the CLI output with `cmd/rexws/testdata/schema/v1.json`, pin its hash,
and validate an example document for every command against it.

```json
{
  "format": "rexws-field-manifest",
  "schema_version": 1,
  "commands": [
    {
      "name": "list",
      "description": "Discovered workstreams merged with workflow and Rex state.",
      "root": "ListPayload",
      "schema_version": 1
    }
  ],
  "types": {
    "ListPayload": {
      "type": "object",
      "fields": [
        { "name": "schema_version", "type": "integer", "const": 1 },
        { "name": "workstreams", "type": "array", "items": { "type": "object", "ref": "Workstream" } },
        { "name": "warnings", "type": "array", "items": { "type": "string" }, "optional": true }
      ]
    }
  }
}
```

## Compatibility rule

The version changes only when the contract changes:

- **Additive changes keep the version.** A new field, a new optional nested
  field, a new enum value, or a new command may ship under the same
  `schema_version`. Consumers must ignore fields they do not know.
- **Breaking changes bump the version.** A breaking change is any of:
  - removing or renaming a command, field, or type;
  - changing a field's JSON type, `$ref`, array item type, or map value type;
  - changing whether a field is always present (`omitempty` added or removed);
  - changing whether a field can be `null` (pointer added or removed);
  - removing an enum value or narrowing its set;
  - adding a required field (one without `omitempty`);
  - changing the meaning of an existing value, including changing the unit or
    the set of values a string may take;
  - changing a fixed `const` such as `schema_version`.

Adding a required field is breaking because an older producer's documents do
not contain it. Consumers should still tolerate unknown fields, which is what
makes additive changes safe.

The golden test enforces this: any shape change fails until the frozen fixture
and this document are updated deliberately, and its failure names a breaking
change when the fixture is compared at the same version. Every fixture also has
a pinned SHA-256 in `cmd/rexws/schema_test.go`; editing or deleting one fails
until that pin is updated in the same reviewable change. A version bump adds
`testdata/schema/v<p>.json` and its pin; older fixtures are never edited. For an
additive change, update the current fixture with
`go run ./cmd/rexws schema > cmd/rexws/testdata/schema/v1.json`, update this
document and the example under `testdata/schema/examples/`, then update the
pinned hash.

## Conventions

| Term | Meaning |
| --- | --- |
| `type` | `object`, `string`, `integer`, `number`, `boolean`, `array`, or `any`. |
| `ref` | The field's value is the named type in `types`, described below. |
| `items` | The element shape of an array. |
| `values` | The value shape of an object used as a map with arbitrary keys. |
| `enum` | The complete set of allowed string values. |
| `nullable` | The field may be JSON `null`. True only for a pointer without `omitempty`; a pointer with `omitempty` is omitted instead. |
| `optional` | The field may be absent (`omitempty`). A non-optional field is always present. |
| `const` | The field always has this value. |

- Object fields appear in Go struct order. Consumers must not depend on key
  order, but two runs over the same data produce identical bytes.
- An omitted optional field is absent, never `null`. A present-but-empty array
  is `[]`; arrays documented below as "always" are never `null`.
- Named string enums carry their value set on the type in `types`; a plain
  string field that is not a named type (for example `CleanupItem.kind`) carries
  its `enum` on the field itself.
- `workflow_status` is the configured stage ID, not a fixed enum: stages come
  from `config.json`. The built-in set is `in_progress`, `blocked`,
  `in_review`, `ready`, and `done`; `workflow_status_label` is the display
  label for the same stage.
- Repository paths are canonical absolute paths.
- `agent_state` is what the coding agent in a Rex session is doing;
  `unknown` means a live session could not be read, `stopped` means no Rex
  session, and `idle` means a session with no reporting agent.

## Commands

### `rexws list --json`

| Field | Type | Presence | Notes |
| --- | --- | --- | --- |
| `schema_version` | integer | always | `1`. |
| `workstreams` | array of `Workstream` | always | Sorted by project, then attached worktrees before detached, then branch. |
| `warnings` | array of string | optional | Omitted when empty. A Rex failure is a warning, not an error. Worktrees skipped because their path could not be read are also warnings and name their project and path. Config warnings for invalid status styles or low-contrast fixed colors are included too. |

### `rexws status --json`

| Field | Type | Presence | Notes |
| --- | --- | --- | --- |
| `schema_version` | integer | always | `1`. |
| `path` | string | always | Canonical worktree path. |
| `workflow_status` | string | always | Configured stage ID. |
| `workflow_status_label` | string | always | Display label. |
| `updated_at` | string (date-time) | always | RFC 3339 timestamp from `state.json`. |

### `rexws switch --json`

| Field | Type | Presence | Notes |
| --- | --- | --- | --- |
| `schema_version` | integer | always | `1`. |
| `session_name` | string | always | `<project>/<branch>`. |
| `session_id` | string | always | Selected or created session ID. |
| `window_id` | string | optional | Present when `--window` selected a window. |
| `window_label` | string | optional | Present when the selected window has a label. |
| `created` | boolean | always | True when a new session was created. |

### `rexws new --json`

| Field | Type | Presence | Notes |
| --- | --- | --- | --- |
| `schema_version` | integer | always | `1`. |
| `project` | string | always | Registered project name. |
| `branch` | string | always | Created branch. |
| `path` | string | always | Canonical worktree path. |
| `provider` | string | always | Provider that created the worktree. |
| `session_id` | string | optional | Present only with `--switch`. |
| `session_created` | boolean | optional | Present only with `--switch`; false when an existing session was selected. Never `null`. |

### `rexws rm --json`

A partial failure after the worktree was removed still writes this document to
stdout, with `error` set — including when the provider removed the worktree and
then exited non-zero, or when the removal could not be re-verified — so a script
can read the retained-branch report and the reason. `--json` with `--force`
requires `--yes`; the loss preview goes to stderr.

| Field | Type | Presence | Notes |
| --- | --- | --- | --- |
| `schema_version` | integer | always | `1`. |
| `project` | string | always | Project name. |
| `branch` | string | always | Branch that was checked out (empty for a detached worktree). |
| `path` | string | always | Canonical worktree path. |
| `provider` | string | always | Provider that removed the worktree. |
| `closed_session_id` | string | optional | Present when a live Rex session was closed. |
| `deleted_branch` | string | optional | Present only when `--delete-branch` deleted the branch. |
| `retained_branches` | array of `RetainedBranch` | always | Always an array, even when empty. |
| `retained` | `RemoveRetained` | optional | Plan-time retained-branch report; absent when the branch was deleted or detached. Never `null`. |
| `warnings` | array of string | optional | Omitted when empty. |
| `loss` | `RemoveLoss` | optional | Present only with `--force`. Never `null`. |
| `error` | string | optional | Present on a partial failure after removal. |

`retained` and `loss` are deliberately separate: unmerged and unpushed commits
on a retained branch are not losses.

### `rexws project add --json`

| Field | Type | Presence | Notes |
| --- | --- | --- | --- |
| `schema_version` | integer | always | `1`. |
| `project` | `Project` | always | The registered project. |
| `created` | boolean | always | False when it was already registered. |

### `rexws here --json`

| Field | Type | Presence | Notes |
| --- | --- | --- | --- |
| `schema_version` | integer | always | `1`. |
| `project` | string | always | Project name. |
| `branch` | string | always | Branch. |
| `path` | string | always | Canonical worktree path. |
| `session_name` | string | always | Rex label; not unique for detached worktrees or a branch in two worktrees. |
| `workflow_status` | string | always | Configured stage ID. |
| `workflow_status_label` | string | always | Display label. |
| `selector` | string | always | A selector that resolves to exactly this workstream. |
| `detached` | boolean | optional | `true` for a detached worktree; omitted when false. |

Outside every registered worktree, `here` fails with exit code `4` and writes
no JSON document.

### `rexws cleanup --json`

The report is a preview unless `--apply` or `--fix` ran. Projects are sorted by
name and items by branch, kind, and path, so two reports of the same
repositories are byte-identical.

| Field | Type | Presence | Notes |
| --- | --- | --- | --- |
| `schema_version` | integer | always | `1`. |
| `applied` | boolean | always | True after `--apply`. |
| `force` | boolean | always | True when the scan was forced. |
| `projects` | array of `CleanupProject` | always | Every scanned project, sorted. |
| `orphans` | `CleanupOrphans` | always | Orphan section; additive to `projects` and `totals`. |
| `totals` | `CleanupTotals` | always | Summary counts. |
| `warnings` | array of string | always | Always an array. |
| `errors` | array of string | always | Always an array; the process exits non-zero after writing the report when non-empty. |

## Types

### `Workstream`

| Field | Type | Presence | Notes |
| --- | --- | --- | --- |
| `project` | string | always | Project name. |
| `branch` | string | always | Branch, or the abbreviated HEAD for a detached worktree. |
| `path` | string | always | Canonical worktree path. |
| `session_name` | string | always | `<project>/<branch>`. |
| `workflow_status` | string | always | Configured stage ID. |
| `workflow_status_label` | string | always | Display label. |
| `note` | string | optional | Omitted when empty. |
| `rex_session_id` | string | always | Empty when stopped. |
| `agent_state` | enum string | always | `blocked`, `error`, `working`, `unknown`, `done`, `idle`, `stopped`. |
| `agent_detail` | string | optional | Omitted when empty; often a permission prompt kind. |
| `agents` | array of `Agent` | always | Sessions may have fewer agents than blocks. |
| `windows` | array of `Window` | always | Rex windows in order. |
| `detached` | boolean | optional | `true` for a detached worktree; omitted when false. |

### `Agent`

| Field | Type | Presence | Notes |
| --- | --- | --- | --- |
| `block_id` | string | always | Terminal block ID. |
| `block_label` | string | optional | Omitted when empty. |
| `window_id` | string | always | Owning window ID. |
| `window_label` | string | optional | Omitted when empty. |
| `app` | string | optional | Reported app. |
| `effective_app` | string | optional | App after configuration fallback. |
| `state` | enum string | always | Same values as `agent_state`. |
| `detail` | string | optional | Free-form detail, often a prompt kind. |
| `msg` | string | optional | Latest message. |
| `title` | string | optional | Reported title. |
| `updated_at` | string | optional | Reported timestamp. |
| `error` | string | optional | Why the state could not be read. |

### `Window`

| Field | Type | Presence | Notes |
| --- | --- | --- | --- |
| `window_id` | string | always | Window ID. |
| `label` | string | always | Window label. |
| `active` | boolean | always | Active window in the session. |
| `focused_block_id` | string | optional | Omitted when no block is focused. |
| `block_ids` | array of string | optional | Omitted when empty. |

### `RetainedBranch`

| Field | Type | Presence | Notes |
| --- | --- | --- | --- |
| `branch` | string | always | Retained branch. |
| `state` | enum string | always | `merged`, `patch-equivalent`, `unmerged`, or `""` when `error` is set. |
| `default_branch` | string | optional | Comparison branch. |
| `commits` | array of `Commit` | optional | Unmerged commits; omitted when none. |
| `error` | string | optional | Why the state could not be proven. |

### `RemoveRetained`

| Field | Type | Presence | Notes |
| --- | --- | --- | --- |
| `branch` | string | optional | Retained branch. |
| `state` | enum string | optional | `merged`, `patch-equivalent`, or `unmerged`. |
| `default_branch` | string | optional | Comparison branch. |
| `unpushed_commits` | array of `Commit` | always | Commits not on a remote-tracking branch. |
| `unmerged_commits` | array of `Commit` | always | Commits not in the default branch. |

### `RemoveLoss`

| Field | Type | Presence | Notes |
| --- | --- | --- | --- |
| `dirty_files` | array of string | always | `git status --porcelain` lines, including ignored paths. |
| `detached` | boolean | always | True when HEAD was detached. |
| `detached_commits` | array of `Commit` | always | Commits unique to the detached HEAD. |
| `assessment_error` | string | optional | Present when force was used but the loss could not be assessed. |

### `Project`

| Field | Type | Presence | Notes |
| --- | --- | --- | --- |
| `name` | string | always | Project name. |
| `path` | string | always | Canonical repository path. |
| `provider` | string | optional | Project-level provider override. |
| `worktree_root` | string | optional | Project-level worktree root. |

### `Commit`

| Field | Type | Presence | Notes |
| --- | --- | --- | --- |
| `sha` | string | always | Full commit SHA. |
| `subject` | string | always | Commit subject line. |

### `CleanupProject`

| Field | Type | Presence | Notes |
| --- | --- | --- | --- |
| `project` | string | always | Project name. |
| `path` | string | always | Canonical repository path. |
| `default_branch` | string | always | Branch that merge and patch-equivalence are compared against. |
| `items` | array of `CleanupItem` | always | Sorted worktree and branch items. |

### `CleanupItem`

Every item carries every key; empty values are `""`, `false`, or `null`.

| Field | Type | Presence | Notes |
| --- | --- | --- | --- |
| `project` | string | always | Project name. |
| `kind` | enum string | always | `worktree` or `branch`. |
| `path` | string | always | Worktree path, or empty for a branch item. |
| `branch` | string | always | Branch, or empty for a detached worktree. |
| `head` | string | always | HEAD SHA. |
| `classification` | enum string | always | `prunable`, `merged`, `patch-equivalent`, `unmerged`, `dirty`, `protected`, `locked`, `detached`, `active`, `unknown`. |
| `branch_state` | enum string | always | `merged`, `patch-equivalent`, `unmerged`, or `""` when unknown. |
| `safe` | boolean | always | In the no-force safe set. |
| `removable` | boolean | always | The current flags will act on it. |
| `requires_force` | boolean | always | Needs `--force` to act. |
| `dirty` | boolean | always | Worktree has uncommitted, untracked, or ignored changes. |
| `locked` | boolean | always | Worktree is locked. |
| `detached` | boolean | always | HEAD is detached. |
| `active_session` | boolean | always | A live Rex session matches the item. |
| `session_id` | string | always | First matching session, empty when none. |
| `action` | enum string | always | `none`, `prune`, `remove-worktree`, `delete-branch`, `remove-worktree-and-branch`. |
| `result` | enum string | always | `""` in a preview; `kept`, `removed`, or `failed` after apply/fix. |
| `detail` | string | always | Human-readable reason. |
| `loss` | `CleanupLoss` or null | always | `null` when nothing would be lost. |
| `error` | string | always | Empty unless the item was kept or failed during apply. |

### `CleanupLoss`

| Field | Type | Presence | Notes |
| --- | --- | --- | --- |
| `dirty_files` | array of string | always | Always an array; `git status --porcelain` lines. |
| `commits` | array of `Commit` | always | Always an array; commits not in the default branch. |

### `CleanupTotals`

| Field | Type | Presence | Notes |
| --- | --- | --- | --- |
| `scanned` | integer | always | Items examined. |
| `safe` | integer | always | Items in the no-force safe set. |
| `removable` | integer | always | Items the current flags will act on. |
| `kept` | integer | always | Items kept. |
| `removed` | integer | always | Items removed by apply. |
| `failed` | integer | always | Items that failed. |
| `by_classification` | map string→integer | always | Includes every classification key with zero counts. |

### `CleanupOrphans`

| Field | Type | Presence | Notes |
| --- | --- | --- | --- |
| `fixed` | boolean | always | An authorized `--fix` pass ran, including a zero-candidate no-op. |
| `complete` | boolean | always | The stale-state proof covered every project and loaded `state.json`; `false` means stale state was not examined, not that it is clean. |
| `items` | array of `CleanupOrphan` | always | Sorted by kind, project, label, path, and session IDs. |
| `totals` | `CleanupOrphanTotals` | always | Orphan summary. |

### `CleanupOrphan`

| Field | Type | Presence | Notes |
| --- | --- | --- | --- |
| `kind` | enum string | always | `unmatched-session`, `stale-state`, `unregistered-worktree`, `session-label-conflict`. |
| `project` | string | always | Project name, empty when unknown. |
| `label` | string | always | Session label, empty when not applicable. |
| `path` | string | always | Related path, empty when not applicable. |
| `session_ids` | array of string | always | Always an array. |
| `detail` | string | always | Human-readable reason. |
| `fixable` | boolean | always | `--fix` can prove and prune it. |
| `result` | enum string | always | `""` in a preview or before the fix loop; `kept`, `removed`, or `failed` after a fix pass. |
| `error` | string | always | Empty unless the fix failed. |

### `CleanupOrphanTotals`

| Field | Type | Presence | Notes |
| --- | --- | --- | --- |
| `scanned` | integer | always | Orphans examined. |
| `fixable` | integer | always | Orphans `--fix` can prune. |
| `removed` | integer | always | Pruned entries. |
| `failed` | integer | always | Entries that failed to prune. |
| `kept` | integer | always | Entries retained. |
| `by_kind` | map string→integer | always | Includes all four kinds with zero counts. |

## Out of scope

This contract covers the commands above. A future public `rexws events` stream
and future satellites are not part of `schema_version: 1`; they would be
separate streams with their own shapes.
