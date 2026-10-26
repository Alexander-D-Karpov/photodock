package develop

import (
	"fmt"
	"math"
	"regexp"
	"strings"
)

// Local masks (linear/radial) in straightened pre-crop coordinates, so cropping never moves a mask.

const (
	maxMasks       = 16
	maskLocalEV    = 1.5
	maskContrast   = 0.5
	maskTempShift  = 0.04
	maskTintShift  = 0.04
	maskMaxGain    = 16
	maskCoordLimit = 1.5
)

var maskIDRe = regexp.MustCompile(`^[a-z0-9]{1,16}$`)

type MaskAdjust struct {
	Exposure   float64 `json:"exposure,omitempty"`
	Contrast   float64 `json:"contrast,omitempty"`
	Highlights float64 `json:"highlights,omitempty"`
	Shadows    float64 `json:"shadows,omitempty"`
	Temp       float64 `json:"temp,omitempty"`
	Tint       float64 `json:"tint,omitempty"`
	Saturation float64 `json:"saturation,omitempty"`
	Clarity    float64 `json:"clarity,omitempty"`
}

type Mask struct {
	ID      string     `json:"id"`
	Type    string     `json:"type"`
	Off     bool       `json:"off,omitempty"`
	Invert  bool       `json:"invert,omitempty"`
	X0      float64    `json:"x0,omitempty"`
	Y0      float64    `json:"y0,omitempty"`
	X1      float64    `json:"x1,omitempty"`
	Y1      float64    `json:"y1,omitempty"`
	CX      float64    `json:"cx,omitempty"`
	CY      float64    `json:"cy,omitempty"`
	RX      float64    `json:"rx,omitempty"`
	RY      float64    `json:"ry,omitempty"`
	Angle   float64    `json:"angle,omitempty"`
	Feather float64    `json:"feather,omitempty"`
	Adjust  MaskAdjust `json:"adjust"`
}

func normalizeMasks(ms []Mask) ([]Mask, error) {
	if len(ms) == 0 {
		return nil, nil
	}
	if len(ms) > maxMasks {
		return nil, fmt.Errorf("%w: at most %d masks", ErrInvalidRecipe, maxMasks)
	}
	coord := func(v float64) float64 { return roundTo(clamp(v, -0.5, maskCoordLimit), 1e-4) }
	out := make([]Mask, 0, len(ms))
	seen := map[string]bool{}
	for i, m := range ms {
		m.Type = strings.ToLower(strings.TrimSpace(m.Type))
		m.ID = strings.ToLower(strings.TrimSpace(m.ID))
		if !maskIDRe.MatchString(m.ID) || seen[m.ID] {
			m.ID = fmt.Sprintf("m%d", i)
		}
		seen[m.ID] = true
		switch m.Type {
		case "linear":
			m.X0, m.Y0, m.X1, m.Y1 = coord(m.X0), coord(m.Y0), coord(m.X1), coord(m.Y1)
			if math.Hypot(m.X1-m.X0, m.Y1-m.Y0) < 1e-3 {
				m.Y1 = coord(m.Y0 + 0.1)
			}
			m.CX, m.CY, m.RX, m.RY, m.Angle, m.Feather = 0, 0, 0, 0, 0, 0
		case "radial":
			m.CX, m.CY = coord(m.CX), coord(m.CY)
			m.RX = roundTo(clamp(m.RX, 0.005, 2), 1e-4)
			m.RY = roundTo(clamp(m.RY, 0.005, 2), 1e-4)
			a := math.Mod(m.Angle, 360)
			if a > 180 {
				a -= 360
			} else if a <= -180 {
				a += 360
			}
			m.Angle = roundTo(a, 0.1)
			m.Feather = math.Round(clamp(m.Feather, 0, 100))
			m.X0, m.Y0, m.X1, m.Y1 = 0, 0, 0, 0
		default:
			return nil, fmt.Errorf("%w: mask type %q", ErrInvalidRecipe, m.Type)
		}
		a := &m.Adjust
		a.Exposure = roundTo(clamp(a.Exposure, -4, 4), 0.01)
		for _, v := range []*float64{&a.Contrast, &a.Highlights, &a.Shadows, &a.Temp, &a.Tint, &a.Saturation, &a.Clarity} {
			*v = math.Round(clamp(*v, -100, 100))
		}
		out = append(out, m)
	}
	return out, nil
}

// maskSpace maps a processed pixel to straightened, uncropped frame pixels: off + (pixel+0.5)/scale.
type maskSpace struct {
	offX, offY     float64
	sx, sy         float64
	frameW, frameH float64
}

func (p *geomPlan) space(x0, y0 int, sx, sy float64) *maskSpace {
	return &maskSpace{
		offX:   p.cropX + float64(x0),
		offY:   p.cropY + float64(y0),
		sx:     sx,
		sy:     sy,
		frameW: p.frameW,
		frameH: p.frameH,
	}
}

type maskEval struct {
	adj      MaskAdjust
	invert   bool
	linear   bool
	x0, y0   float64
	dx, dy   float64
	inv2     float64
	cx, cy   float64
	rx, ry   float64
	cos, sin float64
	inner    float64
}

