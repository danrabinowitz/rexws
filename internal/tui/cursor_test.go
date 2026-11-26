package tui

import "testing"

func cursorRows() []Row {
	return []Row{
		{Kind: RowGroup, Key: RowKey{Kind: RowGroup, ID: "g1"}},
		{Kind: RowWorkstream, Key: RowKey{Kind: RowWorkstream, ID: "a"}, Path: "a"},
		{Kind: RowWindow, Key: RowKey{Kind: RowWindow, ID: "wa"}, Path: "a"},
		{Kind: RowWorkstream, Key: RowKey{Kind: RowWorkstream, ID: "b"}, Path: "b"},
		{Kind: RowGroup, Key: RowKey{Kind: RowGroup, ID: "g2"}},
	}
}

func TestClampCursorKeepsVisibleKey(t *testing.T) {
	rows := cursorRows()
	key, index := ClampCursor(rows, RowKey{Kind: RowWindow, ID: "wa"}, 0)
	if key.ID != "wa" || index != 2 {
		t.Errorf("clamp = (%#v, %d)", key, index)
	}
}

func TestClampCursorScansForwardThenBackward(t *testing.T) {
	rows := cursorRows()
	// Previous index is a group at the end; forward fails, backward finds b.
	key, index := ClampCursor(rows, RowKey{Kind: RowWorkstream, ID: "gone"}, 4)
	if key.ID != "b" || index != 3 {
		t.Errorf("clamp = (%#v, %d)", key, index)
	}
	// Previous index is the first group; forward finds a.
	key, index = ClampCursor(rows, RowKey{Kind: RowWorkstream, ID: "gone"}, 0)
	if key.ID != "a" || index != 1 {
		t.Errorf("clamp = (%#v, %d)", key, index)
	}
}

func TestClampCursorNoSelectableRows(t *testing.T) {
	rows := []Row{{Kind: RowGroup}, {Kind: RowGroup}}
	key, index := ClampCursor(rows, RowKey{}, 0)
	if key != (RowKey{}) || index != -1 {
		t.Errorf("clamp = (%#v, %d)", key, index)
	}
	if key, index := ClampCursor(nil, RowKey{}, -1); key != (RowKey{}) || index != -1 {
		t.Errorf("empty clamp = (%#v, %d)", key, index)
	}
}

func moveRows() []Row {
	return []Row{
		{Kind: RowGroup, Key: RowKey{Kind: RowGroup, ID: "g1"}},
		{Kind: RowWorkstream, Key: RowKey{Kind: RowWorkstream, ID: "a"}, Path: "a"},
		{Kind: RowGroup, Key: RowKey{Kind: RowGroup, ID: "g2"}},
		{Kind: RowWorkstream, Key: RowKey{Kind: RowWorkstream, ID: "b"}, Path: "b"},
		{Kind: RowGroup, Key: RowKey{Kind: RowGroup, ID: "g3"}},
	}
}

func TestClampCursorDoesNotWrap(t *testing.T) {
	rows := moveRows()
	// The previous row is the trailing group and the key is gone, so the
	// forward scan finds nothing. The backward scan selects b at the end
	// rather than wrapping around to a.
	key, index := ClampCursor(rows, RowKey{Kind: RowWorkstream, ID: "gone"}, 4)
	if key.ID != "b" || index != 3 {
		t.Errorf("clamp = (%#v, %d)", key, index)
	}
}

func TestMoveCursorSingleStepWrapsAndSkipsGroups(t *testing.T) {
	rows := moveRows()
	tests := []struct {
		name    string
		current int
		delta   int
		wantID  string
		wantRow int
	}{
		{"down skips the group header", 1, 1, "b", 3},
		{"down wraps to the first row", 3, 1, "a", 1},
		{"up skips the group header", 3, -1, "a", 1},
		{"up wraps to the last row", 1, -1, "b", 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, index := MoveCursor(rows, tt.current, tt.delta)
			if key.ID != tt.wantID || index != tt.wantRow {
				t.Errorf("MoveCursor(%d, %d) = (%#v, %d), want (%q, %d)",
					tt.current, tt.delta, key, index, tt.wantID, tt.wantRow)
			}
		})
	}
}

