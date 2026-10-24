package develop

import "math"

// affine2 maps (x, y) -> (a*x + b*y + c, d*x + e*y + f).
type affine2 struct{ a, b, c, d, e, f float64 }

var identityAffine = affine2{a: 1, e: 1}

func (t affine2) apply(x, y float64) (float64, float64) {
	return t.a*x + t.b*y + t.c, t.d*x + t.e*y + t.f
}

// then returns the transform that applies t first, then u.
func (t affine2) then(u affine2) affine2 {
	return affine2{
		a: u.a*t.a + u.b*t.d, b: u.a*t.b + u.b*t.e, c: u.a*t.c + u.b*t.f + u.c,
		d: u.d*t.a + u.e*t.d, e: u.d*t.b + u.e*t.e, f: u.d*t.c + u.e*t.f + u.f,
	}
}

// dihedralOp returns, for an input of size w×h, the inverse mapping from
// output to input coordinates and the output size.
type dihedralOp func(w, h float64) (affine2, float64, float64)

func opRotate(quarter int) dihedralOp {
	return func(w, h float64) (affine2, float64, float64) {
		switch ((quarter % 4) + 4) % 4 {
		case 1:
			return affine2{b: 1, d: -1, f: h}, h, w
		case 2:
			return affine2{a: -1, c: w, e: -1, f: h}, w, h
		case 3:
			return affine2{b: -1, c: w, d: 1}, h, w
		}
		return identityAffine, w, h
	}
}

func opFlipH(w, h float64) (affine2, float64, float64) {
	return affine2{a: -1, c: w, e: 1}, w, h
}

func opFlipV(w, h float64) (affine2, float64, float64) {
	return affine2{a: 1, e: -1, f: h}, w, h
}

func opTranspose(w, h float64) (affine2, float64, float64) {
	return affine2{b: 1, d: 1}, h, w
}

func orientationOps(o int) []dihedralOp {
	switch o {
	case 2:
		return []dihedralOp{opFlipH}
	case 3:
		return []dihedralOp{opRotate(2)}
	case 4:
		return []dihedralOp{opFlipV}
	case 5:
		return []dihedralOp{opTranspose}
	case 6:
		return []dihedralOp{opRotate(1)}
	case 7:
		return []dihedralOp{opTranspose, opRotate(2)}
	case 8:
		return []dihedralOp{opRotate(3)}
	}
	return nil
}

// geomPlan is the whole geometry stage as one inverse mapping from an output
// pixel to a sensor pixel: orientation, rotation, flips, straighten, crop and
// (optionally) the lens model. Fit previews, 1:1 regions and full renders all
// sample through it, so they agree pixel for pixel.
type geomPlan struct {
	toSensor     affine2
	dispW, dispH float64
	frameW       float64
	frameH       float64
	cosA, sinA   float64
	cropX, cropY float64
	outW, outH   float64
	lens         *lensModel
}

func newGeomPlan(srcW, srcH, orientation int, g Geometry, lens *lensModel) *geomPlan {
	w, h := float64(srcW), float64(srcH)
	inv := identityAffine
	ops := orientationOps(orientation)
	if q := NormalizeRotation(g.Rotate) / 90; q != 0 {
		ops = append(ops, opRotate(q))
	}
	if g.FlipH {
		ops = append(ops, opFlipH)
	}
	if g.FlipV {
		ops = append(ops, opFlipV)
	}
	for _, op := range ops {
		m, nw, nh := op(w, h)
		inv = m.then(inv)
		w, h = nw, nh
	}

	p := &geomPlan{toSensor: inv, dispW: w, dispH: h, cosA: 1, lens: lens}
	k := 1.0
	if g.Angle != 0 {
		th := g.Angle * math.Pi / 180
		p.cosA, p.sinA = math.Cos(th), math.Sin(th)
		ac, as := math.Abs(p.cosA), math.Abs(p.sinA)
		k = math.Min(w/(w*ac+h*as), h/(w*as+h*ac))
	}
	p.frameW, p.frameH = w*k, h*k

	crop := g.Crop
	if crop == nil {
		crop = &Rect{W: 1, H: 1}
	}
	p.cropX = math.Round(crop.X * p.frameW)
	p.cropY = math.Round(crop.Y * p.frameH)
	p.outW = math.Max(1, math.Min(math.Round(crop.W*p.frameW), math.Floor(p.frameW)-p.cropX))
	p.outH = math.Max(1, math.Min(math.Round(crop.H*p.frameH), math.Floor(p.frameH)-p.cropY))
	return p
}

func (p *geomPlan) size(scale float64) (int, int) {
	return max(1, int(math.Round(p.outW*scale))), max(1, int(math.Round(p.outH*scale)))
}

func (p *geomPlan) sourceOf(x, y float64) (float64, float64) {
	fx := x + p.cropX - p.frameW/2
	fy := y + p.cropY - p.frameH/2
	u := p.cosA*fx + p.sinA*fy + p.dispW/2
	v := -p.sinA*fx + p.cosA*fy + p.dispH/2
	return p.toSensor.apply(u, v)
}

func (img *Image) sampleChannel(x, y float64, c int) float32 {
	x0 := int(math.Floor(x))
	y0 := int(math.Floor(y))
	fx := float32(x - float64(x0))
	fy := float32(y - float64(y0))
	xa := clampInt(x0, 0, img.W-1)
	xb := clampInt(x0+1, 0, img.W-1)
	ya := clampInt(y0, 0, img.H-1)
	yb := clampInt(y0+1, 0, img.H-1)
	p00 := img.Pix[(ya*img.W+xa)*3+c]
	p10 := img.Pix[(ya*img.W+xb)*3+c]
	p01 := img.Pix[(yb*img.W+xa)*3+c]
	p11 := img.Pix[(yb*img.W+xb)*3+c]
	return (p00*(1-fx)+p10*fx)*(1-fy) + (p01*(1-fx)+p11*fx)*fy
}

// warp renders the w×h window at (rx, ry) of the planned output, scaled by scale.
func warp(src *Image, plan *geomPlan, scale float64, rx, ry, w, h int) *Image {
	out := NewImage(w, h)
	lens := plan.lens
	parallelRows(h, func(y0, y1 int) {
		for j := y0; j < y1; j++ {
			oy := (float64(ry+j) + 0.5) / scale
			for i := 0; i < w; i++ {
				ox := (float64(rx+i) + 0.5) / scale
				sx, sy := plan.sourceOf(ox, oy)
				o := (j*w + i) * 3
				if lens == nil {
					src.sampleBilinear(sx-0.5, sy-0.5, out.Pix[o:o+3])
					continue
				}
				xr, yr, xg, yg, xb, yb, gain := lens.lookup(sx, sy)
				g := float32(gain)
				out.Pix[o] = src.sampleChannel(xr-0.5, yr-0.5, 0) * g
				out.Pix[o+1] = src.sampleChannel(xg-0.5, yg-0.5, 1) * g
				out.Pix[o+2] = src.sampleChannel(xb-0.5, yb-0.5, 2) * g
			}
		}
	})
	return out
}

func (img *Image) cropPx(x, y, w, h int) *Image {
	out := NewImage(w, h)
	for j := 0; j < h; j++ {
		copy(out.Pix[j*w*3:(j+1)*w*3], img.Pix[((y+j)*img.W+x)*3:((y+j)*img.W+x+w)*3])
	}
	return out
}
