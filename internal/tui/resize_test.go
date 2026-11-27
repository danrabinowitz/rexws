package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestWindowResizeUsesSharedSearchInputWidth(t *testing.T) {
	m := NewModel()
	defer m.Close()
	m.state = renderState(t, 80, 24)

	m.Update(tea.WindowSizeMsg{Width: 48, Height: 18})

	want := searchInputWidth(48)
	if want != 10 {
		t.Fatalf("searchInputWidth(48) = %d, want 10", want)
	}
	if got := m.state.input.Width; got != want {
		t.Errorf("resize input width = %d, want %d", got, want)
	}
}
