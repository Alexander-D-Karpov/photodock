package develop

import (
	"math"
	"math/rand"
	"testing"
)

func TestOKLabRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	for i := 0; i < 1000; i++ {
		r, g, b := rng.Float64(), rng.Float64(), rng.Float64()
		L, a, bb := toOKLab(r, g, b)
		r2, g2, b2 := fromOKLab(L, a, bb)
		if math.Abs(r-r2) > 1e-6 || math.Abs(g-g2) > 1e-6 || math.Abs(b-b2) > 1e-6 {
			t.Fatalf("roundtrip (%v,%v,%v) -> (%v,%v,%v)", r, g, b, r2, g2, b2)
		}
	}
}

func TestNeutralIsNeutralInOKLab(t *testing.T) {
	_, a, b := toOKLab(0.18, 0.18, 0.18)
	if math.Abs(a) > 1e-3 || math.Abs(b) > 1e-3 {
		t.Errorf("grey has chroma a=%v b=%v", a, b)
	}
}

func TestBandWeightsPartition(t *testing.T) {
	for d := 0.0; d < 360; d += 0.5 {
		i, j, w := bandWeights(d)
		if i < 0 || j < 0 || w < 0 || w > 1 {
			t.Fatalf("bad weights at %v: %d %d %v", d, i, j, w)
		}
	}
	if i, _, w := bandWeights(hueCenters[3]); i != 3 || math.Abs(w-1) > 1e-9 {
		t.Errorf("centre of green must be pure green: %d %v", i, w)
	}
}

func TestEmptyColorPlanIsNil(t *testing.T) {
	if newColorPlan(Color{Grading: Grading{Blending: 10, Balance: 20}}) != nil {
		t.Error("blending/balance alone must not create a plan")
	}
}

func srgbPixel(r, g, b float64) (float32, float32, float32) {
	v := rec2020FromSRGB.Apply([3]float64{srgbDecode(r), srgbDecode(g), srgbDecode(b)})
	return float32(v[0]), float32(v[1]), float32(v[2])
}

func chroma(r, g, b float32) float64 {
	_, a, bb := toOKLab(float64(r), float64(g), float64(b))
	return math.Hypot(a, bb)
}

func TestHSLDesaturatesOnlyTargetBand(t *testing.T) {
	var c Color
	c.HSL.Red.Sat = -100
	p := newColorPlan(c)
	rr, rg, rb := srgbPixel(0.8, 0.1, 0.1)
	br, bg, bb := srgbPixel(0.1, 0.2, 0.8)
	if got := chroma(p.apply(rr, rg, rb)); got > chroma(rr, rg, rb)*0.3 {
		t.Errorf("red chroma not removed: %v", got)
	}
	if got := chroma(p.apply(br, bg, bb)); math.Abs(got-chroma(br, bg, bb)) > 1e-4 {
		t.Errorf("blue chroma changed: %v", got)
	}
	gr, gg, gb := p.apply(0.18, 0.18, 0.18)
	if math.Abs(float64(gr-0.18)) > 1e-4 || math.Abs(float64(gb-0.18)) > 1e-4 {
		t.Errorf("neutral changed by HSL: %v %v %v", gr, gg, gb)
	}
}

func TestBlackAndWhiteIsNeutral(t *testing.T) {
	p := newColorPlan(Color{BW: BW{Enabled: true}})
	r, g, b := p.apply(srgbPixel(0.7, 0.3, 0.1))
	if math.Abs(float64(r-g)) > 2e-3 || math.Abs(float64(g-b)) > 2e-3 {
		t.Errorf("b&w output not neutral: %v %v %v", r, g, b)
	}
}

func TestGradingTintsShadowsNotHighlights(t *testing.T) {
	p := newColorPlan(Color{Grading: Grading{Shadows: Wheel{Hue: 30, Sat: 100}}})
	dr, _, db := p.apply(0.02, 0.02, 0.02)
	if dr <= db {
		t.Errorf("shadows not warmed: r=%v b=%v", dr, db)
	}
	hr, _, hb := p.apply(0.9, 0.9, 0.9)
	if math.Abs(float64(hr-hb)) > 0.01 {
		t.Errorf("highlights tinted: r=%v b=%v", hr, hb)
	}
}

func TestMixerMatrix(t *testing.T) {
	if _, on := mixerMatrix(Mixer{}); on {
		t.Error("zero mixer must be inactive")
	}
	m, on := mixerMatrix(Mixer{Red: MixerRow{R: -100, G: 100}})
	if !on || m[0][0] != 0 || m[0][1] != 1 {
		t.Errorf("mixer = %v", m)
	}
}
