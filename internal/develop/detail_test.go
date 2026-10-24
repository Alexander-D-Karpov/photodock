package develop

import (
	"math"
	"math/rand"
	"testing"
)

func noisyGrey(w, h int, sigma float64) *Image {
	rng := rand.New(rand.NewSource(3))
	img := NewImage(w, h)
	for i := range img.Pix {
		img.Pix[i] = float32(0.18 + rng.NormFloat64()*sigma)
	}
	return img
}

func stddev(img *Image, c int) float64 {
	var s, s2 float64
	n := float64(img.W * img.H)
	for p := 0; p < img.W*img.H; p++ {
		v := float64(img.Pix[p*3+c])
		s += v
		s2 += v * v
	}
	m := s / n
	return math.Sqrt(s2/n - m*m)
}

func TestDenoiseReducesNoise(t *testing.T) {
	img := noisyGrey(96, 64, 0.02)
	before := stddev(img, 1)
	denoise(img, Noise{Luma: 80, Chroma: 80}, renderCtx{detailScale: 1, frameLong: 96})
	if after := stddev(img, 1); after > before*0.7 {
		t.Errorf("noise %v -> %v", before, after)
	}
}

func TestSharpenIncreasesEdgeContrast(t *testing.T) {
	img := NewImage(40, 10)
	for y := 0; y < 10; y++ {
		for x := 0; x < 40; x++ {
			v := float32(0.1)
			if x >= 20 {
				v = 0.4
			}
			i := (y*40 + x) * 3
			img.Pix[i], img.Pix[i+1], img.Pix[i+2] = v, v, v
		}
	}
	sharpen(img, Sharpen{Amount: 100, Radius: 1.5}, renderCtx{detailScale: 1, frameLong: 40})
	if img.Pix[(5*40+19)*3] >= 0.1 || img.Pix[(5*40+20)*3] <= 0.4 {
		t.Errorf("edge not enhanced: %v | %v", img.Pix[(5*40+19)*3], img.Pix[(5*40+20)*3])
	}
}

func TestDehazeIncreasesContrast(t *testing.T) {
	img := NewImage(64, 64)
	for p := 0; p < 64*64; p++ {
		v := float32(0.5)
		if p%2 == 0 {
			v = 0.6
		}
		img.Pix[p*3], img.Pix[p*3+1], img.Pix[p*3+2] = v, v, v
	}
	before := stddev(img, 0)
	dehaze(img, 60, renderCtx{detailScale: 1, frameLong: 64})
	if stddev(img, 0) <= before {
		t.Error("dehaze did not increase contrast")
	}
}

func TestRegionMarginGrowsWithLocalOps(t *testing.T) {
	if regionMargin(Recipe{}, 6000) > 10 {
		t.Error("no local ops must need only a small margin")
	}
	if regionMargin(Recipe{Local: Local{Contrast: 20}}, 6000) < 500 {
		t.Error("local contrast must widen the margin")
	}
}
