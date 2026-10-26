package develop

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/jpeg"
	"math"
	"time"

	"github.com/disintegration/imaging"
)

const encodeLUTSize = 65535

var srgbEncodeLUT = buildEncodeLUT()

func buildEncodeLUT() []uint8 {
	lut := make([]uint8, encodeLUTSize+1)
	for i := range lut {
		v := srgbEncode(float64(i) / encodeLUTSize)
		lut[i] = uint8(math.Round(clamp(v, 0, 1) * 255))
	}
	return lut
}

func encodeChannel(v float32) uint8 {
	if !(v > 0) {
		return 0
	}
	if v >= 1 {
		return srgbEncodeLUT[encodeLUTSize]
	}
	return srgbEncodeLUT[int(v*encodeLUTSize+0.5)]
}

func applyGeometry(img *Image, orientation int, g Geometry) *Image {
	plan := newGeomPlan(img.W, img.H, orientation, g, nil)
	w, h := plan.size(1)
	return warp(img, plan, 1, 0, 0, w, h)
}

// applyTone runs all post-geometry stages in linear Rec.2020 (masks before global colour, sharpening last), timed into ctx.timing.
func applyTone(dst, src *Image, rc Recipe, pre *Mat3, ctx renderCtx) {
	if dst != src {
		copy(dst.Pix, src.Pix)
	}
	ctx = ctx.forImage(dst)
	t, c := rc.Tone, rc.Color
	stage := func(name string, fn func()) {
		st := time.Now()
		fn()
		ctx.timing.Since(name, st)
	}

	stage("pre", func() { applyPre(dst, rc, pre) })
	stage("noise", func() { denoise(dst, rc.Detail.Noise, ctx) })
	stage("dehaze", func() { dehaze(dst, rc.Local.Dehaze, ctx) })

	stage("tone", func() {
		var gains []float32
		if t.Shadows != 0 || t.Highlights != 0 {
			gains = localGains(dst, t.Shadows, t.Highlights, ctx.frameLong)
		}
		lut := buildToneLUT(t)
		if gains == nil && lut == nil {
			return
		}
		parallelRows(dst.H, func(y0, y1 int) {
			for p := y0 * dst.W; p < y1*dst.W; p++ {
				i := p * 3
				r, g, b := dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2]
				if gains != nil {
					k := gains[p]
					r, g, b = r*k, g*k, b*k
				}
				if lut != nil {
					k := lut.at(lumR*r + lumG*g + lumB*b)
					r, g, b = r*k, g*k, b*k
				}
				dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2] = r, g, b
			}
		})
	})

	stage("local", func() { localContrast(dst, rc.Local, ctx) })
	stage("masks", func() { applyMasks(dst, rc.Masks, ctx) })

	stage("color", func() {
		plan := newColorPlan(c)
		presence := c.Saturation != 0 || c.Vibrance != 0
		if !presence && plan == nil {
			return
		}
		sat := float32(1 + c.Saturation/100)
		vib := float32(c.Vibrance / 100)
		parallelRows(dst.H, func(y0, y1 int) {
			for i := y0 * dst.W * 3; i < y1*dst.W*3; i += 3 {
				r, g, b := dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2]
				if presence {
					r, g, b = applyColor(r, g, b, sat, vib)
				}
				if plan != nil {
					r, g, b = plan.apply(r, g, b)
				}
				dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2] = r, g, b
			}
		})
	})

	stage("sharpen", func() { sharpen(dst, rc.Detail.Sharpen, ctx) })
}

func toSRGB8(img *Image) *image.NRGBA {
	return toEncoded8(img, srgbFromRec2020)
}

// blurhashOf matches services.GenerateBlurhash: base64 of a 4x4 RGB grid.
func blurhashOf(img image.Image) string {
	tiny := imaging.Resize(img, 4, 4, imaging.Box)
	b := tiny.Bounds()
	pixels := make([]byte, 0, 48)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bb, _ := tiny.At(x, y).RGBA()
			pixels = append(pixels, byte(r>>8), byte(g>>8), byte(bb>>8))
		}
	}
	return base64.StdEncoding.EncodeToString(pixels)
}

func encodeJPEG(img image.Image, quality int) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// applyPre applies the channel mixer, WB matrix (unless done by the decoder) and exposure in one matrix-and-gain pass.
func applyPre(img *Image, rc Recipe, pre *Mat3) {
	gain := float32(math.Exp2(rc.Tone.Exposure))
	mat := pre
	if mm, on := mixerMatrix(rc.Color.Mixer); on {
		if pre != nil {
			mm = mm.Mul(*pre)
		}
		mat = &mm
	}
	var m [9]float32
	if mat != nil {
		m = toMat32(*mat)
	}
	hasMat := mat != nil
	parallelRows(img.H, func(y0, y1 int) {
		for i := y0 * img.W * 3; i < y1*img.W*3; i += 3 {
			r, g, b := img.Pix[i], img.Pix[i+1], img.Pix[i+2]
			if hasMat {
				r, g, b = m[0]*r+m[1]*g+m[2]*b, m[3]*r+m[4]*g+m[5]*b, m[6]*r+m[7]*g+m[8]*b
			}
			img.Pix[i], img.Pix[i+1], img.Pix[i+2] = r*gain, g*gain, b*gain
		}
	})
}

// toEncoded8 converts linear Rec.2020 to 8-bit in the toOutput space with the sRGB transfer curve (also used by P3).
func toEncoded8(img *Image, toOutput Mat3) *image.NRGBA {
	out := image.NewNRGBA(image.Rect(0, 0, img.W, img.H))
	m := toMat32(toOutput)
	parallelRows(img.H, func(y0, y1 int) {
		for y := y0; y < y1; y++ {
			for x := 0; x < img.W; x++ {
				i := (y*img.W + x) * 3
				r, g, b := img.Pix[i], img.Pix[i+1], img.Pix[i+2]
				o := y*out.Stride + x*4
				out.Pix[o] = encodeChannel(m[0]*r + m[1]*g + m[2]*b)
				out.Pix[o+1] = encodeChannel(m[3]*r + m[4]*g + m[5]*b)
				out.Pix[o+2] = encodeChannel(m[6]*r + m[7]*g + m[8]*b)
				out.Pix[o+3] = 255
			}
		}
	})
	return out
}
