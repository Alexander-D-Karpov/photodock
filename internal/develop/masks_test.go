package develop

import (
	"math"
	"testing"
)

func testSpace(w, h float64) *maskSpace {
	return &maskSpace{sx: 1, sy: 1, frameW: w, frameH: h}
}

func TestLinearMaskWeights(t *testing.T) {
	evs := prepareMasks([]Mask{{Type: "linear", X0: 0.5, Y0: 0.2, X1: 0.5, Y1: 0.6, Adjust: MaskAdjust{Exposure: 1}}}, testSpace(100, 100))
	if len(evs) != 1 {
		t.Fatal("mask not prepared")
	}
	m := &evs[0]
	if w := m.weight(10, 10); w != 1 {
		t.Errorf("before start = %v; want 1", w)
	}
	if w := m.weight(50, 40); math.Abs(w-0.5) > 1e-9 {
		t.Errorf("midpoint = %v; want 0.5", w)
	}
	if w := m.weight(90, 80); w != 0 {
		t.Errorf("after end = %v; want 0", w)
	}
}

func TestRadialMaskWeightsAndInvert(t *testing.T) {
	sp := testSpace(200, 100)
	in := prepareMasks([]Mask{{Type: "radial", CX: 0.5, CY: 0.5, RX: 0.2, RY: 0.1, Feather: 50, Adjust: MaskAdjust{Exposure: 1}}}, sp)[0]
	if w := in.weight(100, 50); w != 1 {
		t.Errorf("centre = %v", w)
	}
	if w := in.weight(100+41, 50); w != 0 { // rx = 0.2 × long side 200 = 40 px
		t.Errorf("outside along x = %v", w)
	}
	if w := in.weight(100+30, 50); w <= 0 || w >= 1 {
		t.Errorf("feather zone = %v", w)
	}
	inv := prepareMasks([]Mask{{Type: "radial", CX: 0.5, CY: 0.5, RX: 0.2, RY: 0.1, Invert: true, Adjust: MaskAdjust{Exposure: 1}}}, sp)[0]
	if w := inv.weight(100, 50); w != 0 {
		t.Errorf("inverted centre = %v", w)
	}
}

func TestRadialMaskRotation(t *testing.T) {
	m := prepareMasks([]Mask{{Type: "radial", CX: 0.5, CY: 0.5, RX: 0.3, RY: 0.05, Angle: 90, Adjust: MaskAdjust{Exposure: 1}}}, testSpace(100, 100))[0]
	if w := m.weight(50, 75); w != 1 {
		t.Errorf("rotated long axis = %v", w)
	}
	if w := m.weight(75, 50); w != 0 {
		t.Errorf("rotated short axis = %v", w)
	}
}

func TestNeutralMasksSkipped(t *testing.T) {
	if evs := prepareMasks([]Mask{{Type: "radial", RX: 0.1, RY: 0.1}, {Type: "linear", Off: true, Y1: 1, Adjust: MaskAdjust{Exposure: 1}}}, testSpace(10, 10)); len(evs) != 0 {
		t.Errorf("expected no active masks, got %d", len(evs))
	}
}

func TestApplyMasksExposureOnlyInside(t *testing.T) {
	img := NewImage(20, 10)
	for i := range img.Pix {
		img.Pix[i] = 0.1
	}
	plan := newGeomPlan(20, 10, 1, Geometry{}, nil)
	masks := []Mask{{Type: "linear", X0: 0, Y0: 0.5, X1: 0.5, Y1: 0.5, Adjust: MaskAdjust{Exposure: 1}}}
	applyMasks(img, masks, renderCtx{frameLong: 20, space: plan.space(0, 0, 1, 1)})
	if left := img.Pix[(5*20+0)*3]; math.Abs(float64(left)-0.2) > 0.01 {
		t.Errorf("left edge = %v; want ~0.2", left)
	}
	if right := img.Pix[(5*20+19)*3]; math.Abs(float64(right)-0.1) > 1e-6 {
		t.Errorf("right edge = %v; want 0.1", right)
	}
}

func TestNormalizeMasks(t *testing.T) {
	got, err := normalizeMasks([]Mask{
		{ID: "Bad ID!", Type: "RADIAL", CX: 3, RX: 0, RY: 9, Angle: 270, Feather: 120, X0: 0.3},
		{ID: "a1", Type: "linear", X0: 0.2, Y0: 0.2, X1: 0.2, Y1: 0.2, Adjust: MaskAdjust{Exposure: 9, Contrast: 400}},
	})
	if err != nil {
		t.Fatal(err)
	}
	r := got[0]
	if r.ID != "m0" || r.Type != "radial" || r.CX != maskCoordLimit || r.RX != 0.005 || r.RY != 2 || r.Angle != -90 || r.Feather != 100 || r.X0 != 0 {
		t.Errorf("radial = %+v", r)
	}
	l := got[1]
	if l.Y1 == l.Y0 || l.Adjust.Exposure != 4 || l.Adjust.Contrast != 100 {
		t.Errorf("linear = %+v", l)
	}
	if _, err := normalizeMasks(make([]Mask, maxMasks+1)); err == nil {
		t.Error("too many masks must fail")
	}
	if _, err := normalizeMasks([]Mask{{Type: "brush"}}); err == nil {
		t.Error("unknown type must fail")
	}
}

func TestEmptyMasksKeepHash(t *testing.T) {
	a, _ := Resolve(Recipe{}, 1)
	b, _ := Resolve(Recipe{Masks: []Mask{}}, 1)
	ha, _ := RenderHash("fp", a, "lr")
	hb, _ := RenderHash("fp", b, "lr")
	if ha != hb {
		t.Error("empty mask list must not change the hash")
	}
}

// TestMaskTileMatchesFull checks that a 1:1 tile places a mask exactly where
// the full render does.
func TestMaskTileMatchesFull(t *testing.T) {
	src := NewImage(60, 40)
	for i := range src.Pix {
		src.Pix[i] = 0.1
	}
	rec := Recipe{
		Geometry: Geometry{Crop: &Rect{X: 0.1, Y: 0.1, W: 0.8, H: 0.8}},
		Masks:    []Mask{{Type: "radial", CX: 0.4, CY: 0.5, RX: 0.2, RY: 0.15, Feather: 60, Adjust: MaskAdjust{Exposure: 1}}},
	}
	plan := newGeomPlan(60, 40, 1, rec.Geometry, nil)
	w, h := plan.size(1)
	full := warp(src, plan, 1, 0, 0, w, h)
	applyTone(full, full, rec, nil, renderCtx{frameLong: float64(max(w, h)), space: plan.space(0, 0, 1, 1)})
	tile := warp(src, plan, 1, 10, 6, 20, 15)
	applyTone(tile, tile, rec, nil, renderCtx{frameLong: float64(max(w, h)), space: plan.space(10, 6, 1, 1)})
	sameImage(t, "mask tile", tile, full.cropPx(10, 6, 20, 15))
}
