package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/danrabinowitz/rexws/internal/git"
	"github.com/danrabinowitz/rexws/internal/ops"
)

func TestInfoNoticeLosesBannerToError(t *testing.T) {
	s := newState()
	s = s.addInfo("retained branch")
	s = s.addError("provider remove failed")
	shown, _, ok := s.bannerNotice(time.Now())
	if !ok || shown.text != "provider remove failed" || shown.info {
		t.Fatalf("banner = %#v", shown)
	}
}

func TestInfoNoticeShownAlone(t *testing.T) {
	s := newState()
	s = s.addInfo("retained branch")
	shown, _, ok := s.bannerNotice(time.Now())
	if !ok || shown.text != "retained branch" || !shown.info {
		t.Fatalf("banner = %#v", shown)
	}
}

func TestPersistentWarningBeatsInfo(t *testing.T) {
	s := newState()
	s = s.addInfo("retained branch")
	s = s.setPersistentWarnings([]persistentWarning{{text: "ambiguous session"}})
	shown, _, ok := s.bannerNotice(time.Now())
	if !ok || !shown.persistent || !strings.Contains(shown.text, "ambiguous session") {
		t.Fatalf("banner = %#v", shown)
	}
}

func TestWarningNoticeRanking(t *testing.T) {
	s := newState()
	s = s.addInfo("retained branch")
	s = s.addWarning("leftover directory")
	shown, _, ok := s.bannerNotice(time.Now())
	if !ok || !shown.warning || shown.text != "leftover directory" {
		t.Fatalf("warning did not beat info: %#v", shown)
	}
	s = s.addError("provider failed")
	shown, _, ok = s.bannerNotice(time.Now())
	if !ok || shown.warning || shown.text != "provider failed" {
		t.Fatalf("error did not beat warning: %#v", shown)
	}
}

func TestRetainedBranchNotice(t *testing.T) {
	cases := []struct {
		name   string
		branch ops.RetainedBranch
		want   []string
	}{
		{
			name:   "merged",
			branch: ops.RetainedBranch{Branch: "feat/x", State: ops.CleanupBranchMerged, DefaultBranch: "main"},
			want:   []string{"Retained branch feat/x", "merged into main", "no unmerged commits"},
		},
		{
			name:   "patch-equivalent",
			branch: ops.RetainedBranch{Branch: "feat/x", State: ops.CleanupBranchPatchEquivalent, DefaultBranch: "main"},
			want:   []string{"Retained branch feat/x", "patch-equivalent to main"},
		},
		{
			name:   "unmerged",
			branch: ops.RetainedBranch{Branch: "feat/x", State: ops.CleanupBranchUnmerged, DefaultBranch: "main", Commits: []git.Commit{{SHA: "a"}, {SHA: "b"}}},
			want:   []string{"Retained branch feat/x", "unmerged, 2 commit(s) not in main"},
		},
		{
			name:   "unknown",
			branch: ops.RetainedBranch{Branch: "feat/x", Error: "boom"},
			want:   []string{"Retained branch feat/x", "state unknown (boom)"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := retainedBranchNotice(&tc.branch)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("notice = %q, want %q", got, want)
				}
			}
		})
	}
}
