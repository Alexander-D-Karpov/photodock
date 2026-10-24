package develop

import (
	"math"
	"testing"
)

func sameImage(t *testing.T, name string, got, want *Image) {
	t.Helper()
	if got.W != want.W || got.H != want.H {
		t.Fatalf("%s: dims %dx%d; want %dx%d", name, got.W, got.H, want.W, want.H)
	}
	for i := range want.Pix {
		if math.Abs(float64(got.Pix[i]-want.Pix[i])) > 1e-3 {
			t.Fatalf("%s: pix %d = %v; want %v", name, i, got.Pix[i], want.Pix[i])
		}
	}
}

func TestPlanMatchesDihedralOps(t *testing.T) {
	src := coordImage(5, 3)
	cases := []struct {
		name string
		o    int
		g    Geometry
		want *Image
	}{
		{"orient6", 6, Geometry{}, src.Rotate(90)},
		{"orient8", 8, Geometry{}, src.Rotate(270)},
		{"orient7", 7, Geometry{}, src.Orient(7)},
		{"rotate90", 1, Geometry{Rotate: 90}, src.Rotate(90)},
		{"flipH", 1, Geometry{FlipH: true}, src.FlipH()},
		{"orient6+rot90+flipV", 6, Geometry{Rotate: 90, FlipV: true}, src.Rotate(180).FlipV()},
		{"orient3+rot270+flipH", 3, Geometry{Rotate: 270, FlipH: true}, src.Rotate(180).Rotate(270).FlipH()},
	}
	for _, c := range cases {
		sameImage(t, c.name, applyGeometry(src, c.o, c.g), c.want)
	}
}

func TestPlanCropMatchesImageCrop(t *testing.T) {
	src := coordImage(10, 10)
	r := &Rect{X: 0.2, Y: 0.3, W: 0.5, H: 0.4}
	sameImage(t, "crop", applyGeometry(src, 1, Geometry{Crop: r}), src.Crop(r))
}

func TestWarpRegionMatchesFull(t *testing.T) {
	src := coordImage(40, 30)
	plan := newGeomPlan(40, 30, 6, Geometry{Rotate: 90, Angle: 3}, nil)
	w, h := plan.size(1)
	full := warp(src, plan, 1, 0, 0, w, h)
	tile := warp(src, plan, 1, 5, 7, 10, 8)
	sameImage(t, "region", tile, full.cropPx(5, 7, 10, 8))
}

func TestStraightenPlanKeepsCentre(t *testing.T) {
	plan := newGeomPlan(400, 300, 1, Geometry{Angle: 5}, nil)
	sx, sy := plan.sourceOf(plan.outW/2, plan.outH/2)
	if math.Abs(sx-200) > 1 || math.Abs(sy-150) > 1 {
		t.Errorf("centre maps to (%v,%v)", sx, sy)
	}
	w, h := plan.size(1)
	if w >= 400 || h >= 300 {
		t.Errorf("straighten must auto-crop: %dx%d", w, h)
	}
}

func testProfile(dist []lfDistortionXML, vig []lfVignettingXML) *LensProfile {
	return &LensProfile{ID: "T|t", crop: 1, aspect: 1.5, dist: dist, vig: vig, minFocal: 50, maxFocal: 50}
}

func TestLensIdentity(t *testing.T) {
	p := testProfile([]lfDistortionXML{{Model: "poly3", Focal: 50}}, nil)
	m := newLensModel(p, p.coefficients(50, 0, 0), 300, 200, 1, Lens{Enabled: true})
	if m == nil {
		t.Fatal("expected a model")
	}
	if math.Abs(m.scale-1) > 1e-3 {
		t.Errorf("identity distortion autoscale = %v", m.scale)
	}
	_, _, xg, yg, _, _, gain := m.lookup(123.4, 56.7)
	if math.Abs(xg-123.4) > 1e-6 || math.Abs(yg-56.7) > 1e-6 || gain != 1 {
		t.Errorf("identity lookup moved point: (%v,%v) gain %v", xg, yg, gain)
	}
}

func TestDistortionAutoScaleFillsFrame(t *testing.T) {
	p := testProfile([]lfDistortionXML{{Model: "poly3", Focal: 50, K1: 0.05}}, nil)
	m := newLensModel(p, p.coefficients(50, 0, 0), 300, 200, 1, Lens{Enabled: true})
	if m.scale <= 1.01 {
		t.Fatalf("expected zoom-in autoscale, got %v", m.scale)
	}
	for _, pt := range [][2]float64{{0, 0}, {300, 0}, {0, 200}, {300, 200}, {150, 0}, {0, 100}} {
		_, _, x, y, _, _, _ := m.lookup(pt[0], pt[1])
		if x < -0.5 || y < -0.5 || x > 300.5 || y > 200.5 {
			t.Errorf("corner %v samples outside: (%v,%v)", pt, x, y)
		}
	}
}

