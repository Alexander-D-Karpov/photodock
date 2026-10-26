package develop

import (
	"math"
	"testing"
)

func TestNormalizeCurve(t *testing.T) {
	if normalizeCurve([]Point{{0, 0}, {0.5, 0.5}, {1, 1}}) != nil {
		t.Error("identity curve must normalize to nil")
	}
	got := normalizeCurve([]Point{{1, 1}, {0, 0}, {0.5, 0.6}, {0.505, 0.62}})
	if len(got) != 3 || got[1][0] != 0.505 || got[0][0] != 0 {
		t.Errorf("curve = %v", got)
	}
}

func TestMonotoneSplineInterpolates(t *testing.T) {
	pts := []Point{{0, 0}, {0.25, 0.15}, {0.75, 0.85}, {1, 1}}
	f := monotoneSpline(pts)
	for _, p := range pts {
		if math.Abs(f(p[0])-p[1]) > 1e-9 {
			t.Errorf("f(%v) = %v; want %v", p[0], f(p[0]), p[1])
		}
	}
	prev := -1.0
	for i := 0; i <= 100; i++ {
		v := f(float64(i) / 100)
		if v < prev-1e-12 {
			t.Fatalf("not monotone at %d", i)
		}
		prev = v
	}
}

func TestToneLUTIdentityIsNil(t *testing.T) {
	if buildToneLUT(Tone{Exposure: 1, Shadows: 20}) != nil {
		t.Error("global LUT must be nil when only exposure/local are set")
	}
}

func TestContrastSpreadsTones(t *testing.T) {
	lut := buildToneLUT(Tone{Contrast: 60})
	dark := 0.03 * lut.at(0.03)
	bright := 0.6 * lut.at(0.6)
	if dark >= 0.03 || bright <= 0.6 {
		t.Errorf("contrast: dark %v (<0.03), bright %v (>0.6)", dark, bright)
	}
	grey := 0.18 * lut.at(0.18)
	if math.Abs(float64(grey)-0.18) > 0.01 {
		t.Errorf("mid grey moved to %v", grey)
	}
}

func TestShadowsLiftDarkMore(t *testing.T) {
	img := NewImage(64, 32)
	for y := 0; y < 32; y++ {
		for x := 0; x < 64; x++ {
			v := float32(0.02)
			if x >= 32 {
				v = 0.7
			}
			i := (y*64 + x) * 3
			img.Pix[i], img.Pix[i+1], img.Pix[i+2] = v, v, v
		}
	}
	gains := localGains(img, 80, 0, 64)
	if gains[16*64+2] <= gains[16*64+60] || gains[16*64+2] < 1.5 {
		t.Errorf("shadow gain %v vs highlight gain %v", gains[16*64+2], gains[16*64+60])
	}
}

func TestSaturationZeroIsGrey(t *testing.T) {
	r, g, b := applyColor(0.5, 0.2, 0.1, 0, 0)
	if math.Abs(float64(r-g)) > 1e-6 || math.Abs(float64(g-b)) > 1e-6 {
		t.Errorf("saturation -100 left colour: %v %v %v", r, g, b)
	}
}

func TestStraighten(t *testing.T) {
	src := coordImage(400, 300)
	if src.Straighten(0) != src {
		t.Error("zero angle must be a no-op")
	}
	out := src.Straighten(5)
	if out.W >= 400 || out.H >= 300 {
		t.Fatalf("straighten must auto-crop: %dx%d", out.W, out.H)
	}
	ar := float64(out.W) / float64(out.H)
	if math.Abs(ar-4.0/3.0) > 0.02 {
		t.Errorf("aspect changed: %v", ar)
	}
	cx, cy := at(out, out.W/2, out.H/2)
	if math.Abs(float64(cx)-200) > 2 || math.Abs(float64(cy)-150) > 2 {
		t.Errorf("centre maps to (%d,%d)", cx, cy)
	}
}

func TestWBFromSampleRaster(t *testing.T) {
	x, y := TempTintToXY(3500, 10)
	p := rec2020FromXYZ.Apply(xyToXYZ(x, y))
	wb, err := wbFromSample(p, whiteD65)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(wb.Temp-3500) > 20 || math.Abs(wb.Tint-10) > 1 {
		t.Errorf("sampled %v / %v; want 3500 / 10", wb.Temp, wb.Tint)
	}
}

func TestFastWBNeutralises(t *testing.T) {
	x, y := TempTintToXY(3000, 0)
	illum := xyToXYZ(x, y)
	p := rec2020FromXYZ.Apply(illum)
	m := wbMatrix(illum, whiteD65)
	gx, gy := xyzToXY(xyzFromRec2020.Apply(m.Apply(p)))
	if math.Abs(gx-0.3127) > 1e-3 || math.Abs(gy-0.3290) > 1e-3 {
		t.Errorf("illuminant not neutralised: (%v, %v)", gx, gy)
	}
}

// TestNewFieldsKeepOldHashes guards that Ф1 fields are neutral at zero, so older recipes hash identically.
func TestNewFieldsKeepOldHashes(t *testing.T) {
	r, _ := ParseRecipe([]byte(`{"geometry":{"rotate":90},"tone":{"exposure":0.3}}`))
	res, _ := Resolve(r, 1)
	h, _ := RenderHash("fp", res, "lr")
	r2, _ := ParseRecipe([]byte(`{"geometry":{"rotate":90,"angle":0},"tone":{"exposure":0.3,"contrast":0,"curve":[[0,0],[1,1]]},"color":{}}`))
	res2, _ := Resolve(r2, 1)
	if h2, _ := RenderHash("fp", res2, "lr"); h2 != h {
		t.Errorf("neutral new fields changed hash: %s vs %s", h, h2)
	}
}
