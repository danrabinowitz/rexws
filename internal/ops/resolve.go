package ops

import (
	"context"
	"errors"
	"fmt"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/selector"
)

// ResolveWorkstream resolves the shared selector grammar against the
// worktrees of every registered project. Commands take a selector instead of
// inventing their own lookup; a future `rexws here` resolves "active" through
// the same path.
func ResolveWorkstream(ctx context.Context, cfg config.Config, input string) (selector.Candidate, error) {
	candidate, discovery, err := resolveWorkstream(ctx, cfg, input)
	if err != nil {
		return selector.Candidate{}, resolutionError(err, discovery)
	}
	return candidate, nil
}

// resolveWorkstream is ResolveWorkstream plus the discovery snapshot, so a
// caller can surface projects that could not be read.
func resolveWorkstream(ctx context.Context, cfg config.Config, input string) (selector.Candidate, Discovery, error) {
	discovery := Discover(ctx, cfg)
	resolver, err := selector.FromInputs(discovery.Inputs)
	if err != nil {
		return selector.Candidate{}, discovery, err
	}
	candidate, err := resolver.Resolve(input)
	return candidate, discovery, err
}

// resolutionError adds discovery problems to a selector miss, wrapping both
// so errors.Is and errors.As can inspect either. A successful match is not
// blocked by an unreadable project; destructive callers surface the warning
// separately.
func resolutionError(err error, discovery Discovery) error {
	if err == nil {
		return nil
	}
	discoveryErr := discovery.Err()
	if discoveryErr == nil || !isResolutionMiss(err) {
		return err
	}
	return fmt.Errorf("%w; some projects could not be read: %w", err, discoveryErr)
}

func isResolutionMiss(err error) bool {
	return errors.Is(err, selector.ErrNoMatch) ||
		errors.Is(err, selector.ErrAmbiguous) ||
		errors.Is(err, selector.ErrNoActiveWorkstream)
}
