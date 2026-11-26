package main

import (
	"time"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/contract"
	"github.com/danrabinowitz/rexws/internal/core"
	"github.com/danrabinowitz/rexws/internal/ops"
)

// SchemaVersion is the version of the JSON contract. Every --json command
// carries it as schema_version. Additive fields keep the version; a breaking
// change bumps it. See docs/json-schema.md and SPEC.md, and run `rexws schema`
// for the machine-readable manifest.
const SchemaVersion = contract.SchemaVersion

// The payload types below are the complete --json surface. Their JSON tags are
// the contract: field order, names, nullability, omission, and nested types.
// `rexws schema` derives its manifest by reflecting over these types, so a
// shape change here is visible to the golden tests.

// ListPayload is the `rexws list --json` document.
type ListPayload struct {
	SchemaVersion int               `json:"schema_version"`
	Workstreams   []core.Workstream `json:"workstreams"`
	Warnings      []string          `json:"warnings,omitempty"`
}

// StatusPayload is the `rexws status --json` document.
type StatusPayload struct {
	SchemaVersion       int           `json:"schema_version"`
	Path                string        `json:"path"`
	WorkflowStatus      config.Status `json:"workflow_status"`
	WorkflowStatusLabel string        `json:"workflow_status_label"`
	UpdatedAt           time.Time     `json:"updated_at"`
}

// SwitchPayload is the `rexws switch --json` document.
type SwitchPayload struct {
	SchemaVersion int    `json:"schema_version"`
	SessionName   string `json:"session_name"`
	SessionID     string `json:"session_id"`
	WindowID      string `json:"window_id,omitempty"`
	WindowLabel   string `json:"window_label,omitempty"`
	Created       bool   `json:"created"`
}

// NewPayload is the `rexws new --json` document. SessionID and SessionCreated
// are present only with --switch.
type NewPayload struct {
	SchemaVersion  int    `json:"schema_version"`
	Project        string `json:"project"`
	Branch         string `json:"branch"`
	Path           string `json:"path"`
	Provider       string `json:"provider"`
	SessionID      string `json:"session_id,omitempty"`
	SessionCreated *bool  `json:"session_created,omitempty"`
}

// RmPayload is the stable result document for rm --json. Error is set on a
// partial failure after the worktree was already removed, so stdout still
// carries the retained-branch report and the reason.
type RmPayload struct {
	SchemaVersion    int                  `json:"schema_version"`
	Project          string               `json:"project"`
	Branch           string               `json:"branch"`
	Path             string               `json:"path"`
	Provider         string               `json:"provider"`
	ClosedSessionID  string               `json:"closed_session_id,omitempty"`
	DeletedBranch    string               `json:"deleted_branch,omitempty"`
	RetainedBranches []ops.RetainedBranch `json:"retained_branches"`
	Retained         *ops.RemoveRetained  `json:"retained,omitempty"`
	Warnings         []string             `json:"warnings,omitempty"`
	Loss             *ops.RemoveLoss      `json:"loss,omitempty"`
	Error            string               `json:"error,omitempty"`
}

// ProjectAddPayload is the `rexws project add --json` document.
type ProjectAddPayload struct {
	SchemaVersion int            `json:"schema_version"`
	Project       config.Project `json:"project"`
	Created       bool           `json:"created"`
}

// HerePayload is `rexws here --json`. The field names match list and switch
// where they overlap. selector always resolves to exactly this workstream: it
// is the exact <project>/<branch> label when that label is unique, and
// path:<path> otherwise, so a caller never has to know which form it got.
// session_name stays the human and Rex label, which is not unique for detached
// worktrees or a branch checked out in two worktrees.
type HerePayload struct {
	SchemaVersion       int           `json:"schema_version"`
	Project             string        `json:"project"`
	Branch              string        `json:"branch"`
	Path                string        `json:"path"`
	SessionName         string        `json:"session_name"`
	WorkflowStatus      config.Status `json:"workflow_status"`
	WorkflowStatusLabel string        `json:"workflow_status_label"`
	Selector            string        `json:"selector"`
	Detached            bool          `json:"detached,omitempty"`
}
