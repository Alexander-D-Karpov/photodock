package develop

import (
	"math"
	"runtime"
	"sync"
)

type Image struct {
	W, H int
	Pix  []float32
}

func NewImage(w, h int) *Image {
	return &Image{W: w, H: h, Pix: make([]float32, w*h*3)}
}

func (img *Image) Clone() *Image {
	out := &Image{W: img.W, H: img.H, Pix: make([]float32, len(img.Pix))}
	copy(out.Pix, img.Pix)
	return out
}

func parallelRows(h int, fn func(y0, y1 int)) {
	n := runtime.GOMAXPROCS(0)
	if n > h {
		n = h
	}
	if n < 1 {
		n = 1
	}
	chunk := (h + n - 1) / n
	var wg sync.WaitGroup
	for y0 := 0; y0 < h; y0 += chunk {
		y1 := min(y0+chunk, h)
		wg.Add(1)
		go func(a, b int) {
			defer wg.Done()
			fn(a, b)
		}(y0, y1)
	}
	wg.Wait()
}

func (img *Image) remap(dw, dh int, f func(x, y int) (int, int)) *Image {
	out := NewImage(dw, dh)
	parallelRows(dh, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			for x := 0; x < dw; x++ {
				sx, sy := f(x, y)
				si := (sy*img.W + sx) * 3
				di := (y*dw + x) * 3
				out.Pix[di] = img.Pix[si]
				out.Pix[di+1] = img.Pix[si+1]
				out.Pix[di+2] = img.Pix[si+2]
			}
		}
	})
	return out
}

func (img *Image) Rotate(clockwise int) *Image {
	w, h := img.W, img.H
	switch NormalizeRotation(clockwise) {
	case 90:
		return img.remap(h, w, func(x, y int) (int, int) { return y, h - 1 - x })
	case 180:
		return img.remap(w, h, func(x, y int) (int, int) { return w - 1 - x, h - 1 - y })
	case 270:
		return img.remap(h, w, func(x, y int) (int, int) { return w - 1 - y, x })
	}
	return img
}

func (img *Image) FlipH() *Image {
	w := img.W
	return img.remap(img.W, img.H, func(x, y int) (int, int) { return w - 1 - x, y })
}

func (img *Image) FlipV() *Image {
	h := img.H
	return img.remap(img.W, img.H, func(x, y int) (int, int) { return x, h - 1 - y })
}

func (img *Image) Transpose() *Image {
	return img.remap(img.H, img.W, func(x, y int) (int, int) { return y, x })
}

func (img *Image) Orient(orientation int) *Image {
	switch orientation {
	case 2:
		return img.FlipH()
	case 3:
		return img.Rotate(180)
	case 4:
		return img.FlipV()
	case 5:
		return img.Transpose()
	case 6:
		return img.Rotate(90)
	case 7:
		return img.Transpose().Rotate(180)
	case 8:
		return img.Rotate(270)
	}
	return img
}

func (img *Image) Crop(r *Rect) *Image {
	if r == nil {
		return img
	}
	x0 := int(math.Round(r.X * float64(img.W)))
	y0 := int(math.Round(r.Y * float64(img.H)))
	w := int(math.Round(r.W * float64(img.W)))
	h := int(math.Round(r.H * float64(img.H)))
	x0 = min(max(x0, 0), img.W-1)
	y0 = min(max(y0, 0), img.H-1)
	w = min(max(w, 1), img.W-x0)
	h = min(max(h, 1), img.H-y0)
	if x0 == 0 && y0 == 0 && w == img.W && h == img.H {
		return img
	}
	return img.remap(w, h, func(x, y int) (int, int) { return x + x0, y + y0 })
}

type span struct {
	start int
	w     []float32
}

func resampleWeights(src, dst int) []span {
	out := make([]span, dst)
	scale := float64(src) / float64(dst)
	if scale >= 1 {
		for o := range out {
			a := float64(o) * scale
			b := a + scale
			s := int(math.Floor(a))
			e := min(int(math.Ceil(b)), src)
			ws := make([]float32, e-s)
			var sum float64
			for i := s; i < e; i++ {
				ov := math.Min(b, float64(i+1)) - math.Max(a, float64(i))
				if ov < 0 {
					ov = 0
				}
				ws[i-s] = float32(ov)
				sum += ov
			}
			for i := range ws {
				ws[i] /= float32(sum)
			}
			out[o] = span{start: s, w: ws}
		}
		return out
	}
	for o := range out {
		c := (float64(o)+0.5)*scale - 0.5
		s := int(math.Floor(c))
		f := c - float64(s)
		if s < 0 {
			s, f = 0, 0
		}
		if s >= src-1 {
			out[o] = span{start: src - 1, w: []float32{1}}
			continue
		}
		out[o] = span{start: s, w: []float32{float32(1 - f), float32(f)}}
	}
	return out
}

