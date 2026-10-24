package develop

import "math"

const (
	localContrastRadius = 0.03
	clarityRadius       = 0.008
	dehazeRadius        = 0.01
	maxRegionSide       = 4096
)

// renderCtx carries what scale-dependent operations need to know about the
// frame they run on, so a fit preview, a 1:1 tile and a full render produce
// the same look: radii are relative to frameLong, detail ops to detailScale,
// and masks map pixels back to the frame through space.
type renderCtx struct {
	detailScale float64
	frameLong   float64
	airlight    *[3]float32
	space       *maskSpace
	timing      *Timings
}

func (c renderCtx) forImage(img *Image) renderCtx {
	if c.frameLong <= 0 {
		c.frameLong = float64(max(img.W, img.H))
	}
	if c.detailScale <= 0 {
		c.detailScale = 1
	}
	return c
}

func parallelPixels(w, h int, fn func(i0, i1 int)) {
	parallelRows(h, func(y0, y1 int) { fn(y0*w, y1*w) })
}

func boxMean(src []float32, w, h, r int) []float32 {
	tmp := make([]float32, len(src))
	out := make([]float32, len(src))
	boxBlurH(src, tmp, w, h, r)
	boxBlurV(tmp, out, w, h, r)
	return out
}

// guided is He et al.'s guided filter of p with guide image guide.
func guided(guide, p []float32, w, h, r int, eps float32) []float32 {
	n := len(p)
	meanI := boxMean(guide, w, h, r)
	meanP := boxMean(p, w, h, r)
	ip := make([]float32, n)
	ii := make([]float32, n)
	parallelPixels(w, h, func(i0, i1 int) {
		for i := i0; i < i1; i++ {
			ip[i] = guide[i] * p[i]
			ii[i] = guide[i] * guide[i]
		}
	})
	corrIP := boxMean(ip, w, h, r)
	corrII := boxMean(ii, w, h, r)
	a, b := ip, ii
	parallelPixels(w, h, func(i0, i1 int) {
		for i := i0; i < i1; i++ {
			varI := corrII[i] - meanI[i]*meanI[i]
			if varI < 0 {
				varI = 0
			}
			cov := corrIP[i] - meanI[i]*meanP[i]
			ai := cov / (varI + eps)
			a[i] = ai
			b[i] = meanP[i] - ai*meanI[i]
		}
	})
	ma := boxMean(a, w, h, r)
	mb := boxMean(b, w, h, r)
	out := corrIP
	parallelPixels(w, h, func(i0, i1 int) {
		for i := i0; i < i1; i++ {
			out[i] = ma[i]*guide[i] + mb[i]
		}
	})
	return out
}

// lumPlane returns sqrt(luminance), a roughly perceptual plane to filter in.
func lumPlane(img *Image) []float32 {
	s := make([]float32, img.W*img.H)
	parallelPixels(img.W, img.H, func(i0, i1 int) {
		for p := i0; p < i1; p++ {
			i := p * 3
			y := lumR*img.Pix[i] + lumG*img.Pix[i+1] + lumB*img.Pix[i+2]
			if y < 0 {
				y = 0
			}
			s[p] = float32(math.Sqrt(float64(y)))
		}
	})
	return s
}

// applyLumRatio scales every pixel by (after/before)² so a change computed on
// the sqrt-luminance plane is applied without shifting hue.
func applyLumRatio(img *Image, before, after []float32) {
	parallelPixels(img.W, img.H, func(i0, i1 int) {
		for p := i0; p < i1; p++ {
			b := before[p]
			if b < 1e-4 {
				continue
			}
			a := after[p]
			if a < 0 {
				a = 0
			}
			k := a / b
			k *= k
			if k > 16 {
				k = 16
			}
			i := p * 3
			img.Pix[i] *= k
			img.Pix[i+1] *= k
			img.Pix[i+2] *= k
		}
	})
}

