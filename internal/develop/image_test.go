package develop

import (
	"math"
	"testing"
)

func coordImage(w, h int) *Image {
	img := NewImage(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 3
			img.Pix[i] = float32(x)
			img.Pix[i+1] = float32(y)
		}
	}
	return img
}

func at(img *Image, x, y int) (int, int) {
	i := (y*img.W + x) * 3
	return int(img.Pix[i]), int(img.Pix[i+1])
}

func TestRotate90(t *testing.T) {
	src := coordImage(3, 2)
	dst := src.Rotate(90)
	if dst.W != 2 || dst.H != 3 {
		t.Fatalf("dims = %dx%d; want 2x3", dst.W, dst.H)
	}
	if x, y := at(dst, 0, 0); x != 0 || y != 1 {
		t.Errorf("dst(0,0) = src(%d,%d); want src(0,1)", x, y)
	}
	if x, y := at(dst, 1, 0); x != 0 || y != 0 {
		t.Errorf("dst(1,0) = src(%d,%d); want src(0,0)", x, y)
	}
}

func TestRotateComposition(t *testing.T) {
	src := coordImage(5, 3)
	a := src.Rotate(90).Rotate(90).Rotate(90).Rotate(90)
	b := src.Rotate(90).Rotate(270)
	for i := range src.Pix {
		if a.Pix[i] != src.Pix[i] || b.Pix[i] != src.Pix[i] {
			t.Fatal("rotations do not compose to identity")
		}
	}
}

func TestOrientMatchesRotation(t *testing.T) {
	src := coordImage(4, 3)
	pairs := map[int]*Image{6: src.Rotate(90), 8: src.Rotate(270), 3: src.Rotate(180)}
	for o, want := range pairs {
		got := src.Orient(o)
		if got.W != want.W || got.H != want.H {
			t.Fatalf("orient %d dims %dx%d", o, got.W, got.H)
		}
		for i := range want.Pix {
			if got.Pix[i] != want.Pix[i] {
				t.Fatalf("orient %d differs at %d", o, i)
			}
		}
	}
}

func TestOrient7Transverse(t *testing.T) {
	src := coordImage(4, 3)
	dst := src.Orient(7)
	if dst.W != 3 || dst.H != 4 {
		t.Fatalf("dims = %dx%d", dst.W, dst.H)
	}
	for y := 0; y < dst.H; y++ {
		for x := 0; x < dst.W; x++ {
			sx, sy := at(dst, x, y)
			if sx != src.W-1-y || sy != src.H-1-x {
				t.Fatalf("dst(%d,%d) = src(%d,%d)", x, y, sx, sy)
			}
		}
	}
}

func TestCrop(t *testing.T) {
	src := coordImage(10, 10)
	dst := src.Crop(&Rect{X: 0.2, Y: 0.3, W: 0.5, H: 0.4})
	if dst.W != 5 || dst.H != 4 {
		t.Fatalf("dims = %dx%d; want 5x4", dst.W, dst.H)
	}
	if x, y := at(dst, 0, 0); x != 2 || y != 3 {
		t.Errorf("origin = src(%d,%d); want (2,3)", x, y)
	}
	if src.Crop(nil) != src {
		t.Error("nil crop must be a no-op")
	}
}

func TestResizeConstantAndMean(t *testing.T) {
	src := NewImage(100, 80)
	for i := range src.Pix {
		src.Pix[i] = 0.42
	}
	dst := src.Resize(33, 27)
	for i, v := range dst.Pix {
		if math.Abs(float64(v)-0.42) > 1e-5 {
			t.Fatalf("pix %d = %v; want 0.42", i, v)
		}
	}

	grad := coordImage(64, 64)
	half := grad.Resize(32, 32)
	var a, b float64
	for i := 0; i < len(grad.Pix); i += 3 {
		a += float64(grad.Pix[i])
	}
	for i := 0; i < len(half.Pix); i += 3 {
		b += float64(half.Pix[i])
	}
	if math.Abs(a/float64(64*64)-b/float64(32*32)) > 1e-3 {
		t.Errorf("mean changed: %v -> %v", a/4096, b/1024)
	}
}

func TestFitWithin(t *testing.T) {
	src := NewImage(4000, 3000)
	dst := src.FitWithin(1000, 1000)
	if dst.W != 1000 || dst.H != 750 {
		t.Errorf("fit = %dx%d; want 1000x750", dst.W, dst.H)
	}
	if src.FitWithin(5000, 5000) != src {
		t.Error("fit larger than source must be a no-op")
	}
}

func TestApplyToneExposure(t *testing.T) {
	src := NewImage(2, 1)
	for i := range src.Pix {
		src.Pix[i] = 0.1
	}
	dst := NewImage(2, 1)
	applyTone(dst, src, Recipe{Tone: Tone{Exposure: 1}}, nil, renderCtx{})
	for _, v := range dst.Pix {
		if math.Abs(float64(v)-0.2) > 1e-6 {
			t.Fatalf("exposure +1 EV: %v; want 0.2", v)
		}
	}
	if src.Pix[0] != 0.1 {
		t.Error("source mutated")
	}
}

func TestToSRGB8Neutral(t *testing.T) {
	img := NewImage(1, 1)
	img.Pix[0], img.Pix[1], img.Pix[2] = 0.18, 0.18, 0.18
	out := toSRGB8(img)
	r, g, b := out.Pix[0], out.Pix[1], out.Pix[2]
	if r != g || g != b {
		t.Errorf("neutral grey shifted: %d %d %d", r, g, b)
	}
	if r < 116 || r > 120 {
		t.Errorf("18%% grey encoded to %d; want ~118", r)
	}
}