func (img *Image) Resize(w, h int) *Image {
	if w == img.W && h == img.H {
		return img
	}
	hw := resampleWeights(img.W, w)
	vw := resampleWeights(img.H, h)
	tmp := NewImage(w, img.H)
	parallelRows(img.H, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			row := img.Pix[y*img.W*3 : (y+1)*img.W*3]
			for x, sp := range hw {
				var r, g, b float32
				for i, wt := range sp.w {
					si := (sp.start + i) * 3
					r += row[si] * wt
					g += row[si+1] * wt
					b += row[si+2] * wt
				}
				di := (y*w + x) * 3
				tmp.Pix[di], tmp.Pix[di+1], tmp.Pix[di+2] = r, g, b
			}
		}
	})
	out := NewImage(w, h)
	parallelRows(h, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			sp := vw[y]
			for x := 0; x < w; x++ {
				var r, g, b float32
				for i, wt := range sp.w {
					si := ((sp.start+i)*w + x) * 3
					r += tmp.Pix[si] * wt
					g += tmp.Pix[si+1] * wt
					b += tmp.Pix[si+2] * wt
				}
				di := (y*w + x) * 3
				out.Pix[di], out.Pix[di+1], out.Pix[di+2] = r, g, b
			}
		}
	})
	return out
}

func (img *Image) FitWithin(maxW, maxH int) *Image {
	s := 1.0
	if maxW > 0 && img.W > maxW {
		s = math.Min(s, float64(maxW)/float64(img.W))
	}
	if maxH > 0 && img.H > maxH {
		s = math.Min(s, float64(maxH)/float64(img.H))
	}
	if s >= 1 {
		return img
	}
	w := max(1, int(math.Round(float64(img.W)*s)))
	h := max(1, int(math.Round(float64(img.H)*s)))
	return img.Resize(w, h)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (img *Image) sampleBilinear(x, y float64, dst []float32) {
	x0 := int(math.Floor(x))
	y0 := int(math.Floor(y))
	fx := float32(x - float64(x0))
	fy := float32(y - float64(y0))
	xa := clampInt(x0, 0, img.W-1)
	xb := clampInt(x0+1, 0, img.W-1)
	ya := clampInt(y0, 0, img.H-1)
	yb := clampInt(y0+1, 0, img.H-1)
	i00 := (ya*img.W + xa) * 3
	i10 := (ya*img.W + xb) * 3
	i01 := (yb*img.W + xa) * 3
	i11 := (yb*img.W + xb) * 3
	for c := 0; c < 3; c++ {
		top := img.Pix[i00+c]*(1-fx) + img.Pix[i10+c]*fx
		bot := img.Pix[i01+c]*(1-fx) + img.Pix[i11+c]*fx
		dst[c] = top*(1-fy) + bot*fy
	}
}

// Straighten rotates by angle degrees and crops to the largest centred
// rectangle of the original aspect ratio that has no empty corners.
func (img *Image) Straighten(angle float64) *Image {
	if angle == 0 {
		return img
	}
	th := angle * math.Pi / 180
	c, s := math.Cos(th), math.Sin(th)
	ac, as := math.Abs(c), math.Abs(s)
	sw, sh := float64(img.W), float64(img.H)
	k := math.Min(sw/(sw*ac+sh*as), sh/(sw*as+sh*ac))
	w := max(1, int(math.Floor(sw*k)))
	h := max(1, int(math.Floor(sh*k)))
	out := NewImage(w, h)
	cx, cy := sw/2, sh/2
	ox, oy := float64(w)/2, float64(h)/2
	parallelRows(h, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			dy := float64(y) + 0.5 - oy
			for x := 0; x < w; x++ {
				dx := float64(x) + 0.5 - ox
				sx := c*dx + s*dy + cx - 0.5
				sy := -s*dx + c*dy + cy - 0.5
				i := (y*w + x) * 3
				img.sampleBilinear(sx, sy, out.Pix[i:i+3])
			}
		}
	})
	return out
}