func denoise(img *Image, n Noise, ctx renderCtx) {
	w, h := img.W, img.H
	if n.Chroma > 0 {
		r := max(1, int(math.Round((1+n.Chroma/100*7)*ctx.detailScale)))
		cr := make([]float32, w*h)
		cb := make([]float32, w*h)
		lum := make([]float32, w*h)
		parallelPixels(w, h, func(i0, i1 int) {
			for p := i0; p < i1; p++ {
				i := p * 3
				y := lumR*img.Pix[i] + lumG*img.Pix[i+1] + lumB*img.Pix[i+2]
				lum[p] = y
				cr[p] = img.Pix[i] - y
				cb[p] = img.Pix[i+2] - y
			}
		})
		cr = boxBlur3(cr, w, h, r)
		cb = boxBlur3(cb, w, h, r)
		parallelPixels(w, h, func(i0, i1 int) {
			for p := i0; p < i1; p++ {
				i := p * 3
				rr := lum[p] + cr[p]
				bb := lum[p] + cb[p]
				img.Pix[i] = rr
				img.Pix[i+2] = bb
				img.Pix[i+1] = (lum[p] - lumR*rr - lumB*bb) / lumG
			}
		})
	}
	if n.Luma > 0 {
		s := lumPlane(img)
		r := max(1, int(math.Round(2*ctx.detailScale)))
		e := n.Luma / 100 * 0.06
		applyLumRatio(img, s, guided(s, s, w, h, r, float32(e*e)))
	}
}

func sharpen(img *Image, sp Sharpen, ctx renderCtx) {
	if sp.Amount <= 0 {
		return
	}
	radius := sp.Radius * ctx.detailScale
	if radius < 0.25 {
		return
	}
	w, h := img.W, img.H
	s := lumPlane(img)
	blur := make([]float32, len(s))
	copy(blur, s)
	blur = boxBlur3(blur, w, h, max(1, int(math.Round(radius))))
	amt := float32(sp.Amount / 100)
	t := float32(sp.Threshold / 100 * 0.04)
	parallelPixels(w, h, func(i0, i1 int) {
		for p := i0; p < i1; p++ {
			d := s[p] - blur[p]
			m := float32(1)
			if t > 0 {
				ad := d
				if ad < 0 {
					ad = -ad
				}
				m = smoothstep(t*0.5, t, ad)
			}
			blur[p] = s[p] + amt*d*m
		}
	})
	applyLumRatio(img, s, blur)
}

func localContrast(img *Image, l Local, ctx renderCtx) {
	if l.Clarity == 0 && l.Contrast == 0 {
		return
	}
	w, h := img.W, img.H
	s := lumPlane(img)
	out := make([]float32, len(s))
	copy(out, s)
	if l.Contrast != 0 {
		base := make([]float32, len(s))
		copy(base, s)
		base = boxBlur3(base, w, h, max(1, int(localContrastRadius*ctx.frameLong)))
		k := float32(l.Contrast / 100 * 0.8)
		parallelPixels(w, h, func(i0, i1 int) {
			for p := i0; p < i1; p++ {
				out[p] += k * (s[p] - base[p])
			}
		})
	}
	if l.Clarity != 0 {
		base := guided(s, s, w, h, max(1, int(clarityRadius*ctx.frameLong)), 0.01)
		k := float32(l.Clarity / 100)
		parallelPixels(w, h, func(i0, i1 int) {
			for p := i0; p < i1; p++ {
				v := s[p]
				m := smoothstep(0.05, 0.35, v) * (1 - smoothstep(0.65, 1, v))
				out[p] += k * m * (v - base[p])
			}
		})
	}
	applyLumRatio(img, s, out)
}

