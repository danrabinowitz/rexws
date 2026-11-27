package tui

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/muesli/termenv"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/ops"
)

// goldenCase is one byte-exact render fixture. Goldens are written only with
// UPDATE_GOLDEN=1 and are pinned to termenv.Ascii; the ASCII glyph profile is
// selected per case.
type goldenCase struct {
	name   string
	width  int
	height int
	ascii  string
	state  func(t *testing.T) State
}

func TestRenderGoldens(t *testing.T) {
	setProfile(t, termenv.Ascii)
	update := os.Getenv("UPDATE_GOLDEN") != ""
	for _, tc := range goldenCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("REXWS_ASCII", tc.ascii)
			s := tc.state(t)
			out := Render(s)
			assertScreen(t, out, tc.width, tc.height)

			path := filepath.Join("testdata", "render", tc.name+".golden")
			if update {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden %s: %v (run UPDATE_GOLDEN=1 to create it)", path, err)
			}
			if string(want) != out {
				t.Errorf("golden %s mismatch\n--- want ---\n%s\n--- got ---\n%s", tc.name, want, out)
			}
		})
	}
}

func goldenDashboard(width, height int, expanded bool) func(*testing.T) State {
	return func(t *testing.T) State {
		s := renderState(t, width, height)
		if expanded {
			s = s.withExpanded("/wt/rexws", true)
		}
		return s
	}
}

func goldenFilteredSearch(width, height int) func(*testing.T) State {
	return func(t *testing.T) State {
		s := renderState(t, width, height)
		s = s.setQuery("tests")
		return s.enterSearch()
	}
}

func goldenStatusModal(width, height int) func(*testing.T) State {
	return func(t *testing.T) State {
		s := renderState(t, width, height)
		s, _ = Reduce(s, runeKey('s'))
		return s
	}
}

func goldenCleanupList(width, height int) func(*testing.T) State {
	return func(t *testing.T) State {
		s := cleanupTestState(t)
		s.width, s.height = width, height
		return s
	}
}

func goldenCleanupConfirm(width, height int) func(*testing.T) State {
	return func(t *testing.T) State {
		s := cleanupTestState(t)
		s.width, s.height = width, height
		s, _ = Reduce(s, runeKey('a'))
		return s.openCleanupConfirm()
	}
}

func goldenHelp(width, height int) func(*testing.T) State {
	return func(t *testing.T) State {
		s := renderState(t, width, height)
		s.mode = ModeHelp
		return s
	}
}

func goldenFilteredContext(width, height int) func(*testing.T) State {
	return func(t *testing.T) State {
		s := renderState(t, width, height)
		return s.setQuery("tests")
	}
}

func goldenHealthLong(width, height int) func(*testing.T) State {
	return func(t *testing.T) State {
		s := renderState(t, width, height)
		s.rexAvailable, s.rexEverLoaded = false, true
		return s.rebuild()
	}
}

func goldenCountsWithQuery(width, height int) func(*testing.T) State {
	return func(t *testing.T) State {
		s := renderState(t, width, height)
		return s.setQuery("m1-m2-core-rex")
	}
}

func goldenPendingSuffix(width, height int) func(*testing.T) State {
	return func(t *testing.T) State {
		s := renderState(t, width, height)
		s.pending = true
		s.pendingLabel = "switching…"
		return s.rebuild()
	}
}

func goldenCleanupApplied(width, height int) func(*testing.T) State {
	return func(t *testing.T) State {
		s := cleanupTestState(t)
		s.width, s.height = width, height
		report := s.cleanup.report
		report.Applied = true
		report.Totals = ops.CleanupTotals{Removed: 1, Kept: 5}
		report.Projects[0].Items[0].Result = ops.CleanupResultRemoved
		for i := 1; i < len(report.Projects[0].Items); i++ {
			report.Projects[0].Items[i].Result = ops.CleanupResultKept
		}
		s.cleanup = s.cleanup.clone()
		s.cleanup.report = report
		s.cleanup.applied = true
		return s.rebuildCleanup()
	}
}

func goldenCleanupDetail(width, height int) func(*testing.T) State {
	return func(t *testing.T) State {
		s := cleanupTestState(t)
		s.width, s.height = width, height
		s = cleanupCursorTo(s, 2) // dirty review item
		s.mode = ModeCleanupDetail
		return s
	}
}

