package develop

import "math"

// Colour operations (HSL, B&W mix, grading) work in OKLab/OKLCh: hues are
// separated perceptually and every effect is weighted by chroma, so neutral
// greys are never tinted.

var (
	oklabM1 = Mat3{
		{0.8189330101, 0.3618667424, -0.1288597137},
		{0.0329845436, 0.9293118715, 0.0361456387},
		{0.0482003018, 0.2643662691, 0.6338517070},
	}
	oklabM2 = Mat3{
		{0.2104542553, 0.7936177850, -0.0040720468},
		{1.9779984951, -2.4285922050, 0.4505937099},
		{0.0259040371, 0.7827717662, -0.8086757660},
	}
	lmsFromRec2020 = oklabM1.Mul(xyzFromRec2020)
	rec2020FromLMS = mustInverse(lmsFromRec2020)
	oklabM2Inv     = mustInverse(oklabM2)
	hueCenters     = [8]float64{29, 55, 100, 142, 195, 264, 295, 330}
)

const (
	hslHueRange   = 30.0
	hslLumEV      = 0.75
	bwLumEV       = 1.0
	gradingChroma = 0.05
	gradingLum    = 0.1
)

func toOKLab(r, g, b float64) (float64, float64, float64) {
	m := &lmsFromRec2020
	l := math.Cbrt(m[0][0]*r + m[0][1]*g + m[0][2]*b)
	mm := math.Cbrt(m[1][0]*r + m[1][1]*g + m[1][2]*b)
	s := math.Cbrt(m[2][0]*r + m[2][1]*g + m[2][2]*b)
	n := &oklabM2
	return n[0][0]*l + n[0][1]*mm + n[0][2]*s,
		n[1][0]*l + n[1][1]*mm + n[1][2]*s,
		n[2][0]*l + n[2][1]*mm + n[2][2]*s
}

func fromOKLab(L, a, b float64) (float64, float64, float64) {
	n := &oklabM2Inv
	l := n[0][0]*L + n[0][1]*a + n[0][2]*b
	mm := n[1][0]*L + n[1][1]*a + n[1][2]*b
	s := n[2][0]*L + n[2][1]*a + n[2][2]*b
	l, mm, s = l*l*l, mm*mm*mm, s*s*s
	m := &rec2020FromLMS
	return m[0][0]*l + m[0][1]*mm + m[0][2]*s,
		m[1][0]*l + m[1][1]*mm + m[1][2]*s,
		m[2][0]*l + m[2][1]*mm + m[2][2]*s
}

func smooth64(a, b, x float64) float64 {
	t := (x - a) / (b - a)
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	return t * t * (3 - 2*t)
}

// bandWeights returns the two neighbouring hue bands of an OKLCh hue (degrees)
// and the raised-cosine weight of the first.
func bandWeights(deg float64) (int, int, float64) {
	for i := 0; i < 8; i++ {
		c0 := hueCenters[i]
		c1 := hueCenters[(i+1)%8]
		h := deg
		if i == 7 {
			c1 += 360
			if h < c0 {
				h += 360
			}
		}
		if h >= c0 && h < c1 {
			t := (h - c0) / (c1 - c0)
			return i, (i + 1) % 8, 0.5 * (1 + math.Cos(math.Pi*t))
		}
	}
	return 7, 0, 1
}

// oklabHueOf converts an HSV-style hue (degrees, as on the grading wheels) to
// the OKLab hue angle of that sRGB primary mix.
func oklabHueOf(deg float64) float64 {
	h := math.Mod(math.Mod(deg, 360)+360, 360) / 60
	i := int(h)
	f := h - float64(i)
	var r, g, b float64
	switch i {
	case 0:
		r, g, b = 1, f, 0
	case 1:
		r, g, b = 1-f, 1, 0
	case 2:
		r, g, b = 0, 1, f
	case 3:
		r, g, b = 0, 1-f, 1
	case 4:
		r, g, b = f, 0, 1
	default:
		r, g, b = 1, 0, 1-f
	}
	lin := rec2020FromSRGB.Apply([3]float64{srgbDecode(r), srgbDecode(g), srgbDecode(b)})
	_, a, bb := toOKLab(lin[0], lin[1], lin[2])
	return math.Atan2(bb, a)
}