func prepareMasks(ms []Mask, sp *maskSpace) []maskEval {
	long := math.Max(sp.frameW, sp.frameH)
	var out []maskEval
	for _, m := range ms {
		if m.Off || m.Adjust == (MaskAdjust{}) {
			continue
		}
		e := maskEval{adj: m.Adjust, invert: m.Invert}
		if m.Type == "linear" {
			e.linear = true
			e.x0, e.y0 = m.X0*sp.frameW, m.Y0*sp.frameH
			e.dx, e.dy = m.X1*sp.frameW-e.x0, m.Y1*sp.frameH-e.y0
			d2 := e.dx*e.dx + e.dy*e.dy
			if d2 < 1e-9 {
				continue
			}
			e.inv2 = 1 / d2
		} else {
			e.cx, e.cy = m.CX*sp.frameW, m.CY*sp.frameH
			e.rx = math.Max(m.RX*long, 1e-6)
			e.ry = math.Max(m.RY*long, 1e-6)
			a := m.Angle * math.Pi / 180
			e.cos, e.sin = math.Cos(a), math.Sin(a)
			e.inner = 1 - m.Feather/100
		}
		out = append(out, e)
	}
	return out
}

func (m *maskEval) weight(fx, fy float64) float64 {
	var w float64
	if m.linear {
		t := ((fx-m.x0)*m.dx + (fy-m.y0)*m.dy) * m.inv2
		w = 1 - smooth64(0, 1, t)
	} else {
		dx, dy := fx-m.cx, fy-m.cy
		lx := (dx*m.cos + dy*m.sin) / m.rx
		ly := (-dx*m.sin + dy*m.cos) / m.ry
		r := math.Sqrt(lx*lx + ly*ly)
		if m.inner >= 0.999 {
			if r <= 1 {
				w = 1
			}
		} else {
			w = 1 - smooth64(m.inner, 1, r)
		}
	}
	if m.invert {
		w = 1 - w
	}
	return w
}

func applyMasks(img *Image, masks []Mask, ctx renderCtx) {
	if ctx.space == nil || len(masks) == 0 {
		return
	}
	evs := prepareMasks(masks, ctx.space)
	if len(evs) == 0 {
		return
	}
	w, h := img.W, img.H
	var s, base []float32
	for _, e := range evs {
		if e.adj.Clarity != 0 {
			s = lumPlane(img)
			base = guided(s, s, w, h, max(1, int(clarityRadius*ctx.frameLong)), 0.01)
			break
		}
	}
	sp := ctx.space
	parallelRows(h, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			fy := sp.offY + (float64(y)+0.5)/sp.sy
			for x := 0; x < w; x++ {
				fx := sp.offX + (float64(x)+0.5)/sp.sx
				var ev, con, sh, hl, cl, sat, temp, tint float64
				hit := false
				for k := range evs {
					m := &evs[k]
					wt := m.weight(fx, fy)
					if wt <= 0 {
						continue
					}
					hit = true
					a := m.adj
					ev += wt * a.Exposure
					con += wt * a.Contrast / 100
					sh += wt * a.Shadows / 100
					hl += wt * a.Highlights / 100
					cl += wt * a.Clarity / 100
					sat += wt * a.Saturation / 100
					temp += wt * a.Temp / 100
					tint += wt * a.Tint / 100
				}
				if !hit {
					continue
				}
				p := y*w + x
				i := p * 3
				r, g, b := float64(img.Pix[i]), float64(img.Pix[i+1]), float64(img.Pix[i+2])
				k := math.Exp2(ev)
				yl := (lumR*r + lumG*g + lumB*b) * k
				if con != 0 && yl > 1e-6 {
					yc := 0.18 * math.Pow(yl/0.18, 1+con*maskContrast)
					k *= yc / yl
					yl = yc
				}
				if (sh != 0 || hl != 0) && yl > 0 {
					e := math.Sqrt(yl)
					k *= math.Exp2(maskLocalEV * (sh*(1-smooth64(0, 0.5, e)) + hl*smooth64(0.5, 1, e)))
				}
				if cl != 0 && s != nil {
					s0 := float64(s[p])
					if s0 > 1e-4 {
						d := s0 - float64(base[p])
						mid := smooth64(0.05, 0.35, s0) * (1 - smooth64(0.65, 1, s0))
						ratio := (s0 + cl*mid*d) / s0
						k *= ratio * ratio
					}
				}
				k = math.Min(math.Max(k, 0), maskMaxGain)
				r, g, b = r*k, g*k, b*k
				if sat != 0 || temp != 0 || tint != 0 {
					L, la, lb := toOKLab(r, g, b)
					f := math.Max(0, 1+sat)
					la = la*f + tint*maskTintShift*smooth64(0, 0.1, L)
					lb = lb*f + temp*maskTempShift*smooth64(0, 0.1, L)
					r, g, b = fromOKLab(L, la, lb)
				}
				img.Pix[i], img.Pix[i+1], img.Pix[i+2] = float32(r), float32(g), float32(b)
			}
		}
	})
}
