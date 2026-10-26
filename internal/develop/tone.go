package develop

import (
	"fmt"
	"math"
)

const (
	toneLUTSize     = 8192
	toneLUTMaxS     = 2.0
	toneMaxGain     = 32
	localMaxEV      = 1.5
	localRadius     = 0.015
	midGreyEncoded  = 0.4587
	lumR            = 0.2627
	lumG            = 0.6780
	lumB            = 0.0593
	highlightSlope  = 0.5
	contrastUpRange = 0.7
	contrastDnRange = 0.5
)

func wbMatrix(illuminant, neutral [3]float64) Mat3 {
	return rec2020FromXYZ.Mul(CAT(illuminant, neutral)).Mul(xyzFromRec2020)
}

// rasterWB is the white-balance matrix for non-RAW sources; RAW WB is applied by the decoder via channel multipliers.
func rasterWB(r Recipe, raw bool) *Mat3 {
	if raw || r.WB.Mode != WBCustom {
		return nil
	}
	x, y := TempTintToXY(r.WB.Temp, r.WB.Tint)
	m := wbMatrix(xyToXYZ(x, y), whiteD65)
	return &m
}

func wbFromSample(rgb, from [3]float64) (*WhiteBalance, error) {
	lp := bradfordM.Apply(xyzFromRec2020.Apply(rgb))
	la := bradfordM.Apply(from)
	ld := bradfordM.Apply(whiteD65)
	var lt [3]float64
	for i := range lt {
		lt[i] = lp[i] * la[i] / ld[i]
	}
	xyz := bradfordInv.Apply(lt)
	if xyz[1] <= 0 || xyz[0]+xyz[1]+xyz[2] <= 0 {
		return nil, fmt.Errorf("%w: sampled colour is not usable", ErrInvalidRecipe)
	}
	t, tint := XYToTempTint(xyzToXY(xyz))
	return &WhiteBalance{
		Temp: math.Round(clamp(t, MinTemp, MaxTemp)),
		Tint: roundTo(clamp(tint, -MaxTint, MaxTint), 0.1),
	}, nil
}

func monotoneSpline(pts []Point) func(float64) float64 {
	n := len(pts)
	if n < 2 {
		return func(x float64) float64 { return x }
	}
	xs := make([]float64, n)
	ys := make([]float64, n)
	for i, p := range pts {
		xs[i], ys[i] = p[0], p[1]
	}
	d := make([]float64, n-1)
	for i := 0; i < n-1; i++ {
		if h := xs[i+1] - xs[i]; h > 0 {
			d[i] = (ys[i+1] - ys[i]) / h
		}
	}
	m := make([]float64, n)
	m[0], m[n-1] = d[0], d[n-2]
	for i := 1; i < n-1; i++ {
		if d[i-1]*d[i] > 0 {
			m[i] = (d[i-1] + d[i]) / 2
		}
	}
	for i := 0; i < n-1; i++ {
		if d[i] == 0 {
			m[i], m[i+1] = 0, 0
			continue
		}
		a, b := m[i]/d[i], m[i+1]/d[i]
		if s := a*a + b*b; s > 9 {
			t := 3 / math.Sqrt(s)
			m[i], m[i+1] = t*a*d[i], t*b*d[i]
		}
	}
	return func(x float64) float64 {
		if x <= xs[0] {
			return ys[0]
		}
		if x >= xs[n-1] {
			if x <= 1 {
				return ys[n-1]
			}
			return ys[n-1] + (x - 1)
		}
		i := 0
		for i < n-2 && x > xs[i+1] {
			i++
		}
		h := xs[i+1] - xs[i]
		t := (x - xs[i]) / h
		t2, t3 := t*t, t*t*t
		return (2*t3-3*t2+1)*ys[i] + (t3-2*t2+t)*h*m[i] + (-2*t3+3*t2)*ys[i+1] + (t3-t2)*h*m[i+1]
	}
}

func encodedTone(t Tone) func(float64) float64 {
	bp := -t.Blacks / 100 * 0.1
	wp := 1 - t.Whites/100*0.3
	k := 1 + t.Contrast/100*contrastUpRange
	if t.Contrast < 0 {
		k = 1 + t.Contrast/100*contrastDnRange
	}
	p := midGreyEncoded
	var curve func(float64) float64
	if len(t.Curve) >= 2 {
		curve = monotoneSpline(t.Curve)
	}
	return func(e float64) float64 {
		v := (e - bp) / (wp - bp)
		if v < 0 {
			v = 0
		}
		switch {
		case v <= p:
			v = p * math.Pow(v/p, k)
		case v <= 1:
			v = 1 - (1-p)*math.Pow((1-v)/(1-p), k)
		default:
			v = 1 + (v-1)*highlightSlope
		}
		if curve != nil {
			v = curve(v)
		}
		return math.Max(v, 0)
	}
}