// estimateAirlight averages the brightest 0.1% of the dark channel.
func estimateAirlight(img *Image) [3]float32 {
	w, h := img.W, img.H
	n := w * h
	dark := make([]float32, n)
	parallelPixels(w, h, func(i0, i1 int) {
		for p := i0; p < i1; p++ {
			i := p * 3
			dark[p] = min(img.Pix[i], img.Pix[i+1], img.Pix[i+2])
		}
	})
	dark = boxBlur3(dark, w, h, max(1, int(dehazeRadius*float64(max(w, h)))))

	const bins = 4096
	var hist [bins]int
	bin := func(v float32) int { return clampInt(int(v/2*bins), 0, bins-1) }
	for _, v := range dark {
		hist[bin(v)]++
	}
	need := max(1, n/1000)
	thr, acc := bins-1, 0
	for ; thr > 0; thr-- {
		if acc += hist[thr]; acc >= need {
			break
		}
	}
	var sum [3]float64
	count := 0
	for p, v := range dark {
		if bin(v) >= thr {
			i := p * 3
			sum[0] += float64(img.Pix[i])
			sum[1] += float64(img.Pix[i+1])
			sum[2] += float64(img.Pix[i+2])
			count++
		}
	}
	a := [3]float32{1, 1, 1}
	if count > 0 {
		for c := 0; c < 3; c++ {
			a[c] = float32(math.Max(sum[c]/float64(count), 0.05))
		}
	}
	return a
}

func dehaze(img *Image, amount float64, ctx renderCtx) {
	if amount == 0 {
		return
	}
	var a [3]float32
	if ctx.airlight != nil {
		a = *ctx.airlight
	} else {
		a = estimateAirlight(img)
	}
	w, h := img.W, img.H
	k := float32(amount / 100)
	if k < 0 {
		f := -k * 0.6
		parallelPixels(w, h, func(i0, i1 int) {
			for p := i0; p < i1; p++ {
				i := p * 3
				for c := 0; c < 3; c++ {
					img.Pix[i+c] = img.Pix[i+c]*(1-f) + a[c]*f
				}
			}
		})
		return
	}
	r := max(1, int(dehazeRadius*ctx.frameLong))
	t := make([]float32, w*h)
	parallelPixels(w, h, func(i0, i1 int) {
		for p := i0; p < i1; p++ {
			i := p * 3
			t[p] = min(img.Pix[i]/a[0], img.Pix[i+1]/a[1], img.Pix[i+2]/a[2])
		}
	})
	t = boxBlur3(t, w, h, r)
	parallelPixels(w, h, func(i0, i1 int) {
		for p := i0; p < i1; p++ {
			t[p] = 1 - 0.95*k*t[p]
		}
	})
	t = guided(lumPlane(img), t, w, h, r, 1e-3)
	parallelPixels(w, h, func(i0, i1 int) {
		for p := i0; p < i1; p++ {
			tt := t[p]
			if tt < 0.1 {
				tt = 0.1
			} else if tt > 1 {
				tt = 1
			}
			i := p * 3
			for c := 0; c < 3; c++ {
				v := (img.Pix[i+c]-a[c])/tt + a[c]
				if v < 0 {
					v = 0
				}
				img.Pix[i+c] = v
			}
		}
	})
}

// regionMargin is how many extra pixels a 1:1 tile must render around itself
// so neighbourhood filters see the same context as in a full render.
func regionMargin(r Recipe, long int) int {
	l := float64(long)
	m := 8
	if r.Tone.Shadows != 0 || r.Tone.Highlights != 0 {
		m = max(m, int(3*localRadius*l)+2)
	}
	if r.Local.Contrast != 0 {
		m = max(m, int(3*localContrastRadius*l)+2)
	}
	clarity := r.Local.Clarity != 0
	for _, mk := range r.Masks {
		if !mk.Off && mk.Adjust.Clarity != 0 {
			clarity = true
		}
	}
	if clarity {
		m = max(m, int(2*clarityRadius*l)+2)
	}
	if r.Local.Dehaze != 0 {
		m = max(m, int(5*dehazeRadius*l)+2)
	}
	if r.Detail.Noise.Chroma > 0 {
		m = max(m, 26)
	}
	return m
}
