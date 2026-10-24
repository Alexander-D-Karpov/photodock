package develop

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func rawFixture(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("dcraw_emu"); err != nil {
		t.Skip("dcraw_emu not installed")
	}
	p := os.Getenv("PHOTODOCK_RAW_FIXTURE")
	if p == "" {
		p = filepath.Join("testdata", "sample.arw")
	}
	if _, err := os.Stat(p); err != nil {
		t.Skip("no RAW fixture (set PHOTODOCK_RAW_FIXTURE)")
	}
	abs, _ := filepath.Abs(p)
	return abs
}

func TestRawPipelineIntegration(t *testing.T) {
	src := rawFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dec := NewDecoder(t.TempDir())
	info, err := dec.RawInfo(ctx, src, "fixture")
	if err != nil {
		t.Fatalf("RawInfo: %v", err)
	}
	if !validMul(info.AsShot) || info.AsShot[1] != 1 {
		t.Fatalf("as-shot multipliers = %v", info.AsShot)
	}

	cam, err := dec.CameraMatrix(ctx, src, info)
	if err != nil {
		t.Fatalf("CameraMatrix: %v", err)
	}
	temp, tint := XYToTempTint(xyzToXY(whiteOfMuls(cam, info.AsShot)))
	t.Logf("as-shot %v daylight %v orientation %d -> %.0fK tint %.1f", info.AsShot, info.Daylight, info.Orientation, temp, tint)
	if temp < 2000 || temp > 12000 || tint < -80 || tint > 80 {
		t.Errorf("as-shot temp/tint implausible: %v / %v", temp, tint)
	}

	x, y := TempTintToXY(temp, tint)
	mul, err := mulsForWhite(cam, xyToXYZ(x, y))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if d := mul[i]/info.AsShot[i] - 1; d > 0.01 || d < -0.01 {
			t.Errorf("as-shot -> temp/tint -> mul drifted: %v vs %v", mul, info.AsShot)
		}
	}

	img, err := dec.DecodeRaw(ctx, src, DecodeOptions{Demosaic: "aahd", Highlights: "clip", Mul: info.AsShot, Half: true, ColorSpace: ColorRec2020})
	if err != nil {
		t.Fatalf("DecodeRaw: %v", err)
	}
	oriented := applyGeometry(img, info.Orientation, Geometry{})
	if info.Orientation >= 5 && oriented.W > oriented.H {
		t.Errorf("portrait shot rendered landscape: %dx%d", oriented.W, oriented.H)
	}
	if out := toSRGB8(oriented.FitWithin(800, 800)); out.Bounds().Dx() == 0 {
		t.Error("empty output")
	}
}