type toneLUT struct {
	gain []float32
}

func buildToneLUT(t Tone) *toneLUT {
	if t.Contrast == 0 && t.Whites == 0 && t.Blacks == 0 && len(t.Curve) == 0 {
		return nil
	}
	f := encodedTone(t)
	g := make([]float32, toneLUTSize+1)
	for i := 1; i <= toneLUTSize; i++ {
		s := float64(i) / toneLUTSize * toneLUTMaxS
		y := s * s
		y2 := math.Pow(f(math.Pow(y, 1/2.2)), 2.2)
		g[i] = float32(math.Min(y2/y, toneMaxGain))
	}
	g[0] = g[1]
	return &toneLUT{gain: g}
}

func (l *toneLUT) at(y float32) float32 {
	if y <= 0 {
		return l.gain[0]
	}
	s := float32(math.Sqrt(float64(y))) * (toneLUTSize / toneLUTMaxS)
	if s >= toneLUTSize {
		return l.gain[toneLUTSize]
	}
	i := int(s)
	f := s - float32(i)
	return l.gain[i]*(1-f) + l.gain[i+1]*f
}

func smoothstep(a, b, x float32) float32 {
	t := (x - a) / (b - a)
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	return t * t * (3 - 2*t)
}

func boxBlurH(src, dst []float32, w, h, r int) {
	norm := 1 / float32(2*r+1)
	parallelRows(h, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			row := src[y*w : (y+1)*w]
			out := dst[y*w : (y+1)*w]
			var acc float32
			for k := -r; k <= r; k++ {
				acc += row[clampInt(k, 0, w-1)]
			}
			for x := 0; x < w; x++ {
				out[x] = acc * norm
				acc += row[clampInt(x+r+1, 0, w-1)] - row[clampInt(x-r, 0, w-1)]
			}
		}
	})
}

func boxBlurV(src, dst []float32, w, h, r int) {
	norm := 1 / float32(2*r+1)
	parallelRows(w, func(x0, x1 int) {
		for x := x0; x < x1; x++ {
			var acc float32
			for k := -r; k <= r; k++ {
				acc += src[clampInt(k, 0, h-1)*w+x]
			}
			for y := 0; y < h; y++ {
				dst[y*w+x] = acc * norm
				acc += src[clampInt(y+r+1, 0, h-1)*w+x] - src[clampInt(y-r, 0, h-1)*w+x]
			}
		}
	})
}

func boxBlur3(plane []float32, w, h, r int) []float32 {
	a := plane
	b := make([]float32, len(plane))
	for i := 0; i < 3; i++ {
		boxBlurH(a, b, w, h, r)
		boxBlurV(b, a, w, h, r)
	}
	return a
}

func localGains(img *Image, shadows, highlights, frameLong float64) []float32 {
	w, h := img.W, img.H
	plane := make([]float32, w*h)
	parallelRows(h, func(y0, y1 int) {
		for p := y0 * w; p < y1*w; p++ {
			i := p * 3
			y := lumR*img.Pix[i] + lumG*img.Pix[i+1] + lumB*img.Pix[i+2]
			if y < 0 {
				y = 0
			}
			s := float32(math.Sqrt(float64(y)))
			if s > 1.5 {
				s = 1.5
			}
			plane[p] = s
		}
	})
	r := max(1, int(localRadius*frameLong))
	blurred := boxBlur3(plane, w, h, r)
	sh := float32(shadows / 100 * localMaxEV)
	hl := float32(highlights / 100 * localMaxEV)
	parallelRows(h, func(y0, y1 int) {
		for p := y0 * w; p < y1*w; p++ {
			b := blurred[p]
			ev := sh*(1-smoothstep(0, 0.55, b)) + hl*smoothstep(0.45, 1, b)
			blurred[p] = float32(math.Exp2(float64(ev)))
		}
	})
	return blurred
}

func applyColor(r, g, b, sat, vib float32) (float32, float32, float32) {
	y := lumR*r + lumG*g + lumB*b
	s := sat
	if vib != 0 {
		mx := max(r, g, b)
		mn := min(r, g, b)
		var st float32
		if mx > 1e-6 {
			st = (mx - mn) / mx
		}
		if st > 1 {
			st = 1
		}
		s *= 1 + vib*(1-st)
	}
	return y + (r-y)*s, y + (g-y)*s, y + (b-y)*s
}