func TestVignettingBrightensCorners(t *testing.T) {
	p := testProfile(nil, []lfVignettingXML{{Model: "pa", Focal: 50, Aperture: 2.8, Distance: 10, K1: -0.4}})
	m := newLensModel(p, p.coefficients(50, 2.8, 10), 300, 200, 1, Lens{Enabled: true})
	_, _, _, _, _, _, centre := m.lookup(150, 100)
	_, _, _, _, _, _, corner := m.lookup(0, 0)
	if math.Abs(centre-1) > 1e-6 || corner <= 1.2 {
		t.Errorf("gain centre %v corner %v", centre, corner)
	}
}

const testLensXML = `<!DOCTYPE lensdatabase SYSTEM "lensfun-database.dtd">
<lensdatabase version="2">
  <camera><maker>Sony</maker><model>ILCE-7M4</model><mount>Sony E</mount><cropfactor>1</cropfactor></camera>
  <lens>
    <maker>Sony</maker><model>FE 24-70mm f/2.8 GM</model><mount>Sony E</mount><cropfactor>1</cropfactor>
    <calibration>
      <distortion model="ptlens" focal="24" a="0.01" b="-0.03" c="0.02"/>
      <distortion model="ptlens" focal="70" a="0.03" b="-0.05" c="0.04"/>
      <tca model="poly3" focal="24" vr="1.0002" vb="0.9998"/>
      <vignetting model="pa" focal="24" aperture="2.8" distance="10" k1="-0.5" k2="0.1" k3="-0.05"/>
    </calibration>
  </lens>
  <lens>
    <maker>Sony</maker><model>FE 24-105mm f/4 G OSS</model><mount>Sony E</mount><cropfactor>1</cropfactor>
    <calibration><distortion model="poly3" focal="24" k1="-0.02"/></calibration>
  </lens>
  <lens>
    <maker>Sigma</maker><model>24-70mm f/2.8 DG DN | A</model><mount>Sony E</mount><cropfactor>1</cropfactor>
    <calibration><distortion model="poly3" focal="24" k1="-0.01"/></calibration>
  </lens>
</lensdatabase>`

func TestLensfunParseAndInterpolate(t *testing.T) {
	db := newLensDB()
	if err := db.addXML([]byte(testLensXML)); err != nil {
		t.Fatal(err)
	}
	if len(db.lenses) != 3 || db.camera("SONY", "ILCE-7M4") == nil {
		t.Fatalf("lenses %d, camera %v", len(db.lenses), db.camera("SONY", "ILCE-7M4"))
	}
	p := db.byID["Sony|FE 24-70mm f/2.8 GM"]
	co := p.coefficients(47, 2.8, 10)
	if co.distModel != "ptlens" || math.Abs(co.dist[0]-0.02) > 1e-9 || math.Abs(co.dist[2]-0.03) > 1e-9 {
		t.Errorf("interpolated distortion = %v %v", co.distModel, co.dist)
	}
	if co.tcaModel != "poly3" || co.red[0] != 1.0002 || co.blue[0] != 0.9998 {
		t.Errorf("tca = %v %v", co.red, co.blue)
	}
	if !co.hasVig || co.vig[0] != -0.5 {
		t.Errorf("vignetting = %v", co.vig)
	}
}

func TestLensCandidates(t *testing.T) {
	db := newLensDB()
	_ = db.addXML([]byte(testLensXML))
	got := db.Candidates("FE 24-70mm F2.8 GM", "SONY", "Sony E", 3)
	if len(got) == 0 || got[0].ID != "Sony|FE 24-70mm f/2.8 GM" {
		t.Fatalf("candidates = %+v", got)
	}
	for _, c := range got {
		if c.ID == "Sony|FE 24-105mm f/4 G OSS" {
			t.Error("different focal range must not match")
		}
	}
	if s := db.Search("sigma 24-70", 5); len(s) == 0 || s[0].Maker != "Sigma" {
		t.Errorf("search = %+v", s)
	}
}

// TestSystemLensfunLoads checks the installed Lensfun database parses when
// present (Arch ships it under version_1).
func TestSystemLensfunLoads(t *testing.T) {
	dir := detectLensfunDir()
	if dir == "" {
		t.Skip("lensfun not installed")
	}
	db := loadLensDB(dir)
	if len(db.lenses) < 100 || len(db.cameras) < 100 {
		t.Errorf("lensfun %s: %d lenses, %d cameras", dir, len(db.lenses), len(db.cameras))
	}
	if got := db.Candidates("E PZ 16-50mm F3.5-5.6 OSS", "SONY", "Sony E", 3); len(got) == 0 {
		t.Error("no candidate for the Sony E PZ 16-50mm kit lens")
	} else {
		t.Logf("16-50 candidates: %+v", got)
	}
}