func TestMoveCursorWrapLandsOnListEdges(t *testing.T) {
	// Selectable rows sit at index 0 and at the last index.
	rows := []Row{
		{Kind: RowWorkstream, Key: RowKey{Kind: RowWorkstream, ID: "a"}, Path: "a"},
		{Kind: RowGroup, Key: RowKey{Kind: RowGroup, ID: "g"}},
		{Kind: RowWorkstream, Key: RowKey{Kind: RowWorkstream, ID: "b"}, Path: "b"},
	}
	key, index := MoveCursor(rows, 2, 1)
	if key.ID != "a" || index != 0 {
		t.Errorf("down wrap = (%#v, %d), want a at 0", key, index)
	}
	key, index = MoveCursor(rows, 0, -1)
	if key.ID != "b" || index != 2 {
		t.Errorf("up wrap = (%#v, %d), want b at 2", key, index)
	}
}

func TestMoveCursorWrapNeedsTwoSelectableRows(t *testing.T) {
	group := Row{Kind: RowGroup, Key: RowKey{Kind: RowGroup, ID: "g"}}
	workstream := func(id string) Row {
		return Row{Kind: RowWorkstream, Key: RowKey{Kind: RowWorkstream, ID: id}, Path: id}
	}
	tests := []struct {
		name    string
		rows    []Row
		current int
		delta   int
		wantID  string
		wantRow int
	}{
		{"zero rows", nil, -1, 1, "", -1},
		{"headers only", []Row{group, group}, 0, 1, "", -1},
		{"one row down stays", []Row{group, workstream("a")}, 1, 1, "a", 1},
		{"one row up stays", []Row{group, workstream("a")}, 1, -1, "a", 1},
		{"two rows down wraps", []Row{group, workstream("a"), group, workstream("b")}, 3, 1, "a", 1},
		{"two rows up wraps", []Row{group, workstream("a"), group, workstream("b")}, 1, -1, "b", 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, index := MoveCursor(tt.rows, tt.current, tt.delta)
			if key.ID != tt.wantID || index != tt.wantRow {
				t.Errorf("MoveCursor(%d, %d) = (%#v, %d), want (%q, %d)",
					tt.current, tt.delta, key, index, tt.wantID, tt.wantRow)
			}
		})
	}
}

func TestMoveCursorClampedDoesNotWrap(t *testing.T) {
	rows := moveRows()
	key, index := moveCursor(rows, 3, 1, false)
	if key.ID != "b" || index != 3 {
		t.Errorf("forward at the edge = (%#v, %d)", key, index)
	}
	key, index = moveCursor(rows, 1, -1, false)
	if key.ID != "a" || index != 1 {
		t.Errorf("backward at the edge = (%#v, %d)", key, index)
	}
}

func TestMoveCursorWithoutSelection(t *testing.T) {
	rows := moveRows()
	key, index := MoveCursor(rows, -1, 1)
	if key.ID != "a" || index != 1 {
		t.Errorf("forward = (%#v, %d)", key, index)
	}
	key, index = MoveCursor(rows, -1, -1)
	if key.ID != "b" || index != 3 {
		t.Errorf("backward = (%#v, %d)", key, index)
	}
}

func TestMoveCursorPageStopsAtEdges(t *testing.T) {
	rows := cursorRows()
	key, index := MoveCursor(rows, 1, 10)
	if key.ID != "b" || index != 3 {
		t.Errorf("page down = (%#v, %d)", key, index)
	}
	key, index = MoveCursor(rows, 3, -10)
	if key.ID != "a" || index != 1 {
		t.Errorf("page up = (%#v, %d)", key, index)
	}
}

func TestFirstChildAndParent(t *testing.T) {
	rows := cursorRows()
	key, index, ok := FirstChild(rows, 1)
	if !ok || key.ID != "wa" || index != 2 {
		t.Errorf("first child = (%#v, %d, %v)", key, index, ok)
	}
	if _, _, ok := FirstChild(rows, 2); ok {
		t.Error("window has a first child")
	}
	key, index, ok = ParentIndex(rows, 2)
	if !ok || key.ID != "a" || index != 1 {
		t.Errorf("parent = (%#v, %d, %v)", key, index, ok)
	}
	if _, _, ok := ParentIndex(rows, 1); ok {
		t.Error("workstream has a parent row")
	}
}
