package ops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/core"
	"github.com/danrabinowitz/rexws/internal/selector"
)

// ResolveWorkstream resolves the shared selector grammar against the
// worktrees of every registered project. Commands take a selector instead of
// inventing their own lookup; `rexws here` resolves the current directory
// through ResolveHere.
func ResolveWorkstream(ctx context.Context, cfg config.Config, input string) (selector.Candidate, error) {
	candidate, _, err := ResolveWorkstreamWithWarnings(ctx, cfg, input)
	return candidate, err
}

// ResolveWorkstreamWithWarnings resolves like ResolveWorkstream and also
// reports worktrees that were skipped because their paths could not be read
// and could have matched the selector. Callers print these warnings on stderr;
// a warning does not replace the selected readable candidate.
func ResolveWorkstreamWithWarnings(ctx context.Context, cfg config.Config, input string) (selector.Candidate, []string, error) {
	candidate, discovery, _, warnings, err := resolveWorkstream(ctx, cfg, input)
	if err != nil {
		return selector.Candidate{}, warnings, resolutionError(err, discovery)
	}
	return candidate, warnings, nil
}

// ResolveHere resolves the workstream whose worktree contains dir, using the
// same containment rule as the active/current selector. An empty dir means the
// process working directory; a non-empty dir applies the rule there instead,
// which is what `rexws here --dir` and a future TUI need. It returns the
// discovery snapshot so callers can build the joined workstream view without
// listing worktrees twice.
func ResolveHere(ctx context.Context, cfg config.Config, dir string) (selector.Candidate, Discovery, error) {
	candidate, discovery, _, err := ResolveHereWithWarnings(ctx, cfg, dir)
	return candidate, discovery, err
}

// ResolveHereWithWarnings resolves like ResolveHere and also reports skipped
// worktrees whose path contains the resolved directory. They are empty when
// the directory is unrelated to every skipped worktree.
func ResolveHereWithWarnings(ctx context.Context, cfg config.Config, dir string) (selector.Candidate, Discovery, []string, error) {
	discovery := Discover(ctx, cfg)
	resolver := selector.FromInputs(discovery.Inputs)
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return selector.Candidate{}, discovery, nil, fmt.Errorf("cannot determine the current directory: %w", err)
		}
		candidate, warnings, err := resolver.CurrentWithWarnings(cwd)
		if err == nil {
			return candidate, discovery, warnings, nil
		}
		if isResolutionMiss(err) {
			return selector.Candidate{}, discovery, warnings, resolutionError(withNoProjectsHint(err, cfg), discovery)
		}
		return selector.Candidate{}, discovery, warnings, fmt.Errorf("cannot determine the current directory: %w", err)
	}
	candidate, warnings, err := resolver.CurrentWithWarnings(dir)
	if err != nil {
		err = withNoProjectsHint(resolutionError(err, discovery), cfg)
		return selector.Candidate{}, discovery, warnings, fmt.Errorf("--dir %q: %w", dir, err)
	}
	return candidate, discovery, warnings, nil
}

// ContextSelector names workstream so another command can resolve it from any
// directory: the exact <project>/<branch> label when that label is unique in
// the discovered set, otherwise path:<canonical path>. Detached worktrees
// always use the path form because their commit-based label can repeat and
// changes with HEAD. A label that would itself be parsed as another selector
// form (a project name containing ":" or surrounding whitespace) also uses the
// path form, so the result always resolves to this workstream. The path form is
// absolute and uses the exact-path rule.
func ContextSelector(workstreams []core.Workstream, workstream core.Workstream) string {
	name := workstream.SessionName
	if workstream.Detached ||
		strings.HasPrefix(name, "-") ||
		strings.TrimSpace(name) != name ||
		strings.Contains(name, ":") ||
		!uniqueSessionName(workstreams, name) {
		return "path:" + workstream.Path
	}
	return name
}

func uniqueSessionName(workstreams []core.Workstream, name string) bool {
	seen := false
	for _, workstream := range workstreams {
		if workstream.SessionName != name {
			continue
		}
		if seen {
			return false
		}
		seen = true
	}
	return true
}

// withNoProjectsHint names the fix when no project is registered at all; the
// generic message would only point at `rexws list`.
func withNoProjectsHint(err error, cfg config.Config) error {
	if len(cfg.Projects) != 0 || !errors.Is(err, selector.ErrNoActiveWorkstream) {
		return err
	}
	return fmt.Errorf("%w; no projects are registered; add one with `rexws project add <path>`", err)
}

// resolveWorkstream resolves a selector and also returns the discovery
// snapshot, the resolver, and skipped-worktree warnings, so a caller can
// surface incomplete discovery, disclose every skipped worktree, or reuse the
// worktrees.
func resolveWorkstream(ctx context.Context, cfg config.Config, input string) (selector.Candidate, Discovery, *selector.Resolver, []string, error) {
	discovery := Discover(ctx, cfg)
	resolver := selector.FromInputs(discovery.Inputs)
	candidate, warnings, err := resolver.ResolveWithWarnings(input)
	return candidate, discovery, resolver, warnings, err
}

// AllSkippedWarnings reports every worktree left out of the resolver, not only
// the ones a selector could match. Destructive callers use it to disclose an
// incomplete candidate set even when the target is an explicit path. It reuses
// the resolver built for selection and uses the same stable order as the
// selector's own warnings.
func AllSkippedWarnings(resolver *selector.Resolver) []string {
	skipped := resolver.Skipped()
	sort.SliceStable(skipped, func(i, j int) bool {
		if skipped[i].SessionName != skipped[j].SessionName {
			return skipped[i].SessionName < skipped[j].SessionName
		}
		return skipped[i].Path < skipped[j].Path
	})
	warnings := make([]string, 0, len(skipped))
	for _, item := range skipped {
		warnings = append(warnings, item.Warning())
	}
	return warnings
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
