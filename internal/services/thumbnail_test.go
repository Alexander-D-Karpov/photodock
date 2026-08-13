package services

import "testing"

func TestPlaceholderDims(t *testing.T) {
	cases := []struct {
		w, h         int
		wantW, wantH int
	}{
		{4000, 3000, 64, 48}, // landscape 4:3
		{3000, 4000, 48, 64}, // portrait 3:4
		{6024, 4024, 64, 43}, // Sony ARW landscape
		{4024, 6024, 43, 64}, // rotated portrait
		{1000, 1000, 64, 64}, // square
		{6400, 100, 64, 1},   // extreme wide clamps to >=1
		{0, 0, 32, 32},       // unknown dims fall back to square
		{-5, 10, 32, 32},     // invalid dims fall back
	}
	for _, c := range cases {
		gotW, gotH := placeholderDims(c.w, c.h)
		if gotW != c.wantW || gotH != c.wantH {
			t.Errorf("placeholderDims(%d,%d) = %dx%d; want %dx%d", c.w, c.h, gotW, gotH, c.wantW, c.wantH)
		}
		// Placeholder must preserve the photo's orientation (never square-ify a
		// non-square photo), which is the whole point of the fix.
		if c.w > 0 && c.h > 0 && c.w != c.h {
			photoLandscape := c.w > c.h
			phLandscape := gotW > gotH
			if photoLandscape != phLandscape {
				t.Errorf("placeholderDims(%d,%d) = %dx%d flipped orientation", c.w, c.h, gotW, gotH)
			}
		}
	}
}
