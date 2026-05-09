package repl

import "testing"

// All four pixels equal → solid block, fg = that colour, mask = 1111.
func TestBestQuarterCellSolid(t *testing.T) {
	c := [3]byte{120, 200, 50}
	mask, fg, _ := bestQuarterCell([4][3]byte{c, c, c, c})
	if mask != 0b1111 {
		t.Errorf("solid mask = %04b, want 1111", mask)
	}
	if fg != c {
		t.Errorf("solid fg = %v, want %v", fg, c)
	}
}

// Top half pure white, bottom half pure black → top/bottom partition wins
// with zero variance. Mask should describe top quadrants as one group.
func TestBestQuarterCellTopBottom(t *testing.T) {
	white := [3]byte{255, 255, 255}
	black := [3]byte{0, 0, 0}
	mask, fg, bg := bestQuarterCell([4][3]byte{white, white, black, black})
	// quarterPartitions includes 0b0011 (top vs bottom). Score = 0 here.
	// The picker may select either side as fg; verify the mask is one of
	// the two equivalent representations (0b0011 or 0b1100) and colours
	// match accordingly.
	switch mask {
	case 0b0011:
		if fg != white || bg != black {
			t.Errorf("mask=0011 expected fg=white bg=black; got fg=%v bg=%v", fg, bg)
		}
	case 0b1100:
		if fg != black || bg != white {
			t.Errorf("mask=1100 expected fg=black bg=white; got fg=%v bg=%v", fg, bg)
		}
	default:
		t.Errorf("expected top/bottom partition (0011 or 1100), got %04b", mask)
	}
}

// Diagonal split: TL+BR red, TR+BL blue. Should prefer the diagonal partition.
func TestBestQuarterCellDiagonal(t *testing.T) {
	red := [3]byte{255, 0, 0}
	blue := [3]byte{0, 0, 255}
	mask, _, _ := bestQuarterCell([4][3]byte{red, blue, blue, red})
	// 0b1001 = {TL, BR} group; 0b0110 = {TR, BL} group. Either is valid.
	if mask != 0b1001 && mask != 0b0110 {
		t.Errorf("expected diagonal split (1001 or 0110), got %04b", mask)
	}
}

// computeFit must (a) respect cell-budget bounds and (b) preserve the
// source aspect ratio on screen, accounting for cellAspectRatio.
func TestComputeFitFitsBudget(t *testing.T) {
	// 1024×768 source into a 200-cell wide × 60-row terminal.
	cellsW, cellsH := computeFit(1024, 768, 200, 60)
	if cellsW > 200 || cellsH > 60 {
		t.Errorf("cells=%dx%d exceeds budget (200×60)", cellsW, cellsH)
	}
	// On screen the rendered dimensions are cellsW × cellsH*R char-widths.
	// That ratio must match srcW/srcH within rounding error.
	srcAR := 1024.0 / 768.0
	screenAR := float64(cellsW) / (float64(cellsH) * cellAspectRatio)
	if screenAR < srcAR*0.97 || screenAR > srcAR*1.03 {
		t.Errorf("screen AR drift: src=%.3f screen=%.3f", srcAR, screenAR)
	}
}

// Quarter-block fit on a wide terminal should use width-bounded scaling
// when the source is wider than tall, and uses all available rows.
func TestComputeFitWideTerminal(t *testing.T) {
	// 1024×768 src, 80×24 terminal — a small terminal where rows are tight.
	cellsW, cellsH := computeFit(1024, 768, 80, 24)
	// We expect to be row-bounded (24 rows × 2 R × 1024/768 = 64 cellsW).
	if cellsW != 64 {
		t.Errorf("cellsW = %d, want 64 (row-bounded fit)", cellsW)
	}
	if cellsH != 24 {
		t.Errorf("cellsH = %d, want 24 (uses all rows)", cellsH)
	}
}

// Mouse mapping inverts the render layout.
func TestMouseToSrcPixel(t *testing.T) {
	layout := blockLayout{
		srcW: 1024, srcH: 768,
		leftPadCells: 10, topPadCells: 5,
		cellSrcWidth: 8.0, cellSrcHeight: 16.0,
	}
	// Click at cell (50, 25): rel cell = (40, 20) → src ≈ (40+0.5)*8, (20+0.5)*16
	x, y, ok := mouseToSrcPixel(50, 25, layout)
	if !ok {
		t.Fatal("expected ok")
	}
	wantX, wantY := 324, 328
	if x != wantX || y != wantY {
		t.Errorf("mouse map = (%d,%d), want (%d,%d)", x, y, wantX, wantY)
	}

	// Click in the letterbox margin → not ok.
	if _, _, ok := mouseToSrcPixel(2, 2, layout); ok {
		t.Error("expected !ok in left/top margin")
	}
}

func TestBuildIndexMap(t *testing.T) {
	m := buildIndexMap(100, 10)
	if len(m) != 10 {
		t.Fatalf("len = %d, want 10", len(m))
	}
	for i, v := range m {
		if v < 0 || v >= 100 {
			t.Errorf("m[%d] = %d out of range", i, v)
		}
		if i > 0 && v < m[i-1] {
			t.Errorf("non-monotonic at %d: %d < %d", i, v, m[i-1])
		}
	}
	m = buildIndexMap(3, 9)
	if len(m) != 9 {
		t.Fatalf("len = %d, want 9", len(m))
	}
	for i, v := range m {
		if v < 0 || v >= 3 {
			t.Errorf("upsampled m[%d] = %d out of range", i, v)
		}
	}
}

func TestBuildIndexMapZeroDst(t *testing.T) {
	m := buildIndexMap(100, 0)
	if len(m) != 0 {
		t.Errorf("zero dst: len = %d, want 0", len(m))
	}
}

func TestBuildIndexMapSameSize(t *testing.T) {
	m := buildIndexMap(5, 5)
	for i, v := range m {
		if v != i {
			t.Errorf("same-size map[%d] = %d, want %d", i, v, i)
		}
	}
}