func mixerMatrix(m Mixer) (Mat3, bool) {
	out := identity3
	on := false
	for i, row := range [3]MixerRow{m.Red, m.Green, m.Blue} {
		for j, d := range [3]float64{row.R, row.G, row.B} {
			if d != 0 {
				on = true
				out[i][j] += d / 100
			}
		}
	}
	return out, on
}

type colorPlan struct {
	hsl     bool
	hue     [8]float64
	sat     [8]float64
	lum     [8]float64
	bw      bool
	mix     [8]float64
	grading bool
	off     [4][2]float64
	lums    [4]float64
	pivot   float64
	width   float64
}

func newColorPlan(c Color) *colorPlan {
	p := &colorPlan{}
	for i, b := range c.HSL.bands() {
		p.hue[i] = b.Hue / 100 * hslHueRange * math.Pi / 180
		p.sat[i] = b.Sat / 100
		p.lum[i] = b.Lum / 100 * hslLumEV
		if b.Hue != 0 || b.Sat != 0 || b.Lum != 0 {
			p.hsl = true
		}
	}
	if c.BW.Enabled {
		p.bw = true
		for i, v := range c.BW.Mix.values() {
			p.mix[i] = *v / 100 * bwLumEV
		}
	}
	g := c.Grading
	for i, w := range [4]Wheel{g.Shadows, g.Midtones, g.Highlights, g.Global} {
		if w.Sat != 0 {
			ang := oklabHueOf(w.Hue)
			k := w.Sat / 100 * gradingChroma
			p.off[i] = [2]float64{k * math.Cos(ang), k * math.Sin(ang)}
			p.grading = true
		}
		if w.Lum != 0 {
			p.lums[i] = w.Lum / 100 * gradingLum
			p.grading = true
		}
	}
	p.pivot = 0.5 - g.Balance/100*0.2
	p.width = 0.25 + g.Blending/50*0.2
	if !p.hsl && !p.bw && !p.grading {
		return nil
	}
	return p
}

func (p *colorPlan) apply(r, g, b float32) (float32, float32, float32) {
	L, a, bb := toOKLab(float64(r), float64(g), float64(b))
	if p.hsl || p.bw {
		C := math.Hypot(a, bb)
		h := math.Atan2(bb, a)
		cw := smooth64(0.02, 0.08, C)
		deg := h * 180 / math.Pi
		if deg < 0 {
			deg += 360
		}
		i, j, wi := bandWeights(deg)
		wj := 1 - wi
		if p.hsl && cw > 0 {
			h += (p.hue[i]*wi + p.hue[j]*wj) * cw
			C *= math.Max(0, 1+p.sat[i]*wi+p.sat[j]*wj)
			L *= math.Exp2((p.lum[i]*wi + p.lum[j]*wj) * cw)
		}
		if p.bw {
			if cw > 0 {
				L *= math.Exp2((p.mix[i]*wi + p.mix[j]*wj) * cw)
			}
			C = 0
		}
		a, bb = C*math.Cos(h), C*math.Sin(h)
	}
	if p.grading {
		s := smooth64(p.pivot-p.width, p.pivot+p.width, L)
		ws, wm, wh := (1-s)*(1-s), 2*s*(1-s), s*s
		fade := smooth64(0, 0.1, L)
		a += fade * (ws*p.off[0][0] + wm*p.off[1][0] + wh*p.off[2][0] + p.off[3][0])
		bb += fade * (ws*p.off[0][1] + wm*p.off[1][1] + wh*p.off[2][1] + p.off[3][1])
		L = math.Max(0, L+ws*p.lums[0]+wm*p.lums[1]+wh*p.lums[2]+p.lums[3])
	}
	ro, gout, bo := fromOKLab(L, a, bb)
	return float32(ro), float32(gout), float32(bo)
}
