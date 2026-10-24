package develop

import (
	"math"
	"testing"
)

func TestStagedIDPattern(t *testing.T) {
	for _, ok := range []string{"1730000000000000000-a1b2c3d4", "12-0f"} {
		if !stagedIDPattern.MatchString(ok) {
			t.Errorf("%q must be valid", ok)
		}
	}
	for _, bad := range []string{"", "../x", "abc", "1-XYZ", "1-ab/c"} {
		if stagedIDPattern.MatchString(bad) {
			t.Errorf("%q must be rejected", bad)
		}
	}
}

func TestRotateRecipeRoundTrip(t *testing.T) {
	r := Recipe{
		Geometry: Geometry{Rotate: 90, Crop: &Rect{X: 0.1, Y: 0.2, W: 0.5, H: 0.3}},
		Masks: []Mask{
			{Type: "linear", X0: 0.2, Y0: 0.1, X1: 0.6, Y1: 0.7},
			{Type: "radial", CX: 0.3, CY: 0.4, RX: 0.2, RY: 0.1, Angle: 30},
		},
	}
	back := RotateRecipe(RotateRecipe(r, 90), -90)
	if back.Geometry.Rotate != 90 {
		t.Errorf("rotate = %d", back.Geometry.Rotate)
	}
	c := back.Geometry.Crop
	if math.Abs(c.X-0.1) > 1e-9 || math.Abs(c.Y-0.2) > 1e-9 || math.Abs(c.W-0.5) > 1e-9 || math.Abs(c.H-0.3) > 1e-9 {
		t.Errorf("crop = %+v", c)
	}
	if m := back.Masks[0]; math.Abs(m.X0-0.2) > 1e-9 || math.Abs(m.Y1-0.7) > 1e-9 {
		t.Errorf("linear = %+v", m)
	}
	if m := back.Masks[1]; math.Abs(m.CX-0.3) > 1e-9 || math.Abs(m.Angle-30) > 1e-9 {
		t.Errorf("radial = %+v", m)
	}
	if r.Masks[1].Angle != 30 {
		t.Error("RotateRecipe must not mutate the input masks")
	}
}

func TestRotateRecipeWithSingleFlipInverts(t *testing.T) {
	r := RotateRecipe(Recipe{Geometry: Geometry{FlipH: true}}, 90)
	if r.Geometry.Rotate != 270 {
		t.Errorf("visual cw with one flip must store 270, got %d", r.Geometry.Rotate)
	}
}
