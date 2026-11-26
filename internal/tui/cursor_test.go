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

func TestMoveCursorSkipsGroupsAndDoesNotWrap(t *testing.T) {
	rows := cursorRows()
	key, index := MoveCursor(rows, 1, 1)
	if key.ID != "wa" || index != 2 {
		t.Errorf("down = (%#v, %d)", key, index)
	}
	key, index = MoveCursor(rows, 3, 1)
	if key.ID != "b" || index != 3 {
		t.Errorf("no wrap = (%#v, %d)", key, index)
	}
	key, index = MoveCursor(rows, 1, -1)
	if key.ID != "a" || index != 1 {
		t.Errorf("up at first = (%#v, %d)", key, index)
	}
	key, index = MoveCursor(rows, 2, -1)
	if key.ID != "a" || index != 1 {
		t.Errorf("up skips group = (%#v, %d)", key, index)
	}
}

func TestMoveCursorWithoutSelection(t *testing.T) {
	rows := cursorRows()
	key, index := MoveCursor(rows, -1, 1)
	if key.ID != "a" || index != 1 {
		t.Errorf("forward = (%#v, %d)", key, index)
	}
	key, index = MoveCursor(rows, -1, -1)
	if key.ID != "b" || index != 3 {
		t.Errorf("backward = (%#v, %d)", key, index)
	}
}

func TestMoveCursorPage(t *testing.T) {
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
