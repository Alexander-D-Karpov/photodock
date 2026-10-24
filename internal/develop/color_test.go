package develop

import (
	"math"
	"math/rand"
	"testing"
)

func TestMatrixInverse(t *testing.T) {
	m := xyzFromRec2020.Mul(rec2020FromXYZ)
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			want := 0.0
			if i == j {
				want = 1
			}
			if math.Abs(m[i][j]-want) > 1e-6 {
				t.Fatalf("m[%d][%d] = %v", i, j, m[i][j])
			}
		}
	}
}

func TestSRGBRoundTrip(t *testing.T) {
	for i := 0; i <= 100; i++ {
		v := float64(i) / 100
		if got := srgbEncode(srgbDecode(v)); math.Abs(got-v) > 1e-9 {
			t.Fatalf("roundtrip %v -> %v", v, got)
		}
	}
}

func TestPlanckNearD65(t *testing.T) {
	x, y := planckXY(6504)
	if math.Abs(x-0.3127) > 0.003 || math.Abs(y-0.3290) > 0.008 {
		t.Errorf("planck(6504) = (%v, %v)", x, y)
	}
}

func TestTempTintRoundTrip(t *testing.T) {
	for _, temp := range []float64{2500, 3200, 4500, 5500, 6500, 8000, 12000} {
		for _, tint := range []float64{-60, 0, 60} {
			x, y := TempTintToXY(temp, tint)
			gt, gtint := XYToTempTint(x, y)
			if math.Abs(gt-temp)/temp > 0.005 {
				t.Errorf("temp %v tint %v -> temp %v", temp, tint, gt)
			}
			if math.Abs(gtint-tint) > 0.5 {
				t.Errorf("temp %v tint %v -> tint %v", temp, tint, gtint)
			}
		}
	}
}

// Tint names the illuminant correction like Lightroom: positive tint assumes
// a greener (above-locus) light, so the corrected image turns magenta.
func TestTintDirection(t *testing.T) {
	_, v0 := xyToUV(TempTintToXY(5000, 0))
	_, vGreen := xyToUV(TempTintToXY(5000, 50))
	if vGreen <= v0 {
		t.Errorf("positive tint must move above the locus (green illuminant): v %v -> %v", v0, vGreen)
	}
	for _, c := range []struct {
		tint    float64
		magenta bool
	}{{50, true}, {-50, false}} {
		src := NewImage(1, 1)
		src.Pix[0], src.Pix[1], src.Pix[2] = 0.18, 0.18, 0.18
		dst := NewImage(1, 1)
		r := Recipe{WB: WBParams{Mode: WBCustom, Temp: 6504, Tint: c.tint}}
		applyTone(dst, src, r, rasterWB(r, false), renderCtx{})
		o := toSRGB8(dst).Pix
		if magenta := o[1] < o[0] && o[1] < o[2]; magenta != c.magenta {
			t.Errorf("tint %+v rendered %v; want magenta=%v", c.tint, o[:3], c.magenta)
		}
	}
}

func TestCATIdentity(t *testing.T) {
	m := CAT(whiteD65, whiteD65)
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			want := 0.0
			if i == j {
				want = 1
			}
			if math.Abs(m[i][j]-want) > 1e-9 {
				t.Fatalf("CAT(D65,D65)[%d][%d] = %v", i, j, m[i][j])
			}
		}
	}
	if got := CAT(whiteD50, whiteD65).Apply(whiteD50); math.Abs(got[0]-whiteD65[0]) > 1e-4 || math.Abs(got[2]-whiteD65[2]) > 1e-4 {
		t.Errorf("D50 white mapped to %v", got)
	}
}

func TestMulsWhiteRoundTrip(t *testing.T) {
	cam := Mat3{{0.62, 0.25, 0.08}, {0.28, 0.85, -0.13}, {0.03, -0.12, 1.02}}
	for _, temp := range []float64{3000, 5000, 7500} {
		x, y := TempTintToXY(temp, 0)
		mul, err := mulsForWhite(cam, xyToXYZ(x, y))
		if err != nil {
			t.Fatal(err)
		}
		gx, gy := xyzToXY(whiteOfMuls(cam, mul))
		if math.Abs(gx-x) > 1e-9 || math.Abs(gy-y) > 1e-9 {
			t.Errorf("temp %v: (%v,%v) -> (%v,%v)", temp, x, y, gx, gy)
		}
		if mul[1] != 1 {
			t.Errorf("green multiplier = %v; want 1", mul[1])
		}
	}
}

func TestWarmerLightNeedsMoreBlueGain(t *testing.T) {
	cam := Mat3{{0.62, 0.25, 0.08}, {0.28, 0.85, -0.13}, {0.03, -0.12, 1.02}}
	x1, y1 := TempTintToXY(3000, 0)
	x2, y2 := TempTintToXY(6500, 0)
	m1, _ := mulsForWhite(cam, xyToXYZ(x1, y1))
	m2, _ := mulsForWhite(cam, xyToXYZ(x2, y2))
	if m1[2] <= m2[2] {
		t.Errorf("tungsten blue gain %v must exceed daylight %v", m1[2], m2[2])
	}
}

func TestSolveCameraMatrix(t *testing.T) {
	want := Mat3{{0.62, 0.25, 0.08}, {0.28, 0.85, -0.13}, {0.03, -0.12, 1.02}}
	mul := [3]float64{2.1, 1, 1.6}
	rng := rand.New(rand.NewSource(1))
	cam := NewImage(200, 100)
	xyz := NewImage(200, 100)
	for i := 0; i < cam.W*cam.H; i++ {
		c := [3]float64{0.05 + rng.Float64()*0.75, 0.05 + rng.Float64()*0.75, 0.05 + rng.Float64()*0.75}
		raw := [3]float64{c[0] / mul[0], c[1] / mul[1], c[2] / mul[2]}
		y := want.Apply(raw)
		for k := 0; k < 3; k++ {
			cam.Pix[i*3+k] = float32(c[k])
			xyz.Pix[i*3+k] = float32(y[k])
		}
	}
	got, err := solveCameraMatrix(cam, xyz, mul)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			if math.Abs(got[i][j]-want[i][j]) > 1e-3 {
				t.Errorf("m[%d][%d] = %v; want %v", i, j, got[i][j], want[i][j])
			}
		}
	}
}

func TestSolveCameraMatrixNeedsPixels(t *testing.T) {
	if _, err := solveCameraMatrix(NewImage(10, 10), NewImage(10, 10), [3]float64{1, 1, 1}); err == nil {
		t.Error("expected error on black calibration frame")
	}
}