func goldenNewForm(width, height int) func(*testing.T) State {
	return func(t *testing.T) State {
		s := renderState(t, width, height)
		s, _ = Reduce(s, runeKey('n'))
		s.newForm.branch.SetValue("feat/x")
		return s
	}
}

func goldenRemoveModal(width, height int) func(*testing.T) State {
	return func(t *testing.T) State {
		s := renderState(t, width, height)
		s.remove = &removeModal{
			plan: ops.RemovePlan{
				Project:  config.Project{Name: "rexws"},
				Path:     "/wt/rexws",
				Branch:   "m1-m2-core-rex",
				Warnings: []error{errors.New("project bad: missing")},
			},
			project: "rexws",
			branch:  "m1-m2-core-rex",
		}
		s.mode = ModeRemove
		return s
	}
}

func goldenCases() []goldenCase {
	return []goldenCase{
		// Byte-identity guards captured from 6052313 before the 48-column tier.
		{"60x18-dashboard-collapsed", 60, 18, "0", goldenDashboard(60, 18, false)},
		{"60x18-dashboard-expanded", 60, 18, "0", goldenDashboard(60, 18, true)},
		{"60x18-filtered-search", 60, 18, "0", goldenFilteredSearch(60, 18)},
		{"60x18-cleanup-list", 60, 18, "0", goldenCleanupList(60, 18)},
		{"60x44-dashboard", 60, 44, "0", goldenDashboard(60, 44, false)},
		{"80x24-dashboard-collapsed", 80, 24, "0", goldenDashboard(80, 24, false)},
		{"80x24-dashboard-expanded", 80, 24, "0", goldenDashboard(80, 24, true)},
		{"80x24-status-modal", 80, 24, "0", goldenStatusModal(80, 24)},
		{"80x24-cleanup-confirm", 80, 24, "0", goldenCleanupConfirm(80, 24)},
		{"80x44-dashboard", 80, 44, "0", goldenDashboard(80, 44, false)},
		{"120x40-dashboard", 120, 40, "0", goldenDashboard(120, 40, false)},
		{"200x98-dashboard", 200, 98, "0", goldenDashboard(200, 98, false)},

		// The 48-59 tier.
		{"48x44-dashboard-collapsed", 48, 44, "0", goldenDashboard(48, 44, false)},
		{"48x44-dashboard-expanded", 48, 44, "0", goldenDashboard(48, 44, true)},
		{"48x44-filtered-context", 48, 44, "0", goldenFilteredContext(48, 44)},
		{"48x44-health-long", 48, 44, "0", goldenHealthLong(48, 44)},
		{"48x44-counts-with-query", 48, 44, "0", goldenCountsWithQuery(48, 44)},
		{"48x44-pending-suffix", 48, 44, "0", goldenPendingSuffix(48, 44)},
		{"48x18-dashboard-expanded", 48, 18, "0", goldenDashboard(48, 18, true)},
		{"48x18-help", 48, 18, "0", goldenHelp(48, 18)},
		{"48x44-help", 48, 44, "0", goldenHelp(48, 44)},
		{"48x44-cleanup-list", 48, 44, "0", goldenCleanupList(48, 44)},
		{"48x44-cleanup-applied", 48, 44, "0", goldenCleanupApplied(48, 44)},
		{"48x44-cleanup-detail", 48, 44, "0", goldenCleanupDetail(48, 44)},
		{"48x44-cleanup-confirm", 48, 44, "0", goldenCleanupConfirm(48, 44)},
		{"48x44-status-modal", 48, 44, "0", goldenStatusModal(48, 44)},
		{"48x44-new-form", 48, 44, "0", goldenNewForm(48, 44)},
		{"48x44-remove-modal", 48, 44, "0", goldenRemoveModal(48, 44)},

		// ASCII glyph variants of the 48 dashboard and help.
		{"48x44-dashboard-collapsed-ascii", 48, 44, "1", goldenDashboard(48, 44, false)},
		{"48x44-dashboard-expanded-ascii", 48, 44, "1", goldenDashboard(48, 44, true)},
		{"48x44-help-ascii", 48, 44, "1", goldenHelp(48, 44)},
	}
}
