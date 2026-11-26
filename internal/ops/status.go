package ops

import (
	"context"
	"time"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/pathutil"
)

// StatusChange is the outcome of setting a workstream's workflow status.
// Changed is false when the workstream already had that status; updated_at is
// then left untouched.
type StatusChange struct {
	Path      string
	Status    config.Status
	Label     string
	UpdatedAt time.Time
	Changed   bool
}

// SetStatus records a workstream's workflow status. The path must belong to a
// discovered worktree.
func SetStatus(ctx context.Context, cfg config.Config, path string, status config.Status) (StatusChange, error) {
	canonical, err := pathutil.Canonical(path)
	if err != nil {
		return StatusChange{}, err
	}
	discovery := Discover(ctx, cfg)
	_, _, found, err := discovery.Worktree(canonical)
	if err != nil {
		return StatusChange{}, err
	}
	if !found {
		return StatusChange{}, notFoundError(discovery, canonical)
	}

	state, err := config.LoadState(cfg.StatusIDs())
	if err != nil {
		return StatusChange{}, err
	}
	current := state.For(canonical, cfg.DefaultStatusValue())
	if current.Status == status {
		return StatusChange{
			Path:      canonical,
			Status:    status,
			Label:     cfg.StatusLabel(status),
			UpdatedAt: current.UpdatedAt,
			Changed:   false,
		}, nil
	}
	stored := state.SetStatus(canonical, status, time.Now())
	if err := state.Save(); err != nil {
		return StatusChange{}, err
	}
	return StatusChange{
		Path:      canonical,
		Status:    stored.Status,
		Label:     cfg.StatusLabel(stored.Status),
		UpdatedAt: stored.UpdatedAt,
		Changed:   true,
	}, nil
}
