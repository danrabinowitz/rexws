package ops

import (
	"context"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/rex"
)

// SwitchWorkstream selects (or creates) the Rex session labeled sessionName
// in dir using the configured Rex app client.
func SwitchWorkstream(ctx context.Context, cfg config.Config, sessionName, dir, window string) (rex.SwitchResult, error) {
	return SwitchWorkstreamWithClient(ctx, cfg, cfg.RexAppClient(), sessionName, dir, window)
}

// SwitchWorkstreamWithClient is SwitchWorkstream with an explicit app client.
// An empty client falls back to the first connected Rex app.
func SwitchWorkstreamWithClient(ctx context.Context, cfg config.Config, client, sessionName, dir, window string) (rex.SwitchResult, error) {
	return rex.New(cfg.RexBinary()).Switch(ctx, sessionName, dir, client, window)
}

// ResolveAppClient chooses the Rex app client for a switch: an explicit value,
// then the configured client, then the first connected Rex app. Resolving up
// front means a missing app fails before a session is created.
func ResolveAppClient(ctx context.Context, cfg config.Config, explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if client := cfg.RexAppClient(); client != "" {
		return client, nil
	}
	return rex.New(cfg.RexBinary()).AppClient(ctx)
}
