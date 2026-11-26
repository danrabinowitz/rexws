package tui

// ClampCursor selects key if it is still visible, otherwise clamps the
// previous row index into the new row list and scans forward, then backward,
// for a selectable row. It returns the selected key and its row index, or a
// zero key and -1 when nothing is selectable.
func ClampCursor(rows []Row, key RowKey, previous int) (RowKey, int) {
	if len(rows) == 0 {
		return RowKey{}, -1
	}
	for i, row := range rows {
		if row.Selectable() && row.Key == key {
			return key, i
		}
	}

	start := previous
	if start < 0 {
		start = 0
	}
	if start >= len(rows) {
		start = len(rows) - 1
	}
	for i := start; i < len(rows); i++ {
		if rows[i].Selectable() {
			return rows[i].Key, i
		}
	}
	for i := start - 1; i >= 0; i-- {
		if rows[i].Selectable() {
			return rows[i].Key, i
		}
	}
	return RowKey{}, -1
}

// MoveCursor moves the selection by delta selectable rows. A single-step move
// wraps: forward from the last selectable row selects the first, and backward
// from the first selects the last, crossing group headers without selecting
// them. Larger moves stop at the edges. delta may be negative. When there is
// no selection, it selects the first selectable row for a forward move and the
// last for a backward move.
func MoveCursor(rows []Row, current, delta int) (RowKey, int) {
	return moveCursor(rows, current, delta, delta == 1 || delta == -1)
}

// moveCursor implements MoveCursor. When wrap is set, a step that runs past the
// first or last selectable row continues from the opposite end. The starting
// row is never a wrap target, so an existing selection with fewer than two
// selectable rows does not move.
func moveCursor(rows []Row, current, delta int, wrap bool) (RowKey, int) {
	if delta == 0 || len(rows) == 0 {
		return ClampCursor(rows, RowKey{}, current)
	}
	step := 1
	if delta < 0 {
		step = -1
		delta = -delta
	}
	index := current
	if index < 0 || index >= len(rows) || !rows[index].Selectable() {
		index = -1
	}
	for n := 0; n < delta; n++ {
		next := -1
		if index < 0 {
			if step > 0 {
				for i := 0; i < len(rows); i++ {
					if rows[i].Selectable() {
						next = i
						break
					}
				}
			} else {
				for i := len(rows) - 1; i >= 0; i-- {
					if rows[i].Selectable() {
						next = i
						break
					}
				}
			}
		} else {
			for i := index + step; i >= 0 && i < len(rows); i += step {
				if rows[i].Selectable() {
					next = i
					break
				}
			}
			if next < 0 && wrap {
				next = wrapIndex(rows, index, step)
			}
		}
		if next < 0 {
			break
		}
		index = next
	}
	if index < 0 {
		return RowKey{}, -1
	}
	return rows[index].Key, index
}

// wrapIndex returns the first selectable row for a forward step and the last
// for a backward step, scanning the side opposite index. It returns -1 when
// index is the only selectable row.
func wrapIndex(rows []Row, index, step int) int {
	if step > 0 {
		for i := 0; i < index; i++ {
			if rows[i].Selectable() {
				return i
			}
		}
		return -1
	}
	for i := len(rows) - 1; i > index; i-- {
		if rows[i].Selectable() {
			return i
		}
	}
	return -1
}

// FirstChild returns the first visible child row after parentIndex.
func FirstChild(rows []Row, parentIndex int) (RowKey, int, bool) {
	if parentIndex < 0 || parentIndex >= len(rows) || rows[parentIndex].Kind != RowWorkstream {
		return RowKey{}, -1, false
	}
	path := rows[parentIndex].Path
	for i := parentIndex + 1; i < len(rows); i++ {
		row := rows[i]
		if row.Kind == RowWindow && row.Path == path {
			return row.Key, i, true
		}
		if row.Kind != RowWindow {
			break
		}
	}
	return RowKey{}, -1, false
}

// ParentIndex returns the workstream row that owns a child row.
func ParentIndex(rows []Row, childIndex int) (RowKey, int, bool) {
	if childIndex < 0 || childIndex >= len(rows) || rows[childIndex].Kind != RowWindow {
		return RowKey{}, -1, false
	}
	path := rows[childIndex].Path
	for i := childIndex - 1; i >= 0; i-- {
		row := rows[i]
		if row.Kind == RowWorkstream && row.Path == path {
			return row.Key, i, true
		}
		if row.Kind != RowWindow {
			break
		}
	}
	return RowKey{}, -1, false
}
